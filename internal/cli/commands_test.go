package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// sliceResetter is the interface implemented by pflag's StringSlice/StringArray
// values; Replace atomically swaps the entire slice so it can be cleared.
type sliceResetter interface {
	Replace([]string) error
}

// resetAllFlags restores every flag in the command tree to its default value so
// repeated Execute() calls in the same test process don't leak state between
// invocations (cobra/pflag keep both the flag value and Changed bit otherwise).
func resetAllFlags(cmd *cobra.Command) {
	reset := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			switch f.Value.Type() {
			case "stringSlice", "stringArray":
				// Set("") appends rather than clears; use Replace to empty the slice.
				if sr, ok := f.Value.(sliceResetter); ok {
					_ = sr.Replace(nil)
				}
			default:
				_ = fs.Set(f.Name, f.DefValue)
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
	// Tests don't have real iptables; stub the apply hook so add/remove
	// flows exercise the store path without touching the host.
	pf.SetApplyRuleFunc(func(action string, hostPort, guestPort int, guestIP, proto string) error {
		return nil
	})

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

func TestCLI_VMList_FilterBySearch_MatchesName(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-01", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "db-primary", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 4, RAMMB: 4096}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "web-staging", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048}})

	out, err := runCLI("vm", "list", "--search", "web")
	if err != nil {
		t.Fatalf("vm list --search: %v", err)
	}
	if !strings.Contains(out, "web-prod-01") || !strings.Contains(out, "web-staging") || strings.Contains(out, "db-primary") {
		t.Fatalf("unexpected filtered output: %q", out)
	}
}

func TestCLI_VMList_FilterBySearch_MatchesDescription(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Description: "Customer A jumpbox", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Description: "Internal tooling", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--search", "customer")
	if err != nil {
		t.Fatalf("vm list --search: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("expected only alpha (customer-* description), got %q", out)
	}
}

func TestCLI_VMList_FilterBySearch_MatchesTag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Tags: []string{"team-storage"}, Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Tags: []string{"experiment"}, Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--search", "team-")
	if err != nil {
		t.Fatalf("vm list --search: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("expected only alpha (team-* tag), got %q", out)
	}
}

func TestCLI_VMList_FilterBySearch_NoMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--search", "needle-not-present")
	if err != nil {
		t.Fatalf("vm list --search: %v", err)
	}
	if strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("expected no matching rows, got %q", out)
	}
}

func TestCLI_VMList_FilterBySearch_CombinesWithStatus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-01", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "web-staging", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "db-primary", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--search", "web", "--status", "running")
	if err != nil {
		t.Fatalf("vm list --search --status: %v", err)
	}
	if !strings.Contains(out, "web-prod-01") || strings.Contains(out, "web-staging") || strings.Contains(out, "db-primary") {
		t.Fatalf("expected only web-prod-01 (web + running), got %q", out)
	}
}

func TestCLI_VMList_FilterByImage_ExactMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "rocky9.qcow2"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "ubuntu.qcow2"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "rocky9.qcow2"}})

	out, err := runCLI("vm", "list", "--image", "rocky9.qcow2")
	if err != nil {
		t.Fatalf("vm list --image: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || !strings.Contains(out, "gamma") {
		t.Fatalf("expected alpha + gamma only (rocky9.qcow2), got %q", out)
	}
}

func TestCLI_VMList_FilterByImage_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "Rocky9.qcow2"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "ubuntu.qcow2"}})

	out, err := runCLI("vm", "list", "--image", "ROCKY9.QCOW2")
	if err != nil {
		t.Fatalf("vm list --image: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("expected only alpha (case-insensitive image match), got %q", out)
	}
}

func TestCLI_VMList_FilterByImage_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "rocky9.qcow2"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "ubuntu.qcow2"}})

	out, err := runCLI("vm", "list", "--image", "   ")
	if err != nil {
		t.Fatalf("vm list --image: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("whitespace-only --image should be a no-op; got %q", out)
	}
}

func TestCLI_VMList_FilterByImage_NoMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "rocky9.qcow2"}})

	out, err := runCLI("vm", "list", "--image", "does-not-exist.qcow2")
	if err != nil {
		t.Fatalf("vm list --image: %v", err)
	}
	if strings.Contains(out, "alpha") {
		t.Fatalf("expected no rows for unknown image, got %q", out)
	}
}

func TestCLI_VMList_FilterByImage_ComposesWithStatus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "rocky9.qcow2"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "rocky9.qcow2"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, Image: "ubuntu.qcow2"}})

	out, err := runCLI("vm", "list", "--image", "rocky9.qcow2", "--status", "running")
	if err != nil {
		t.Fatalf("vm list --image --status: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || strings.Contains(out, "gamma") {
		t.Fatalf("expected only alpha (running rocky9), got %q", out)
	}
}

func TestCLI_VMList_FilterByAutoStart_True(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: true}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: false}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: true}})

	out, err := runCLI("vm", "list", "--auto-start", "true")
	if err != nil {
		t.Fatalf("vm list --auto-start true: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || !strings.Contains(out, "gamma") {
		t.Fatalf("expected alpha+gamma only, got %q", out)
	}
}

func TestCLI_VMList_FilterByAutoStart_False(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: true}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: false}})

	out, err := runCLI("vm", "list", "--auto-start", "false")
	if err != nil {
		t.Fatalf("vm list --auto-start false: %v", err)
	}
	if strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("expected only beta, got %q", out)
	}
}

func TestCLI_VMList_FilterByLocked_True(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Locked: true}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Locked: false}})

	out, err := runCLI("vm", "list", "--locked", "true")
	if err != nil {
		t.Fatalf("vm list --locked true: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("expected only alpha, got %q", out)
	}
}

func TestCLI_VMList_FilterByAutoStart_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: true}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--auto-start", "TrUe")
	if err != nil {
		t.Fatalf("vm list --auto-start TrUe: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("expected only alpha, got %q", out)
	}
}

func TestCLI_VMList_FilterByAutoStart_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: true}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--auto-start", "")
	if err != nil {
		t.Fatalf("vm list --auto-start <empty>: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("expected all rows when --auto-start is empty, got %q", out)
	}
}

func TestCLI_VMList_RejectsInvalidAutoStart(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--auto-start", "maybe")
	if err == nil {
		t.Fatalf("expected error from invalid --auto-start")
	}
	if !strings.Contains(err.Error(), "must be 'true' or 'false'") {
		t.Fatalf("err = %v, want contains \"must be 'true' or 'false'\"", err)
	}
}

func TestCLI_VMList_RejectsInvalidLocked(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--locked", "yes")
	if err == nil {
		t.Fatalf("expected error from invalid --locked")
	}
	if !strings.Contains(err.Error(), "must be 'true' or 'false'") {
		t.Fatalf("err = %v, want contains \"must be 'true' or 'false'\"", err)
	}
}

