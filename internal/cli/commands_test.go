package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// =====================================================// Test helpers
// =====================================================
// captureOutput redirects os.Stdout during f() and returns what was written.
func captureOutput(f func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// resetAllFlags restores every flag in the command tree to its default value so
// repeated Execute() calls in the same test process don't leak state between
// invocations (cobra/pflag keep both the flag value and Changed bit otherwise).
func resetAllFlags(cmd *cobra.Command) {
	reset := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.DefValue == "[]" {
				_ = f.Value.Set("")
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		})
	}

	reset(cmd.Flags())
	reset(cmd.PersistentFlags())
	for _, sub := range cmd.Commands() {
		resetAllFlags(sub)
	}
}

// runCLI executes CLI args and captures stdout, returning the output and error.
// cobra usage/error output is silenced so tests stay clean.
func runCLI(args ...string) (string, error) {
	resetAllFlags(rootCmd)
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	defer func() {
		rootCmd.SilenceUsage = false
		rootCmd.SilenceErrors = false
	}()

	var err error
	out := captureOutput(func() {
		rootCmd.SetArgs(args)
		err = rootCmd.Execute()
	})
	return out, err
}

var tableCellSplitRe = regexp.MustCompile(`\s{2,}`)

func tableRows(t *testing.T, out string) [][]string {
	t.Helper()

	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		t.Fatal("expected table output, got empty string")
	}

	lines := strings.Split(trimmed, "\n")
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		cells := tableCellSplitRe.Split(strings.TrimSpace(line), -1)
		rows = append(rows, cells)
	}
	return rows
}

// withMockVM sets vmManagerOverride to a MockManager and returns the mock + cleanup.
func withMockVM(t *testing.T) (*vm.MockManager, func()) {
	t.Helper()
	mock := vm.NewMockManager()
	vmManagerOverride = func() (vm.Manager, func(), error) {
		return mock, func() {}, nil
	}
	return mock, func() { vmManagerOverride = nil }
}

// withTestStorage sets storageManagerOverride to a real Manager backed by a temp dir.
// Returns the underlying store (for direct seeding) and a cleanup func.
func withTestStorage(t *testing.T) (*store.Store, *storage.Manager, func()) {
	t.Helper()
	dir := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.Storage.DBPath = filepath.Join(dir, "test.db")
	cfg.Storage.ImagesDir = filepath.Join(dir, "images")
	os.MkdirAll(cfg.Storage.ImagesDir, 0755)

	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	mgr := storage.NewManager(cfg, s)

	storageManagerOverride = func() (*storage.Manager, func(), error) {
		return mgr, func() {}, nil
	}
	return s, mgr, func() {
		storageManagerOverride = nil
		s.Close()
	}
}

// withTestPortForwarder sets portForwarderOverride to a real PortForwarder backed by a temp store.
func withTestPortForwarder(t *testing.T) (*store.Store, *network.PortForwarder, func()) {
	t.Helper()
	dir := t.TempDir()

	s, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	pf := network.NewPortForwarder(s)

	portForwarderOverride = func() (*network.PortForwarder, func(), error) {
		return pf, func() {}, nil
	}
	return s, pf, func() {
		portForwarderOverride = nil
		s.Close()
	}
}

// =====================================================// VM command tests
// =====================================================
func writeTestConfig(t *testing.T, pidFile string) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "daemon:\n  pid_file: \"" + pidFile + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func TestCLI_VMCreate(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "create", "my-vm", "--image", "ubuntu-22.04")
	if err != nil {
		t.Fatalf("vm create: %v", err)
	}
	if !strings.Contains(out, "VM created successfully") {
		t.Errorf("expected success message, got: %q", out)
	}
	if mock.VMCount() != 1 {
		t.Errorf("expected 1 VM in mock, got %d", mock.VMCount())
	}
}

