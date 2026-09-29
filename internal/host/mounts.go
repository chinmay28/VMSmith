package host

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Mount is one entry of the kernel mount table.
type Mount struct {
	Path     string
	FSType   string
	Source   string
	ReadOnly bool
}

// pseudoFSTypes are filesystems that never hold persistent data, so they
// are useless as VM disk locations.
var pseudoFSTypes = map[string]bool{
	"autofs": true, "binfmt_misc": true, "bpf": true, "cgroup": true, "cgroup2": true,
	"configfs": true, "debugfs": true, "devpts": true, "devtmpfs": true, "efivarfs": true,
	"fusectl": true, "hugetlbfs": true, "mqueue": true, "nsfs": true, "overlay": true,
	"proc": true, "pstore": true, "ramfs": true, "rpc_pipefs": true, "securityfs": true,
	"squashfs": true, "sysfs": true, "tmpfs": true, "tracefs": true, "iso9660": true,
	"udf": true, "fuse.gvfsd-fuse": true, "fuse.portal": true, "fuse.snapfuse": true,
}

// IsPersistent reports whether the mount can hold VM disks: writable and
// backed by real storage rather than a virtual or in-memory filesystem.
func (m Mount) IsPersistent() bool {
	return !m.ReadOnly && !pseudoFSTypes[m.FSType]
}

// ParseMountInfo parses the /proc/<pid>/mountinfo format (proc(5)):
//
//	36 35 98:0 /mnt1 /mnt/parent rw,noatime master:1 - ext3 /dev/root rw
//
// Field 5 is the mount point, field 6 the per-mount options, and after the
// "-" separator come the filesystem type and source. Octal escapes (\040
// for space) in paths are decoded. Malformed lines are skipped.
func ParseMountInfo(r io.Reader) ([]Mount, error) {
	var out []Mount
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		ro := false
		for _, opt := range strings.Split(fields[5], ",") {
			if opt == "ro" {
				ro = true
			}
		}
		out = append(out, Mount{
			Path:     unescapeMountField(fields[4]),
			FSType:   fields[sep+1],
			Source:   unescapeMountField(fields[sep+2]),
			ReadOnly: ro,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading mount table: %w", err)
	}
	return out, nil
}

// unescapeMountField decodes the kernel's \ooo octal escapes.
func unescapeMountField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// PersistentMountPoints returns the mount points of the host's writable,
// storage-backed filesystems (see Mount.IsPersistent).
func PersistentMountPoints(mountInfoPath string) ([]string, error) {
	f, err := os.Open(mountInfoPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	mounts, err := ParseMountInfo(f)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range mounts {
		if m.IsPersistent() {
			out = append(out, m.Path)
		}
	}
	return out, nil
}