func TestCLI_VMList_FilterByAutoStartAndLocked(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: true, Locked: true}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: true, Locked: false}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, AutoStart: false, Locked: true}})

	out, err := runCLI("vm", "list", "--auto-start", "true", "--locked", "true")
	if err != nil {
		t.Fatalf("vm list --auto-start true --locked true: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || strings.Contains(out, "gamma") {
		t.Fatalf("expected only alpha, got %q", out)
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

func TestCLI_VMList_SortByName(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-3", Name: "Charlie", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "Bravo", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "name")
	if err != nil {
		t.Fatalf("vm list --sort name: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"alpha", "Bravo", "Charlie"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByCreatedAtDesc(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	base := time.Unix(1_700_000_000, 0)
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "first", CreatedAt: base, State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "second", CreatedAt: base.Add(time.Hour), State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "third", CreatedAt: base.Add(2 * time.Hour), State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "created_at", "--order", "desc")
	if err != nil {
		t.Fatalf("vm list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"third", "second", "first"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestCLI_VMList_RejectsInvalidSort(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "ram_mb")
	if err == nil || !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("expected invalid --sort error, got %v", err)
	}
}

func TestCLI_VMList_RejectsInvalidOrder(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--order", "sideways")
	if err == nil || !strings.Contains(err.Error(), "invalid --order") {
		t.Fatalf("expected invalid --order error, got %v", err)
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

func TestCLI_VMRestart(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-r", Name: "rebooter", State: types.VMStateRunning})

	out, err := runCLI("vm", "restart", "vm-r")
	if err != nil {
		t.Fatalf("vm restart: %v", err)
	}
	if !strings.Contains(out, "vm-r") || !strings.Contains(out, "restarted") {
		t.Errorf("expected VM id and 'restarted' in output, got: %q", out)
	}

	got, _ := mock.Get(nil, "vm-r")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestCLI_VMRestart_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "restart", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_VMReboot(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-rb", Name: "rebooter", State: types.VMStateRunning})

	out, err := runCLI("vm", "reboot", "vm-rb")
	if err != nil {
		t.Fatalf("vm reboot: %v", err)
	}
	if !strings.Contains(out, "vm-rb") || !strings.Contains(out, "rebooted") {
		t.Errorf("expected VM id and 'rebooted' in output, got: %q", out)
	}

	got, _ := mock.Get(nil, "vm-rb")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestCLI_VMReboot_NotRunning(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-stopped", State: types.VMStateStopped})

	_, err := runCLI("vm", "reboot", "vm-stopped")
	if err == nil {
		t.Fatal("expected error for non-running VM")
	}
	if !strings.Contains(err.Error(), "vm_not_running") && !strings.Contains(err.Error(), "must be running") {
		t.Errorf("expected vm_not_running error, got: %v", err)
	}
}

func TestCLI_VMReboot_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "reboot", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_VMForceStop(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-fs", Name: "wedged", State: types.VMStateRunning})

	out, err := runCLI("vm", "force-stop", "vm-fs")
	if err != nil {
		t.Fatalf("vm force-stop: %v", err)
	}
	if !strings.Contains(out, "vm-fs") || !strings.Contains(out, "force-stopped") {
		t.Errorf("expected VM id and 'force-stopped' in output, got: %q", out)
	}

	got, _ := mock.Get(nil, "vm-fs")
	if got.State != types.VMStateStopped {
		t.Errorf("State = %q, want stopped", got.State)
	}
}

func TestCLI_VMForceStop_AlreadyStopped(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-fs2", Name: "stopped", State: types.VMStateStopped})

	_, err := runCLI("vm", "force-stop", "vm-fs2")
	if err == nil {
		t.Fatal("expected error for already-stopped VM")
	}
	if !strings.Contains(err.Error(), "vm_already_stopped") && !strings.Contains(err.Error(), "already stopped") {
		t.Errorf("expected vm_already_stopped error, got: %v", err)
	}
}

func TestCLI_VMForceStop_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "force-stop", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_VMSuspend(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s", Name: "pauseme", State: types.VMStateRunning})

	out, err := runCLI("vm", "suspend", "vm-s")
	if err != nil {
		t.Fatalf("vm suspend: %v", err)
	}
	if !strings.Contains(out, "vm-s") || !strings.Contains(out, "suspended") {
		t.Errorf("expected VM id and 'suspended' in output, got: %q", out)
	}

	got, _ := mock.Get(nil, "vm-s")
	if got.State != types.VMStatePaused {
		t.Errorf("State = %q, want paused", got.State)
	}
}

func TestCLI_VMSuspend_NotRunning(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-stopped", State: types.VMStateStopped})

	_, err := runCLI("vm", "suspend", "vm-stopped")
	if err == nil {
		t.Fatal("expected error for non-running VM")
	}
}

func TestCLI_VMSuspend_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "suspend", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

func TestCLI_VMResume(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-r", Name: "resumeme", State: types.VMStatePaused})

	out, err := runCLI("vm", "resume", "vm-r")
	if err != nil {
		t.Fatalf("vm resume: %v", err)
	}
	if !strings.Contains(out, "vm-r") || !strings.Contains(out, "resumed") {
		t.Errorf("expected VM id and 'resumed' in output, got: %q", out)
	}

	got, _ := mock.Get(nil, "vm-r")
	if got.State != types.VMStateRunning {
		t.Errorf("State = %q, want running", got.State)
	}
}

func TestCLI_VMResume_NotPaused(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-running", State: types.VMStateRunning})

	_, err := runCLI("vm", "resume", "vm-running")
	if err == nil {
		t.Fatal("expected error for non-paused VM")
	}
}

func TestCLI_VMResume_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "resume", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent VM")
	}
}

// TestCLI_VMLifecycleAll_Verbs covers the `--all` / `--tag` selectors that
// 2.3.8 adds to restart, force-stop, reboot, suspend, and resume.  Each verb
// is exercised against a seed mix of running, stopped, and paused VMs to make
// sure the state filter only acts on eligible machines and that the
// past-tense label in the output matches the verb.
func TestCLI_VMLifecycleAll_Verbs(t *testing.T) {
	cases := []struct {
		verb         string
		seed         []*types.VM
		eligibleIDs  []string
		ineligibleID string // a VM that exists but should not be touched
		summary      string // expected substring like "Restarted 2 VM(s):"
		wantState    types.VMState
	}{
		{
			verb: "restart",
			seed: []*types.VM{
				{ID: "vm-1", State: types.VMStateRunning},
				{ID: "vm-2", State: types.VMStateRunning},
				{ID: "vm-3", State: types.VMStateStopped},
			},
			eligibleIDs:  []string{"vm-1", "vm-2"},
			ineligibleID: "vm-3",
			summary:      "Restarted 2 VM(s):",
			wantState:    types.VMStateRunning,
		},
		{
			verb: "force-stop",
			seed: []*types.VM{
				{ID: "vm-1", State: types.VMStateRunning},
				{ID: "vm-2", State: types.VMStateRunning},
				{ID: "vm-3", State: types.VMStateStopped},
			},
			eligibleIDs:  []string{"vm-1", "vm-2"},
			ineligibleID: "vm-3",
			summary:      "Force-stopped 2 VM(s):",
			wantState:    types.VMStateStopped,
		},
		{
			verb: "reboot",
			seed: []*types.VM{
				{ID: "vm-1", State: types.VMStateRunning},
				{ID: "vm-2", State: types.VMStateStopped},
			},
			eligibleIDs:  []string{"vm-1"},
			ineligibleID: "vm-2",
			summary:      "Rebooted 1 VM(s):",
			wantState:    types.VMStateRunning,
		},
		{
			verb: "suspend",
			seed: []*types.VM{
				{ID: "vm-1", State: types.VMStateRunning},
				{ID: "vm-2", State: types.VMStatePaused},
			},
			eligibleIDs:  []string{"vm-1"},
			ineligibleID: "vm-2",
			summary:      "Suspended 1 VM(s):",
			wantState:    types.VMStatePaused,
		},
		{
			verb: "resume",
			seed: []*types.VM{
				{ID: "vm-1", State: types.VMStatePaused},
				{ID: "vm-2", State: types.VMStateRunning},
			},
			eligibleIDs:  []string{"vm-1"},
			ineligibleID: "vm-2",
			summary:      "Resumed 1 VM(s):",
			wantState:    types.VMStateRunning,
		},
	}

	for _, tc := range cases {
		t.Run(tc.verb, func(t *testing.T) {
			mock, cleanup := withMockVM(t)
			defer cleanup()

			for _, vm := range tc.seed {
				mock.SeedVM(vm)
			}

			out, err := runCLI("vm", tc.verb, "--all")
			if err != nil {
				t.Fatalf("vm %s --all: %v", tc.verb, err)
			}
			if !strings.Contains(out, tc.summary) {
				t.Fatalf("output %q is missing %q", out, tc.summary)
			}
			for _, id := range tc.eligibleIDs {
				if !strings.Contains(out, id) {
					t.Fatalf("output %q is missing eligible id %q", out, id)
				}
				got, _ := mock.Get(nil, id)
				if got.State != tc.wantState {
					t.Fatalf("vm %s state = %q, want %q", id, got.State, tc.wantState)
				}
			}
			if tc.ineligibleID != "" && strings.Contains(out, tc.ineligibleID) {
				t.Fatalf("output %q unexpectedly mentions ineligible id %q", out, tc.ineligibleID)
			}
		})
	}
}

func TestCLI_VMRestart_AllWithTag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", State: types.VMStateRunning, Tags: []string{"prod"}})
	mock.SeedVM(&types.VM{ID: "vm-2", State: types.VMStateRunning, Tags: []string{"dev"}})

	out, err := runCLI("vm", "restart", "--all", "--tag", "prod")
	if err != nil {
		t.Fatalf("vm restart --all --tag: %v", err)
	}
	if !strings.Contains(out, "Restarted 1 VM(s): vm-1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCLI_VMSuspend_AllNoMatches(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-stopped", State: types.VMStateStopped})

	out, err := runCLI("vm", "suspend", "--all")
	if err != nil {
		t.Fatalf("vm suspend --all (no matches): %v", err)
	}
	if !strings.Contains(out, "No suspendable VMs found") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestCLI_VMForceStop_AllRejectsID(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "force-stop", "vm-1", "--all")
	if err == nil || !strings.Contains(err.Error(), "cannot specify a VM id when using --all") {
		t.Fatalf("expected --all/id validation error, got %v", err)
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

func TestCLI_VMLockUnlock(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-lk", Name: "important", Spec: types.VMSpec{CPUs: 1}})

	out, err := runCLI("vm", "lock", "vm-lk")
	if err != nil {
		t.Fatalf("vm lock: %v", err)
	}
	if !strings.Contains(out, "locked") {
		t.Errorf("expected 'locked' in output, got: %q", out)
	}
	got, _ := mock.Get(nil, "vm-lk")
	if !got.Spec.Locked {
		t.Errorf("Spec.Locked = false, want true after lock")
	}

	// Locked VM rejects delete with vm_locked.
	if _, err := runCLI("vm", "delete", "vm-lk"); err == nil {
		t.Error("expected delete error on locked VM")
	}

	// Unlock and confirm delete works.
	out, err = runCLI("vm", "unlock", "vm-lk")
	if err != nil {
		t.Fatalf("vm unlock: %v", err)
	}
	if !strings.Contains(out, "unlocked") {
		t.Errorf("expected 'unlocked' in output, got: %q", out)
	}
	if _, err := runCLI("vm", "delete", "vm-lk"); err != nil {
		t.Fatalf("vm delete after unlock: %v", err)
	}
	if mock.VMCount() != 0 {
		t.Error("expected VM to be deleted after unlock")
	}
}

func TestCLI_VMLock_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	if _, err := runCLI("vm", "lock", "nonexistent"); err == nil {
		t.Error("expected error for nonexistent VM")
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

func TestCLI_VMClone(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{
		ID:          "vm-source",
		Name:        "source",
		Description: "Base app server",
		Tags:        []string{"prod", "golden"},
		Spec:        types.VMSpec{Name: "source", CPUs: 4, RAMMB: 8192, DiskGB: 80, Tags: []string{"prod", "golden"}},
		State:       types.VMStateStopped,
	})

	out, err := runCLI("vm", "clone", "vm-source", "--name", "clone-a")
	if err != nil {
		t.Fatalf("vm clone: %v", err)
	}
	for _, want := range []string{"VM cloned successfully", "Source ID: vm-source", "Name:      clone-a", "State:     stopped", "Tags:      prod, golden"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got %q", want, out)
		}
	}

	cloned, err := mock.Get(nil, "vm-mock-1")
	if err != nil {
		t.Fatalf("get cloned VM: %v", err)
	}
	if cloned.Name != "clone-a" {
		t.Fatalf("clone name = %q, want clone-a", cloned.Name)
	}
	if cloned.State != types.VMStateStopped {
		t.Fatalf("clone state = %q, want stopped", cloned.State)
	}
	if cloned.Description != "Base app server" {
		t.Fatalf("clone description = %q", cloned.Description)
	}
}

func TestCLI_VMClone_RequiresName(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "clone", "vm-source")
	if err == nil {
		t.Fatal("expected error when --name is missing")
	}
}

func TestCLI_VMClone_NotFound(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "clone", "missing", "--name", "clone-a")
	if err == nil {
		t.Fatal("expected error for nonexistent source VM")
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
	mock.CreateSnapshot(nil, "vm-2", types.SnapshotSpec{Name: "snap-a", Description: "before upgrade"})
	mock.CreateSnapshot(nil, "vm-2", types.SnapshotSpec{Name: "snap-b"})

	out, err := runCLI("snapshot", "list", "vm-2")
	if err != nil {
		t.Fatalf("snapshot list: %v", err)
	}

	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}

	headers := rows[0]
	wantHeaders := []string{"NAME", "CREATED", "TAGS", "DESCRIPTION"}
	if strings.Join(headers, "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("headers = %v, want %v", headers, wantHeaders)
	}

	if got := rows[1][0]; got != "snap-a" {
		t.Fatalf("first snapshot name = %q, want snap-a", got)
	}
	if got := rows[2][0]; got != "snap-b" {
		t.Fatalf("second snapshot name = %q, want snap-b", got)
	}
	// rows[1][2] is now the TAGS column; the description sits at index 3
	// (and may be split across further columns by tableRows for multi-
	// word values).
	if got := rows[1][3]; got != "before" && got != "before upgrade" {
		t.Fatalf("first snapshot description column = %q, want 'before' (multi-word descriptions get split by tableRows)", got)
	}
	for i, row := range rows[1:] {
		if _, err := time.Parse("2006-01-02 15:04:05", row[1]); err != nil {
			t.Fatalf("snapshot row %d created timestamp = %q: %v", i+1, row[1], err)
		}
	}
}

func TestCLI_SnapshotCreate_DescriptionFlag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-d", Name: "noted"})

	if _, err := runCLI("snapshot", "create", "vm-d", "--name", "first", "--description", "before patch"); err != nil {
		t.Fatalf("snapshot create: %v", err)
	}

	snaps, err := mock.ListSnapshots(nil, "vm-d")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(snaps))
	}
	if snaps[0].Description != "before patch" {
		t.Fatalf("description = %q, want 'before patch'", snaps[0].Description)
	}
}

func TestCLI_SnapshotCreate_DescriptionTooLong(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-d2", Name: "noted2"})

	long := strings.Repeat("x", 1025)
	if _, err := runCLI("snapshot", "create", "vm-d2", "--name", "snap", "--description", long); err == nil {
		t.Fatal("expected error for description over 1024 chars")
	}
}

func TestCLI_SnapshotList_SortByName(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s-sort", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-sort", Name: "Charlie"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-sort", Name: "alpha"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-sort", Name: "Bravo"})

	out, err := runCLI("snapshot", "list", "vm-s-sort", "--sort", "name")
	if err != nil {
		t.Fatalf("snapshot list --sort name: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][0], rows[2][0], rows[3][0]}
	want := []string{"alpha", "Bravo", "Charlie"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_SnapshotList_SortByCreatedAtDesc(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	mock.SeedVM(&types.VM{ID: "vm-s-time", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-time", Name: "first", CreatedAt: base})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-time", Name: "second", CreatedAt: base.Add(time.Hour)})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-time", Name: "third", CreatedAt: base.Add(2 * time.Hour)})

	out, err := runCLI("snapshot", "list", "vm-s-time", "--sort", "created_at", "--order", "desc")
	if err != nil {
		t.Fatalf("snapshot list --sort created_at --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][0], rows[2][0], rows[3][0]}
	want := []string{"third", "second", "first"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestCLI_SnapshotList_RejectsInvalidSort(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-bad-sort"})

	_, err := runCLI("snapshot", "list", "vm-bad-sort", "--sort", "description")
	if err == nil || !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("expected invalid --sort error, got %v", err)
	}
}

func TestCLI_SnapshotList_RejectsInvalidOrder(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-bad-order"})

	_, err := runCLI("snapshot", "list", "vm-bad-order", "--order", "sideways")
	if err == nil || !strings.Contains(err.Error(), "invalid --order") {
		t.Fatalf("expected invalid --order error, got %v", err)
	}
}

func TestCLI_SnapshotList_SearchByName(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s-search", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-search", Name: "pre-upgrade"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-search", Name: "rollback-point"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-search", Name: "weekly-backup"})

	out, err := runCLI("snapshot", "list", "vm-s-search", "--search", "upgrade")
	if err != nil {
		t.Fatalf("snapshot list --search: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 match, got %d rows: %v", len(rows), rows)
	}
	if rows[1][0] != "pre-upgrade" {
		t.Fatalf("expected match pre-upgrade, got %q", rows[1][0])
	}
}

func TestCLI_SnapshotList_SearchByDescription(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s-sdesc", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-sdesc", Name: "snap-001", Description: "Before applying CIS hardening playbook"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-sdesc", Name: "snap-002", Description: "Routine nightly cut"})

	out, err := runCLI("snapshot", "list", "vm-s-sdesc", "--search", "hardening")
	if err != nil {
		t.Fatalf("snapshot list --search hardening: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 match, got %d rows: %v", len(rows), rows)
	}
	if rows[1][0] != "snap-001" {
		t.Fatalf("expected match snap-001, got %q", rows[1][0])
	}
}

func TestCLI_SnapshotList_SearchCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s-scase", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-scase", Name: "Pre-Upgrade"})

	out, err := runCLI("snapshot", "list", "vm-s-scase", "--search", "UPGRADE")
	if err != nil {
		t.Fatalf("snapshot list --search UPGRADE: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 match (case-insensitive), got %d rows: %v", len(rows), rows)
	}
}

func TestCLI_SnapshotList_SearchNoMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s-snomatch", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-snomatch", Name: "alpha"})

	out, err := runCLI("snapshot", "list", "vm-s-snomatch", "--search", "needle-not-present")
	if err != nil {
		t.Fatalf("snapshot list --search no-match: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected header-only output for no match, got %d rows: %v", len(rows), rows)
	}
}