func TestCLI_VMCreate_WithAllFlags(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "create", "full-vm",
		"--image", "ubuntu",
		"--cpus", "4",
		"--ram", "8192",
		"--disk", "50",
		"--description", "Production web server",
		"--tag", "Prod",
		"--tag", "web",
		"--ssh-key", "ssh-ed25519 AAAA test",
		"--default-user", "rocky",
	)
	if err != nil {
		t.Fatalf("vm create: %v", err)
	}
	if !strings.Contains(out, "full-vm") {
		t.Errorf("expected VM name in output, got: %q", out)
	}

	vms, _ := mock.List(nil)
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(vms))
	}
	if vms[0].Spec.CPUs != 4 {
		t.Errorf("CPUs = %d, want 4", vms[0].Spec.CPUs)
	}
	if vms[0].Spec.RAMMB != 8192 {
		t.Errorf("RAMMB = %d, want 8192", vms[0].Spec.RAMMB)
	}
	if vms[0].Spec.DefaultUser != "rocky" {
		t.Errorf("DefaultUser = %q, want rocky", vms[0].Spec.DefaultUser)
	}
	if vms[0].Description != "Production web server" {
		t.Errorf("Description = %q", vms[0].Description)
	}
	if strings.Join(vms[0].Tags, ",") != "prod,web" {
		t.Errorf("Tags = %v", vms[0].Tags)
	}
}

func TestCLI_VMCreate_MissingImage(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "create", "no-image-vm")
	if err == nil {
		t.Error("expected error when --image is missing")
	}
}

func TestCLI_VMCreate_ManagerError(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.CreateErr = types.ErrTest

	_, err := runCLI("vm", "create", "will-fail", "--image", "ubuntu")
	if err == nil {
		t.Error("expected error from manager, got nil")
	}
}

func TestCLI_VMList_Empty(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "list")
	if err != nil {
		t.Fatalf("vm list: %v", err)
	}
	// Should show header even when empty
	if !strings.Contains(out, "ID") || !strings.Contains(out, "NAME") {
		t.Errorf("expected table header, got: %q", out)
	}
}

func TestCLI_VMList_WithVMs(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Tags: []string{"prod", "web"}, State: types.VMStateRunning, IP: "192.168.100.10", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Tags: []string{"dev"}, State: types.VMStateStopped, IP: "", Spec: types.VMSpec{CPUs: 4, RAMMB: 4096}})

	out, err := runCLI("vm", "list")
	if err != nil {
		t.Fatalf("vm list: %v", err)
	}

	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}

	headers := rows[0]
	wantHeaders := []string{"ID", "NAME", "STATE", "IP", "CPUS", "RAM (MB)", "TAGS"}
	if strings.Join(headers, "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("headers = %v, want %v", headers, wantHeaders)
	}

	if !regexp.MustCompile(`(?m)^vm-1\s+alpha\s+running\s+192\.168\.100\.10\s+2\s+2048\s+prod,web$`).MatchString(strings.TrimSpace(out)) {
		t.Fatalf("expected vm-1 row in output, got %q", out)
	}
	if !regexp.MustCompile(`(?m)^vm-2\s+beta\s+stopped\s+4\s+4096\s+dev$`).MatchString(strings.TrimSpace(out)) {
		t.Fatalf("expected vm-2 row in output, got %q", out)
	}
}

func TestCLI_VMList_FilterByTag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Tags: []string{"prod"}, State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Tags: []string{"dev"}, State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 4, RAMMB: 4096}})

	out, err := runCLI("vm", "list", "--tag", "prod")
	if err != nil {
		t.Fatalf("vm list --tag: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("unexpected filtered output: %q", out)
	}
}

func TestCLI_VMList_FilterByStatus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 4, RAMMB: 4096}})

	out, err := runCLI("vm", "list", "--status", "running")
	if err != nil {
		t.Fatalf("vm list --status: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("unexpected filtered output: %q", out)
	}
}

