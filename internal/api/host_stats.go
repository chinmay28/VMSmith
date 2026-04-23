package api

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vmsmith/vmsmith/pkg/types"
)

var (
	readFile = os.ReadFile
	statFS   = syscall.Statfs
)

type cpuSample struct {
	idle  uint64
	total uint64
}

func readCPUSample() (cpuSample, error) {
	data, err := readFile("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	if !scanner.Scan() {
		return cpuSample{}, fmt.Errorf("missing cpu line in /proc/stat")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, fmt.Errorf("invalid cpu line in /proc/stat")
	}

	var sample cpuSample
	for i, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, fmt.Errorf("parse cpu stat %q: %w", field, err)
		}
		sample.total += value
		if i == 3 || i == 4 {
			sample.idle += value
		}
	}
	return sample, nil
}

func readMemInfo() (total, available uint64, err error) {
	data, err := readFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	var totalKB, availableKB uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			totalKB, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, 0, err
			}
		case "MemAvailable":
			availableKB, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, 0, err
			}
		}
	}
	if totalKB == 0 {
		return 0, 0, fmt.Errorf("MemTotal missing from /proc/meminfo")
	}
	return totalKB * 1024, availableKB * 1024, nil
}

func percent(used, total uint64) int {
	if total == 0 {
		return 0
	}
	return int(math.Round((float64(used) / float64(total)) * 100))
}

func collectHostStats(ctx context.Context, storagePath string, vmCount int) (*types.HostStats, error) {
	first, err := readCPUSample()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(120 * time.Millisecond):
	}
	second, err := readCPUSample()
	if err != nil {
		return nil, err
	}
	memoryTotal, memoryAvailable, err := readMemInfo()
	if err != nil {
		return nil, err
	}

	targetPath := storagePath
	if targetPath == "" {
		targetPath = "/"
	}
	for {
		if _, err := os.Stat(targetPath); err == nil {
			break
		}
		parent := filepath.Dir(targetPath)
		if parent == targetPath {
			targetPath = "/"
			break
		}
		targetPath = parent
	}
	var fs syscall.Statfs_t
	if err := statFS(targetPath, &fs); err != nil {
		return nil, err
	}

	totalTicks := second.total - first.total
	idleTicks := second.idle - first.idle
	usedCPUPercent := 0
	if totalTicks > 0 && idleTicks <= totalTicks {
		usedCPUPercent = percent(totalTicks-idleTicks, totalTicks)
	}

	diskTotal := fs.Blocks * uint64(fs.Bsize)
	diskAvailable := fs.Bavail * uint64(fs.Bsize)
	diskUsed := diskTotal - diskAvailable
	ramUsed := memoryTotal - memoryAvailable
	cpuAvailable := 100 - usedCPUPercent
	if cpuAvailable < 0 {
		cpuAvailable = 0
	}

	return &types.HostStats{
		VMCount: vmCount,
		CPU: types.HostResourceUsageSummary{
			Used:       uint64(usedCPUPercent),
			Total:      100,
			Available:  uint64(cpuAvailable),
			Percentage: usedCPUPercent,
		},
		RAM: types.HostResourceUsageSummary{
			Used:       ramUsed,
			Total:      memoryTotal,
			Available:  memoryAvailable,
			Percentage: percent(ramUsed, memoryTotal),
		},
		Disk: types.HostResourceUsageSummary{
			Used:       diskUsed,
			Total:      diskTotal,
			Available:  diskAvailable,
			Percentage: percent(diskUsed, diskTotal),
		},
	}, nil
}