func TestCLI_SnapshotList_SearchComposesWithSort(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-s-scompose", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-scompose", Name: "upgrade-beta"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-scompose", Name: "rollback"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-s-scompose", Name: "upgrade-alpha"})

	out, err := runCLI("snapshot", "list", "vm-s-scompose", "--search", "upgrade", "--sort", "name")
	if err != nil {
		t.Fatalf("snapshot list --search + --sort: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 matches, got %d rows: %v", len(rows), rows)
	}
	got := []string{rows[1][0], rows[2][0]}
	want := []string{"upgrade-alpha", "upgrade-beta"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestCLI_SnapshotRestore(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-r", Name: "restorable"})
	mock.CreateSnapshot(nil, "vm-r", types.SnapshotSpec{Name: "good-state"})

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
	mock.CreateSnapshot(nil, "vm-del", types.SnapshotSpec{Name: "temp-snap"})

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

func TestCLI_SnapshotDelete_NoFlags(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()
	if _, err := runCLI("snapshot", "delete", "vm-x"); err == nil {
		t.Fatal("expected error when neither --name nor --prefix is given")
	}
}

func TestCLI_SnapshotDelete_BothFlags(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()
	if _, err := runCLI("snapshot", "delete", "vm-x", "--name", "a", "--prefix", "b-"); err == nil {
		t.Fatal("expected error when both --name and --prefix are given")
	}
}

func TestCLI_SnapshotDelete_PrefixDeletesAllMatching(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-bd", Name: "snapper"})
	for _, n := range []string{"manual-rollback", "auto-nightly-1", "auto-nightly-2", "auto-weekly-1"} {
		mock.CreateSnapshot(nil, "vm-bd", types.SnapshotSpec{Name: n})
	}

	out, err := runCLI("snapshot", "delete", "vm-bd", "--prefix", "auto-nightly-")
	if err != nil {
		t.Fatalf("snapshot delete --prefix: %v", err)
	}
	if !strings.Contains(out, "auto-nightly-1") || !strings.Contains(out, "auto-nightly-2") {
		t.Errorf("expected both deleted names in output, got: %q", out)
	}

	survivors, _ := mock.ListSnapshots(nil, "vm-bd")
	want := map[string]bool{"manual-rollback": true, "auto-weekly-1": true}
	if len(survivors) != 2 {
		t.Fatalf("survivors = %d, want 2", len(survivors))
	}
	for _, s := range survivors {
		if !want[s.Name] {
			t.Errorf("unexpected survivor: %s", s.Name)
		}
	}
}

func TestCLI_SnapshotDelete_PrefixNoMatchPrintsMessage(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-nm"})
	mock.CreateSnapshot(nil, "vm-nm", types.SnapshotSpec{Name: "manual-1"})

	out, err := runCLI("snapshot", "delete", "vm-nm", "--prefix", "auto-")
	if err != nil {
		t.Fatalf("snapshot delete --prefix: %v", err)
	}
	if !strings.Contains(out, "No snapshots match prefix") {
		t.Errorf("expected no-match message, got: %q", out)
	}

	snaps, _ := mock.ListSnapshots(nil, "vm-nm")
	if len(snaps) != 1 {
		t.Errorf("survivors = %d, want 1 (manual-1 untouched)", len(snaps))
	}
}

func TestCLI_SnapshotEdit_SetsDescription(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-edit", Name: "snapper"})
	mock.CreateSnapshot(nil, "vm-edit", types.SnapshotSpec{Name: "snap-x", Description: "old"})

	out, err := runCLI("snapshot", "edit", "vm-edit", "snap-x", "--description", "new desc")
	if err != nil {
		t.Fatalf("snapshot edit: %v", err)
	}
	if !strings.Contains(out, "snap-x") {
		t.Errorf("expected snapshot name in output, got: %q", out)
	}
	if !strings.Contains(out, "new desc") {
		t.Errorf("expected new description in output, got: %q", out)
	}

	snaps, _ := mock.ListSnapshots(nil, "vm-edit")
	if len(snaps) != 1 || snaps[0].Description != "new desc" {
		t.Errorf("manager state did not update, got %+v", snaps)
	}
}

func TestCLI_SnapshotEdit_ClearsDescription(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-clr", Name: "snapper"})
	mock.CreateSnapshot(nil, "vm-clr", types.SnapshotSpec{Name: "snap-c", Description: "stale"})

	if _, err := runCLI("snapshot", "edit", "vm-clr", "snap-c", "--description", ""); err != nil {
		t.Fatalf("snapshot edit: %v", err)
	}
	snaps, _ := mock.ListSnapshots(nil, "vm-clr")
	if len(snaps) != 1 || snaps[0].Description != "" {
		t.Errorf("expected cleared description, got %+v", snaps)
	}
}

func TestCLI_SnapshotEdit_NoFlagIsNoOp(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-noop", Name: "snapper"})
	mock.CreateSnapshot(nil, "vm-noop", types.SnapshotSpec{Name: "snap-n", Description: "kept"})

	if _, err := runCLI("snapshot", "edit", "vm-noop", "snap-n"); err != nil {
		t.Fatalf("snapshot edit: %v", err)
	}
	snaps, _ := mock.ListSnapshots(nil, "vm-noop")
	if len(snaps) != 1 || snaps[0].Description != "kept" {
		t.Errorf("expected description untouched, got %+v", snaps)
	}
}

func TestCLI_SnapshotEdit_DescriptionTooLong(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-len", Name: "snapper"})
	mock.CreateSnapshot(nil, "vm-len", types.SnapshotSpec{Name: "snap-l"})

	long := strings.Repeat("y", 1025)
	if _, err := runCLI("snapshot", "edit", "vm-len", "snap-l", "--description", long); err == nil {
		t.Fatal("expected error for description over 1024 chars")
	}
}

func TestCLI_SnapshotEdit_NotFound(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-nf", Name: "snapper"})

	if _, err := runCLI("snapshot", "edit", "vm-nf", "missing", "--description", "anything"); err == nil {
		t.Fatal("expected error for missing snapshot")
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
	wantHeaders := []string{"ID", "NAME", "SIZE", "FORMAT", "TAGS", "CREATED"}
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

func TestCLI_ImageList_SortByName(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-3", Name: "Charlie", Path: "/t/c.qcow2", SizeBytes: 100, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-1", Name: "alpha", Path: "/t/a.qcow2", SizeBytes: 100, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-2", Name: "Bravo", Path: "/t/b.qcow2", SizeBytes: 100, Format: "qcow2", CreatedAt: now})

	out, err := runCLI("image", "list", "--sort", "name")
	if err != nil {
		t.Fatalf("image list --sort name: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"alpha", "Bravo", "Charlie"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_ImageList_SortBySizeDesc(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-small", Name: "small", Path: "/t/s.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-big", Name: "big", Path: "/t/b.qcow2", SizeBytes: 1 << 30, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-mid", Name: "mid", Path: "/t/m.qcow2", SizeBytes: 1 << 20, Format: "qcow2", CreatedAt: now})

	out, err := runCLI("image", "list", "--sort", "size", "--order", "desc")
	if err != nil {
		t.Fatalf("image list --sort size --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d", len(rows))
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"big", "mid", "small"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestCLI_ImageList_RejectsInvalidSort(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "list", "--sort", "format")
	if err == nil || !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("expected invalid --sort error, got %v", err)
	}
}

func TestCLI_ImageList_RejectsInvalidOrder(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "list", "--order", "sideways")
	if err == nil || !strings.Contains(err.Error(), "invalid --order") {
		t.Fatalf("expected invalid --order error, got %v", err)
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

func TestCLI_ImageDelete_NoArgsErrors(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI("image", "delete"); err == nil {
		t.Error("expected error when neither image-id nor --tag is provided")
	}
}

func TestCLI_ImageDelete_BothIDAndTagErrors(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI("image", "delete", "img-x", "--tag", "rc"); err == nil {
		t.Error("expected error when both image-id and --tag are provided")
	}
}

func TestCLI_ImageDelete_TagDeletesAllMatching(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	dir := t.TempDir()
	for _, spec := range []struct {
		id, name string
		tags     []string
	}{
		{"img-prod", "prod", []string{"prod"}},
		{"img-rc-a", "rc-a", []string{"rc-2026-05"}},
		{"img-rc-b", "rc-b", []string{"rc-2026-05", "linux"}},
		{"img-rc-old", "rc-old", []string{"rc-2026-04"}},
	} {
		path := filepath.Join(dir, spec.name+".qcow2")
		os.WriteFile(path, []byte("seed"), 0o644)
		s.PutImage(&types.Image{
			ID: spec.id, Name: spec.name, Path: path, SizeBytes: 4, Format: "qcow2",
			Tags: spec.tags, CreatedAt: time.Now(),
		})
	}

	out, err := runCLI("image", "delete", "--tag", "RC-2026-05")
	if err != nil {
		t.Fatalf("image delete --tag: %v", err)
	}
	if !strings.Contains(out, "img-rc-a") || !strings.Contains(out, "img-rc-b") {
		t.Errorf("expected both deleted ids in output, got: %q", out)
	}

	survivors, _ := s.ListImages()
	want := map[string]bool{"img-prod": true, "img-rc-old": true}
	if len(survivors) != 2 {
		t.Fatalf("survivors = %d, want 2", len(survivors))
	}
	for _, img := range survivors {
		if !want[img.ID] {
			t.Errorf("unexpected survivor: %s", img.ID)
		}
	}
}

func TestCLI_ImageDelete_TagNoMatchPrintsMessage(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	s.PutImage(&types.Image{
		ID: "img-keep", Name: "keep", Path: "/tmp/keep.qcow2", SizeBytes: 4,
		Format: "qcow2", Tags: []string{"prod"}, CreatedAt: time.Now(),
	})

	out, err := runCLI("image", "delete", "--tag", "missing")
	if err != nil {
		t.Fatalf("image delete --tag: %v", err)
	}
	if !strings.Contains(out, "No images carry tag") {
		t.Errorf("expected no-match message, got: %q", out)
	}

	imgs, _ := s.ListImages()
	if len(imgs) != 1 {
		t.Errorf("survivors = %d, want 1 (img-keep untouched)", len(imgs))
	}
}

func TestCLI_ImageEdit_DescriptionAndTags(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	s.PutImage(&types.Image{
		ID:          "img-meta",
		Name:        "lab-image",
		Path:        "/tmp/lab.qcow2",
		SizeBytes:   1024,
		Format:      "qcow2",
		Description: "old",
		Tags:        []string{"old"},
		CreatedAt:   time.Now(),
	})

	out, err := runCLI("image", "edit", "img-meta",
		"--description", "promoted to release",
		"--tag", "rocky",
		"--tag", "rc",
	)
	if err != nil {
		t.Fatalf("image edit: %v", err)
	}
	if !strings.Contains(out, "Image img-meta updated") {
		t.Fatalf("unexpected output: %q", out)
	}

	stored, err := s.GetImage("img-meta")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if stored.Description != "promoted to release" {
		t.Errorf("Description = %q", stored.Description)
	}
	if got := stored.Tags; len(got) != 2 || got[0] != "rc" || got[1] != "rocky" {
		t.Errorf("Tags = %v, want [rc rocky]", got)
	}
}

func TestCLI_ImageEdit_NoFlagsErrors(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	s.PutImage(&types.Image{ID: "img-noop", Name: "noop", Path: "/tmp/x.qcow2", Format: "qcow2", CreatedAt: time.Now()})

	if _, err := runCLI("image", "edit", "img-noop"); err == nil {
		t.Error("expected error when no flags provided")
	}
}

func TestCLI_ImageList_FilterByTag(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-qa", Name: "qa-image", Path: "/tmp/qa.qcow2", SizeBytes: 1024, Format: "qcow2", Tags: []string{"qa"}, CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-prod", Name: "prod-image", Path: "/tmp/prod.qcow2", SizeBytes: 1024, Format: "qcow2", Tags: []string{"prod"}, CreatedAt: now})

	out, err := runCLI("image", "list", "--tag", "PROD")
	if err != nil {
		t.Fatalf("image list --tag: %v", err)
	}
	if !strings.Contains(out, "prod-image") || strings.Contains(out, "qa-image") {
		t.Fatalf("filter result = %q", out)
	}
}

// --- Image list --search (5.4.9) ---

func TestCLI_ImageList_FilterBySearch_MatchesName(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-rocky", Name: "rocky9-base", Path: "/tmp/r.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-ubuntu", Name: "ubuntu-22", Path: "/tmp/u.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: now})

	out, err := runCLI("image", "list", "--search", "rocky")
	if err != nil {
		t.Fatalf("image list --search: %v", err)
	}
	if !strings.Contains(out, "rocky9-base") {
		t.Fatalf("expected rocky9-base in output, got %q", out)
	}
	if strings.Contains(out, "ubuntu-22") {
		t.Fatalf("did not expect ubuntu-22 in output, got %q", out)
	}
}

func TestCLI_ImageList_FilterBySearch_MatchesDescription(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-1", Name: "alpha", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", Description: "Hardened CIS-1 build", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-2", Name: "beta", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", Description: "Stock cloud image", CreatedAt: now})

	out, err := runCLI("image", "list", "--search", "hardened")
	if err != nil {
		t.Fatalf("image list --search: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("filter on description failed, got %q", out)
	}
}

func TestCLI_ImageList_FilterBySearch_MatchesTag(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-1", Name: "alpha", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", Tags: []string{"team-storage"}, CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-2", Name: "beta", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", Tags: []string{"team-net"}, CreatedAt: now})

	out, err := runCLI("image", "list", "--search", "storage")
	if err != nil {
		t.Fatalf("image list --search: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("filter on tag failed, got %q", out)
	}
}

func TestCLI_ImageList_FilterBySearch_NoMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-1", Name: "alpha", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: now})

	out, err := runCLI("image", "list", "--search", "needle-not-present")
	if err != nil {
		t.Fatalf("image list --search: %v", err)
	}
	if strings.Contains(out, "alpha") {
		t.Fatalf("expected empty list for no-match, got %q", out)
	}
}

func TestCLI_ImageList_FilterBySearch_CombinesWithTag(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-1", Name: "rocky9-prod", Path: "/tmp/1.qcow2", SizeBytes: 1024, Format: "qcow2", Tags: []string{"prod"}, CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-2", Name: "rocky9-qa", Path: "/tmp/2.qcow2", SizeBytes: 1024, Format: "qcow2", Tags: []string{"qa"}, CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-3", Name: "ubuntu-prod", Path: "/tmp/3.qcow2", SizeBytes: 1024, Format: "qcow2", Tags: []string{"prod"}, CreatedAt: now})

	out, err := runCLI("image", "list", "--search", "rocky", "--tag", "prod")
	if err != nil {
		t.Fatalf("image list --search --tag: %v", err)
	}
	if !strings.Contains(out, "rocky9-prod") {
		t.Fatalf("expected rocky9-prod (intersection of search+tag), got %q", out)
	}
	if strings.Contains(out, "rocky9-qa") || strings.Contains(out, "ubuntu-prod") {
		t.Fatalf("did not expect non-intersecting images, got %q", out)
	}
}