func TestCLI_VMList_FilterByTagAndStatus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Tags: []string{"prod"}, State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Tags: []string{"prod"}, State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 4, RAMMB: 4096}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Tags: []string{"dev"}, State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})

	out, err := runCLI("vm", "list", "--tag", "prod", "--status", "running")
	if err != nil {
		t.Fatalf("vm list --tag --status: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || strings.Contains(out, "gamma") {
		t.Fatalf("unexpected filtered output: %q", out)
	}
}

func TestCLI_VMList_LimitAndOffset(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	base := time.Unix(1_700_000_000, 0)
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", CreatedAt: base, State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", CreatedAt: base.Add(time.Second), State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 4, RAMMB: 4096}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", CreatedAt: base.Add(2 * time.Second), State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--offset", "1", "--limit", "1")
	if err != nil {
		t.Fatalf("vm list --offset --limit: %v", err)
	}

	if strings.Contains(out, "alpha") || strings.Contains(out, "gamma") || !strings.Contains(out, "beta") {
		t.Fatalf("unexpected paginated output: %q", out)
	}
}

func TestCLI_VMList_InvalidOffset(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--offset", "-1")
	if err == nil || !strings.Contains(err.Error(), "--offset must be >= 0") {
		t.Fatalf("expected invalid offset error, got %v", err)
	}
}

func TestCLI_VMStart(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s", Name: "sleeper", State: types.VMStateStopped})

	out, err := runCLI("vm", "start", "vm-s")
	if err != nil {
		t.Fatalf("vm start: %v", err)
	}
	if !strings.Contains(out, "vm-s") {
		t.Errorf("expected VM id in output, got: %q", out)
	}

	got, _ := mock.Get(nil, "vm-s")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestCLI_VMStart_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "start", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_VMStart_All(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateStopped})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateRunning})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateStopped, Tags: []string{"prod"}})

	out, err := runCLI("vm", "start", "--all")
	if err != nil {
		t.Fatalf("vm start --all: %v", err)
	}
	if !strings.Contains(out, "Started 2 VM(s):") || !strings.Contains(out, "vm-1") || !strings.Contains(out, "vm-3") {
		t.Fatalf("unexpected output: %q", out)
	}

	for _, id := range []string{"vm-1", "vm-3"} {
		got, _ := mock.Get(nil, id)
		if got.State != types.VMStateRunning {
			t.Fatalf("%s state = %q, want running", id, got.State)
		}
	}
}

func TestCLI_VMStart_AllWithTag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateStopped, Tags: []string{"prod"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Tags: []string{"dev"}})

	out, err := runCLI("vm", "start", "--all", "--tag", "prod")
	if err != nil {
		t.Fatalf("vm start --all --tag: %v", err)
	}
	if !strings.Contains(out, "Started 1 VM(s): vm-1") {
		t.Fatalf("unexpected output: %q", out)
	}
	if got, _ := mock.Get(nil, "vm-1"); got.State != types.VMStateRunning {
		t.Fatalf("vm-1 state = %q, want running", got.State)
	}
	if got, _ := mock.Get(nil, "vm-2"); got.State != types.VMStateStopped {
		t.Fatalf("vm-2 state = %q, want stopped", got.State)
	}
}

func TestCLI_VMStart_AllRejectsID(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "start", "vm-1", "--all")
	if err == nil || !strings.Contains(err.Error(), "cannot specify a VM id when using --all") {
		t.Fatalf("expected --all/id validation error, got %v", err)
	}
}

func TestCLI_VMStop(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-r", Name: "runner", State: types.VMStateRunning})

	out, err := runCLI("vm", "stop", "vm-r")
	if err != nil {
		t.Fatalf("vm stop: %v", err)
	}
	if !strings.Contains(out, "vm-r") {
		t.Errorf("expected VM id in output, got: %q", out)
	}

	got, _ := mock.Get(nil, "vm-r")
	if got.State != types.VMStateStopped {
		t.Errorf("State = %q, want stopped", got.State)
	}
}