func TestCLI_TemplateCreate_List_Delete(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	out, err := runCLI(
		"template", "create", "web-template",
		"--image", "ubuntu-24.04",
		"--cpus", "4",
		"--ram", "8192",
		"--disk", "80",
		"--description", "Production web template",
		"--tag", "Prod",
		"--tag", "web",
		"--default-user", "ubuntu",
	)
	if err != nil {
		t.Fatalf("template create: %v", err)
	}
	if !strings.Contains(out, "Template created successfully") {
		t.Fatalf("expected success output, got %q", out)
	}

	storedTemplates, err := s.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(storedTemplates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(storedTemplates))
	}
	stored := storedTemplates[0]
	if !strings.HasPrefix(stored.ID, "tmpl-") {
		t.Fatalf("stored.ID = %q, want tmpl-*", stored.ID)
	}
	if stored.CreatedAt.IsZero() || stored.UpdatedAt.IsZero() {
		t.Fatalf("expected CreatedAt/UpdatedAt to be set, got %#v", stored)
	}
	if stored.Name != "web-template" || stored.Image != "ubuntu-24.04" {
		t.Fatalf("stored template = %#v", stored)
	}
	if stored.CPUs != 4 || stored.RAMMB != 8192 || stored.DiskGB != 80 {
		t.Fatalf("unexpected sizing in template: %#v", stored)
	}
	if stored.DefaultUser != "ubuntu" {
		t.Fatalf("DefaultUser = %q, want ubuntu", stored.DefaultUser)
	}
	if strings.Join(stored.Tags, ",") != "prod,web" {
		t.Fatalf("Tags = %v", stored.Tags)
	}
	if !strings.Contains(out, "ID:    "+stored.ID) {
		t.Fatalf("expected create output to include template id %q, got %q", stored.ID, out)
	}

	listOut, err := runCLI("template", "list")
	if err != nil {
		t.Fatalf("template list: %v", err)
	}
	rows := tableRows(t, listOut)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d rows: %q", len(rows), listOut)
	}
	wantHeader := []string{"ID", "NAME", "IMAGE", "CPUS", "RAM (MB)", "DISK (GB)", "TAGS"}
	if strings.Join(rows[0], "|") != strings.Join(wantHeader, "|") {
		t.Fatalf("header = %v, want %v", rows[0], wantHeader)
	}
	if rows[1][0] != stored.ID || rows[1][1] != "web-template" || rows[1][2] != "ubuntu-24.04" || rows[1][3] != "4" || rows[1][4] != "8192" || rows[1][5] != "80" || rows[1][6] != "prod,web" {
		t.Fatalf("unexpected template row: %v", rows[1])
	}

	deleteOut, err := runCLI("template", "delete", stored.ID)
	if err != nil {
		t.Fatalf("template delete: %v", err)
	}
	if got := strings.TrimSpace(deleteOut); got != "Template "+stored.ID+" deleted" {
		t.Fatalf("delete output = %q, want exact deleted template id", got)
	}

	storedTemplates, err = s.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates after delete: %v", err)
	}
	if len(storedTemplates) != 0 {
		t.Fatalf("expected templates to be deleted, got %d", len(storedTemplates))
	}
}

func TestCLI_TemplateEdit_DescriptionAndTags(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI(
		"template", "create", "edit-target",
		"--image", "ubuntu-24.04",
		"--tag", "first",
	); err != nil {
		t.Fatalf("template create: %v", err)
	}

	templates, err := s.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	id := templates[0].ID

	out, err := runCLI(
		"template", "edit", id,
		"--description", "edited",
		"--tag", "second",
		"--tag", "third",
	)
	if err != nil {
		t.Fatalf("template edit: %v", err)
	}
	if !strings.Contains(out, "updated") {
		t.Fatalf("expected confirmation, got %q", out)
	}

	updated, err := s.GetTemplate(id)
	if err != nil {
		t.Fatalf("GetTemplate: %v", err)
	}
	if updated.Description != "edited" {
		t.Errorf("Description = %q", updated.Description)
	}
	if got := strings.Join(updated.Tags, ","); got != "second,third" {
		t.Errorf("Tags = %q, want second,third", got)
	}
}

func TestCLI_TemplateEdit_NoFlagsErrors(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI("template", "create", "no-edit", "--image", "ubuntu"); err != nil {
		t.Fatalf("template create: %v", err)
	}

	if _, err := runCLI("template", "edit", "tmpl-anything"); err == nil {
		t.Error("expected error when no edit flags are provided")
	}
}

func TestCLI_TemplateEdit_ClearTags(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI(
		"template", "create", "clear-target",
		"--image", "ubuntu",
		"--tag", "drop1",
		"--tag", "drop2",
	); err != nil {
		t.Fatalf("template create: %v", err)
	}

	tpls, _ := s.ListTemplates()
	id := tpls[0].ID

	if _, err := runCLI("template", "edit", id, "--clear-tags"); err != nil {
		t.Fatalf("template edit --clear-tags: %v", err)
	}
	updated, _ := s.GetTemplate(id)
	if len(updated.Tags) != 0 {
		t.Errorf("Tags = %v, want []", updated.Tags)
	}
}

func TestCLI_TemplateList_FilterByTag(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI("template", "create", "alpha", "--image", "ubuntu", "--tag", "prod"); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := runCLI("template", "create", "beta", "--image", "ubuntu", "--tag", "dev"); err != nil {
		t.Fatalf("create beta: %v", err)
	}

	out, err := runCLI("template", "list", "--tag", "PROD")
	if err != nil {
		t.Fatalf("template list --tag: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d: %q", len(rows), out)
	}
	if rows[1][1] != "alpha" {
		t.Fatalf("filtered row name = %q, want alpha", rows[1][1])
	}
}

func TestCLI_TemplateCreate_RejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		code    string
		msgPart string
	}{
		{
			name:    "invalid name",
			args:    []string{"template", "create", "bad name", "--image", "ubuntu-24.04"},
			code:    "invalid_name",
			msgPart: "template name must be",
		},
		{
			name:    "invalid cpu",
			args:    []string{"template", "create", "web-template", "--image", "ubuntu-24.04", "--cpus", "129"},
			code:    "invalid_spec",
			msgPart: "cpus must be between 1 and 128",
		},
		{
			name:    "empty tag",
			args:    []string{"template", "create", "web-template", "--image", "ubuntu-24.04", "--tag", " ", "--tag", "prod"},
			code:    "invalid_spec",
			msgPart: "tags cannot contain empty values",
		},
		{
			name:    "duplicate name",
			args:    []string{"template", "create", "existing-template", "--image", "ubuntu-24.04"},
			code:    "invalid_name",
			msgPart: "already exists",
		},
	}

	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	s.PutTemplate(&types.VMTemplate{ID: "tmpl-existing", Name: "existing-template", Image: "ubuntu-22.04", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runCLI(tt.args...)
			if err == nil {
				t.Fatal("expected error")
			}
			apiErr, ok := err.(*types.APIError)
			if !ok {
				t.Fatalf("expected APIError, got %T: %v", err, err)
			}
			if apiErr.Code != tt.code {
				t.Fatalf("error code = %q, want %q", apiErr.Code, tt.code)
			}
			if !strings.Contains(apiErr.Message, tt.msgPart) {
				t.Fatalf("error message = %q, want substring %q", apiErr.Message, tt.msgPart)
			}
		})
	}
}

func TestCLI_TemplateDelete_NotFound(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI("template", "delete", "tmpl-missing"); err == nil {
		t.Error("expected error for nonexistent template")
	}
}

func TestCLI_TemplateDelete_NoArgsErrors(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI("template", "delete"); err == nil {
		t.Error("expected error when neither template-id nor --tag is provided")
	}
}

func TestCLI_TemplateDelete_BothIDAndTagErrors(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	if _, err := runCLI("template", "delete", "tmpl-x", "--tag", "legacy"); err == nil {
		t.Error("expected error when both template-id and --tag are provided")
	}
}

func TestCLI_TemplateDelete_TagDeletesAllMatching(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	for _, spec := range []struct {
		id, name string
		tags     []string
	}{
		{"tmpl-prod", "prod", []string{"prod"}},
		{"tmpl-legacy-a", "legacy-a", []string{"legacy-rocky8"}},
		{"tmpl-legacy-b", "legacy-b", []string{"legacy-rocky8", "linux"}},
		{"tmpl-keep", "keep", []string{"rc-2026-05"}},
	} {
		s.PutTemplate(&types.VMTemplate{
			ID: spec.id, Name: spec.name, Image: "rocky9.qcow2",
			Tags: spec.tags, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
	}

	// Tag matching is case-insensitive.
	out, err := runCLI("template", "delete", "--tag", "LEGACY-ROCKY8")
	if err != nil {
		t.Fatalf("template delete --tag: %v", err)
	}
	if !strings.Contains(out, "tmpl-legacy-a") || !strings.Contains(out, "tmpl-legacy-b") {
		t.Errorf("expected both deleted ids in output, got: %q", out)
	}

	survivors, _ := s.ListTemplates()
	want := map[string]bool{"tmpl-prod": true, "tmpl-keep": true}
	if len(survivors) != 2 {
		t.Fatalf("survivors = %d, want 2", len(survivors))
	}
	for _, tpl := range survivors {
		if !want[tpl.ID] {
			t.Errorf("unexpected survivor: %s", tpl.ID)
		}
	}
}

func TestCLI_TemplateDelete_TagNoMatchPrintsMessage(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	s.PutTemplate(&types.VMTemplate{
		ID: "tmpl-keep", Name: "keep", Image: "rocky9.qcow2",
		Tags: []string{"prod"}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})

	out, err := runCLI("template", "delete", "--tag", "missing")
	if err != nil {
		t.Fatalf("template delete --tag: %v", err)
	}
	if !strings.Contains(out, "No templates carry tag") {
		t.Errorf("expected no-match message, got: %q", out)
	}

	tpls, _ := s.ListTemplates()
	if len(tpls) != 1 {
		t.Errorf("survivors = %d, want 1 (tmpl-keep untouched)", len(tpls))
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
		ID:          "pf-1",
		VMID:        "vm-x",
		HostPort:    2222,
		GuestPort:   22,
		GuestIP:     "192.168.100.10",
		Protocol:    types.ProtocolTCP,
		Description: "ssh-jumpbox",
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
	wantHeaders := []string{"ID", "HOST PORT", "GUEST", "PROTOCOL", "DESCRIPTION", "TAGS"}
	if strings.Join(headers, "|") != strings.Join(wantHeaders, "|") {
		t.Fatalf("headers = %v, want %v", headers, wantHeaders)
	}

	if got := strings.Join(rows[1], "|"); got != "pf-1|2222|192.168.100.10:22|tcp|ssh-jumpbox" {
		t.Fatalf("first row = %v", rows[1])
	}
	if got := strings.Join(rows[2], "|"); got != "pf-2|8443|192.168.100.10:443|udp" {
		t.Fatalf("second row = %v", rows[2])
	}
}

func seedPortListFixtures(t *testing.T, s *store.Store) {
	t.Helper()
	pfs := []*types.PortForward{
		{ID: "vm-s/22001", VMID: "vm-s", HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "ssh jumpbox"},
		{ID: "vm-s/8081", VMID: "vm-s", HostPort: 8081, GuestPort: 80, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "web frontend"},
		{ID: "vm-s/9090", VMID: "vm-s", HostPort: 9090, GuestPort: 9090, GuestIP: "192.168.100.40", Protocol: types.ProtocolUDP, Description: "Metrics scrape"},
	}
	for _, p := range pfs {
		if err := s.PutPortForward(p); err != nil {
			t.Fatalf("seed %s: %v", p.ID, err)
		}
	}
}

func TestCLI_PortList_SortByHostPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--sort", "host_port")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 4 {
		t.Fatalf("expected header + 3 rows, got %d rows from %q", len(rows), out)
	}
	wantHostPorts := []string{"8081", "9090", "22001"}
	for i, hp := range wantHostPorts {
		if rows[i+1][1] != hp {
			t.Errorf("row %d host_port = %q, want %q (full: %v)", i, rows[i+1][1], hp, rows[i+1])
		}
	}
}

func TestCLI_PortList_SortByHostPortDesc(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--sort", "host_port", "--order", "desc")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	wantHostPorts := []string{"22001", "9090", "8081"}
	for i, hp := range wantHostPorts {
		if rows[i+1][1] != hp {
			t.Errorf("row %d host_port = %q, want %q", i, rows[i+1][1], hp)
		}
	}
}

func TestCLI_PortList_SortByDescription(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--sort", "description")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	// case-insensitive: "metrics" < "ssh" < "web"
	wantDescriptions := []string{"Metrics scrape", "ssh jumpbox", "web frontend"}
	for i, d := range wantDescriptions {
		if rows[i+1][4] != d {
			t.Errorf("row %d description = %q, want %q", i, rows[i+1][4], d)
		}
	}
}

func TestCLI_PortList_RejectsInvalidSort(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	_, err := runCLI("port", "list", "vm-s", "--sort", "guest_ip")
	if err == nil {
		t.Fatalf("expected error for invalid --sort")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Errorf("err = %v, want 'invalid --sort'", err)
	}
}

func TestCLI_PortList_RejectsInvalidOrder(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	_, err := runCLI("port", "list", "vm-s", "--order", "sideways")
	if err == nil {
		t.Fatalf("expected error for invalid --order")
	}
	if !strings.Contains(err.Error(), "invalid --order") {
		t.Errorf("err = %v, want 'invalid --order'", err)
	}
}

// --- Port forward search filter (5.4.11) ---

func TestCLI_PortList_FilterBySearch_Description(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--search", "ssh")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d rows from %q", len(rows), out)
	}
	if rows[1][1] != "22001" {
		t.Errorf("row host_port = %q, want 22001", rows[1][1])
	}
}

func TestCLI_PortList_FilterBySearch_Protocol(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--search", "udp")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d rows from %q", len(rows), out)
	}
	if rows[1][3] != "udp" {
		t.Errorf("row protocol = %q, want udp", rows[1][3])
	}
}

func TestCLI_PortList_FilterBySearch_HostPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--search", "8081")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d rows from %q", len(rows), out)
	}
	if rows[1][1] != "8081" {
		t.Errorf("row host_port = %q, want 8081", rows[1][1])
	}
}

func TestCLI_PortList_FilterBySearch_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	// "Metrics scrape" description on the udp rule; uppercase needle must match.
	out, err := runCLI("port", "list", "vm-s", "--search", "METRICS")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d rows from %q", len(rows), out)
	}
	if rows[1][1] != "9090" {
		t.Errorf("row host_port = %q, want 9090", rows[1][1])
	}
}

func TestCLI_PortList_FilterBySearch_NoMatch(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--search", "needle-not-present")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected header only, got %d rows from %q", len(rows), out)
	}
}

func TestCLI_PortList_FilterBySearch_ComposesWithSort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)
	// Add a second tcp rule whose description also contains "web" so the
	// sort step has something to do after the search filter.
	if err := s.PutPortForward(&types.PortForward{
		ID: "vm-s/8082", VMID: "vm-s", HostPort: 8082, GuestPort: 81,
		GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "web backend",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("port", "list", "vm-s", "--search", "web", "--sort", "host_port")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}
	if rows[1][1] != "8081" || rows[2][1] != "8082" {
		t.Errorf("rows host_port = (%q,%q), want (8081,8082)", rows[1][1], rows[2][1])
	}
}

// ============================================================
// Port-list pagination flags (5.4.20)
// ============================================================