func TestCLI_VMStop_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "stop", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_VMStop_AllWithTag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Tags: []string{"prod"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateRunning, Tags: []string{"dev"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateStopped, Tags: []string{"prod"}})

	out, err := runCLI("vm", "stop", "--all", "--tag", "prod")
	if err != nil {
		t.Fatalf("vm stop --all --tag: %v", err)
	}
	if !strings.Contains(out, "Stopped 1 VM(s): vm-1") {
		t.Fatalf("unexpected output: %q", out)
	}
	if got, _ := mock.Get(nil, "vm-1"); got.State != types.VMStateStopped {
		t.Fatalf("vm-1 state = %q, want stopped", got.State)
	}
	if got, _ := mock.Get(nil, "vm-2"); got.State != types.VMStateRunning {
		t.Fatalf("vm-2 state = %q, want running", got.State)
	}
}

func TestCLI_VMStop_AllNoMatches(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateStopped, Tags: []string{"prod"}})

	out, err := runCLI("vm", "stop", "--all", "--tag", "prod")
	if err != nil {
		t.Fatalf("vm stop --all no matches: %v", err)
	}
	if !strings.Contains(out, "No stoppable VMs matched tag \"prod\"") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCLI_VMDelete(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-d", Name: "doomed"})

	out, err := runCLI("vm", "delete", "vm-d")
	if err != nil {
		t.Fatalf("vm delete: %v", err)
	}
	if !strings.Contains(out, "vm-d") {
		t.Errorf("expected VM id in output, got: %q", out)
	}
	if mock.VMCount() != 0 {
		t.Error("expected VM to be deleted")
	}
}

func TestCLI_VMDelete_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "delete", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_VMInfo(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{
		ID:        "vm-info",
		Name:      "infovm",
		State:     types.VMStateRunning,
		IP:        "192.168.100.42",
		DiskPath:  "/var/lib/vmsmith/vms/vm-info/disk.qcow2",
		CreatedAt: time.Now(),
		Spec:      types.VMSpec{CPUs: 4, RAMMB: 8192, DiskGB: 20, Image: "ubuntu-22.04", DefaultUser: "ubuntu"},
	})

	out, err := runCLI("vm", "info", "vm-info")
	if err != nil {
		t.Fatalf("vm info: %v", err)
	}
	for _, want := range []string{"infovm", "running", "192.168.100.42", "4", "8192", "ubuntu-22.04", "ubuntu", "ssh ubuntu@192.168.100.42"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %q", want, out)
		}
	}
}

func TestCLI_VMInfo_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "info", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

// =====================================================// VM edit command tests
// =====================================================
func TestCLI_VMEdit(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{
		ID:   "vm-edit1",
		Name: "edit-me",
		Spec: types.VMSpec{Name: "edit-me", CPUs: 2, RAMMB: 2048, DiskGB: 20},
	})

	out, err := runCLI("vm", "edit", "vm-edit1", "--cpus", "4")
	if err != nil {
		t.Fatalf("vm edit: %v", err)
	}
	if !strings.Contains(out, "VM updated") {
		t.Errorf("expected 'VM updated' in output, got: %s", out)
	}
	if !strings.Contains(out, "CPUs:  4") {
		t.Errorf("expected CPUs: 4 in output, got: %s", out)
	}
}

func TestCLI_VMEdit_NoFlags(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-nf", Name: "noflag"})

	_, err := runCLI("vm", "edit", "vm-nf")
	if err == nil {
		t.Fatal("expected error when no flags provided")
	}
}

func TestCLI_VMEdit_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "edit", "vm-nonexistent", "--cpus", "2")
	if err == nil {
		t.Fatal("expected error for nonexistent VM")
	}
}

func TestCLI_VMEdit_RAM(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{
		ID:   "vm-ram",
		Name: "ramtest",
		Spec: types.VMSpec{Name: "ramtest", CPUs: 1, RAMMB: 1024, DiskGB: 10},
	})

	out, err := runCLI("vm", "edit", "vm-ram", "--ram", "4096")
	if err != nil {
		t.Fatalf("vm edit --ram: %v", err)
	}
	if !strings.Contains(out, "RAM:   4096 MB") {
		t.Errorf("expected RAM: 4096 MB in output, got: %s", out)
	}
}

// =====================================================// Snapshot command tests
// =====================================================
func TestCLI_SnapshotCreate(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap", Name: "snapme"})

	out, err := runCLI("snapshot", "create", "vm-snap", "--name", "before-update")
	if err != nil {
		t.Fatalf("snapshot create: %v", err)
	}
	if !strings.Contains(out, "before-update") {
		t.Errorf("expected snapshot name in output, got: %q", out)
	}

	snaps, _ := mock.ListSnapshots(nil, "vm-snap")
	if len(snaps) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(snaps))
	}
}

func TestCLI_SnapshotCreate_MissingName(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("snapshot", "create", "vm-1")
	if err == nil {
		t.Error("expected error when --name is missing")
	}
}

func TestCLI_SnapshotCreate_VMNotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("snapshot", "create", "nonexistent", "--name", "snap")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_SnapshotList_Empty(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "bare"})

	out, err := runCLI("snapshot", "list", "vm-1")
	if err != nil {
		t.Fatalf("snapshot list: %v", err)
	}
	if !strings.Contains(out, "NAME") {
		t.Errorf("expected table header, got: %q", out)
	}
}

func TestCLI_SnapshotList_WithSnapshots(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-2", Name: "snappy"})
	mock.CreateSnapshot(nil, "vm-2", "snap-a")
	mock.CreateSnapshot(nil, "vm-2", "snap-b")

	out, err := runCLI("snapshot", "list", "vm-2")
	if err != nil {
		t.Fatalf("snapshot list: %v", err)
	}

	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}

	headers := rows[0]
	wantHeaders := []string{"NAME", "CREATED"}
	if strings.Join(headers, "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("headers = %v, want %v", headers, wantHeaders)
	}

	if got := rows[1][0]; got != "snap-a" {
		t.Fatalf("first snapshot name = %q, want snap-a", got)
	}
	if got := rows[2][0]; got != "snap-b" {
		t.Fatalf("second snapshot name = %q, want snap-b", got)
	}
	for i, row := range rows[1:] {
		if len(row) != 2 {
			t.Fatalf("snapshot row %d = %v, want 2 columns", i+1, row)
		}
		if _, err := time.Parse("2006-01-02 15:04:05", row[1]); err != nil {
			t.Fatalf("snapshot row %d created timestamp = %q: %v", i+1, row[1], err)
		}
	}
}

func TestCLI_SnapshotRestore(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-r", Name: "restorable"})
	mock.CreateSnapshot(nil, "vm-r", "good-state")

	out, err := runCLI("snapshot", "restore", "vm-r", "--name", "good-state")
	if err != nil {
		t.Fatalf("snapshot restore: %v", err)
	}
	if !strings.Contains(out, "vm-r") && !strings.Contains(out, "good-state") {
		t.Errorf("expected VM id and snapshot name in output, got: %q", out)
	}
}

func TestCLI_SnapshotRestore_NotFound(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-r2", Name: "restorable2"})

	_, err := runCLI("snapshot", "restore", "vm-r2", "--name", "doesnt-exist")
	if err == nil {
		t.Error("expected error for nonexistent snapshot")
	}
}