func TestCLI_PortList_LimitTruncates(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--sort", "host_port", "--limit", "2")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}
	wantHostPorts := []string{"8081", "9090"}
	for i, hp := range wantHostPorts {
		if rows[i+1][1] != hp {
			t.Errorf("row %d host_port = %q, want %q", i, rows[i+1][1], hp)
		}
	}
}

func TestCLI_PortList_OffsetSkipsFromStart(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--sort", "host_port", "--offset", "1")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d rows from %q", len(rows), out)
	}
	wantHostPorts := []string{"9090", "22001"}
	for i, hp := range wantHostPorts {
		if rows[i+1][1] != hp {
			t.Errorf("row %d host_port = %q, want %q", i, rows[i+1][1], hp)
		}
	}
}

func TestCLI_PortList_LimitAndOffsetTogether(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	// sort=host_port asc => [8081, 9090, 22001]; offset=1, limit=1 => [9090]
	out, err := runCLI("port", "list", "vm-s", "--sort", "host_port", "--offset", "1", "--limit", "1")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d rows from %q", len(rows), out)
	}
	if rows[1][1] != "9090" {
		t.Errorf("got host_port = %q, want 9090", rows[1][1])
	}
}

func TestCLI_PortList_OffsetBeyondEnd(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--offset", "99")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("expected header only, got %d rows from %q", len(rows), out)
	}
}

func TestCLI_PortList_RejectsNegativeLimit(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	_, err := runCLI("port", "list", "vm-s", "--limit", "-1")
	if err == nil {
		t.Fatalf("expected --limit < 0 to error")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("err = %v, want it to mention 'limit'", err)
	}
}

func TestCLI_PortList_RejectsNegativeOffset(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	_, err := runCLI("port", "list", "vm-s", "--offset", "-1")
	if err == nil {
		t.Fatalf("expected --offset < 0 to error")
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("err = %v, want it to mention 'offset'", err)
	}
}

func TestCLI_PortList_PaginationComposesWithFilter(t *testing.T) {
	// Filter narrows to 2 rules, then offset + limit slice within that.
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	for _, p := range []*types.PortForward{
		{ID: "vm-s/8081", VMID: "vm-s", HostPort: 8081, GuestPort: 80, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "web frontend", Tags: []string{"prod"}},
		{ID: "vm-s/9090", VMID: "vm-s", HostPort: 9090, GuestPort: 9090, GuestIP: "192.168.100.40", Protocol: types.ProtocolUDP, Description: "metrics", Tags: []string{"prod"}},
		{ID: "vm-s/2222", VMID: "vm-s", HostPort: 2222, GuestPort: 22, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP, Description: "ssh jumpbox", Tags: []string{"dev"}},
	} {
		if err := s.PutPortForward(p); err != nil {
			t.Fatalf("seed %s: %v", p.ID, err)
		}
	}

	// tag=prod -> [8081, 9090]; sort=host_port desc -> [9090, 8081];
	// offset=1, limit=1 -> [8081]
	out, err := runCLI("port", "list", "vm-s", "--tag", "prod", "--sort", "host_port", "--order", "desc", "--offset", "1", "--limit", "1")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 row, got %d rows from %q", len(rows), out)
	}
	if rows[1][1] != "8081" {
		t.Errorf("got host_port = %q, want 8081", rows[1][1])
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

func TestCLI_PortAdd_DescriptionTooLong(t *testing.T) {
	calledVMManager := false
	vmManagerOverride = func() (vm.Manager, func(), error) {
		calledVMManager = true
		return vm.NewMockManager(), func() {}, nil
	}
	defer func() { vmManagerOverride = nil }()

	_, _, pfCleanup := withTestPortForwarder(t)
	defer pfCleanup()

	_, err := runCLI("port", "add", "vm-any",
		"--host", "2222", "--guest", "22",
		"--description", strings.Repeat("x", 257))
	if err == nil {
		t.Fatal("expected validation error for over-long description")
	}
	if !strings.Contains(err.Error(), "description must be at most") {
		t.Fatalf("expected description-length error, got: %v", err)
	}
	if calledVMManager {
		t.Fatal("expected VM manager initialization to be skipped for invalid description")
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

func TestCLI_PortRemove_NoArgsErrors(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	_, err := runCLI("port", "remove")
	if err == nil {
		t.Fatal("expected error when neither id nor --vm is given")
	}
	if !strings.Contains(err.Error(), "either a port-forward-id") {
		t.Errorf("error = %v, want id-or-vm hint", err)
	}
}

func TestCLI_PortRemove_PositionalAndVMRejected(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	_, err := runCLI("port", "remove", "pf-1", "--vm", "vm-x")
	if err == nil {
		t.Fatal("expected error when both id and --vm given")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want mutual-exclusion hint", err)
	}
}

func TestCLI_PortRemove_InvalidProtocolRejected(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	_, err := runCLI("port", "remove", "--vm", "vm-x", "--protocol", "sctp")
	if err == nil {
		t.Fatal("expected error for invalid protocol")
	}
	if !strings.Contains(err.Error(), "tcp") || !strings.Contains(err.Error(), "udp") {
		t.Errorf("error = %v, want tcp/udp hint", err)
	}
}

func TestCLI_PortRemove_AllForVM(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	s.PutPortForward(&types.PortForward{ID: "pf-1", VMID: "vm-bulk", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})
	s.PutPortForward(&types.PortForward{ID: "pf-2", VMID: "vm-bulk", HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.10", Protocol: types.ProtocolUDP})

	out, err := runCLI("port", "remove", "--vm", "vm-bulk")
	if err != nil {
		t.Fatalf("port remove --vm: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "OK    pf-1") || !strings.Contains(out, "OK    pf-2") {
		t.Errorf("output missing per-rule OK lines: %q", out)
	}
	survivors, _ := s.ListPortForwards("vm-bulk")
	if len(survivors) != 0 {
		t.Errorf("expected all rules removed, survivors: %+v", survivors)
	}
}

func TestCLI_PortRemove_AllForVM_ProtocolFilter(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	s.PutPortForward(&types.PortForward{ID: "pf-tcp-1", VMID: "vm-bp", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.20", Protocol: types.ProtocolTCP})
	s.PutPortForward(&types.PortForward{ID: "pf-udp", VMID: "vm-bp", HostPort: 53, GuestPort: 53, GuestIP: "192.168.100.20", Protocol: types.ProtocolUDP})

	out, err := runCLI("port", "remove", "--vm", "vm-bp", "--protocol", "tcp")
	if err != nil {
		t.Fatalf("port remove --vm --protocol: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "pf-tcp-1") {
		t.Errorf("expected pf-tcp-1 in output, got: %q", out)
	}
	if strings.Contains(out, "pf-udp") {
		t.Errorf("expected pf-udp untouched, got: %q", out)
	}

	survivors, _ := s.ListPortForwards("vm-bp")
	if len(survivors) != 1 || survivors[0].ID != "pf-udp" {
		t.Errorf("survivors = %+v, want only pf-udp", survivors)
	}
}

func TestCLI_PortEdit_SetsDescription(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	s.PutPortForward(&types.PortForward{ID: "pf-edit", VMID: "vm-e", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})

	out, err := runCLI("port", "edit", "pf-edit", "--description", "ssh-jumpbox")
	if err != nil {
		t.Fatalf("port edit: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "Description: ssh-jumpbox") {
		t.Errorf("expected description in output, got %q", out)
	}

	stored, _ := s.ListPortForwards("vm-e")
	if len(stored) != 1 || stored[0].Description != "ssh-jumpbox" {
		t.Errorf("persisted description = %q, want ssh-jumpbox", stored[0].Description)
	}
}

func TestCLI_PortEdit_ClearsDescription(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	s.PutPortForward(&types.PortForward{ID: "pf-clear", VMID: "vm-c", HostPort: 9090, GuestPort: 90, GuestIP: "192.168.100.20", Protocol: types.ProtocolTCP, Description: "stale"})

	out, err := runCLI("port", "edit", "pf-clear", "--description", "")
	if err != nil {
		t.Fatalf("port edit (clear): %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "Description cleared") {
		t.Errorf("expected 'Description cleared' line, got %q", out)
	}

	stored, _ := s.ListPortForwards("vm-c")
	if len(stored) != 1 || stored[0].Description != "" {
		t.Errorf("description not cleared: %+v", stored)
	}
}

func TestCLI_PortEdit_RequiresFlag(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	s.PutPortForward(&types.PortForward{ID: "pf-x", VMID: "vm-x", HostPort: 1, GuestPort: 1, GuestIP: "192.168.100.30", Protocol: types.ProtocolTCP})

	_, err := runCLI("port", "edit", "pf-x")
	if err == nil {
		t.Fatal("expected error when no editable field is supplied")
	}
	if !strings.Contains(err.Error(), "--description") || !strings.Contains(err.Error(), "--tag") {
		t.Errorf("expected error mentioning --description and --tag, got %v", err)
	}
}

func TestCLI_PortEdit_DescriptionTooLong(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	s.PutPortForward(&types.PortForward{ID: "pf-long", VMID: "vm-l", HostPort: 22, GuestPort: 22, GuestIP: "192.168.100.40", Protocol: types.ProtocolTCP})

	_, err := runCLI("port", "edit", "pf-long", "--description", strings.Repeat("x", 257))
	if err == nil {
		t.Fatal("expected validation error for over-long description")
	}
	if !strings.Contains(err.Error(), "description must be at most") {
		t.Errorf("expected length error, got %v", err)
	}
}

func TestCLI_PortEdit_NotFound(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	_, err := runCLI("port", "edit", "pf-missing", "--description", "x")
	if err == nil {
		t.Fatal("expected resource_not_found error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.Code != "resource_not_found" {
		t.Errorf("Code = %q, want resource_not_found", apiErr.Code)
	}
}

func TestCLI_PortAdd_WithTags(t *testing.T) {
	mock, vmCleanup := withMockVM(t)
	defer vmCleanup()
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-tag", Name: "tag", IP: "192.168.100.10"})

	out, err := runCLI("port", "add", "vm-tag", "--host", "8080", "--guest", "80",
		"--tag", "PRODUCTION", "--tag", "web", "--tag", "production")
	if err != nil {
		t.Fatalf("port add: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "Tags: production, web") {
		t.Errorf("expected normalised tags line in output, got %q", out)
	}

	stored, _ := s.ListPortForwards("vm-tag")
	if len(stored) != 1 || strings.Join(stored[0].Tags, ",") != "production,web" {
		t.Errorf("persisted Tags = %v", stored[0].Tags)
	}
}

func TestCLI_PortAdd_RejectsInvalidTag(t *testing.T) {
	mock, vmCleanup := withMockVM(t)
	defer vmCleanup()
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-tag", Name: "tag", IP: "192.168.100.10"})

	_, err := runCLI("port", "add", "vm-tag", "--host", "8080", "--guest", "80",
		"--tag", "has spaces")
	if err == nil {
		t.Fatal("expected validation error for invalid tag")
	}
}

func TestCLI_PortEdit_SetsTags(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	s.PutPortForward(&types.PortForward{ID: "pf-tag", VMID: "vm-e", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})

	out, err := runCLI("port", "edit", "pf-tag", "--tag", "audit", "--tag", "production")
	if err != nil {
		t.Fatalf("port edit: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "Tags: audit, production") {
		t.Errorf("expected normalised tags line in output, got %q", out)
	}

	stored, _ := s.ListPortForwards("vm-e")
	if len(stored) != 1 || strings.Join(stored[0].Tags, ",") != "audit,production" {
		t.Errorf("persisted Tags = %v", stored[0].Tags)
	}
}

func TestCLI_PortEdit_ClearsTags(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	s.PutPortForward(&types.PortForward{ID: "pf-tag", VMID: "vm-e", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"stale"}})

	out, err := runCLI("port", "edit", "pf-tag", "--clear-tags")
	if err != nil {
		t.Fatalf("port edit --clear-tags: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, "Tags cleared") {
		t.Errorf("expected 'Tags cleared' line, got %q", out)
	}

	stored, _ := s.ListPortForwards("vm-e")
	if len(stored) != 1 || len(stored[0].Tags) != 0 {
		t.Errorf("expected tags cleared, got %v", stored[0].Tags)
	}
}

func TestCLI_PortEdit_RejectsConflictingTagFlags(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	s.PutPortForward(&types.PortForward{ID: "pf-tag", VMID: "vm-e", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP})

	_, err := runCLI("port", "edit", "pf-tag", "--tag", "a", "--clear-tags")
	if err == nil {
		t.Fatal("expected error when --tag and --clear-tags are combined")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got %v", err)
	}
}

func TestCLI_PortList_FilterByTag(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	s.PutPortForward(&types.PortForward{ID: "pf-a", VMID: "vm-flt", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"production"}})
	s.PutPortForward(&types.PortForward{ID: "pf-b", VMID: "vm-flt", HostPort: 8443, GuestPort: 443, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"audit"}})

	out, err := runCLI("port", "list", "vm-flt", "--tag", "PRODUCTION")
	if err != nil {
		t.Fatalf("port list --tag: %v", err)
	}
	if !strings.Contains(out, "pf-a") || strings.Contains(out, "pf-b") {
		t.Errorf("expected only pf-a (production), got %q", out)
	}
}

func TestCLI_PortList_FilterByProtocol_TCP(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--protocol", "tcp")
	if err != nil {
		t.Fatalf("port list --protocol tcp: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 tcp rows, got %d rows from %q", len(rows), out)
	}
	for _, row := range rows[1:] {
		if row[3] != "tcp" {
			t.Errorf("unexpected protocol column %q in tcp filter", row[3])
		}
	}
}

func TestCLI_PortList_FilterByProtocol_UDP(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--protocol", "udp")
	if err != nil {
		t.Fatalf("port list --protocol udp: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 udp row, got %d rows from %q", len(rows), out)
	}
	if rows[1][3] != "udp" {
		t.Errorf("row protocol = %q, want udp", rows[1][3])
	}
}

func TestCLI_PortList_FilterByProtocol_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--protocol", "UDP")
	if err != nil {
		t.Fatalf("port list --protocol UDP: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 udp row from uppercase flag, got %d", len(rows))
	}
}

func TestCLI_PortList_FilterByProtocol_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	out, err := runCLI("port", "list", "vm-s", "--protocol", "")
	if err != nil {
		t.Fatalf("port list --protocol '': %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 4 {
		t.Fatalf("empty protocol should return all 3 rules + header, got %d rows", len(rows))
	}
}

func TestCLI_PortList_RejectsInvalidProtocol(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	_, err := runCLI("port", "list", "vm-s", "--protocol", "sctp")
	if err == nil {
		t.Fatalf("expected invalid protocol to error")
	}
	if !strings.Contains(err.Error(), "invalid --protocol") {
		t.Errorf("error = %v, want 'invalid --protocol' message", err)
	}
}

func TestCLI_PortList_FilterByProtocol_ComposesWithTag(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	s.PutPortForward(&types.PortForward{ID: "pf-a", VMID: "vm-pcmb", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"web"}})
	s.PutPortForward(&types.PortForward{ID: "pf-b", VMID: "vm-pcmb", HostPort: 53000, GuestPort: 53, GuestIP: "192.168.100.10", Protocol: types.ProtocolUDP, Tags: []string{"web"}})
	s.PutPortForward(&types.PortForward{ID: "pf-c", VMID: "vm-pcmb", HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"admin"}})

	out, err := runCLI("port", "list", "vm-pcmb", "--protocol", "tcp", "--tag", "web")
	if err != nil {
		t.Fatalf("port list --protocol --tag: %v", err)
	}
	if !strings.Contains(out, "pf-a") {
		t.Errorf("expected pf-a in output, got %q", out)
	}
	if strings.Contains(out, "pf-b") || strings.Contains(out, "pf-c") {
		t.Errorf("expected only pf-a, got %q", out)
	}
}

func TestCLI_PortList_ShowsTagsColumn(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	s.PutPortForward(&types.PortForward{ID: "pf-a", VMID: "vm-col", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP, Tags: []string{"production", "web"}})

	out, err := runCLI("port", "list", "vm-col")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 2 {
		t.Fatalf("expected header + 1 row, got %d rows from %q", len(rows), out)
	}
	headers := rows[0]
	if headers[len(headers)-1] != "TAGS" {
		t.Errorf("last header = %q, want TAGS", headers[len(headers)-1])
	}
	body := rows[1]
	if body[len(body)-1] != "production,web" {
		t.Errorf("last row column = %q, want production,web", body[len(body)-1])
	}
}

func TestCLI_PortRemove_NoMatchPrintsMessage(t *testing.T) {
	_, _, cleanup := withTestPortForwarder(t)
	defer cleanup()

	out, err := runCLI("port", "remove", "--vm", "vm-empty", "--protocol", "tcp")
	if err != nil {
		t.Fatalf("port remove --vm: %v", err)
	}
	if !strings.Contains(out, "No port forwards") {
		t.Errorf("expected no-match message, got: %q", out)
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

// =====================================================
// vm top tests (CLI hits a fake daemon /api/v1/vms/stats/top)
// =====================================================

func TestCLI_VMTop_RendersLeaderboard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vms/stats/top" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("metric") != "cpu" {
			t.Errorf("metric = %q, want cpu", q.Get("metric"))
		}
		if q.Get("limit") != "3" {
			t.Errorf("limit = %q, want 3", q.Get("limit"))
		}
		if q.Get("state") != "running" {
			t.Errorf("state = %q, want running", q.Get("state"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"metric":"cpu","limit":3,"state":"running",
			"items":[
				{"vm_id":"vm-a","name":"alpha","state":"running","value":80.0},
				{"vm_id":"vm-b","name":"bravo","state":"running","value":40.0}
			]
		}`))
	}))
	defer srv.Close()

	out, err := runCLI("vm", "top", "--api-url", srv.URL, "--limit", "3")
	if err != nil {
		t.Fatalf("vm top: %v", err)
	}
	for _, want := range []string{"alpha", "vm-a", "80.0%", "bravo", "vm-b", "Top 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestCLI_VMTop_EmptyMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"metric":"cpu","limit":5,"state":"running","items":[]}`))
	}))
	defer srv.Close()

	out, err := runCLI("vm", "top", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("vm top: %v", err)
	}
	if !strings.Contains(out, "No VMs reported a sample") {
		t.Errorf("expected empty message, got %q", out)
	}
}

func TestCLI_VMTop_RejectsUnsupportedMetric(t *testing.T) {
	_, err := runCLI("vm", "top", "--metric", "bogus", "--api-url", "http://invalid")
	if err == nil {
		t.Fatal("expected error for invalid metric")
	}
	if !strings.Contains(err.Error(), "unsupported metric") {
		t.Errorf("error = %v, want unsupported metric", err)
	}
}

func TestCLI_VMTop_PropagatesDaemonDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"code":"metrics_disabled"}`))
	}))
	defer srv.Close()

	_, err := runCLI("vm", "top", "--api-url", srv.URL)
	if err == nil {
		t.Fatal("expected error when daemon reports metrics_disabled")
	}
	if !strings.Contains(err.Error(), "metrics are disabled") {
		t.Errorf("error = %v, want 'metrics are disabled' message", err)
	}
}

func TestCLI_VMTop_FormatsByteRates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"metric":"net_rx","limit":5,"state":"running",
			"items":[{"vm_id":"vm-x","name":"xray","state":"running","value":2097152}]
		}`))
	}))
	defer srv.Close()

	out, err := runCLI("vm", "top", "--metric", "net_rx", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("vm top: %v", err)
	}
	if !strings.Contains(out, "MB/s") {
		t.Errorf("expected MB/s output for byte rate, got:\n%s", out)
	}
}