func TestCLI_SnapshotDelete(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-del", Name: "snapper"})
	mock.CreateSnapshot(nil, "vm-del", "temp-snap")

	out, err := runCLI("snapshot", "delete", "vm-del", "--name", "temp-snap")
	if err != nil {
		t.Fatalf("snapshot delete: %v", err)
	}
	if !strings.Contains(out, "temp-snap") {
		t.Errorf("expected snapshot name in output, got: %q", out)
	}

	snaps, _ := mock.ListSnapshots(nil, "vm-del")
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots after delete, got %d", len(snaps))
	}
}

// =====================================================// Image command tests
// =====================================================
func TestCLI_ImageList_Empty(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	out, err := runCLI("image", "list")
	if err != nil {
		t.Fatalf("image list: %v", err)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "NAME") {
		t.Errorf("expected table header, got: %q", out)
	}
}

func TestCLI_ImageList_WithImages(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	// Seed directly into the store (bypassing qemu-img)
	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{
		ID:        "img-1",
		Name:      "golden-image",
		Path:      "/tmp/golden-image.qcow2",
		SizeBytes: 1073741824, // 1 GB
		Format:    "qcow2",
		CreatedAt: now,
	})
	s.PutImage(&types.Image{
		ID:        "img-2",
		Name:      "backup-image",
		Path:      "/tmp/backup-image.qcow2",
		SizeBytes: 536870912, // 512 MB
		Format:    "qcow2",
		CreatedAt: now,
	})

	out, err := runCLI("image", "list")
	if err != nil {
		t.Fatalf("image list: %v", err)
	}

	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}

	headers := rows[0]
	wantHeaders := []string{"ID", "NAME", "SIZE", "FORMAT", "CREATED"}
	if strings.Join(headers, "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("headers = %v, want %v", headers, wantHeaders)
	}

	if got := rows[1]; strings.Join(got, "|") != "img-1|golden-image|1.0 GB|qcow2|2026-03-28 08:30:00" {
		t.Fatalf("first row = %v", got)
	}
	if got := rows[2]; strings.Join(got, "|") != "img-2|backup-image|512.0 MB|qcow2|2026-03-28 08:30:00" {
		t.Fatalf("second row = %v", got)
	}
}

func TestCLI_ImageList_LimitAndOffset(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-1", Name: "golden-image", Path: "/tmp/golden-image.qcow2", SizeBytes: 1073741824, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-2", Name: "backup-image", Path: "/tmp/backup-image.qcow2", SizeBytes: 536870912, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-3", Name: "archive-image", Path: "/tmp/archive-image.qcow2", SizeBytes: 268435456, Format: "qcow2", CreatedAt: now})

	out, err := runCLI("image", "list", "--offset", "1", "--limit", "1")
	if err != nil {
		t.Fatalf("image list --offset --limit: %v", err)
	}

	if strings.Contains(out, "golden-image") || strings.Contains(out, "archive-image") || !strings.Contains(out, "backup-image") {
		t.Fatalf("unexpected paginated output: %q", out)
	}
}

func TestCLI_ImageList_InvalidLimit(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "list", "--limit", "-1")
	if err == nil || !strings.Contains(err.Error(), "--limit must be >= 0") {
		t.Fatalf("expected invalid limit error, got %v", err)
	}
}

func TestCLI_ImageDelete(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	// Create a real (empty) file so os.Remove doesn't error
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.qcow2")
	os.WriteFile(imgPath, []byte{}, 0644)

	s.PutImage(&types.Image{
		ID:        "img-del",
		Name:      "to-delete",
		Path:      imgPath,
		SizeBytes: 0,
		Format:    "qcow2",
		CreatedAt: time.Now(),
	})

	out, err := runCLI("image", "delete", "img-del")
	if err != nil {
		t.Fatalf("image delete: %v", err)
	}
	if !strings.Contains(out, "img-del") {
		t.Errorf("expected image id in output, got: %q", out)
	}

	// Verify removed from store
	imgs, _ := s.ListImages()
	if len(imgs) != 0 {
		t.Errorf("expected 0 images after delete, got %d", len(imgs))
	}
}

func TestCLI_ImageDelete_NotFound(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "delete", "nonexistent-img")
	if err == nil {
		t.Error("expected error for nonexistent image")
	}
}

// =====================================================// Port forward command tests
// =====================================================
func TestCLI_PortList_Empty(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	out, err := runCLI("port", "list", "vm-any")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "HOST PORT") {
		t.Errorf("expected table header, got: %q", out)
	}
}

func TestCLI_PortList_WithForwards(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	// Seed a port forward directly
	s.PutPortForward(&types.PortForward{
		ID:        "pf-1",
		VMID:      "vm-x",
		HostPort:  2222,
		GuestPort: 22,
		GuestIP:   "192.168.100.10",
		Protocol:  types.ProtocolTCP,
	})
	s.PutPortForward(&types.PortForward{
		ID:        "pf-2",
		VMID:      "vm-x",
		HostPort:  8443,
		GuestPort: 443,
		GuestIP:   "192.168.100.10",
		Protocol:  types.ProtocolUDP,
	})

	out, err := runCLI("port", "list", "vm-x")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}

	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}

	headers := rows[0]
	wantHeaders := []string{"ID", "HOST PORT", "GUEST", "PROTOCOL"}
	if strings.Join(headers, "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("headers = %v, want %v", headers, wantHeaders)
	}

	if got := strings.Join(rows[1], "|"); got != "pf-1|2222|192.168.100.10:22|tcp" {
		t.Fatalf("first row = %v", rows[1])
	}
	if got := strings.Join(rows[2], "|"); got != "pf-2|8443|192.168.100.10:443|udp" {
		t.Fatalf("second row = %v", rows[2])
	}
}

func TestCLI_PortAdd_NoIP(t *testing.T) {
	mock, vmCleanup := withMockVM(t)
	defer vmCleanup()
	_, _, pfCleanup := withTestPortForwarder(t)
	defer pfCleanup()

	// VM exists but has no IP yet
	mock.SeedVM(&types.VM{ID: "vm-noip", Name: "noip", IP: ""})

	_, err := runCLI("port", "add", "vm-noip", "--host", "2222", "--guest", "22")
	if err == nil {
		t.Error("expected error for VM with no IP")
	}
	if !strings.Contains(err.Error(), "IP") {
		t.Errorf("expected IP-related error, got: %v", err)
	}
}