// =====================================================
// webhook edit tests (CLI hits a fake daemon PATCH /api/v1/webhooks/{id})
// =====================================================

// fakeWebhookDaemon returns an httptest.Server that captures the most recent
// PATCH request body and returns canned responses based on the configured
// status code.  Each test mutates the per-request hooks via the returned
// struct to drive different code paths.
type fakeWebhookDaemon struct {
	lastBody   []byte
	lastMethod string
	status     int
	respBody   string
}

func newFakeWebhookDaemon(t *testing.T, status int, body string) (*httptest.Server, *fakeWebhookDaemon) {
	t.Helper()
	state := &fakeWebhookDaemon{status: status, respBody: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.lastMethod = r.Method
		state.lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(state.status)
		w.Write([]byte(state.respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func TestCLI_WebhookEdit_SetsURL(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-1","url":"https://new.example.com/x","active":true,"created_at":"2024-01-01T00:00:00Z"}`)

	out, err := runCLI("webhook", "edit", "wh-1", "--api-url", srv.URL, "--url", "https://new.example.com/x")
	if err != nil {
		t.Fatalf("webhook edit: %v\nout: %s", err, out)
	}
	if state.lastMethod != http.MethodPatch {
		t.Fatalf("expected PATCH, got %s", state.lastMethod)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v\nbody=%s", err, state.lastBody)
	}
	if sent.URL == nil || *sent.URL != "https://new.example.com/x" {
		t.Errorf("URL not set in PATCH body: %+v", sent)
	}
	if sent.Secret != nil || sent.EventTypes != nil || sent.Active != nil {
		t.Errorf("unexpected fields populated: %+v", sent)
	}
	if !strings.Contains(out, "Webhook updated: wh-1") {
		t.Errorf("expected confirmation in output: %s", out)
	}
}

func TestCLI_WebhookEdit_RotatesSecret(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-2","url":"https://x","active":true,"created_at":"2024-01-01T00:00:00Z"}`)

	if _, err := runCLI("webhook", "edit", "wh-2", "--api-url", srv.URL, "--secret", "newsecret"); err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v", err)
	}
	if sent.Secret == nil || *sent.Secret != "newsecret" {
		t.Errorf("Secret not sent in PATCH body: %+v", sent)
	}
}

func TestCLI_WebhookEdit_TogglesActive(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-3","url":"https://x","active":false,"created_at":"2024-01-01T00:00:00Z"}`)

	out, err := runCLI("webhook", "edit", "wh-3", "--api-url", srv.URL, "--active=false")
	if err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v", err)
	}
	if sent.Active == nil || *sent.Active != false {
		t.Errorf("Active=false not sent: %+v", sent)
	}
	if !strings.Contains(out, "Active: false") {
		t.Errorf("expected active=false in output: %s", out)
	}
}

func TestCLI_WebhookEdit_SetsEventTypes(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-4","url":"https://x","active":true,"event_types":["vm.started","vm.stopped"],"created_at":"2024-01-01T00:00:00Z"}`)

	if _, err := runCLI("webhook", "edit", "wh-4", "--api-url", srv.URL, "--event-types", "vm.started,vm.stopped"); err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v", err)
	}
	if sent.EventTypes == nil {
		t.Fatalf("EventTypes not sent in PATCH body: %s", state.lastBody)
	}
	if len(*sent.EventTypes) != 2 {
		t.Errorf("EventTypes len = %d, want 2: %v", len(*sent.EventTypes), *sent.EventTypes)
	}
}

func TestCLI_WebhookEdit_ClearsEventTypes(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-5","url":"https://x","active":true,"created_at":"2024-01-01T00:00:00Z"}`)

	out, err := runCLI("webhook", "edit", "wh-5", "--api-url", srv.URL, "--clear-event-types")
	if err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v", err)
	}
	if sent.EventTypes == nil {
		t.Fatalf("EventTypes not sent (nil): %s", state.lastBody)
	}
	if len(*sent.EventTypes) != 0 {
		t.Errorf("EventTypes should be empty slice, got %v", *sent.EventTypes)
	}
	if !strings.Contains(out, "(all events)") {
		t.Errorf("expected '(all events)' in output: %s", out)
	}
}

func TestCLI_WebhookEdit_RejectsConflictingEventTypeFlags(t *testing.T) {
	srv, _ := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-c","url":"https://x","active":true,"created_at":"2024-01-01T00:00:00Z"}`)

	_, err := runCLI("webhook", "edit", "wh-c", "--api-url", srv.URL,
		"--event-types", "vm.started", "--clear-event-types")
	if err == nil {
		t.Fatal("expected error when both flags are passed")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want 'mutually exclusive'", err)
	}
}

func TestCLI_WebhookEdit_RejectsNoFlags(t *testing.T) {
	_, err := runCLI("webhook", "edit", "wh-noop", "--api-url", "http://invalid")
	if err == nil {
		t.Fatal("expected error when no fields are passed")
	}
	if !strings.Contains(err.Error(), "no fields to update") {
		t.Errorf("error = %v, want 'no fields to update'", err)
	}
}

func TestCLI_WebhookEdit_SetsDescription(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-d","url":"https://x","active":true,"description":"Slack notifier","created_at":"2024-01-01T00:00:00Z"}`)

	out, err := runCLI("webhook", "edit", "wh-d", "--api-url", srv.URL, "--description", "Slack notifier")
	if err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v", err)
	}
	if sent.Description == nil || *sent.Description != "Slack notifier" {
		t.Errorf("Description not sent: %+v", sent)
	}
	if !strings.Contains(out, "Description: Slack notifier") {
		t.Errorf("expected description in output: %s", out)
	}
}

func TestCLI_WebhookEdit_ClearsDescription(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-d","url":"https://x","active":true,"created_at":"2024-01-01T00:00:00Z"}`)

	if _, err := runCLI("webhook", "edit", "wh-d", "--api-url", srv.URL, "--description", ""); err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v", err)
	}
	if sent.Description == nil || *sent.Description != "" {
		t.Errorf("Description should be empty string (clear), got %+v", sent.Description)
	}
}

func TestCLI_WebhookEdit_DescriptionTooLong(t *testing.T) {
	long := strings.Repeat("a", 1025)
	_, err := runCLI("webhook", "edit", "wh-d", "--api-url", "http://invalid", "--description", long)
	if err == nil {
		t.Fatal("expected error for >1024 char description")
	}
	if !strings.Contains(err.Error(), "1024 characters") {
		t.Errorf("error = %v, want '1024 characters'", err)
	}
}

func TestCLI_WebhookAdd_WithDescription(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusCreated,
		`{"id":"wh-new","url":"https://x","active":true,"description":"Slack alerts","created_at":"2024-01-01T00:00:00Z"}`)

	out, err := runCLI("webhook", "add", "--api-url", srv.URL, "--url", "https://x", "--secret", "k", "--description", "  Slack alerts  ")
	if err != nil {
		t.Fatalf("webhook add: %v", err)
	}
	if state.lastMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", state.lastMethod)
	}
	var sent types.WebhookCreateRequest
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v", err)
	}
	if sent.Description != "Slack alerts" {
		t.Errorf("Description not trimmed/sent: %q", sent.Description)
	}
	if !strings.Contains(out, "Description: Slack alerts") {
		t.Errorf("expected description in output: %s", out)
	}
}

func TestCLI_WebhookAdd_DescriptionTooLong(t *testing.T) {
	long := strings.Repeat("a", 1025)
	_, err := runCLI("webhook", "add", "--api-url", "http://invalid", "--url", "https://x", "--secret", "k", "--description", long)
	if err == nil {
		t.Fatal("expected error for >1024 char description")
	}
	if !strings.Contains(err.Error(), "1024 characters") {
		t.Errorf("error = %v, want '1024 characters'", err)
	}
}

func TestCLI_WebhookEdit_PropagatesDaemonError(t *testing.T) {
	srv, _ := newFakeWebhookDaemon(t, http.StatusNotFound,
		`{"code":"resource_not_found","message":"webhook not found"}`)

	_, err := runCLI("webhook", "edit", "wh-missing", "--api-url", srv.URL, "--active=false")
	if err == nil {
		t.Fatal("expected error when daemon returns 404")
	}
	if !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "resource_not_found") {
		t.Errorf("error = %v, want 'HTTP 404' + 'resource_not_found'", err)
	}
}

// --- Webhook bulk-delete (2.3.10) ---

func TestCLI_WebhookDelete_NoArgsErrors(t *testing.T) {
	_, err := runCLI("webhook", "delete", "--api-url", "http://invalid")
	if err == nil {
		t.Fatal("expected error when no id and no --event-type")
	}
	if !strings.Contains(err.Error(), "exactly one of") {
		t.Errorf("error = %v, want 'exactly one of'", err)
	}
}