func TestCLI_PortAdd_VMNotFound(t *testing.T) {
	_, vmCleanup := withMockVM(t)
	defer vmCleanup()
	_, _, pfCleanup := withTestPortForwarder(t)
	defer pfCleanup()

	_, err := runCLI("port", "add", "nonexistent", "--host", "8080", "--guest", "80")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_PortAdd_InvalidInputRejectedBeforeVMLookup(t *testing.T) {
	calledVMManager := false
	vmManagerOverride = func() (vm.Manager, func(), error) {
		calledVMManager = true
		return vm.NewMockManager(), func() {}, nil
	}
	defer func() { vmManagerOverride = nil }()

	_, _, pfCleanup := withTestPortForwarder(t)
	defer pfCleanup()

	_, err := runCLI("port", "add", "vm-any", "--host", "0", "--guest", "22")
	if err == nil {
		t.Fatal("expected validation error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != "invalid_port_forward" {
		t.Fatalf("error code = %q, want invalid_port_forward", apiErr.Code)
	}
	if calledVMManager {
		t.Fatal("expected VM manager initialization to be skipped for invalid input")
	}
}

func TestCLI_PortRemove_NotFound(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	_, err := runCLI("port", "remove", "nonexistent-pf")
	if err == nil {
		t.Error("expected error for nonexistent port forward")
	}
}

// =====================================================// Net command tests
// =====================================================
func TestCLI_NetInterfaces(t *testing.T) {
	out, err := runCLI("net", "interfaces")
	if err != nil {
		t.Fatalf("net interfaces: %v", err)
	}
	// Should always show the header and at least one interface (loopback)
	if !strings.Contains(out, "INTERFACE") {
		t.Errorf("expected table header, got: %q", out)
	}
}

func TestCLI_NetInterfaces_All(t *testing.T) {
	out, err := runCLI("net", "interfaces", "--all")
	if err != nil {
		t.Fatalf("net interfaces --all: %v", err)
	}
	if !strings.Contains(out, "INTERFACE") {
		t.Errorf("expected table header, got: %q", out)
	}
}

// =====================================================// Full CLI lifecycle integration test
// =====================================================
func TestCLI_VMLifecycle(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	// 1. Create
	out, err := runCLI("vm", "create", "lifecycle-vm", "--image", "ubuntu", "--cpus", "2", "--ram", "2048")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out, "VM created successfully") {
		t.Errorf("create: unexpected output: %q", out)
	}

	// 2. List — should appear
	out, err = runCLI("vm", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "lifecycle-vm") {
		t.Errorf("list: expected VM name, got: %q", out)
	}

	// Get VM ID from mock
	vms, _ := mock.List(nil)
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(vms))
	}
	vmID := vms[0].ID

	// 3. Create snapshot
	_, err = runCLI("snapshot", "create", vmID, "--name", "checkpoint")
	if err != nil {
		t.Fatalf("snapshot create: %v", err)
	}

	// 4. List snapshots
	out, err = runCLI("snapshot", "list", vmID)
	if err != nil {
		t.Fatalf("snapshot list: %v", err)
	}
	if !strings.Contains(out, "checkpoint") {
		t.Errorf("snapshot list: expected 'checkpoint', got: %q", out)
	}

	// 5. Stop
	_, err = runCLI("vm", "stop", vmID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	v, _ := mock.Get(nil, vmID)
	if v.State != types.VMStateStopped {
		t.Errorf("after stop: State = %q, want stopped", v.State)
	}

	// 6. Start
	_, err = runCLI("vm", "start", vmID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	v, _ = mock.Get(nil, vmID)
	if v.State != types.VMStateRunning {
		t.Errorf("after start: State = %q, want running", v.State)
	}

	// 7. Restore snapshot
	_, err = runCLI("snapshot", "restore", vmID, "--name", "checkpoint")
	if err != nil {
		t.Fatalf("snapshot restore: %v", err)
	}

	// 8. Delete snapshot
	_, err = runCLI("snapshot", "delete", vmID, "--name", "checkpoint")
	if err != nil {
		t.Fatalf("snapshot delete: %v", err)
	}
	snaps, _ := mock.ListSnapshots(nil, vmID)
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snaps))
	}

	// 9. Delete VM
	_, err = runCLI("vm", "delete", vmID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if mock.VMCount() != 0 {
		t.Error("expected VM to be gone after delete")
	}
}

func TestCLI_DaemonStatus_Running(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "vmsmith.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	oldCfgFile := cfgFile
	cfgFile = writeTestConfig(t, pidFile)
	defer func() { cfgFile = oldCfgFile }()

	out, err := runCLI("daemon", "status", "--config", cfgFile)
	if err != nil {
		t.Fatalf("daemon status: %v", err)
	}
	if !strings.Contains(out, "vmSmith daemon is running (PID "+strconv.Itoa(os.Getpid())+")") {
		t.Fatalf("expected running output, got %q", out)
	}
}

func TestCLI_DaemonStatus_NotRunning(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "missing.pid")

	oldCfgFile := cfgFile
	cfgFile = writeTestConfig(t, pidFile)
	defer func() { cfgFile = oldCfgFile }()

	out, err := runCLI("daemon", "status", "--config", cfgFile)
	if err != nil {
		t.Fatalf("daemon status: %v", err)
	}
	if !strings.Contains(out, "vmSmith daemon is not running") {
		t.Fatalf("expected not-running output, got %q", out)
	}
}