func TestCLI_WebhookDelete_BothIDAndEventTypeErrors(t *testing.T) {
	_, err := runCLI("webhook", "delete", "wh-1", "--event-type", "vm.deleted", "--api-url", "http://invalid")
	if err == nil {
		t.Fatal("expected error when both id and --event-type are passed")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want 'mutually exclusive'", err)
	}
}

func TestCLI_WebhookDelete_SinglePositional(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusNoContent, ``)

	out, err := runCLI("webhook", "delete", "wh-positional", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("webhook delete: %v\nout: %s", err, out)
	}
	if state.lastMethod != http.MethodDelete {
		t.Fatalf("expected DELETE, got %s", state.lastMethod)
	}
	if !strings.Contains(out, "Webhook wh-positional deleted") {
		t.Errorf("expected confirmation: %s", out)
	}
}

func TestCLI_WebhookDelete_EventTypeBulk(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"results":[{"id":"wh-a","success":true},{"id":"wh-b","success":true}]}`)

	out, err := runCLI("webhook", "delete", "--api-url", srv.URL, "--event-type", "vm.deleted")
	if err != nil {
		t.Fatalf("webhook delete: %v\nout: %s", err, out)
	}
	if state.lastMethod != http.MethodPost {
		t.Fatalf("expected POST to /webhooks/bulk_delete, got %s", state.lastMethod)
	}
	var sent struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding sent body: %v\nbody=%s", err, state.lastBody)
	}
	if sent.EventType != "vm.deleted" {
		t.Errorf("event_type not forwarded: got %q", sent.EventType)
	}
	if !strings.Contains(out, "OK    wh-a") || !strings.Contains(out, "OK    wh-b") {
		t.Errorf("expected per-target OK lines: %s", out)
	}
}

func TestCLI_WebhookDelete_EventTypeTrimsWhitespace(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK, `{"results":[]}`)

	if _, err := runCLI("webhook", "delete", "--api-url", srv.URL, "--event-type", "  vm.deleted  "); err != nil {
		t.Fatalf("webhook delete: %v", err)
	}
	var sent struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if sent.EventType != "vm.deleted" {
		t.Errorf("whitespace not trimmed: got %q", sent.EventType)
	}
}

func TestCLI_WebhookDelete_EventTypeNoMatchPrintsMessage(t *testing.T) {
	srv, _ := newFakeWebhookDaemon(t, http.StatusOK, `{"results":[]}`)

	out, err := runCLI("webhook", "delete", "--api-url", srv.URL, "--event-type", "nope.event")
	if err != nil {
		t.Fatalf("webhook delete: %v", err)
	}
	if !strings.Contains(out, `No webhooks subscribed to event type "nope.event"`) {
		t.Errorf("expected no-match message, got: %s", out)
	}
}

func TestCLI_WebhookDelete_PartialFailureSurfaces(t *testing.T) {
	srv, _ := newFakeWebhookDaemon(t, http.StatusOK,
		`{"results":[{"id":"wh-a","success":true},{"id":"wh-b","success":false,"code":"resource_not_found","message":"webhook not found"}]}`)

	out, err := runCLI("webhook", "delete", "--api-url", srv.URL, "--event-type", "vm.deleted")
	if err == nil {
		t.Fatal("expected error when at least one target fails")
	}
	if !strings.Contains(err.Error(), "1 of 2 webhooks failed") {
		t.Errorf("error = %v, want '1 of 2 webhooks failed'", err)
	}
	if !strings.Contains(out, "OK    wh-a") || !strings.Contains(out, "FAIL  wh-b") {
		t.Errorf("expected per-target lines: %s", out)
	}
}

// --- Template list sort (5.4.7) ---

func TestCLI_TemplateList_SortByName(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "Charlie", Image: "ubuntu", CreatedAt: time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "ubuntu", CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "Bravo", Image: "ubuntu", CreatedAt: time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "name")
	if err != nil {
		t.Fatalf("template list --sort: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want header + 3", len(rows))
	}
	want := []string{"alpha", "Bravo", "Charlie"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByCreatedAtDesc(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "first", Image: "ubuntu", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "second", Image: "ubuntu", CreatedAt: t0.Add(time.Hour), UpdatedAt: t0.Add(time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "third", Image: "ubuntu", CreatedAt: t0.Add(2 * time.Hour), UpdatedAt: t0.Add(2 * time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "created_at", "--order", "desc")
	if err != nil {
		t.Fatalf("template list: %v", err)
	}
	rows := tableRows(t, out)
	want := []string{"third", "second", "first"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_RejectsInvalidSort(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("template", "list", "--sort", "ram_mb")
	if err == nil {
		t.Fatal("expected error for unsupported --sort, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Errorf("error = %v, want it to mention 'invalid --sort'", err)
	}
}

func TestCLI_TemplateList_RejectsInvalidOrder(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("template", "list", "--order", "sideways")
	if err == nil {
		t.Fatal("expected error for unsupported --order, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --order") {
		t.Errorf("error = %v, want it to mention 'invalid --order'", err)
	}
}

func TestCLI_TemplateList_FilterBySearch_MatchesName(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--search", "rocky")
	if err != nil {
		t.Fatalf("template list --search: %v", err)
	}
	if !strings.Contains(out, "rocky9-base") {
		t.Fatalf("expected rocky9-base in output, got %q", out)
	}
	if strings.Contains(out, "ubuntu-22") {
		t.Fatalf("did not expect ubuntu-22 in output, got %q", out)
	}
}

func TestCLI_TemplateList_FilterBySearch_MatchesDescription(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "rocky9.qcow2", Description: "Hardened CIS-1 build", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "large", Image: "rocky9.qcow2", Description: "Stock cloud image", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--search", "hardened")
	if err != nil {
		t.Fatalf("template list --search: %v", err)
	}
	if !strings.Contains(out, "small") || strings.Contains(out, "large") {
		t.Fatalf("filter on description failed, got %q", out)
	}
}

func TestCLI_TemplateList_FilterBySearch_MatchesTag(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "rocky9.qcow2", Tags: []string{"team-storage"}, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "large", Image: "rocky9.qcow2", Tags: []string{"team-net"}, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--search", "storage")
	if err != nil {
		t.Fatalf("template list --search: %v", err)
	}
	if !strings.Contains(out, "small") || strings.Contains(out, "large") {
		t.Fatalf("filter on tag failed, got %q", out)
	}
}

func TestCLI_TemplateList_FilterBySearch_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "Rocky9-Base", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--search", "ROCKY")
	if err != nil {
		t.Fatalf("template list --search: %v", err)
	}
	if !strings.Contains(out, "Rocky9-Base") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_TemplateList_FilterBySearch_NoMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "alpha", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--search", "needle-not-present")
	if err != nil {
		t.Fatalf("template list --search: %v", err)
	}
	if strings.Contains(out, "alpha") {
		t.Fatalf("expected empty list for no-match, got %q", out)
	}
}

func TestCLI_TemplateList_FilterBySearch_CombinesWithTag(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky9-prod", Image: "rocky9.qcow2", Tags: []string{"prod"}, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "rocky9-qa", Image: "rocky9.qcow2", Tags: []string{"qa"}, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-prod", Image: "ubuntu.qcow2", Tags: []string{"prod"}, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--search", "rocky", "--tag", "prod")
	if err != nil {
		t.Fatalf("template list --search --tag: %v", err)
	}
	if !strings.Contains(out, "rocky9-prod") {
		t.Fatalf("expected rocky9-prod (intersection of search+tag), got %q", out)
	}
	if strings.Contains(out, "rocky9-qa") || strings.Contains(out, "ubuntu-prod") {
		t.Fatalf("did not expect non-intersecting templates, got %q", out)
	}
}

// =====================================================
// webhook list --search tests
// =====================================================
//
// The CLI sends GET /api/v1/webhooks?search=<q>; the daemon does the actual
// matching.  These tests stand in a fake daemon that captures the query
// string + returns canned JSON so we can assert the wire shape end-to-end
// without spinning up the real api server.

// newFakeWebhookListDaemon returns an httptest.Server that captures the most
// recent GET path + query and serves a canned JSON body.  Tests inspect
// `state.lastQuery` to assert that the CLI forwarded the `?search=` param.
type fakeWebhookListDaemon struct {
	lastPath  string
	lastQuery string
	status    int
	respBody  string
}

func newFakeWebhookListDaemon(t *testing.T, status int, body string) (*httptest.Server, *fakeWebhookListDaemon) {
	t.Helper()
	state := &fakeWebhookListDaemon{status: status, respBody: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(state.status)
		w.Write([]byte(state.respBody))
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func TestCLI_WebhookList_ForwardsSearchParam(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK,
		`[{"id":"wh-1","url":"https://hooks.example.com/audit","active":true,"event_types":["vm.started"],"created_at":"2024-01-01T00:00:00Z"}]`)

	out, err := runCLI("webhook", "list", "--api-url", srv.URL, "--search", "audit")
	if err != nil {
		t.Fatalf("webhook list: %v\nout=%s", err, out)
	}
	if state.lastPath != "/api/v1/webhooks" {
		t.Fatalf("path = %q, want /api/v1/webhooks", state.lastPath)
	}
	if !strings.Contains(state.lastQuery, "search=audit") {
		t.Fatalf("query missing search=audit: %q", state.lastQuery)
	}
	if !strings.Contains(out, "wh-1") {
		t.Fatalf("expected webhook id wh-1 in output: %s", out)
	}
}

func TestCLI_WebhookList_TrimsAndLowercasesSearch(t *testing.T) {
	// The CLI trims and lowercases the needle before forwarding so the
	// daemon's per-page search response is consistent regardless of
	// shell quoting noise.
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--search", "  AUDIT  "); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "search=audit") {
		t.Fatalf("expected normalized 'search=audit' in query, got %q", state.lastQuery)
	}
	if strings.Contains(state.lastQuery, "AUDIT") || strings.Contains(state.lastQuery, "+++") {
		t.Fatalf("query should not retain whitespace or uppercase: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_EmptySearchOmitsParam(t *testing.T) {
	// `--search ""` (or omitted) must not add `search=` to the URL — the
	// query should be unscoped so the daemon returns every webhook.
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--search", ""); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if strings.Contains(state.lastQuery, "search=") {
		t.Fatalf("empty search must not be forwarded: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_NoMatchPrintsEmptyMessage(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	out, err := runCLI("webhook", "list", "--api-url", srv.URL, "--search", "needle-not-present")
	if err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(out, "No webhooks registered.") {
		t.Fatalf("expected empty-list message, got %q", out)
	}
}

// =====================================================
// webhook list --sort / --order tests
// =====================================================

func TestCLI_WebhookList_ForwardsSortAndOrder(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--sort", "url", "--order", "desc"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "sort=url") {
		t.Fatalf("query missing sort=url: %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "order=desc") {
		t.Fatalf("query missing order=desc: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_NormalisesSortAndOrderCase(t *testing.T) {
	// The CLI trims + lowercases sort/order client-side so callers can pass
	// shell-friendly forms like ` URL ` without surprising the daemon.
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--sort", "  URL  ", "--order", "DESC"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "sort=url") {
		t.Fatalf("expected normalized sort=url, got %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "order=desc") {
		t.Fatalf("expected normalized order=desc, got %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_RejectsInvalidSort(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL, "--sort", "secret")
	if err == nil {
		t.Fatalf("expected error for invalid sort, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("error = %q, want it to mention 'invalid --sort'", err.Error())
	}
}

func TestCLI_WebhookList_RejectsInvalidOrder(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL, "--order", "sideways")
	if err == nil {
		t.Fatalf("expected error for invalid order, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --order") {
		t.Fatalf("error = %q, want it to mention 'invalid --order'", err.Error())
	}
}

func TestCLI_WebhookList_EmptySortAndOrderOmitParams(t *testing.T) {
	// No --sort / --order flags should leave the daemon URL un-decorated so
	// the daemon's default id/asc applies.
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if strings.Contains(state.lastQuery, "sort=") {
		t.Fatalf("empty sort must not be forwarded: %q", state.lastQuery)
	}
	if strings.Contains(state.lastQuery, "order=") {
		t.Fatalf("empty order must not be forwarded: %q", state.lastQuery)
	}
}

// =====================================================
// webhook tags (2.2.15) CLI tests
// =====================================================

func TestCLI_WebhookAdd_WithTags(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusCreated,
		`{"id":"wh-tagged","url":"https://x","active":true,"tags":["audit","production"],"created_at":"2024-01-01T00:00:00Z"}`)

	out, err := runCLI("webhook", "add", "--api-url", srv.URL, "--url", "https://x",
		"--secret", "k", "--tag", "production", "--tag", "audit")
	if err != nil {
		t.Fatalf("webhook add: %v", err)
	}
	var sent types.WebhookCreateRequest
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	// Cobra preserves command-line order; validation/normalisation happens
	// server-side. The CLI just forwards the raw list.
	if len(sent.Tags) != 2 {
		t.Errorf("expected 2 tags forwarded, got %v", sent.Tags)
	}
	if !strings.Contains(out, "Tags:") {
		t.Errorf("expected Tags: line in output: %s", out)
	}
}

func TestCLI_WebhookEdit_SetsTags(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-t","url":"https://x","active":true,"tags":["audit","production"],"created_at":"2024-01-01T00:00:00Z"}`)

	out, err := runCLI("webhook", "edit", "wh-t", "--api-url", srv.URL,
		"--tag", "production", "--tag", "audit")
	if err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if sent.Tags == nil {
		t.Fatalf("Tags pointer should be set, got nil")
	}
	if len(*sent.Tags) != 2 {
		t.Errorf("expected 2 tags forwarded, got %v", *sent.Tags)
	}
	if !strings.Contains(out, "Tags:") {
		t.Errorf("expected Tags: line in output: %s", out)
	}
}

func TestCLI_WebhookEdit_ClearsTags(t *testing.T) {
	srv, state := newFakeWebhookDaemon(t, http.StatusOK,
		`{"id":"wh-t","url":"https://x","active":true,"created_at":"2024-01-01T00:00:00Z"}`)

	if _, err := runCLI("webhook", "edit", "wh-t", "--api-url", srv.URL, "--clear-tags"); err != nil {
		t.Fatalf("webhook edit: %v", err)
	}
	var sent types.WebhookUpdateSpec
	if err := json.Unmarshal(state.lastBody, &sent); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if sent.Tags == nil || len(*sent.Tags) != 0 {
		t.Errorf("expected Tags to be empty slice for clear, got %+v", sent.Tags)
	}
}

func TestCLI_WebhookEdit_RejectsConflictingTagFlags(t *testing.T) {
	// --tag and --clear-tags are mutually exclusive; the CLI rejects before
	// reaching the daemon.
	_, err := runCLI("webhook", "edit", "wh-t", "--api-url", "http://invalid",
		"--tag", "production", "--clear-tags")
	if err == nil {
		t.Fatal("expected error for conflicting --tag + --clear-tags")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %v, want 'mutually exclusive'", err)
	}
}

func TestCLI_WebhookList_ForwardsTagFilter(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--tag", "  Production  "); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "tag=production") {
		t.Fatalf("expected normalized 'tag=production' in query, got %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_EmptyTagOmitsParam(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--tag", ""); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if strings.Contains(state.lastQuery, "tag=") {
		t.Fatalf("empty tag must not be forwarded: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ShowsTagsColumn(t *testing.T) {
	// The webhook list table now includes a TAGS column. Make sure a webhook
	// with tags renders them in the row.
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK,
		`[{"id":"wh-1","url":"https://x","active":true,"tags":["audit","production"],"created_at":"2024-01-01T00:00:00Z"}]`)

	out, err := runCLI("webhook", "list", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(out, "TAGS") {
		t.Errorf("expected TAGS header in output: %s", out)
	}
	if !strings.Contains(out, "audit,production") {
		t.Errorf("expected joined tags in row: %s", out)
	}
}

// ---------------------------------------------------------------------------
// vmsmith webhook test <id> — CLI parallel of POST /webhooks/{id}/test.
//
// Each test seeds an httptest.Server that returns a canned WebhookTestResult
// (or a non-200 error envelope) and asserts that runCLI:
//   - sends POST to the correct path
//   - forwards Authorization / api-url
//   - prints a human verdict by default and the raw JSON under --json
//   - exits non-zero when the receiver returned !success (so the command
//     composes with shell pipelines)
// ---------------------------------------------------------------------------

// fakeWebhookTestDaemon captures the most recent POST to
// /api/v1/webhooks/{id}/test and returns canned bodies.
type fakeWebhookTestDaemon struct {
	lastMethod string
	lastPath   string
	lastAuth   string
	status     int
	respBody   string
}

func newFakeWebhookTestDaemon(t *testing.T, status int, body string) (*httptest.Server, *fakeWebhookTestDaemon) {
	t.Helper()
	state := &fakeWebhookTestDaemon{status: status, respBody: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.lastMethod = r.Method
		state.lastPath = r.URL.Path
		state.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(state.status)
		_, _ = io.WriteString(w, state.respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func TestCLI_WebhookTest_Success(t *testing.T) {
	srv, state := newFakeWebhookTestDaemon(t, http.StatusOK, `{
		"success": true,
		"status_code": 204,
		"duration_ms": 42,
		"attempted_at": "2026-05-16T00:00:00Z",
		"event_id": "wh-test-1"
	}`)

	out, err := runCLI("webhook", "test", "wh-ok", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("webhook test: %v\nout: %s", err, out)
	}
	if state.lastMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", state.lastMethod)
	}
	if state.lastPath != "/api/v1/webhooks/wh-ok/test" {
		t.Fatalf("path = %q, want /api/v1/webhooks/wh-ok/test", state.lastPath)
	}
	for _, want := range []string{"OK", "wh-ok", "204", "42 ms", "wh-test-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestCLI_WebhookTest_FailureExitsNonZero(t *testing.T) {
	// Daemon returns HTTP 200 with success=false — receiver responded with
	// 500 or signature rejection. The command must surface the failure and
	// exit non-zero so it composes with shell || pipelines.
	srv, _ := newFakeWebhookTestDaemon(t, http.StatusOK, `{
		"success": false,
		"status_code": 500,
		"duration_ms": 31,
		"attempted_at": "2026-05-16T00:00:00Z",
		"error": "receiver returned 500"
	}`)

	out, err := runCLI("webhook", "test", "wh-bad", "--api-url", srv.URL)
	if err == nil {
		t.Fatalf("expected non-nil err when delivery failed; out=%s", out)
	}
	if !strings.Contains(err.Error(), "test delivery failed") {
		t.Errorf("err = %q, want 'test delivery failed' substring", err.Error())
	}
	for _, want := range []string{"FAIL", "wh-bad", "500", "receiver returned 500"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestCLI_WebhookTest_JSONFlagEmitsRaw(t *testing.T) {
	canned := `{"success":true,"status_code":200,"duration_ms":12,"attempted_at":"2026-05-16T00:00:00Z","event_id":"e-1"}`
	srv, _ := newFakeWebhookTestDaemon(t, http.StatusOK, canned)

	out, err := runCLI("webhook", "test", "wh-ok", "--api-url", srv.URL, "--json")
	if err != nil {
		t.Fatalf("webhook test: %v\nout: %s", err, out)
	}
	if !strings.Contains(out, canned) {
		t.Errorf("--json output should include raw body verbatim. got=%s want substring=%s", out, canned)
	}
	// Human table headers must not appear when --json is set.
	if strings.Contains(out, "Duration:") || strings.Contains(out, "Status:") {
		t.Errorf("--json output should suppress the human table: %s", out)
	}
}

func TestCLI_WebhookTest_ForwardsAuthHeader(t *testing.T) {
	srv, state := newFakeWebhookTestDaemon(t, http.StatusOK,
		`{"success":true,"status_code":204,"duration_ms":5,"attempted_at":"2026-05-16T00:00:00Z"}`)

	if _, err := runCLI("webhook", "test", "wh-1", "--api-url", srv.URL, "--api-key", "secret-token"); err != nil {
		t.Fatalf("webhook test: %v", err)
	}
	if state.lastAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q, want %q", state.lastAuth, "Bearer secret-token")
	}
}

func TestCLI_WebhookTest_NotFoundPropagatesDaemonError(t *testing.T) {
	// 404 from daemon (unknown webhook id) must surface as a daemon error
	// rather than a silent success.
	srv, _ := newFakeWebhookTestDaemon(t, http.StatusNotFound,
		`{"error":{"code":"resource_not_found","message":"webhook not found"}}`)

	out, err := runCLI("webhook", "test", "wh-missing", "--api-url", srv.URL)
	if err == nil {
		t.Fatalf("expected error for 404; out=%s", out)
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("err = %q, want HTTP 404 in message", err.Error())
	}
}

func TestCLI_WebhookTest_ServiceUnavailablePropagates(t *testing.T) {
	// 503 webhook_test_unavailable — daemon was built without the runtime
	// tester wired. The CLI should surface the daemon's message instead
	// of pretending success.
	srv, _ := newFakeWebhookTestDaemon(t, http.StatusServiceUnavailable,
		`{"error":{"code":"webhook_test_unavailable","message":"webhook test deliveries are not enabled"}}`)

	_, err := runCLI("webhook", "test", "wh-any", "--api-url", srv.URL)
	if err == nil {
		t.Fatalf("expected error for 503")
	}
	if !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("err = %q, want HTTP 503 in message", err.Error())
	}
}

func TestCLI_WebhookTest_RequiresID(t *testing.T) {
	// cobra.ExactArgs(1) means missing positional id is rejected before any
	// HTTP call is made.
	_, err := runCLI("webhook", "test", "--api-url", "http://127.0.0.1:1")
	if err == nil {
		t.Fatalf("expected error when id is missing")
	}
}

// --- Snapshot tag tests (roadmap 2.2.17) ---

func TestCLI_SnapshotCreate_WithTags(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t1", Name: "host"})

	if _, err := runCLI("snapshot", "create", "vm-t1", "--name", "snap-a",
		"--tag", "production", "--tag", "audit"); err != nil {
		t.Fatalf("snapshot create: %v", err)
	}

	snaps, err := mock.ListSnapshots(nil, "vm-t1")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 || len(snaps[0].Tags) != 2 {
		t.Fatalf("expected 1 snapshot with 2 tags, got %+v", snaps)
	}
	if snaps[0].Tags[0] != "production" || snaps[0].Tags[1] != "audit" {
		t.Errorf("tags = %v, want [production audit] (mock preserves caller order; the API normalises server-side)", snaps[0].Tags)
	}
}

func TestCLI_SnapshotCreate_PrintsTags(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t1p", Name: "host"})
	out, err := runCLI("snapshot", "create", "vm-t1p", "--name", "snap-a", "--tag", "prod")
	if err != nil {
		t.Fatalf("snapshot create: %v", err)
	}
	if !strings.Contains(out, "Tags: prod") {
		t.Errorf("output should include 'Tags: prod', got %q", out)
	}
}

func TestCLI_SnapshotEdit_SetsTags(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t2", Name: "host"})
	mock.CreateSnapshot(nil, "vm-t2", types.SnapshotSpec{Name: "snap-a"})

	if _, err := runCLI("snapshot", "edit", "vm-t2", "snap-a", "--tag", "backup"); err != nil {
		t.Fatalf("snapshot edit: %v", err)
	}

	snaps, _ := mock.ListSnapshots(nil, "vm-t2")
	if len(snaps) != 1 || len(snaps[0].Tags) != 1 || snaps[0].Tags[0] != "backup" {
		t.Errorf("expected one tag 'backup', got %+v", snaps)
	}
}

func TestCLI_SnapshotEdit_ClearTagsFlag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t3", Name: "host"})
	mock.CreateSnapshot(nil, "vm-t3", types.SnapshotSpec{Name: "snap-a", Tags: []string{"to-remove"}})

	if _, err := runCLI("snapshot", "edit", "vm-t3", "snap-a", "--clear-tags"); err != nil {
		t.Fatalf("snapshot edit --clear-tags: %v", err)
	}

	snaps, _ := mock.ListSnapshots(nil, "vm-t3")
	if len(snaps) != 1 || len(snaps[0].Tags) != 0 {
		t.Errorf("expected tags cleared, got %+v", snaps)
	}
}

func TestCLI_SnapshotEdit_RejectsConflictingTagFlags(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t4", Name: "host"})
	mock.CreateSnapshot(nil, "vm-t4", types.SnapshotSpec{Name: "snap-a"})

	_, err := runCLI("snapshot", "edit", "vm-t4", "snap-a", "--tag", "x", "--clear-tags")
	if err == nil {
		t.Fatalf("expected error when --tag and --clear-tags are both set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want 'mutually exclusive'", err.Error())
	}
}

func TestCLI_SnapshotList_FilterByTag(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t5", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-t5", Name: "snap-a", Tags: []string{"audit"}})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-t5", Name: "snap-b", Tags: []string{"production"}})

	out, err := runCLI("snapshot", "list", "vm-t5", "--tag", "audit")
	if err != nil {
		t.Fatalf("snapshot list --tag: %v", err)
	}
	if !strings.Contains(out, "snap-a") {
		t.Errorf("output should include snap-a: %q", out)
	}
	if strings.Contains(out, "snap-b") {
		t.Errorf("output should not include snap-b (tag mismatch): %q", out)
	}
}

func TestCLI_SnapshotList_FilterByTag_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t6", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-t6", Name: "snap-a", Tags: []string{"audit"}})

	out, err := runCLI("snapshot", "list", "vm-t6", "--tag", "AUDIT")
	if err != nil {
		t.Fatalf("snapshot list --tag AUDIT: %v", err)
	}
	if !strings.Contains(out, "snap-a") {
		t.Errorf("output should include snap-a (case-insensitive): %q", out)
	}
}

func TestCLI_SnapshotList_ShowsTagsColumn(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-t7", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-t7", Name: "snap-a", Tags: []string{"prod", "backup"}})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-t7", Name: "snap-b"})

	out, err := runCLI("snapshot", "list", "vm-t7")
	if err != nil {
		t.Fatalf("snapshot list: %v", err)
	}
	if !strings.Contains(out, "prod,backup") {
		t.Errorf("tagged row should show 'prod,backup' joined column, got %q", out)
	}
}

// --- webhook list pagination flag forwarding (roadmap 5.4.19) ---

func TestCLI_WebhookList_ForwardsLimit(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--limit", "25"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "per_page=25") {
		t.Fatalf("expected per_page=25 in query, got %q", state.lastQuery)
	}
	// --page defaults to 1, which should not be forwarded explicitly so the
	// daemon's default-page-1 contract handles it. Use a leading "&" or "?"
	// boundary check so per_page=N doesn't trigger a false positive.
	if strings.Contains(state.lastQuery, "&page=") || strings.HasPrefix(state.lastQuery, "page=") {
		t.Fatalf("page=1 default must not be forwarded: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ForwardsLimitAndPage(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--limit", "10", "--page", "3"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "per_page=10") {
		t.Fatalf("expected per_page=10 in query, got %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "page=3") {
		t.Fatalf("expected page=3 in query, got %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_PageWithoutLimitOmitsBothParams(t *testing.T) {
	// --page on its own is a no-op without --limit because the daemon
	// returns the full set when per_page is unset. Forwarding page=2
	// alone would be misleading.
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--page", "5"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if strings.Contains(state.lastQuery, "per_page=") {
		t.Fatalf("per_page must not be forwarded without --limit: %q", state.lastQuery)
	}
	if strings.Contains(state.lastQuery, "&page=") || strings.HasPrefix(state.lastQuery, "page=") {
		t.Fatalf("page must not be forwarded without --limit: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_LimitComposesWithFilters(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--search", "audit", "--tag", "production", "--sort", "url", "--order", "desc",
		"--limit", "5"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	for _, want := range []string{"search=audit", "tag=production", "sort=url", "order=desc", "per_page=5"} {
		if !strings.Contains(state.lastQuery, want) {
			t.Errorf("query missing %q: %q", want, state.lastQuery)
		}
	}
}
