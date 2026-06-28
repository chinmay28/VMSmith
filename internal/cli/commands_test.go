package cli

import (
	"bytes"
	"context"
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

func TestCLI_VMCreate_Windows(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "create", "win-vm",
		"--image", "win2022.qcow2",
		"--os", "windows",
		"--os-variant", "windows-server-2022",
		"--admin-password", "P@ssw0rd!",
		"--ram", "4096",
		"--disk", "64",
	)
	if err != nil {
		t.Fatalf("vm create (windows): %v", err)
	}
	if !strings.Contains(out, "windows-server-2022") {
		t.Errorf("expected OS variant in output, got: %q", out)
	}

	vms, _ := mock.List(nil)
	if len(vms) != 1 {
		t.Fatalf("expected 1 VM, got %d", len(vms))
	}
	if !vms[0].Spec.IsWindows() {
		t.Errorf("OSType = %q, want windows", vms[0].Spec.OSType)
	}
	if vms[0].Spec.OSVariant != "windows-server-2022" {
		t.Errorf("OSVariant = %q, want windows-server-2022", vms[0].Spec.OSVariant)
	}
}

func TestCLI_VMCreate_WindowsGeneratesAdminPassword(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "create", "win-autopw",
		"--image", "win2022.qcow2",
		"--os", "windows",
		"--ram", "4096",
		"--disk", "64",
	)
	if err != nil {
		t.Fatalf("vm create: %v", err)
	}
	if !strings.Contains(out, "Generated Administrator password") {
		t.Errorf("expected one-time password banner; got %q", out)
	}
	// Extract the password line — it's the indented line after the banner.
	if !strings.Contains(out, "Save it now") {
		t.Errorf("expected one-time-only warning in output; got %q", out)
	}
}

func TestCLI_VMCreate_LinuxDoesNotShowPasswordBanner(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "create", "linux-vm",
		"--image", "ubuntu.qcow2",
	)
	if err != nil {
		t.Fatalf("vm create: %v", err)
	}
	if strings.Contains(out, "Generated Administrator password") {
		t.Errorf("Linux VM should not show password banner; got %q", out)
	}
}

func TestCLI_VMCreate_WindowsWithExplicitPasswordDoesNotShowBanner(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	out, err := runCLI("vm", "create", "win-explicit-pw",
		"--image", "win2022.qcow2",
		"--os", "windows",
		"--admin-password", "MyChosen!Pass1",
		"--ram", "4096",
		"--disk", "64",
	)
	if err != nil {
		t.Fatalf("vm create: %v", err)
	}
	if strings.Contains(out, "Generated Administrator password") {
		t.Errorf("explicit admin_password should suppress the banner; got %q", out)
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

func TestCLI_VMList_FilterByDefaultUser_ExactMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "root"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "ubuntu"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "ubuntu"}})

	out, err := runCLI("vm", "list", "--default-user", "ubuntu")
	if err != nil {
		t.Fatalf("vm list --default-user: %v", err)
	}
	if strings.Contains(out, "alpha") || !strings.Contains(out, "beta") || !strings.Contains(out, "gamma") {
		t.Fatalf("expected only ubuntu VMs (beta, gamma), got %q", out)
	}
}

func TestCLI_VMList_FilterByDefaultUser_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "ec2-user"}})

	out, err := runCLI("vm", "list", "--default-user", "EC2-USER")
	if err != nil {
		t.Fatalf("vm list --default-user: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected case-insensitive match for alpha, got %q", out)
	}
}

func TestCLI_VMList_FilterByDefaultUser_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "root"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "ubuntu"}})

	out, err := runCLI("vm", "list", "--default-user", "")
	if err != nil {
		t.Fatalf("vm list --default-user: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("expected empty filter to return all, got %q", out)
	}
}

func TestCLI_VMList_FilterByDefaultUser_NoMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "root"}})

	out, err := runCLI("vm", "list", "--default-user", "nobody")
	if err != nil {
		t.Fatalf("vm list --default-user: %v", err)
	}
	if strings.Contains(out, "alpha") {
		t.Fatalf("expected no matches, got %q", out)
	}
}

func TestCLI_VMList_FilterByDefaultUser_ComposesWithStatus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "root"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "root"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "ubuntu"}})

	out, err := runCLI("vm", "list", "--default-user", "root", "--status", "running")
	if err != nil {
		t.Fatalf("vm list --default-user --status: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || strings.Contains(out, "gamma") {
		t.Fatalf("expected only alpha (root + running), got %q", out)
	}
}

// TestCLI_VMList_FilterByDefaultUser_RootMatchesEmpty verifies that
// `--default-user root` matches VMs whose Spec.DefaultUser is empty —
// the runtime default per `lifecycle.go` is root, so the filter should
// match both explicit-root and unset entries.
func TestCLI_VMList_FilterByDefaultUser_RootMatchesEmpty(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: ""}})       // implicit root
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "root"}})    // explicit root
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "gamma", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DefaultUser: "ubuntu"}}) // not root

	out, err := runCLI("vm", "list", "--default-user", "root")
	if err != nil {
		t.Fatalf("vm list --default-user root: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") || strings.Contains(out, "gamma") {
		t.Fatalf("expected alpha + beta (root-SSH VMs), got %q", out)
	}
}

// 5.6.8 — per-os-family filter on `vmsmith vm list`.

func TestCLI_VMList_FilterByOSType_ExactMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "winbox", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--os-type", "windows")
	if err != nil {
		t.Fatalf("vm list --os-type: %v", err)
	}
	if strings.Contains(out, "alpha") || !strings.Contains(out, "winbox") {
		t.Fatalf("expected only winbox, got %q", out)
	}
}

func TestCLI_VMList_FilterByOSType_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "winbox", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--os-type", "WINDOWS")
	if err != nil {
		t.Fatalf("vm list --os-type: %v", err)
	}
	if !strings.Contains(out, "winbox") {
		t.Fatalf("expected case-insensitive match for winbox, got %q", out)
	}
}

func TestCLI_VMList_FilterByOSType_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "winbox", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--os-type", "")
	if err != nil {
		t.Fatalf("vm list --os-type empty: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "winbox") {
		t.Fatalf("expected empty filter to return all, got %q", out)
	}
}

// TestCLI_VMList_FilterByOSType_LinuxMatchesEmpty: mirrors the
// `--default-user root` empty-means-root contract: empty OSType resolves
// to linux (`pkg/types/vm.go::VMSpec.ResolvedOSType`), so the filter must
// match both explicit-linux and unset entries.
func TestCLI_VMList_FilterByOSType_LinuxMatchesEmpty(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: ""}})               // implicit linux
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}}) // explicit linux
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "winbox", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--os-type", "linux")
	if err != nil {
		t.Fatalf("vm list --os-type linux: %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") || strings.Contains(out, "winbox") {
		t.Fatalf("expected alpha + beta (linux), got %q", out)
	}
}

func TestCLI_VMList_FilterByOSType_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})

	_, err := runCLI("vm", "list", "--os-type", "plan9")
	if err == nil {
		t.Fatal("expected error for invalid --os-type, got nil")
	}
	if !strings.Contains(err.Error(), "--os-type") {
		t.Fatalf("error should reference --os-type, got %v", err)
	}
}

func TestCLI_VMList_FilterByOSType_ComposesWithStatus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "a-win-run", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "b-win-stop", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "c-lin-run", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})

	out, err := runCLI("vm", "list", "--os-type", "windows", "--status", "running")
	if err != nil {
		t.Fatalf("vm list compose: %v", err)
	}
	if !strings.Contains(out, "a-win-run") || strings.Contains(out, "b-win-stop") || strings.Contains(out, "c-lin-run") {
		t.Fatalf("expected only a-win-run, got %q", out)
	}
}

// 5.4.66 — `--os-variant` on `vmsmith vm list`. Sub-axis of `--os-type windows`:
// case-insensitive exact-match against `spec.os_variant`; empty stored value
// excluded; unknown variant rejected client-side before the manager is
// contacted.

func TestCLI_VMList_FilterByOSVariant_ExactMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11-host", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "srv22", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "linux", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})

	out, err := runCLI("vm", "list", "--os-variant", "windows-11")
	if err != nil {
		t.Fatalf("vm list --os-variant: %v", err)
	}
	if !strings.Contains(out, "win11-host") || strings.Contains(out, "srv22") || strings.Contains(out, "linux") {
		t.Fatalf("expected only win11-host, got %q", out)
	}
}

func TestCLI_VMList_FilterByOSVariant_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "srv22", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"}})

	out, err := runCLI("vm", "list", "--os-variant", "WINDOWS-SERVER-2022")
	if err != nil {
		t.Fatalf("vm list --os-variant case: %v", err)
	}
	if !strings.Contains(out, "srv22") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByOSVariant_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})

	out, err := runCLI("vm", "list", "--os-variant", "")
	if err != nil {
		t.Fatalf("vm list --os-variant empty: %v", err)
	}
	if !strings.Contains(out, "win11") || !strings.Contains(out, "linux") {
		t.Fatalf("expected empty filter to return all VMs, got %q", out)
	}
}

// TestCLI_VMList_FilterByOSVariant_ExcludesEmptyStored documents the
// no-empty-match contract: unlike `--os-type linux` which matches empty-stored,
// `--os-variant` has no documented default so empty drops out whenever the
// filter is set.
func TestCLI_VMList_FilterByOSVariant_ExcludesEmptyStored(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-unset", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--os-variant", "windows-11")
	if err != nil {
		t.Fatalf("vm list --os-variant: %v", err)
	}
	if !strings.Contains(out, "win11") || strings.Contains(out, "win-unset") {
		t.Fatalf("expected only win11 (empty-stored excluded), got %q", out)
	}
}

func TestCLI_VMList_FilterByOSVariant_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-11"}})

	_, err := runCLI("vm", "list", "--os-variant", "windows-12")
	if err == nil {
		t.Fatal("expected error for invalid --os-variant, got nil")
	}
	if !strings.Contains(err.Error(), "--os-variant") {
		t.Fatalf("error should reference --os-variant, got %v", err)
	}
}

func TestCLI_VMList_FilterByOSVariant_ComposesWithOSType(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "a-win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-11"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "b-srv22", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-server-2022"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "c-win10", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, OSVariant: "windows-10"}})

	out, err := runCLI("vm", "list", "--os-type", "windows", "--os-variant", "windows-11")
	if err != nil {
		t.Fatalf("vm list compose: %v", err)
	}
	if !strings.Contains(out, "a-win11") || strings.Contains(out, "b-srv22") || strings.Contains(out, "c-win10") {
		t.Fatalf("expected only a-win11, got %q", out)
	}
}

// 5.4.68 — `--firmware` on `vmsmith vm list`. Three-value vocabulary
// (bios|uefi|ovmf); `bios` matches stored "" or "bios" (the SeaBIOS default);
// `uefi` and `ovmf` strict-match the stored value.

func TestCLI_VMList_FilterByFirmware_ExactMatchUEFI(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, Firmware: types.FirmwareUEFI}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "legacy", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Firmware: types.FirmwareBIOS}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "ovmf-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, Firmware: types.FirmwareOVMF}})

	out, err := runCLI("vm", "list", "--firmware", "uefi")
	if err != nil {
		t.Fatalf("vm list --firmware: %v", err)
	}
	if !strings.Contains(out, "win11") || strings.Contains(out, "legacy") || strings.Contains(out, "ovmf-vm") {
		t.Fatalf("expected only win11 (uefi strict-match), got %q", out)
	}
}

func TestCLI_VMList_FilterByFirmware_BIOSMatchesEmptyStored(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "bios-explicit", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Firmware: types.FirmwareBIOS}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "bios-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "uefi-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, Firmware: types.FirmwareUEFI}})

	out, err := runCLI("vm", "list", "--firmware", "bios")
	if err != nil {
		t.Fatalf("vm list --firmware bios: %v", err)
	}
	if !strings.Contains(out, "bios-explicit") || !strings.Contains(out, "bios-empty") || strings.Contains(out, "uefi-vm") {
		t.Fatalf("expected bios-explicit + bios-empty, no uefi-vm; got %q", out)
	}
}

func TestCLI_VMList_FilterByFirmware_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, Firmware: types.FirmwareUEFI}})

	out, err := runCLI("vm", "list", "--firmware", "UEFI")
	if err != nil {
		t.Fatalf("vm list --firmware UEFI: %v", err)
	}
	if !strings.Contains(out, "win11") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByFirmware_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, Firmware: types.FirmwareUEFI}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "legacy", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--firmware", "")
	if err != nil {
		t.Fatalf("vm list --firmware empty: %v", err)
	}
	if !strings.Contains(out, "win11") || !strings.Contains(out, "legacy") {
		t.Fatalf("expected empty filter to return all VMs, got %q", out)
	}
}

func TestCLI_VMList_FilterByFirmware_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win11", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, Firmware: types.FirmwareUEFI}})

	_, err := runCLI("vm", "list", "--firmware", "coreboot")
	if err == nil {
		t.Fatal("expected error for invalid --firmware, got nil")
	}
	if !strings.Contains(err.Error(), "--firmware") {
		t.Fatalf("error should reference --firmware, got %v", err)
	}
}

func TestCLI_VMList_FilterByFirmware_ComposesWithOSType(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-uefi", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, Firmware: types.FirmwareUEFI}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-uefi", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, Firmware: types.FirmwareUEFI}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win-bios", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, Firmware: types.FirmwareBIOS}})

	out, err := runCLI("vm", "list", "--os-type", "windows", "--firmware", "uefi")
	if err != nil {
		t.Fatalf("vm list compose: %v", err)
	}
	if !strings.Contains(out, "win-uefi") || strings.Contains(out, "linux-uefi") || strings.Contains(out, "win-bios") {
		t.Fatalf("expected only win-uefi, got %q", out)
	}
}

// 5.4.69 — `--disk-bus` on `vmsmith vm list`. Two-value vocabulary
// (virtio|sata); resolution defers to VMSpec.ResolvedDiskBus so an empty
// stored disk_bus matches the OS-family default — Linux VMs match
// `--disk-bus virtio`, Windows VMs match `--disk-bus sata`.

func TestCLI_VMList_FilterByDiskBus_ExactMatchVirtio(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "virtio-explicit", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DiskBus: types.DiskBusVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "sata-explicit", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, DiskBus: types.DiskBusSATA}})

	out, err := runCLI("vm", "list", "--disk-bus", "virtio")
	if err != nil {
		t.Fatalf("vm list --disk-bus virtio: %v", err)
	}
	if !strings.Contains(out, "virtio-explicit") || strings.Contains(out, "sata-explicit") {
		t.Fatalf("expected only virtio-explicit, got %q", out)
	}
}

func TestCLI_VMList_FilterByDiskBus_VirtioMatchesEmptyLinux(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--disk-bus", "virtio")
	if err != nil {
		t.Fatalf("vm list --disk-bus virtio: %v", err)
	}
	if !strings.Contains(out, "linux-empty") || strings.Contains(out, "win-empty") {
		t.Fatalf("expected linux-empty matches default-virtio, no windows; got %q", out)
	}
}

func TestCLI_VMList_FilterByDiskBus_SATAMatchesEmptyWindows(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--disk-bus", "sata")
	if err != nil {
		t.Fatalf("vm list --disk-bus sata: %v", err)
	}
	if !strings.Contains(out, "win-empty") || strings.Contains(out, "linux-empty") {
		t.Fatalf("expected win-empty matches default-sata, no linux; got %q", out)
	}
}

func TestCLI_VMList_FilterByDiskBus_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "virtio-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DiskBus: types.DiskBusVirtio}})

	out, err := runCLI("vm", "list", "--disk-bus", "VIRTIO")
	if err != nil {
		t.Fatalf("vm list --disk-bus VIRTIO: %v", err)
	}
	if !strings.Contains(out, "virtio-vm") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByDiskBus_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "virtio-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DiskBus: types.DiskBusVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "sata-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, DiskBus: types.DiskBusSATA}})

	out, err := runCLI("vm", "list", "--disk-bus", "")
	if err != nil {
		t.Fatalf("vm list --disk-bus empty: %v", err)
	}
	if !strings.Contains(out, "virtio-vm") || !strings.Contains(out, "sata-vm") {
		t.Fatalf("expected empty filter to return all VMs, got %q", out)
	}
}

func TestCLI_VMList_FilterByDiskBus_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "virtio-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, DiskBus: types.DiskBusVirtio}})

	_, err := runCLI("vm", "list", "--disk-bus", "nvme")
	if err == nil {
		t.Fatal("expected error for invalid --disk-bus, got nil")
	}
	if !strings.Contains(err.Error(), "--disk-bus") {
		t.Fatalf("error should reference --disk-bus, got %v", err)
	}
}

func TestCLI_VMList_FilterByDiskBus_ComposesWithOSType(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-virtio", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, DiskBus: types.DiskBusVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-virtio", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, DiskBus: types.DiskBusVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win-sata", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, DiskBus: types.DiskBusSATA}})

	out, err := runCLI("vm", "list", "--os-type", "windows", "--disk-bus", "virtio")
	if err != nil {
		t.Fatalf("vm list compose: %v", err)
	}
	if !strings.Contains(out, "win-virtio") || strings.Contains(out, "linux-virtio") || strings.Contains(out, "win-sata") {
		t.Fatalf("expected only win-virtio, got %q", out)
	}
}

// 5.4.70 — `--nic-model` on `vmsmith vm list`. Two-value vocabulary
// (virtio|e1000e); resolution defers to VMSpec.ResolvedNICModel so an empty
// stored nic_model matches the OS-family default (virtio on Linux, e1000e
// on Windows); an explicit stored value always wins.

func TestCLI_VMList_FilterByNICModel_ExactMatchVirtio(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-virtio", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-e1000e", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})

	out, err := runCLI("vm", "list", "--nic-model", "virtio")
	if err != nil {
		t.Fatalf("vm list --nic-model: %v", err)
	}
	if !strings.Contains(out, "linux-virtio") || strings.Contains(out, "win-e1000e") {
		t.Fatalf("expected only linux-virtio, got %q", out)
	}
}

func TestCLI_VMList_FilterByNICModel_VirtioMatchesEmptyLinux(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-explicit", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win-default", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--nic-model", "virtio")
	if err != nil {
		t.Fatalf("vm list --nic-model virtio: %v", err)
	}
	if !strings.Contains(out, "linux-explicit") || !strings.Contains(out, "linux-empty") || strings.Contains(out, "win-default") {
		t.Fatalf("expected linux-explicit + linux-empty, no win-default; got %q", out)
	}
}

func TestCLI_VMList_FilterByNICModel_E1000eMatchesEmptyWindows(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-explicit", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "linux-default", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})

	out, err := runCLI("vm", "list", "--nic-model", "e1000e")
	if err != nil {
		t.Fatalf("vm list --nic-model e1000e: %v", err)
	}
	if !strings.Contains(out, "win-explicit") || !strings.Contains(out, "win-empty") || strings.Contains(out, "linux-default") {
		t.Fatalf("expected win-explicit + win-empty, no linux-default; got %q", out)
	}
}

func TestCLI_VMList_FilterByNICModel_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})

	out, err := runCLI("vm", "list", "--nic-model", "VIRTIO")
	if err != nil {
		t.Fatalf("vm list --nic-model VIRTIO: %v", err)
	}
	if !strings.Contains(out, "linux") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByNICModel_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-virtio", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-e1000e", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})

	out, err := runCLI("vm", "list", "--nic-model", "")
	if err != nil {
		t.Fatalf("vm list --nic-model empty: %v", err)
	}
	if !strings.Contains(out, "linux-virtio") || !strings.Contains(out, "win-e1000e") {
		t.Fatalf("expected empty filter to return all VMs, got %q", out)
	}
}

func TestCLI_VMList_FilterByNICModel_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})

	_, err := runCLI("vm", "list", "--nic-model", "rtl8139")
	if err == nil {
		t.Fatal("expected error for invalid --nic-model, got nil")
	}
	if !strings.Contains(err.Error(), "--nic-model") {
		t.Fatalf("error should reference --nic-model, got %v", err)
	}
}

func TestCLI_VMList_FilterByNICModel_ComposesWithOSType(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-virtio", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, NICModel: types.NICModelVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-virtio", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, NICModel: types.NICModelVirtio}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win-e1000e", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, NICModel: types.NICModelE1000e}})

	out, err := runCLI("vm", "list", "--os-type", "windows", "--nic-model", "virtio")
	if err != nil {
		t.Fatalf("vm list compose: %v", err)
	}
	if !strings.Contains(out, "win-virtio") || strings.Contains(out, "linux-virtio") || strings.Contains(out, "win-e1000e") {
		t.Fatalf("expected only win-virtio, got %q", out)
	}
}

// 5.4.71 — `--machine` on `vmsmith vm list`. Free-form value (alphabet
// `[A-Za-z0-9._-]+`), case-sensitive (libvirt machine names are
// case-sensitive at the QEMU layer); empty stored spec.machine resolves to
// types.DefaultMachine via VMSpec.ResolvedMachine, so `--machine pc-q35-6.2`
// matches both stored "pc-q35-6.2" AND VMs with no override.

func TestCLI_VMList_FilterByMachine_ExactMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "q35-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Machine: "q35"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "rhel-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Machine: "pc-q35-rhel9.6.0"}})

	out, err := runCLI("vm", "list", "--machine", "q35")
	if err != nil {
		t.Fatalf("vm list --machine: %v", err)
	}
	if !strings.Contains(out, "q35-vm") || strings.Contains(out, "rhel-vm") {
		t.Fatalf("expected only q35-vm, got %q", out)
	}
}

func TestCLI_VMList_FilterByMachine_DefaultMatchesEmpty(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "explicit-default", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Machine: types.DefaultMachine}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "empty-machine", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "other-machine", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Machine: "q35"}})

	out, err := runCLI("vm", "list", "--machine", types.DefaultMachine)
	if err != nil {
		t.Fatalf("vm list --machine %s: %v", types.DefaultMachine, err)
	}
	if !strings.Contains(out, "explicit-default") || !strings.Contains(out, "empty-machine") || strings.Contains(out, "other-machine") {
		t.Fatalf("expected explicit-default + empty-machine, no other-machine; got %q", out)
	}
}

func TestCLI_VMList_FilterByMachine_IsCaseSensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "lower", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Machine: "q35"}})

	out, err := runCLI("vm", "list", "--machine", "Q35")
	if err != nil {
		t.Fatalf("vm list --machine: %v", err)
	}
	if strings.Contains(out, "lower") {
		t.Fatalf("expected case-sensitive non-match, got %q", out)
	}
}

func TestCLI_VMList_FilterByMachine_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "q35-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Machine: "q35"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "default-vm", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--machine", "")
	if err != nil {
		t.Fatalf("vm list --machine empty: %v", err)
	}
	if !strings.Contains(out, "q35-vm") || !strings.Contains(out, "default-vm") {
		t.Fatalf("expected empty filter to return all VMs, got %q", out)
	}
}

// 5.4.72 — `--clock-offset` on `vmsmith vm list`. Two-value vocabulary
// (utc / localtime), case-insensitive; empty stored spec.clock_offset
// resolves to the OS-family default via VMSpec.ResolvedClockOffset (utc for
// Linux, localtime for Windows), so `--clock-offset utc` matches both
// stored "utc" AND Linux VMs with no override.

func TestCLI_VMList_FilterByClockOffset_ExactMatchUTC(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-utc", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-localtime", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})

	out, err := runCLI("vm", "list", "--clock-offset", "utc")
	if err != nil {
		t.Fatalf("vm list --clock-offset: %v", err)
	}
	if !strings.Contains(out, "linux-utc") || strings.Contains(out, "win-localtime") {
		t.Fatalf("expected only linux-utc, got %q", out)
	}
}

func TestCLI_VMList_FilterByClockOffset_UTCMatchesEmptyLinux(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-explicit", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win-default", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--clock-offset", "utc")
	if err != nil {
		t.Fatalf("vm list --clock-offset utc: %v", err)
	}
	if !strings.Contains(out, "linux-explicit") || !strings.Contains(out, "linux-empty") || strings.Contains(out, "win-default") {
		t.Fatalf("expected linux-explicit + linux-empty, no win-default; got %q", out)
	}
}

func TestCLI_VMList_FilterByClockOffset_LocaltimeMatchesEmptyWindows(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-explicit", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-empty", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "linux-default", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux}})

	out, err := runCLI("vm", "list", "--clock-offset", "localtime")
	if err != nil {
		t.Fatalf("vm list --clock-offset localtime: %v", err)
	}
	if !strings.Contains(out, "win-explicit") || !strings.Contains(out, "win-empty") || strings.Contains(out, "linux-default") {
		t.Fatalf("expected win-explicit + win-empty, no linux-default; got %q", out)
	}
}

func TestCLI_VMList_FilterByClockOffset_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})

	out, err := runCLI("vm", "list", "--clock-offset", "UTC")
	if err != nil {
		t.Fatalf("vm list --clock-offset UTC: %v", err)
	}
	if !strings.Contains(out, "linux") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByClockOffset_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-utc", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win-localtime", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})

	out, err := runCLI("vm", "list", "--clock-offset", "")
	if err != nil {
		t.Fatalf("vm list --clock-offset empty: %v", err)
	}
	if !strings.Contains(out, "linux-utc") || !strings.Contains(out, "win-localtime") {
		t.Fatalf("expected empty filter to return all VMs, got %q", out)
	}
}

func TestCLI_VMList_FilterByMachine_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "x", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Machine: "q35"}})

	// Slash isn't in the [A-Za-z0-9._-]+ alphabet.
	_, err := runCLI("vm", "list", "--machine", "pc-q35/6.2")
	if err == nil {
		t.Fatal("expected error for invalid --machine, got nil")
	}
	if !strings.Contains(err.Error(), "--machine") {
		t.Fatalf("error should reference --machine, got %v", err)
	}
}

func TestCLI_VMList_FilterByMachine_ComposesWithOSType(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-q35", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, Machine: "q35"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-q35", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, Machine: "q35"}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win-default", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows}})

	out, err := runCLI("vm", "list", "--os-type", "windows", "--machine", "q35")
	if err != nil {
		t.Fatalf("vm list compose: %v", err)
	}
	if !strings.Contains(out, "win-q35") || strings.Contains(out, "linux-q35") || strings.Contains(out, "win-default") {
		t.Fatalf("expected only win-q35, got %q", out)
	}
}

func TestCLI_VMList_FilterByClockOffset_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})

	_, err := runCLI("vm", "list", "--clock-offset", "gmt")
	if err == nil {
		t.Fatal("expected error for invalid --clock-offset, got nil")
	}
	if !strings.Contains(err.Error(), "--clock-offset") {
		t.Fatalf("error should reference --clock-offset, got %v", err)
	}
}

func TestCLI_VMList_FilterByClockOffset_ComposesWithOSType(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-utc", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetUTC}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-utc", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, OSType: types.OSTypeLinux, ClockOffset: types.ClockOffsetUTC}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win-localtime", Spec: types.VMSpec{CPUs: 1, RAMMB: 4096, OSType: types.OSTypeWindows, ClockOffset: types.ClockOffsetLocaltime}})

	out, err := runCLI("vm", "list", "--os-type", "windows", "--clock-offset", "utc")
	if err != nil {
		t.Fatalf("vm list compose: %v", err)
	}
	if !strings.Contains(out, "win-utc") || strings.Contains(out, "linux-utc") || strings.Contains(out, "win-localtime") {
		t.Fatalf("expected only win-utc, got %q", out)
	}
}

// 5.7.9 — `--gpu` on `vmsmith vm list`. Mirrors the API `?gpu=` filter:
// any-of exact match against the VM's requested passthrough addresses
// (normalised long form); accepts the long ('0000:01:00.0') or short
// ('01:00.0') form; VMs with no requested GPUs drop out when set; garbage
// fails `IsValidPCIAddress` with an error so a typo surfaces before the
// daemon is contacted.

func TestCLI_VMList_FilterByGPU_ExactMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "no-gpu", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "rtx-4080", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, GPUs: []string{"0000:01:00.0"}}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "other-slot", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, GPUs: []string{"0000:02:00.0"}}})

	out, err := runCLI("vm", "list", "--gpu", "0000:01:00.0")
	if err != nil {
		t.Fatalf("vm list --gpu: %v", err)
	}
	if !strings.Contains(out, "rtx-4080") || strings.Contains(out, "other-slot") || strings.Contains(out, "no-gpu") {
		t.Fatalf("expected only rtx-4080, got %q", out)
	}
}

func TestCLI_VMList_FilterByGPU_ShortFormMatchesLongFormStored(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "long-stored", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, GPUs: []string{"0000:01:00.0"}}})

	out, err := runCLI("vm", "list", "--gpu", "01:00.0")
	if err != nil {
		t.Fatalf("vm list --gpu: %v", err)
	}
	if !strings.Contains(out, "long-stored") {
		t.Fatalf("expected short-form to match long-form stored, got %q", out)
	}
}

func TestCLI_VMList_FilterByGPU_AnyOfMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "single", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, GPUs: []string{"0000:01:00.0"}}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "dual", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, GPUs: []string{"0000:02:00.0", "0000:03:00.0"}}})

	out, err := runCLI("vm", "list", "--gpu", "0000:03:00.0")
	if err != nil {
		t.Fatalf("vm list --gpu: %v", err)
	}
	if !strings.Contains(out, "dual") || strings.Contains(out, "single") {
		t.Fatalf("expected only dual, got %q", out)
	}
}

func TestCLI_VMList_FilterByGPU_ExcludesEmpty(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "no-gpu", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "with-gpu", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, GPUs: []string{"0000:01:00.0"}}})

	out, err := runCLI("vm", "list", "--gpu", "0000:01:00.0")
	if err != nil {
		t.Fatalf("vm list --gpu: %v", err)
	}
	if !strings.Contains(out, "with-gpu") || strings.Contains(out, "no-gpu") {
		t.Fatalf("expected only with-gpu, got %q", out)
	}
}

func TestCLI_VMList_FilterByGPU_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "a", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, GPUs: []string{"0000:01:00.0"}}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "b", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--gpu", "")
	if err != nil {
		t.Fatalf("vm list --gpu empty: %v", err)
	}
	if !strings.Contains(out, "a") || !strings.Contains(out, "b") {
		t.Fatalf("expected empty filter to return all VMs, got %q", out)
	}
}

func TestCLI_VMList_FilterByGPU_RejectsInvalidValue(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "x", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	_, err := runCLI("vm", "list", "--gpu", "not-a-pci-addr")
	if err == nil {
		t.Fatal("expected error for invalid --gpu, got nil")
	}
	if !strings.Contains(err.Error(), "--gpu") {
		t.Fatalf("error should reference --gpu, got %v", err)
	}
}

// 5.4.36 — per-network filter on `vmsmith vm list`.
func cliVMWithNetwork(id, name string, netNames ...string) *types.VM {
	attachments := make([]types.NetworkAttachment, 0, len(netNames))
	for _, n := range netNames {
		attachments = append(attachments, types.NetworkAttachment{Name: n})
	}
	return &types.VM{ID: id, Name: name, Spec: types.VMSpec{CPUs: 1, RAMMB: 512, Networks: attachments}}
}

func TestCLI_VMList_FilterByNetwork_ExactMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(cliVMWithNetwork("vm-1", "alpha", "data-net"))
	mock.SeedVM(cliVMWithNetwork("vm-2", "beta", "storage-net"))
	mock.SeedVM(cliVMWithNetwork("vm-3", "gamma", "data-net", "storage-net"))

	out, err := runCLI("vm", "list", "--network", "data-net")
	if err != nil {
		t.Fatalf("vm list --network: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || !strings.Contains(out, "gamma") {
		t.Fatalf("expected only data-net VMs (alpha, gamma), got %q", out)
	}
}

func TestCLI_VMList_FilterByNetwork_IsCaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(cliVMWithNetwork("vm-1", "alpha", "Data-Net"))

	out, err := runCLI("vm", "list", "--network", "DATA-NET")
	if err != nil {
		t.Fatalf("vm list --network: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected case-insensitive match for alpha, got %q", out)
	}
}

func TestCLI_VMList_FilterByNetwork_NoMatch(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(cliVMWithNetwork("vm-1", "alpha", "data-net"))

	out, err := runCLI("vm", "list", "--network", "nope")
	if err != nil {
		t.Fatalf("vm list --network: %v", err)
	}
	if strings.Contains(out, "alpha") {
		t.Fatalf("expected no matches, got %q", out)
	}
}

func TestCLI_VMList_FilterByNetwork_ComposesWithStatus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	running := cliVMWithNetwork("vm-1", "alpha", "data-net")
	running.State = types.VMStateRunning
	stopped := cliVMWithNetwork("vm-2", "beta", "data-net")
	stopped.State = types.VMStateStopped
	mock.SeedVM(running)
	mock.SeedVM(stopped)

	out, err := runCLI("vm", "list", "--network", "data-net", "--status", "running")
	if err != nil {
		t.Fatalf("vm list --network --status: %v", err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("expected only alpha (data-net + running), got %q", out)
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

// --- 5.4.30: vm list --since / --until time-range filter on created_at ---

func TestCLI_VMList_FilterBySince(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "early", CreatedAt: day(1), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "late", CreatedAt: day(30), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--since", "2026-05-10T00:00:00Z")
	if err != nil {
		t.Fatalf("vm list --since: %v", err)
	}
	if !strings.Contains(out, "late") || strings.Contains(out, "early") {
		t.Fatalf("expected only late, got %q", out)
	}
}

func TestCLI_VMList_FilterByUntil(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "early", CreatedAt: day(1), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "late", CreatedAt: day(30), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--until", "2026-05-15T00:00:00Z")
	if err != nil {
		t.Fatalf("vm list --until: %v", err)
	}
	if !strings.Contains(out, "early") || strings.Contains(out, "late") {
		t.Fatalf("expected only early, got %q", out)
	}
}

func TestCLI_VMList_FilterBySinceAndUntil(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "vm-1", CreatedAt: day(1), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "vm-15", CreatedAt: day(15), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "vm-30", CreatedAt: day(30), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--since", "2026-05-10T00:00:00Z", "--until", "2026-05-20T00:00:00Z")
	if err != nil {
		t.Fatalf("vm list --since --until: %v", err)
	}
	if !strings.Contains(out, "vm-15") || strings.Contains(out, "vm-1\t") || strings.Contains(out, "vm-30") {
		t.Fatalf("expected only vm-15, got %q", out)
	}
}

func TestCLI_VMList_RejectsInvalidSince(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--since", "yesterday")
	if err == nil || !strings.Contains(err.Error(), "invalid --since") {
		t.Fatalf("expected invalid --since error, got %v", err)
	}
}

func TestCLI_VMList_RejectsInvalidUntil(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--until", "2026-13-99")
	if err == nil || !strings.Contains(err.Error(), "invalid --until") {
		t.Fatalf("expected invalid --until error, got %v", err)
	}
}

func TestCLI_VMList_EmptySinceOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "any", CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--since", "  ")
	if err != nil {
		t.Fatalf("vm list --since '  ': %v", err)
	}
	if !strings.Contains(out, "any") {
		t.Fatalf("whitespace --since should be no-op, got %q", out)
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

	_, err := runCLI("vm", "list", "--sort", "memory")
	if err == nil || !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("expected invalid --sort error, got %v", err)
	}
}

func TestCLI_VMList_SortByCPUs(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "small", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "large", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 8, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "medium", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 4, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "cpus")
	if err != nil {
		t.Fatalf("vm list --sort cpus: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"small", "medium", "large"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByRAMMBDesc(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "tiny", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "big", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 8192}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "med", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 2048}})

	out, err := runCLI("vm", "list", "--sort", "ram_mb", "--order", "desc")
	if err != nil {
		t.Fatalf("vm list --sort ram_mb --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d", len(rows))
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"big", "med", "tiny"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestCLI_VMList_SortByDiskGB(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "small", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024, DiskGB: 10}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "huge", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024, DiskGB: 500}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "med", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024, DiskGB: 100}})

	out, err := runCLI("vm", "list", "--sort", "disk_gb")
	if err != nil {
		t.Fatalf("vm list --sort disk_gb: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d", len(rows))
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"small", "med", "huge"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q", i, got[i], want[i])
		}
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

func TestCLI_VMList_SortByIPNumeric(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "big-ten", State: types.VMStateRunning, IP: "192.168.100.10", Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "stopped", State: types.VMStateStopped, IP: "", Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "small-two", State: types.VMStateRunning, IP: "192.168.100.2", Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "ip")
	if err != nil {
		t.Fatalf("vm list --sort ip: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	// Numeric sort: 192.168.100.2 first (not 192.168.100.10 — lex would invert).
	// Empty IP sinks to the tail in ascending order.
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"small-two", "big-ten", "stopped"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_RejectsInvalidSort_IPMentioned(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "address")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "invalid --sort") || !strings.Contains(msg, "ip") {
		t.Errorf("error %q should mention 'invalid --sort' and advertise 'ip' as a valid axis", msg)
	}
	// 5.4.88 — the error message must also advertise `image` now that the
	// VM list whitelist includes it.
	if !strings.Contains(msg, "image") {
		t.Errorf("error %q should advertise 'image' as a valid axis", msg)
	}
	// 5.4.91 — the error message must also advertise `default_user` now
	// that the VM list whitelist includes it.
	if !strings.Contains(msg, "default_user") {
		t.Errorf("error %q should advertise 'default_user' as a valid axis", msg)
	}
}

// ============================================================
// VM list `image` sort axis (5.4.88)
// ============================================================

func TestCLI_VMList_SortByImage_AscEmptyTrailing(t *testing.T) {
	// Concrete images sort case-insensitively (alpine < rocky9). The
	// empty-image VM sinks to the tail in ascending order, mirroring the
	// nil-trailing contract on the runtime IP axis (5.4.85).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "rocky-prod", State: types.VMStateRunning, Spec: types.VMSpec{Image: "rocky9.qcow2", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "no-image", State: types.VMStateStopped, Spec: types.VMSpec{Image: "", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "alpine-bastion", State: types.VMStateRunning, Spec: types.VMSpec{Image: "alpine.qcow2", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "image")
	if err != nil {
		t.Fatalf("vm list --sort image: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"alpine-bastion", "rocky-prod", "no-image"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByImage_CaseInsensitive(t *testing.T) {
	// `Rocky9.qcow2` and `rocky9.qcow2` collate as identical so the sort
	// agrees with the case-insensitive `?image=` filter (5.4.22).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{Image: "Rocky9.qcow2", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "alpine", State: types.VMStateRunning, Spec: types.VMSpec{Image: "alpine.qcow2", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{Image: "rocky9.qcow2", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "image", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort image: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// alpine < rocky9 (case-folded). Equal-image cohort tiebreaks on id
	// ascending so vm-1 ("uppercase") precedes vm-3 ("lowercase").
	want := []string{"alpine", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// ============================================================
// VM list `default_user` sort axis (5.4.91)
// ============================================================

func TestCLI_VMList_SortByDefaultUser_AscEmptyResolvesToRoot(t *testing.T) {
	// Empty `default_user` resolves to "root" so the unset VM collates
	// with the explicit-root VM in alphabetical order rather than sinking
	// to the tail — diverges from the nil-trailing image-sort contract
	// because `default_user` has a documented default.
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "ec2", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "ec2-user", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "explicit-root", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "root", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "unset", State: types.VMStateStopped, Spec: types.VMSpec{DefaultUser: "", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "admin", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "admin", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "default_user")
	if err != nil {
		t.Fatalf("vm list --sort default_user: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	want := []string{"admin", "ec2", "explicit-root", "unset"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByDefaultUser_CaseInsensitive(t *testing.T) {
	// `ROOT` and `root` collate as identical so the sort agrees with the
	// case-insensitive `?default_user=` filter (5.4.23).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "ROOT", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "admin", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "admin", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{DefaultUser: "root", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "default_user", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort default_user: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"admin", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
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

// ============================================================
// VM list `gpu` sort axis (5.7.13)
// ============================================================

func TestCLI_VMList_SortByGPU_AscEmptyTrailing(t *testing.T) {
	// Concrete GPUs sort lexicographically on the canonical long form;
	// VMs with no requested GPUs sink to the tail in ascending order,
	// mirroring the nil-trailing contract on the runtime IP axis (5.4.85)
	// and the image axis (5.4.88).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "second-slot", State: types.VMStateRunning, Spec: types.VMSpec{GPUs: []string{"0000:02:00.0"}, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "no-gpu", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "first-slot", State: types.VMStateRunning, Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "gpu")
	if err != nil {
		t.Fatalf("vm list --sort gpu: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"first-slot", "second-slot", "no-gpu"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByGPU_NormalisesShortForm(t *testing.T) {
	// A VM persisted with the short PCI form ("01:00.0") collates
	// identically to one persisted with the long form ("0000:01:00.0")
	// so the sort agrees with the alphabet contract on `?gpu=` (5.7.9).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "long-02", State: types.VMStateRunning, Spec: types.VMSpec{GPUs: []string{"0000:02:00.0"}, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "short-01", State: types.VMStateRunning, Spec: types.VMSpec{GPUs: []string{"01:00.0"}, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "long-03", State: types.VMStateRunning, Spec: types.VMSpec{GPUs: []string{"0000:03:00.0"}, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "gpu", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort gpu: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"short-01", "long-02", "long-03"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// ============================================================
// VM list `os_type` sort axis (5.4.100)
// ============================================================

func TestCLI_VMList_SortByOSType_AscEmptyResolvesToLinux(t *testing.T) {
	// Empty `os_type` resolves to "linux" via ResolvedOSType so the unset
	// VM collates with the explicit-linux VM in alphabetical order rather
	// than sinking to the tail — diverges from the nil-trailing image-sort
	// contract because `os_type` has a documented default, same rationale
	// as the `default_user` axis (5.4.91).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "win-1", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeWindows, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-explicit", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeLinux, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "linux-empty", State: types.VMStateStopped, Spec: types.VMSpec{OSType: "", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "win-2", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeWindows, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "os_type")
	if err != nil {
		t.Fatalf("vm list --sort os_type: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	want := []string{"linux-explicit", "linux-empty", "win-1", "win-2"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByOSType_CaseInsensitive(t *testing.T) {
	// `WINDOWS` and `windows` must collate as identical so the sort agrees
	// with the case-insensitive `?os_type=` filter (5.6.8).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{OSType: "WINDOWS", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux", State: types.VMStateRunning, Spec: types.VMSpec{OSType: "linux", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{OSType: "windows", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "os_type", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort os_type: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"linux", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByOSType_RejectsUnknownAxis(t *testing.T) {
	// A nearby misspelling (e.g. "os-type") must surface a clear error that
	// advertises `os_type` in the whitelist so operators discover the right
	// flag.
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "os-type")
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "os_type") {
		t.Errorf("expected --sort error message to advertise 'os_type' as a valid axis, got: %v", err)
	}
}

// 5.4.101 — case-insensitive `firmware` sort axis with empty→bios resolution.

func TestCLI_VMList_SortByFirmware_AscEmptyResolvesToBIOS(t *testing.T) {
	// Empty `firmware` resolves to "bios" via resolveFirmware so the unset
	// VM collates with the explicit-bios VM in alphabetical order rather
	// than sinking to the tail — diverges from the nil-trailing image-sort
	// contract because `firmware` has a documented default, same rationale
	// as the `os_type` axis (5.4.100) collapsing empty to "linux".
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uefi-vm", State: types.VMStateRunning, Spec: types.VMSpec{Firmware: types.FirmwareUEFI, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "bios-explicit", State: types.VMStateRunning, Spec: types.VMSpec{Firmware: types.FirmwareBIOS, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "bios-empty", State: types.VMStateStopped, Spec: types.VMSpec{Firmware: "", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "ovmf-vm", State: types.VMStateRunning, Spec: types.VMSpec{Firmware: types.FirmwareOVMF, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "firmware")
	if err != nil {
		t.Fatalf("vm list --sort firmware: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	want := []string{"bios-explicit", "bios-empty", "ovmf-vm", "uefi-vm"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByFirmware_CaseInsensitive(t *testing.T) {
	// `UEFI` and `uefi` must collate as identical so the sort agrees with
	// the case-insensitive `?firmware=` filter (5.4.68).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{Firmware: "UEFI", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "bios", State: types.VMStateRunning, Spec: types.VMSpec{Firmware: "bios", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{Firmware: "uefi", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "firmware", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort firmware: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"bios", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByFirmware_RejectsUnknownAxis(t *testing.T) {
	// A nearby misspelling (e.g. "fw") must surface a clear error that
	// advertises `firmware` in the whitelist so operators discover the right
	// flag.
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "fw")
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "firmware") {
		t.Errorf("expected --sort error message to advertise 'firmware' as a valid axis, got: %v", err)
	}
}

// 5.4.103 — case-insensitive `os_variant` sort axis with nil-trailing semantics.

func TestCLI_VMList_SortByOSVariant_AscEmptyTrailing(t *testing.T) {
	// Diverges from os_type / firmware — `os_variant` has no documented
	// default so empty stored values (typically Linux guests) sink to the
	// tail in ascending order, mirroring the nil-trailing semantics on
	// image / gpu / ip rather than collapsing to a default.
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "ws22", State: types.VMStateRunning, Spec: types.VMSpec{OSVariant: "windows-server-2022", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-empty", State: types.VMStateStopped, Spec: types.VMSpec{OSVariant: "", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "win10", State: types.VMStateRunning, Spec: types.VMSpec{OSVariant: "windows-10", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "win11", State: types.VMStateRunning, Spec: types.VMSpec{OSVariant: "windows-11", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "os_variant")
	if err != nil {
		t.Fatalf("vm list --sort os_variant: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	want := []string{"win10", "win11", "ws22", "linux-empty"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByOSVariant_CaseInsensitive(t *testing.T) {
	// `Windows-11` and `windows-11` must collate as identical so the sort
	// agrees with the case-insensitive `?os_variant=` filter (5.4.66).
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{OSVariant: "WINDOWS-11", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "win10", State: types.VMStateRunning, Spec: types.VMSpec{OSVariant: "windows-10", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{OSVariant: "windows-11", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "os_variant", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort os_variant: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"win10", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByOSVariant_RejectsUnknownAxis(t *testing.T) {
	// A nearby misspelling (e.g. "osvariant") must surface a clear error
	// that advertises `os_variant` in the whitelist so operators discover
	// the right flag.
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "osvariant")
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "os_variant") {
		t.Errorf("expected --sort error message to advertise 'os_variant' as a valid axis, got: %v", err)
	}
}

// 5.4.104 — case-insensitive `disk_bus` sort axis with OS-family-aware default.

func TestCLI_VMList_SortByDiskBus_AscResolvesOSFamilyDefault(t *testing.T) {
	// Empty `disk_bus` resolves to the OS-family default via ResolvedDiskBus
	// (virtio for Linux, sata for Windows) so empty VMs collate with the
	// explicit-bus cohort of the same family rather than sinking to the
	// tail — mirrors the firmware (5.4.101) and os_type (5.4.100) documented
	// default rationale.
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-virtio", State: types.VMStateRunning, Spec: types.VMSpec{DiskBus: types.DiskBusVirtio, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-sata", State: types.VMStateRunning, Spec: types.VMSpec{DiskBus: types.DiskBusSATA, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "linux-empty", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})                                            // empty Linux → virtio
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "windows-empty", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeWindows, CPUs: 2, RAMMB: 2048, DiskGB: 32}}) // empty Windows → sata

	out, err := runCLI("vm", "list", "--sort", "disk_bus")
	if err != nil {
		t.Fatalf("vm list --sort disk_bus: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	// asc: sata cohort first (vm-2 linux-sata, vm-4 windows-empty), then
	// virtio (vm-1 linux-virtio, vm-3 linux-empty). Equal-bus cohort
	// tiebreaks on id ascending.
	want := []string{"linux-sata", "windows-empty", "linux-virtio", "linux-empty"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByDiskBus_CaseInsensitive(t *testing.T) {
	// `VIRTIO` and `virtio` must collate as identical so the sort agrees
	// with the case-insensitive `?disk_bus=` filter contract.
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{DiskBus: "VIRTIO", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "sata", State: types.VMStateRunning, Spec: types.VMSpec{DiskBus: "sata", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{DiskBus: "virtio", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "disk_bus", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort disk_bus: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"sata", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByDiskBus_RejectsUnknownAxis(t *testing.T) {
	// A nearby misspelling must surface a clear error that advertises
	// `disk_bus` in the whitelist so operators discover the right axis.
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "diskbus") // missing underscore
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "disk_bus") {
		t.Errorf("expected --sort error message to advertise 'disk_bus' as a valid axis, got: %v", err)
	}
}

// 5.4.105 — case-insensitive `nic_model` sort axis with OS-family-aware default.

func TestCLI_VMList_SortByNICModel_AscResolvesOSFamilyDefault(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-virtio", State: types.VMStateRunning, Spec: types.VMSpec{NICModel: types.NICModelVirtio, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-e1000e", State: types.VMStateRunning, Spec: types.VMSpec{NICModel: types.NICModelE1000e, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "linux-empty", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})                                            // empty Linux → virtio
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "windows-empty", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeWindows, CPUs: 2, RAMMB: 2048, DiskGB: 32}}) // empty Windows → e1000e

	out, err := runCLI("vm", "list", "--sort", "nic_model")
	if err != nil {
		t.Fatalf("vm list --sort nic_model: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	// asc: e1000e cohort first (vm-2, vm-4), then virtio (vm-1, vm-3).
	want := []string{"linux-e1000e", "windows-empty", "linux-virtio", "linux-empty"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByNICModel_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{NICModel: "VIRTIO", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "e1000e", State: types.VMStateRunning, Spec: types.VMSpec{NICModel: "e1000e", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{NICModel: "virtio", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "nic_model", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort nic_model: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"e1000e", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByNICModel_RejectsUnknownAxis(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "nicmodel") // missing underscore
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "nic_model") {
		t.Errorf("expected --sort error message to advertise 'nic_model' as a valid axis, got: %v", err)
	}
}

// 5.4.107 — case-sensitive `machine` sort axis with documented-default fallback.

func TestCLI_VMList_SortByMachine_AscResolvesDefault(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "virt", State: types.VMStateRunning, Spec: types.VMSpec{Machine: "virt-7.2", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "q35", State: types.VMStateRunning, Spec: types.VMSpec{Machine: "q35", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "empty", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}}) // empty → pc-q35-6.2
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "old-pc-q35", State: types.VMStateRunning, Spec: types.VMSpec{Machine: "pc-q35-5.2", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "machine")
	if err != nil {
		t.Fatalf("vm list --sort machine: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	// asc alphabetical: pc-q35-5.2 < pc-q35-6.2 < q35 < virt-7.2.
	// vm-3 (empty → pc-q35-6.2) sits in the default bucket between vm-4 and vm-2.
	want := []string{"old-pc-q35", "empty", "q35", "virt"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByMachine_CaseSensitive(t *testing.T) {
	// libvirt machine names are case-sensitive at the QEMU layer. Mirror the
	// case-sensitive `?machine=` filter contract: ASCII uppercase < lowercase.
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "lower-q35", State: types.VMStateRunning, Spec: types.VMSpec{Machine: "q35", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "upper-q35", State: types.VMStateRunning, Spec: types.VMSpec{Machine: "Q35", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "upper-pc", State: types.VMStateRunning, Spec: types.VMSpec{Machine: "PC-Q35-6.2", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "machine", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort machine: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// asc: "PC-Q35-6.2" < "Q35" < "q35" (ASCII uppercase < lowercase)
	want := []string{"upper-pc", "upper-q35", "lower-q35"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByMachine_RejectsUnknownAxis(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "machinetype") // not an axis
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "machine") {
		t.Errorf("expected --sort error message to advertise 'machine' as a valid axis, got: %v", err)
	}
}

// 5.4.106 — case-insensitive `clock_offset` sort axis with OS-family-aware default.

func TestCLI_VMList_SortByClockOffset_AscResolvesOSFamilyDefault(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "linux-utc", State: types.VMStateRunning, Spec: types.VMSpec{ClockOffset: types.ClockOffsetUTC, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "linux-localtime", State: types.VMStateRunning, Spec: types.VMSpec{ClockOffset: types.ClockOffsetLocaltime, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "linux-empty", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-4", Name: "windows-empty", State: types.VMStateRunning, Spec: types.VMSpec{OSType: types.OSTypeWindows, CPUs: 2, RAMMB: 2048, DiskGB: 32}})

	out, err := runCLI("vm", "list", "--sort", "clock_offset")
	if err != nil {
		t.Fatalf("vm list --sort clock_offset: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 5 {
		t.Fatalf("expected header + 4 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1], rows[4][1]}
	want := []string{"linux-localtime", "windows-empty", "linux-utc", "linux-empty"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByClockOffset_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "uppercase", State: types.VMStateRunning, Spec: types.VMSpec{ClockOffset: "UTC", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "localtime", State: types.VMStateRunning, Spec: types.VMSpec{ClockOffset: "localtime", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "lowercase", State: types.VMStateRunning, Spec: types.VMSpec{ClockOffset: "utc", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "clock_offset", "--order", "asc")
	if err != nil {
		t.Fatalf("vm list --sort clock_offset: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	want := []string{"localtime", "uppercase", "lowercase"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByClockOffset_RejectsUnknownAxis(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "clockoffset") // missing underscore
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "clock_offset") {
		t.Errorf("expected --sort error message to advertise 'clock_offset' as a valid axis, got: %v", err)
	}
}

// 5.4.108 — boolean `auto_start` sort axis.

func TestCLI_VMList_SortByAutoStart_AscPutsFalseFirst(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "enabled-a", State: types.VMStateRunning, Spec: types.VMSpec{AutoStart: true, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "disabled", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "enabled-b", State: types.VMStateRunning, Spec: types.VMSpec{AutoStart: true, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "auto_start")
	if err != nil {
		t.Fatalf("vm list --sort auto_start: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// asc: false cohort first (vm-2 disabled), then true cohort
	// (vm-1 enabled-a, vm-3 enabled-b) — id tiebreak ascending.
	want := []string{"disabled", "enabled-a", "enabled-b"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByAutoStart_DescPutsTrueFirst(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "enabled-a", State: types.VMStateRunning, Spec: types.VMSpec{AutoStart: true, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "disabled", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "enabled-b", State: types.VMStateRunning, Spec: types.VMSpec{AutoStart: true, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "auto_start", "--order", "desc")
	if err != nil {
		t.Fatalf("vm list --sort auto_start --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// desc inverts id tiebreak so enabled-b precedes enabled-a in
	// the true cohort, and the disabled cohort sinks to the tail.
	want := []string{"enabled-b", "enabled-a", "disabled"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByAutoStart_RejectsUnknownAxis(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "autostart") // missing underscore
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "auto_start") {
		t.Errorf("expected --sort error message to advertise 'auto_start' as a valid axis, got: %v", err)
	}
}

// 5.4.109 — boolean `locked` sort axis.

func TestCLI_VMList_SortByLocked_AscPutsFalseFirst(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "locked-a", State: types.VMStateRunning, Spec: types.VMSpec{Locked: true, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "unlocked", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "locked-b", State: types.VMStateRunning, Spec: types.VMSpec{Locked: true, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "locked")
	if err != nil {
		t.Fatalf("vm list --sort locked: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// asc: false cohort first (vm-2 unlocked), then true cohort
	// (vm-1 locked-a, vm-3 locked-b) — id tiebreak ascending.
	want := []string{"unlocked", "locked-a", "locked-b"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByLocked_DescPutsTrueFirst(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "locked-a", State: types.VMStateRunning, Spec: types.VMSpec{Locked: true, CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "unlocked", State: types.VMStateStopped, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "locked-b", State: types.VMStateRunning, Spec: types.VMSpec{Locked: true, CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "locked", "--order", "desc")
	if err != nil {
		t.Fatalf("vm list --sort locked --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// desc inverts id tiebreak so locked-b precedes locked-a in
	// the true cohort, and the unlocked cohort sinks to the tail.
	want := []string{"locked-b", "locked-a", "unlocked"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByLocked_RejectsUnknownAxis(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "lock") // typo
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("expected --sort error message to advertise 'locked' as a valid axis, got: %v", err)
	}
}

// 5.4.110 — numeric IP `nat_static_ip` sort axis (CIDR-aware, nil-trailing).

func TestCLI_VMList_SortByNatStaticIP_AscNumeric(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "high", State: types.VMStateRunning, Spec: types.VMSpec{NatStaticIP: "192.168.100.10/24", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "low", State: types.VMStateRunning, Spec: types.VMSpec{NatStaticIP: "192.168.100.2/24", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "dhcp", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "nat_static_ip")
	if err != nil {
		t.Fatalf("vm list --sort nat_static_ip: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// asc numeric: 192.168.100.2 < 192.168.100.10 < (empty)
	want := []string{"low", "high", "dhcp"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByNatStaticIP_DescNilLeading(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "high", State: types.VMStateRunning, Spec: types.VMSpec{NatStaticIP: "192.168.100.10/24", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "low", State: types.VMStateRunning, Spec: types.VMSpec{NatStaticIP: "192.168.100.2/24", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "dhcp", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "nat_static_ip", "--order", "desc")
	if err != nil {
		t.Fatalf("vm list --sort nat_static_ip --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// desc: empty heads, then numeric descending.
	want := []string{"dhcp", "high", "low"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByNatStaticIP_RejectsUnknownAxis(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "natstaticip") // missing underscores
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "nat_static_ip") {
		t.Errorf("expected --sort error message to advertise 'nat_static_ip' as a valid axis, got: %v", err)
	}
}

// 5.4.111 — numeric IP `nat_gateway` sort axis (bare IP, nil-trailing).

func TestCLI_VMList_SortByNatGateway_AscNumeric(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "high", State: types.VMStateRunning, Spec: types.VMSpec{NatGateway: "192.168.100.10", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "low", State: types.VMStateRunning, Spec: types.VMSpec{NatGateway: "192.168.100.2", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "no-gw", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "nat_gateway")
	if err != nil {
		t.Fatalf("vm list --sort nat_gateway: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// asc numeric: 192.168.100.2 < 192.168.100.10 < (empty)
	want := []string{"low", "high", "no-gw"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByNatGateway_DescNilLeading(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "high", State: types.VMStateRunning, Spec: types.VMSpec{NatGateway: "192.168.100.10", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "low", State: types.VMStateRunning, Spec: types.VMSpec{NatGateway: "192.168.100.2", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "no-gw", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "nat_gateway", "--order", "desc")
	if err != nil {
		t.Fatalf("vm list --sort nat_gateway --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// desc: empty heads, then numeric descending.
	want := []string{"no-gw", "high", "low"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByNatGateway_RejectsUnknownAxis(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "natgateway") // missing underscore
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "nat_gateway") {
		t.Errorf("expected --sort error message to advertise 'nat_gateway' as a valid axis, got: %v", err)
	}
}

// ----- 5.4.120: `description` sort axis on the VM list -----

func TestCLI_VMList_SortByDescription_Asc(t *testing.T) {
	// asc: case-insensitive alphabetical on `spec.description`, with
	// VMs that have an empty description sinking to the tail. Mirrors
	// the image (5.4.118) / template (5.4.119) description axes.
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{Description: "Web prod", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "bravo", State: types.VMStateRunning, Spec: types.VMSpec{Description: "", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "charlie", State: types.VMStateRunning, Spec: types.VMSpec{Description: "alpha-svc", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "description")
	if err != nil {
		t.Fatalf("vm list --sort description: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// asc: `alpha-svc` < `Web prod` (case-folded; capital `W` should
	// collate with lowercase `w` and stay in the same bucket as the
	// case-insensitive `?search=` filter), then (empty) at the tail.
	want := []string{"charlie", "alpha", "bravo"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByDescription_DescEmptyHeads(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", State: types.VMStateRunning, Spec: types.VMSpec{Description: "aaa", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "bravo", State: types.VMStateRunning, Spec: types.VMSpec{Description: "", CPUs: 1, RAMMB: 1024}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "charlie", State: types.VMStateRunning, Spec: types.VMSpec{Description: "zzz", CPUs: 1, RAMMB: 1024}})

	out, err := runCLI("vm", "list", "--sort", "description", "--order", "desc")
	if err != nil {
		t.Fatalf("vm list --sort description --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][1], rows[2][1], rows[3][1]}
	// desc: empty heads (the undocumented majority), then reverse-alphabetic.
	want := []string{"bravo", "charlie", "alpha"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_VMList_SortByDescription_RejectsUnknownAxis(t *testing.T) {
	// Garbage --sort value must surface a clear error that advertises
	// `description` in the whitelist so operators know it's a valid axis.
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "descriptionz")
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("expected --sort error message to advertise 'description' as a valid axis, got: %v", err)
	}
}

func TestCLI_VMList_SortByGPU_RejectsUnknownAxis(t *testing.T) {
	// Garbage --sort value must surface a clear error that advertises `gpu`
	// in the whitelist so operators know it's a valid axis.
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--sort", "gpus") // typo
	if err == nil {
		t.Fatalf("expected error for invalid --sort, got nil")
	}
	if !strings.Contains(err.Error(), "gpu") {
		t.Errorf("expected --sort error message to advertise 'gpu' as a valid axis, got: %v", err)
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

func TestCLI_VMEdit_ClockOffset(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{
		ID:   "vm-clk",
		Name: "clocktest",
		Spec: types.VMSpec{Name: "clocktest", CPUs: 2, RAMMB: 2048, DiskGB: 20, OSType: types.OSTypeWindows},
	})

	if _, err := runCLI("vm", "edit", "vm-clk", "--clock-offset", "UTC"); err != nil {
		t.Fatalf("vm edit --clock-offset: %v", err)
	}
	updated, err := mock.Get(context.Background(), "vm-clk")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Spec.ClockOffset != "utc" {
		t.Errorf("Spec.ClockOffset = %q, want utc (cli lowercases)", updated.Spec.ClockOffset)
	}

	// Clearing the override (--clock-offset "") returns the OS-family default
	// at next render via ResolvedClockOffset().
	if _, err := runCLI("vm", "edit", "vm-clk", "--clock-offset", ""); err != nil {
		t.Fatalf("vm edit --clock-offset \"\": %v", err)
	}
	cleared, _ := mock.Get(context.Background(), "vm-clk")
	if cleared.Spec.ClockOffset != "" {
		t.Errorf("Spec.ClockOffset after clear = %q, want empty", cleared.Spec.ClockOffset)
	}
	if got := cleared.Spec.ResolvedClockOffset(); got != "localtime" {
		t.Errorf("ResolvedClockOffset after clear = %q, want localtime (windows default)", got)
	}
}

func TestCLI_VMCreate_ClockOffset(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	if _, err := runCLI("vm", "create", "clk-create",
		"--image", "rocky9", "--cpus", "2", "--ram", "2048", "--disk", "20",
		"--clock-offset", "UTC"); err != nil {
		t.Fatalf("vm create: %v", err)
	}
	vms, _ := mock.List(context.Background())
	if len(vms) != 1 {
		t.Fatalf("vms len = %d, want 1", len(vms))
	}
	if vms[0].Spec.ClockOffset != "utc" {
		t.Errorf("Spec.ClockOffset = %q, want utc (cli lowercases)", vms[0].Spec.ClockOffset)
	}
}

// TestCLI_VMCreate_DeviceOverrides exercises the 5.6.15 per-VM device
// override flags. Each flag is lowercased + whitespace-trimmed by the CLI
// before being forwarded to the daemon, mirroring the --os / --clock-offset
// contract.
func TestCLI_VMCreate_DeviceOverrides(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	if _, err := runCLI("vm", "create", "dev-cli",
		"--image", "rocky9", "--cpus", "2", "--ram", "2048", "--disk", "20",
		"--disk-bus", "SATA",
		"--nic-model", "E1000E",
		"--machine", "pc-q35-rhel9.6.0",
		"--firmware", "UEFI",
		"--virtio-win-iso", "/tmp/virtio-win.iso",
	); err != nil {
		t.Fatalf("vm create: %v", err)
	}
	vms, _ := mock.List(context.Background())
	if len(vms) != 1 {
		t.Fatalf("vms len = %d, want 1", len(vms))
	}
	got := vms[0].Spec
	if got.DiskBus != "sata" {
		t.Errorf("DiskBus = %q, want sata", got.DiskBus)
	}
	if got.NICModel != "e1000e" {
		t.Errorf("NICModel = %q, want e1000e", got.NICModel)
	}
	if got.Machine != "pc-q35-rhel9.6.0" {
		t.Errorf("Machine = %q, want pc-q35-rhel9.6.0", got.Machine)
	}
	if got.Firmware != "uefi" {
		t.Errorf("Firmware = %q, want uefi", got.Firmware)
	}
	if got.VirtioWinISO != "/tmp/virtio-win.iso" {
		t.Errorf("VirtioWinISO = %q, want /tmp/virtio-win.iso", got.VirtioWinISO)
	}
}

// Roadmap 5.6.12 — `vm edit --disk-bus / --nic-model` and the
// `vm set-virtio` shortcut. The CLI lowercases + trims the values
// before forwarding to the daemon, mirroring the `--clock-offset`
// contract.
func TestCLI_VMEdit_DiskBusAndNICModel(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{
		ID:   "vm-bus",
		Name: "wintest",
		Spec: types.VMSpec{Name: "wintest", CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, DiskBus: "sata", NICModel: "e1000e"},
	})

	if _, err := runCLI("vm", "edit", "vm-bus", "--disk-bus", "VIRTIO", "--nic-model", "VIRTIO"); err != nil {
		t.Fatalf("vm edit --disk-bus / --nic-model: %v", err)
	}
	updated, err := mock.Get(context.Background(), "vm-bus")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Spec.DiskBus != "virtio" {
		t.Errorf("Spec.DiskBus = %q, want virtio (cli lowercases)", updated.Spec.DiskBus)
	}
	if updated.Spec.NICModel != "virtio" {
		t.Errorf("Spec.NICModel = %q, want virtio", updated.Spec.NICModel)
	}

	// Clear the override and confirm ResolvedDiskBus falls back to the
	// Windows default (sata).
	if _, err := runCLI("vm", "edit", "vm-bus", "--disk-bus", ""); err != nil {
		t.Fatalf("vm edit --disk-bus \"\": %v", err)
	}
	cleared, _ := mock.Get(context.Background(), "vm-bus")
	if cleared.Spec.DiskBus != "" {
		t.Errorf("Spec.DiskBus after clear = %q, want empty", cleared.Spec.DiskBus)
	}
	if got := cleared.Spec.ResolvedDiskBus(); got != "sata" {
		t.Errorf("ResolvedDiskBus after clear = %q, want sata (windows default)", got)
	}
}

func TestCLI_VMSetVirtio(t *testing.T) {
	// `vm set-virtio` is a shortcut for `vm edit --disk-bus virtio
	// --nic-model virtio`. Verify it sends both fields atomically.
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{
		ID:   "vm-virt",
		Name: "wintest",
		Spec: types.VMSpec{Name: "wintest", CPUs: 2, RAMMB: 4096, DiskGB: 40, OSType: types.OSTypeWindows, DiskBus: "sata", NICModel: "e1000e"},
	})

	out, err := runCLI("vm", "set-virtio", "vm-virt")
	if err != nil {
		t.Fatalf("vm set-virtio: %v", err)
	}
	if !strings.Contains(out, "virtio") {
		t.Errorf("expected virtio in output, got: %q", out)
	}

	updated, err := mock.Get(context.Background(), "vm-virt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Spec.DiskBus != "virtio" {
		t.Errorf("Spec.DiskBus = %q, want virtio", updated.Spec.DiskBus)
	}
	if updated.Spec.NICModel != "virtio" {
		t.Errorf("Spec.NICModel = %q, want virtio", updated.Spec.NICModel)
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
	// 5.4.117 — the rejection message must advertise source_vm so scripts
	// can discover the full supported axis set from the CLI alone.
	if !strings.Contains(err.Error(), "source_vm") {
		t.Errorf("invalid --sort error %q must advertise source_vm", err.Error())
	}
	// 5.4.118 — likewise must advertise the description axis.
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("invalid --sort error %q must advertise description", err.Error())
	}
}

// 5.4.118 — case-insensitive sort axis on the image `description` field.
// Empty descriptions (the common case) sink to the tail of asc / head of
// desc, mirroring every other nullable string sort axis (source_vm, ip,
// guest_ip, image, last_fired_at, last_delivery_at, actor).
func TestCLI_ImageList_SortByDescription_Asc(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-no-desc", Name: "no-desc", Path: "/t/n.qcow2", SizeBytes: 100, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-rocky", Name: "rocky", Path: "/t/r.qcow2", SizeBytes: 100, Format: "qcow2", Description: "Rocky 9 base", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-alpine", Name: "alpine", Path: "/t/a.qcow2", SizeBytes: 100, Format: "qcow2", Description: "alpine 3.19 minimal", CreatedAt: now})

	out, err := runCLI("image", "list", "--sort", "description")
	if err != nil {
		t.Fatalf("image list --sort description: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][0], rows[2][0], rows[3][0]}
	// alpine < Rocky (case-insensitive) < (empty tail)
	want := []string{"img-alpine", "img-rocky", "img-no-desc"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_ImageList_SortByDescription_DescEmptyHeads(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-rocky", Name: "rocky", Path: "/t/r.qcow2", SizeBytes: 100, Format: "qcow2", Description: "Rocky 9 base", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-no-desc", Name: "no-desc", Path: "/t/n.qcow2", SizeBytes: 100, Format: "qcow2", CreatedAt: now})

	out, err := runCLI("image", "list", "--sort", "description", "--order", "desc")
	if err != nil {
		t.Fatalf("image list --sort description --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 3 {
		t.Fatalf("expected header + 2 rows, got %d: %v", len(rows), rows)
	}
	if rows[1][0] != "img-no-desc" {
		t.Errorf("first row id = %q, want img-no-desc (empty description heads desc)", rows[1][0])
	}
}

// 5.4.117 — case-insensitive sort axis on `source_vm` mirroring the existing
// case-insensitive `?source_vm=` exact-match filter; empty `source_vm`
// (uploaded images) sinks to the tail of asc / head of desc.
func TestCLI_ImageList_SortBySourceVM_Asc(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-uploaded", Name: "uploaded", Path: "/t/u.qcow2", SizeBytes: 100, Format: "qcow2", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-from-beta", Name: "from-beta", Path: "/t/b.qcow2", SizeBytes: 100, Format: "qcow2", SourceVM: "VM-BETA", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-from-alpha", Name: "from-alpha", Path: "/t/a.qcow2", SizeBytes: 100, Format: "qcow2", SourceVM: "vm-alpha", CreatedAt: now})

	out, err := runCLI("image", "list", "--sort", "source_vm")
	if err != nil {
		t.Fatalf("image list --sort source_vm: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 4 {
		t.Fatalf("expected header + 3 rows, got %d: %v", len(rows), rows)
	}
	got := []string{rows[1][0], rows[2][0], rows[3][0]}
	// alpha < BETA (case-insensitive) < (empty tail)
	want := []string{"img-from-alpha", "img-from-beta", "img-uploaded"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestCLI_ImageList_SortBySourceVM_DescEmptyHeads(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-from-alpha", Name: "from-alpha", Path: "/t/a.qcow2", SizeBytes: 100, Format: "qcow2", SourceVM: "vm-alpha", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-uploaded", Name: "uploaded", Path: "/t/u.qcow2", SizeBytes: 100, Format: "qcow2", CreatedAt: now})

	out, err := runCLI("image", "list", "--sort", "source_vm", "--order", "desc")
	if err != nil {
		t.Fatalf("image list --sort source_vm --order desc: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) < 3 {
		t.Fatalf("expected header + 2 rows, got %d: %v", len(rows), rows)
	}
	if rows[1][0] != "img-uploaded" {
		t.Errorf("first row id = %q, want img-uploaded (empty source_vm heads desc)", rows[1][0])
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

// --- Image list --min-size / --max-size (5.4.40) ---

func seedSizedImages(t *testing.T, s interface {
	PutImage(*types.Image) error
}) {
	t.Helper()
	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	for _, spec := range []struct {
		id, name string
		size     int64
	}{
		{"img-small", "small", 1 << 20}, // 1 MiB
		{"img-mid", "mid", 1 << 30},     // 1 GiB
		{"img-big", "big", 2 << 30},     // 2 GiB
	} {
		if err := s.PutImage(&types.Image{ID: spec.id, Name: spec.name, Path: "/tmp/" + spec.name + ".qcow2", SizeBytes: spec.size, Format: "qcow2", CreatedAt: now}); err != nil {
			t.Fatalf("seed image: %v", err)
		}
	}
}

func TestCLI_ImageList_FilterByMinSize(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedSizedImages(t, s)

	out, err := runCLI("image", "list", "--min-size", "1073741824") // >= 1 GiB
	if err != nil {
		t.Fatalf("image list --min-size: %v", err)
	}
	if strings.Contains(out, " small ") || !strings.Contains(out, "mid") || !strings.Contains(out, "big") {
		t.Fatalf("expected mid+big (>= 1 GiB), got %q", out)
	}
}

func TestCLI_ImageList_FilterByMaxSize(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedSizedImages(t, s)

	out, err := runCLI("image", "list", "--max-size", "1073741824") // <= 1 GiB
	if err != nil {
		t.Fatalf("image list --max-size: %v", err)
	}
	if !strings.Contains(out, "small") || !strings.Contains(out, "mid") || strings.Contains(out, "big") {
		t.Fatalf("expected small+mid (<= 1 GiB), got %q", out)
	}
}

func TestCLI_ImageList_FilterBySizeRange(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedSizedImages(t, s)

	out, err := runCLI("image", "list", "--min-size", "1048577", "--max-size", "1073741824")
	if err != nil {
		t.Fatalf("image list size range: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + only "mid"
		t.Fatalf("expected header + 1 row (mid), got %d: %v", len(rows), rows)
	}
	if rows[1][1] != "mid" {
		t.Fatalf("expected only mid in range, got %q", rows[1][1])
	}
}

func TestCLI_ImageList_RejectsInvalidMinSize(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "list", "--min-size", "ten-gigs")
	if err == nil || !strings.Contains(err.Error(), "invalid --min-size") {
		t.Fatalf("expected invalid --min-size error, got %v", err)
	}
}

func TestCLI_ImageList_RejectsNegativeMaxSize(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "list", "--max-size=-1")
	if err == nil || !strings.Contains(err.Error(), "invalid --max-size") {
		t.Fatalf("expected invalid --max-size error, got %v", err)
	}
}

// --- image list --prefix (5.4.77) ---

func seedPrefixImages(t *testing.T, s interface {
	PutImage(*types.Image) error
}) {
	t.Helper()
	now := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{"rocky-base", "rocky-gold", "ubuntu-base"} {
		if err := s.PutImage(&types.Image{ID: "img-" + name, Name: name, Path: "/tmp/" + name + ".qcow2", SizeBytes: 1 << 30, Format: "qcow2", CreatedAt: now}); err != nil {
			t.Fatalf("seed image: %v", err)
		}
	}
}

func TestCLI_ImageList_FilterByPrefix_Match(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedPrefixImages(t, s)

	out, err := runCLI("image", "list", "--prefix", "rocky-")
	if err != nil {
		t.Fatalf("image list --prefix: %v", err)
	}
	if !strings.Contains(out, "rocky-base") || !strings.Contains(out, "rocky-gold") || strings.Contains(out, "ubuntu-base") {
		t.Fatalf("expected only rocky-* images, got %q", out)
	}
}

func TestCLI_ImageList_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedPrefixImages(t, s)

	out, err := runCLI("image", "list", "--prefix", "Rocky-")
	if err != nil {
		t.Fatalf("image list --prefix Rocky-: %v", err)
	}
	if strings.Contains(out, "rocky-base") || strings.Contains(out, "rocky-gold") {
		t.Fatalf("expected case-sensitive non-match, got %q", out)
	}
}

func TestCLI_ImageList_FilterByPrefix_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedPrefixImages(t, s)

	out, err := runCLI("image", "list", "--prefix", "   ")
	if err != nil {
		t.Fatalf("image list --prefix '   ': %v", err)
	}
	if !strings.Contains(out, "rocky-base") || !strings.Contains(out, "rocky-gold") || !strings.Contains(out, "ubuntu-base") {
		t.Fatalf("expected every image when prefix is whitespace-only, got %q", out)
	}
}

// --- VM list --min-cpus / --max-cpus (5.4.44) ---

func seedCPUVMs(mock interface {
	SeedVM(*types.VM)
}) {
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "small", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 2, RAMMB: 2048, DiskGB: 20}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "mid", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 4, RAMMB: 4096, DiskGB: 80}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "big", State: types.VMStateRunning, Spec: types.VMSpec{CPUs: 8, RAMMB: 8192, DiskGB: 500}})
}

func TestCLI_VMList_FilterByMinCpus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock)

	out, err := runCLI("vm", "list", "--min-cpus", "4")
	if err != nil {
		t.Fatalf("vm list --min-cpus: %v", err)
	}
	if strings.Contains(out, "small") || !strings.Contains(out, "mid") || !strings.Contains(out, "big") {
		t.Fatalf("expected mid+big (>= 4 vCPUs), got %q", out)
	}
}

func TestCLI_VMList_FilterByMaxCpus(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock)

	out, err := runCLI("vm", "list", "--max-cpus", "4")
	if err != nil {
		t.Fatalf("vm list --max-cpus: %v", err)
	}
	if !strings.Contains(out, "small") || !strings.Contains(out, "mid") || strings.Contains(out, "big") {
		t.Fatalf("expected small+mid (<= 4 vCPUs), got %q", out)
	}
}

func TestCLI_VMList_FilterByCpuRange(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock)

	out, err := runCLI("vm", "list", "--min-cpus", "3", "--max-cpus", "6")
	if err != nil {
		t.Fatalf("vm list cpu range: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + only "mid"
		t.Fatalf("expected header + 1 row (mid), got %d: %v", len(rows), rows)
	}
	if rows[1][1] != "mid" {
		t.Fatalf("expected only mid in range, got %q", rows[1][1])
	}
}

func TestCLI_VMList_RejectsInvalidMinCpus(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--min-cpus", "lots")
	if err == nil || !strings.Contains(err.Error(), "invalid --min-cpus") {
		t.Fatalf("expected invalid --min-cpus error, got %v", err)
	}
}

func TestCLI_VMList_RejectsNegativeMaxCpus(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--max-cpus=-1")
	if err == nil || !strings.Contains(err.Error(), "invalid --max-cpus") {
		t.Fatalf("expected invalid --max-cpus error, got %v", err)
	}
}

// --- VM list --min-ram-mb / --max-ram-mb (5.4.48) ---

func TestCLI_VMList_FilterByMinRAM(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock) // small=2048, mid=4096, big=8192 MB

	out, err := runCLI("vm", "list", "--min-ram-mb", "4096")
	if err != nil {
		t.Fatalf("vm list --min-ram-mb: %v", err)
	}
	if strings.Contains(out, "small") || !strings.Contains(out, "mid") || !strings.Contains(out, "big") {
		t.Fatalf("expected mid+big (>= 4096 MB), got %q", out)
	}
}

func TestCLI_VMList_FilterByMaxRAM(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock)

	out, err := runCLI("vm", "list", "--max-ram-mb", "4096")
	if err != nil {
		t.Fatalf("vm list --max-ram-mb: %v", err)
	}
	if !strings.Contains(out, "small") || !strings.Contains(out, "mid") || strings.Contains(out, "big") {
		t.Fatalf("expected small+mid (<= 4096 MB), got %q", out)
	}
}

func TestCLI_VMList_FilterByRAMRange(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock)

	out, err := runCLI("vm", "list", "--min-ram-mb", "3000", "--max-ram-mb", "5000")
	if err != nil {
		t.Fatalf("vm list ram range: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + only "mid"
		t.Fatalf("expected header + 1 row (mid), got %d: %v", len(rows), rows)
	}
	if rows[1][1] != "mid" {
		t.Fatalf("expected only mid in range, got %q", rows[1][1])
	}
}

func TestCLI_VMList_RejectsInvalidMinRAM(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--min-ram-mb", "lots")
	if err == nil || !strings.Contains(err.Error(), "invalid --min-ram-mb") {
		t.Fatalf("expected invalid --min-ram-mb error, got %v", err)
	}
}

func TestCLI_VMList_RejectsNegativeMaxRAM(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--max-ram-mb=-1")
	if err == nil || !strings.Contains(err.Error(), "invalid --max-ram-mb") {
		t.Fatalf("expected invalid --max-ram-mb error, got %v", err)
	}
}

// --- VM list --min-disk-gb / --max-disk-gb (5.4.50) ---

func TestCLI_VMList_FilterByMinDisk(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock) // small=20, mid=80, big=500 GB

	out, err := runCLI("vm", "list", "--min-disk-gb", "80")
	if err != nil {
		t.Fatalf("vm list --min-disk-gb: %v", err)
	}
	if strings.Contains(out, "small") || !strings.Contains(out, "mid") || !strings.Contains(out, "big") {
		t.Fatalf("expected mid+big (>= 80 GB), got %q", out)
	}
}

func TestCLI_VMList_FilterByMaxDisk(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock)

	out, err := runCLI("vm", "list", "--max-disk-gb", "80")
	if err != nil {
		t.Fatalf("vm list --max-disk-gb: %v", err)
	}
	if !strings.Contains(out, "small") || !strings.Contains(out, "mid") || strings.Contains(out, "big") {
		t.Fatalf("expected small+mid (<= 80 GB), got %q", out)
	}
}

func TestCLI_VMList_FilterByDiskRange(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	seedCPUVMs(mock)

	out, err := runCLI("vm", "list", "--min-disk-gb", "50", "--max-disk-gb", "100")
	if err != nil {
		t.Fatalf("vm list disk range: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + only "mid"
		t.Fatalf("expected header + 1 row (mid), got %d: %v", len(rows), rows)
	}
	if rows[1][1] != "mid" {
		t.Fatalf("expected only mid in range, got %q", rows[1][1])
	}
}

func TestCLI_VMList_RejectsInvalidMinDisk(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--min-disk-gb", "lots")
	if err == nil || !strings.Contains(err.Error(), "invalid --min-disk-gb") {
		t.Fatalf("expected invalid --min-disk-gb error, got %v", err)
	}
}

func TestCLI_VMList_RejectsNegativeMaxDisk(t *testing.T) {
	_, cleanup := withMockVM(t)
	defer cleanup()

	_, err := runCLI("vm", "list", "--max-disk-gb=-1")
	if err == nil || !strings.Contains(err.Error(), "invalid --max-disk-gb") {
		t.Fatalf("expected invalid --max-disk-gb error, got %v", err)
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

// --- Image list --source-vm filter (5.4.27) ---

func TestCLI_ImageList_FilterBySourceVM_ExactMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-a", Name: "from-bastion", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000001", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-b", Name: "from-worker", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000002", CreatedAt: now})

	out, err := runCLI("image", "list", "--source-vm", "vm-1700000000000000001")
	if err != nil {
		t.Fatalf("image list --source-vm: %v", err)
	}
	if !strings.Contains(out, "from-bastion") || strings.Contains(out, "from-worker") {
		t.Fatalf("filter result = %q, want only from-bastion", out)
	}
}

func TestCLI_ImageList_FilterBySourceVM_IsCaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-a", Name: "from-mixed", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "VM-ABC123", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-b", Name: "from-other", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000002", CreatedAt: now})

	out, err := runCLI("image", "list", "--source-vm", "vm-abc123")
	if err != nil {
		t.Fatalf("image list --source-vm: %v", err)
	}
	if !strings.Contains(out, "from-mixed") || strings.Contains(out, "from-other") {
		t.Fatalf("filter result = %q, want only from-mixed", out)
	}
}

func TestCLI_ImageList_FilterBySourceVM_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-a", Name: "from-bastion", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000001", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-b", Name: "from-worker", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000002", CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-c", Name: "uploaded", Path: "/tmp/c.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: now}) // no SourceVM

	out, err := runCLI("image", "list", "--source-vm", "   ")
	if err != nil {
		t.Fatalf("image list --source-vm: %v", err)
	}
	if !strings.Contains(out, "from-bastion") || !strings.Contains(out, "from-worker") || !strings.Contains(out, "uploaded") {
		t.Fatalf("empty filter should be no-op, got %q", out)
	}
}

func TestCLI_ImageList_FilterBySourceVM_NoMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-a", Name: "from-bastion", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000001", CreatedAt: now})

	out, err := runCLI("image", "list", "--source-vm", "vm-does-not-exist")
	if err != nil {
		t.Fatalf("image list --source-vm: %v", err)
	}
	if strings.Contains(out, "from-bastion") {
		t.Fatalf("expected empty list for no-match, got %q", out)
	}
}

func TestCLI_ImageList_FilterBySourceVM_ComposesWithTag(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	now := time.Date(2026, time.March, 28, 8, 30, 0, 0, time.UTC)
	s.PutImage(&types.Image{ID: "img-a", Name: "bastion-release", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000001", Tags: []string{"release"}, CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-b", Name: "bastion-rc", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000001", Tags: []string{"rc"}, CreatedAt: now})
	s.PutImage(&types.Image{ID: "img-c", Name: "worker-release", Path: "/tmp/c.qcow2", SizeBytes: 1024, Format: "qcow2", SourceVM: "vm-1700000000000000002", Tags: []string{"release"}, CreatedAt: now})

	out, err := runCLI("image", "list", "--source-vm", "vm-1700000000000000001", "--tag", "release")
	if err != nil {
		t.Fatalf("image list --source-vm --tag: %v", err)
	}
	if !strings.Contains(out, "bastion-release") {
		t.Fatalf("expected bastion-release (intersection of source-vm+tag), got %q", out)
	}
	if strings.Contains(out, "bastion-rc") || strings.Contains(out, "worker-release") {
		t.Fatalf("did not expect non-intersecting images, got %q", out)
	}
}

// --- Image list --since / --until (roadmap 5.4.29) ---

func TestCLI_ImageList_FilterBySince(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	s.PutImage(&types.Image{ID: "img-a", Name: "early", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: day(1)})
	s.PutImage(&types.Image{ID: "img-b", Name: "late", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: day(30)})

	out, err := runCLI("image", "list", "--since", "2026-05-10T00:00:00Z")
	if err != nil {
		t.Fatalf("image list --since: %v", err)
	}
	if !strings.Contains(out, "late") || strings.Contains(out, "early") {
		t.Fatalf("expected only late, got %q", out)
	}
}

func TestCLI_ImageList_FilterByUntil(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	s.PutImage(&types.Image{ID: "img-a", Name: "early", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: day(1)})
	s.PutImage(&types.Image{ID: "img-b", Name: "late", Path: "/tmp/b.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: day(30)})

	out, err := runCLI("image", "list", "--until", "2026-05-15T00:00:00Z")
	if err != nil {
		t.Fatalf("image list --until: %v", err)
	}
	if !strings.Contains(out, "early") || strings.Contains(out, "late") {
		t.Fatalf("expected only early, got %q", out)
	}
}

func TestCLI_ImageList_FilterBySinceAndUntil(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	s.PutImage(&types.Image{ID: "img-1", Name: "img-1", Path: "/tmp/1.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: day(1)})
	s.PutImage(&types.Image{ID: "img-15", Name: "img-15", Path: "/tmp/15.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: day(15)})
	s.PutImage(&types.Image{ID: "img-30", Name: "img-30", Path: "/tmp/30.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: day(30)})

	out, err := runCLI("image", "list",
		"--since", "2026-05-10T00:00:00Z", "--until", "2026-05-20T00:00:00Z")
	if err != nil {
		t.Fatalf("image list --since --until: %v", err)
	}
	if !strings.Contains(out, "img-15") {
		t.Fatalf("expected img-15 in window, got %q", out)
	}
	// The tabwriter aligns columns so "img-1" appears as a prefix of "img-15"
	// — anchor the boundary checks against the trailing whitespace separator
	// the tabwriter inserts after each cell.
	if strings.Contains(out, "img-30") || strings.Contains(out, "img-1\t") || strings.Contains(out, "img-1 ") {
		t.Fatalf("expected only img-15, got %q", out)
	}
}

func TestCLI_ImageList_RejectsInvalidSince(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "list", "--since", "yesterday")
	if err == nil || !strings.Contains(err.Error(), "invalid --since") {
		t.Fatalf("expected invalid --since error, got %v", err)
	}
}

func TestCLI_ImageList_RejectsInvalidUntil(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("image", "list", "--until", "2026-13-99")
	if err == nil || !strings.Contains(err.Error(), "invalid --until") {
		t.Fatalf("expected invalid --until error, got %v", err)
	}
}

func TestCLI_ImageList_EmptySinceOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	s.PutImage(&types.Image{ID: "img-a", Name: "any", Path: "/tmp/a.qcow2", SizeBytes: 1024, Format: "qcow2", CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)})

	out, err := runCLI("image", "list", "--since", "  ")
	if err != nil {
		t.Fatalf("image list --since '  ': %v", err)
	}
	if !strings.Contains(out, "any") {
		t.Fatalf("whitespace --since should be no-op, got %q", out)
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

	_, err := runCLI("port", "list", "vm-s", "--sort", "definitely-not-a-field")
	if err == nil {
		t.Fatalf("expected error for invalid --sort")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Errorf("err = %v, want 'invalid --sort'", err)
	}
	// Error message must advertise the new guest_ip axis so callers see
	// it in the help string the moment they hit a typo (5.4.86).
	if !strings.Contains(err.Error(), "guest_ip") {
		t.Errorf("err = %v, want it to mention guest_ip", err)
	}
}

// TestCLI_PortList_SortByGuestIP exercises the 5.4.86 guest_ip sort axis
// through the CLI. Numeric IP comparison + empty trails in asc, mirroring the
// type-layer SortPortForwards test suite.
func TestCLI_PortList_SortByGuestIP(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	vmID := "vm-ips"
	seedPFs := []*types.PortForward{
		{ID: vmID + "/22001", VMID: vmID, HostPort: 22001, GuestPort: 22, GuestIP: "192.168.100.10", Protocol: types.ProtocolTCP},
		{ID: vmID + "/22002", VMID: vmID, HostPort: 22002, GuestPort: 22, GuestIP: "192.168.100.2", Protocol: types.ProtocolTCP},
		{ID: vmID + "/22003", VMID: vmID, HostPort: 22003, GuestPort: 22, GuestIP: "", Protocol: types.ProtocolTCP},
	}
	for _, p := range seedPFs {
		if err := s.PutPortForward(p); err != nil {
			t.Fatalf("seed %s: %v", p.ID, err)
		}
	}

	out, err := runCLI("port", "list", vmID, "--sort", "guest_ip")
	if err != nil {
		t.Fatalf("port list: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 4 {
		t.Fatalf("expected header + 3 rows, got %d rows from %q", len(rows), out)
	}
	// Numeric: 192.168.100.2 < 192.168.100.10; empty trails in asc.
	wantHostPorts := []string{"22002", "22001", "22003"}
	for i, hp := range wantHostPorts {
		if rows[i+1][1] != hp {
			t.Errorf("row %d host_port = %q, want %q (full: %v)", i, rows[i+1][1], hp, rows[i+1])
		}
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

func TestCLI_PortList_FilterByMinHostPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s) // host ports: 22001, 8081, 9090

	out, err := runCLI("port", "list", "vm-s", "--min-host-port", "9000")
	if err != nil {
		t.Fatalf("port list --min-host-port: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 { // header + 9090 + 22001
		t.Fatalf("expected header + 2 rows (host_port >= 9000), got %d from %q", len(rows), out)
	}
	if strings.Contains(out, "8081") {
		t.Errorf("8081 should be excluded by --min-host-port 9000, got %q", out)
	}
}

func TestCLI_PortList_FilterByMaxHostPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s) // host ports: 22001, 8081, 9090

	out, err := runCLI("port", "list", "vm-s", "--max-host-port", "9090")
	if err != nil {
		t.Fatalf("port list --max-host-port: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 { // header + 8081 + 9090
		t.Fatalf("expected header + 2 rows (host_port <= 9090), got %d from %q", len(rows), out)
	}
	if strings.Contains(out, "22001") {
		t.Errorf("22001 should be excluded by --max-host-port 9090, got %q", out)
	}
}

func TestCLI_PortList_FilterByHostPortRange(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s) // host ports: 22001, 8081, 9090

	out, err := runCLI("port", "list", "vm-s", "--min-host-port", "8000", "--max-host-port", "9000")
	if err != nil {
		t.Fatalf("port list range: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + 8081 only
		t.Fatalf("expected header + 1 row (8081 in [8000,9000]), got %d from %q", len(rows), out)
	}
	if !strings.Contains(out, "8081") {
		t.Errorf("expected 8081 in range output, got %q", out)
	}
}

func TestCLI_PortList_RejectsInvalidMinHostPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	_, err := runCLI("port", "list", "vm-s", "--min-host-port", "abc")
	if err == nil {
		t.Fatal("expected error for non-numeric --min-host-port")
	}
	if !strings.Contains(err.Error(), "--min-host-port") {
		t.Errorf("error %q should name --min-host-port", err.Error())
	}
}

func TestCLI_PortList_RejectsNegativeMaxHostPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	_, err := runCLI("port", "list", "vm-s", "--max-host-port", "-1")
	if err == nil {
		t.Fatal("expected error for negative --max-host-port")
	}
	if !strings.Contains(err.Error(), "--max-host-port") {
		t.Errorf("error %q should name --max-host-port", err.Error())
	}
}

func TestCLI_PortList_FilterByMinGuestPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s) // guest ports: 22, 80, 9090

	out, err := runCLI("port", "list", "vm-s", "--min-guest-port", "80")
	if err != nil {
		t.Fatalf("port list --min-guest-port: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 { // header + 80 + 9090
		t.Fatalf("expected header + 2 rows (guest_port >= 80), got %d from %q", len(rows), out)
	}
	if strings.Contains(out, "22001") {
		t.Errorf("guest 22 (host 22001) should be excluded by --min-guest-port 80, got %q", out)
	}
}

func TestCLI_PortList_FilterByMaxGuestPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s) // guest ports: 22, 80, 9090

	out, err := runCLI("port", "list", "vm-s", "--max-guest-port", "80")
	if err != nil {
		t.Fatalf("port list --max-guest-port: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 { // header + 22 + 80
		t.Fatalf("expected header + 2 rows (guest_port <= 80), got %d from %q", len(rows), out)
	}
	if strings.Contains(out, "9090") {
		t.Errorf("guest 9090 should be excluded by --max-guest-port 80, got %q", out)
	}
}

func TestCLI_PortList_FilterByGuestPortRange(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s) // guest ports: 22, 80, 9090

	out, err := runCLI("port", "list", "vm-s", "--min-guest-port", "50", "--max-guest-port", "100")
	if err != nil {
		t.Fatalf("port list guest range: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + guest 80 only
		t.Fatalf("expected header + 1 row (guest 80 in [50,100]), got %d from %q", len(rows), out)
	}
	if !strings.Contains(out, "8081") {
		t.Errorf("expected the host 8081 / guest 80 rule in range output, got %q", out)
	}
}

func TestCLI_PortList_RejectsInvalidMinGuestPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	_, err := runCLI("port", "list", "vm-s", "--min-guest-port", "abc")
	if err == nil {
		t.Fatal("expected error for non-numeric --min-guest-port")
	}
	if !strings.Contains(err.Error(), "--min-guest-port") {
		t.Errorf("error %q should name --min-guest-port", err.Error())
	}
}

func TestCLI_PortList_RejectsNegativeMaxGuestPort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListFixtures(t, s)

	_, err := runCLI("port", "list", "vm-s", "--max-guest-port", "-1")
	if err == nil {
		t.Fatal("expected error for negative --max-guest-port")
	}
	if !strings.Contains(err.Error(), "--max-guest-port") {
		t.Errorf("error %q should name --max-guest-port", err.Error())
	}
}

// seedPortListMultiGuestIPFixtures populates four rules on the same VM that
// land on three distinct guest IPs — the multi-NIC layout the --guest-ip
// filter is designed to slice. Two rules share 192.168.100.50 so a positive
// match has to return more than one row.
func seedPortListMultiGuestIPFixtures(t *testing.T, s *store.Store) {
	t.Helper()
	pfs := []*types.PortForward{
		{ID: "vm-gip/22", VMID: "vm-gip", HostPort: 22, GuestPort: 22, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP, Description: "ssh primary"},
		{ID: "vm-gip/8080", VMID: "vm-gip", HostPort: 8080, GuestPort: 80, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP, Description: "http primary"},
		{ID: "vm-gip/8443", VMID: "vm-gip", HostPort: 8443, GuestPort: 443, GuestIP: "10.0.0.7", Protocol: types.ProtocolTCP, Description: "https data-net"},
		{ID: "vm-gip/9090", VMID: "vm-gip", HostPort: 9090, GuestPort: 9090, GuestIP: "10.0.0.8", Protocol: types.ProtocolUDP, Description: "metrics storage-net"},
	}
	for _, p := range pfs {
		if err := s.PutPortForward(p); err != nil {
			t.Fatalf("seed %s: %v", p.ID, err)
		}
	}
}

func TestCLI_PortList_FilterByGuestIP_ExactMatch(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListMultiGuestIPFixtures(t, s)

	out, err := runCLI("port", "list", "vm-gip", "--guest-ip", "192.168.100.50")
	if err != nil {
		t.Fatalf("port list --guest-ip: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 3 { // header + 2 matching rows
		t.Fatalf("expected header + 2 rows, got %d from %q", len(rows), out)
	}
	if strings.Contains(out, "10.0.0.7") || strings.Contains(out, "10.0.0.8") {
		t.Errorf("--guest-ip 192.168.100.50 should exclude data-net/storage-net rules, got %q", out)
	}
}

func TestCLI_PortList_FilterByGuestIP_OtherCohort(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListMultiGuestIPFixtures(t, s)

	out, err := runCLI("port", "list", "vm-gip", "--guest-ip", "10.0.0.7")
	if err != nil {
		t.Fatalf("port list --guest-ip: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + 1 matching row
		t.Fatalf("expected header + 1 row, got %d from %q", len(rows), out)
	}
	if !strings.Contains(out, "8443") {
		t.Errorf("expected the host 8443 / guest 443 rule in output, got %q", out)
	}
}

func TestCLI_PortList_FilterByGuestIP_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	// IPv6 literal so case-insensitive matching is meaningful (IPv4 has no
	// case axis; IPv6 hex digits are case-irrelevant and operators paste
	// either form).
	s.PutPortForward(&types.PortForward{ID: "vm-gip-case/22", VMID: "vm-gip-case", HostPort: 22, GuestPort: 22, GuestIP: "fe80::ABCD", Protocol: types.ProtocolTCP})
	s.PutPortForward(&types.PortForward{ID: "vm-gip-case/80", VMID: "vm-gip-case", HostPort: 80, GuestPort: 80, GuestIP: "192.168.100.50", Protocol: types.ProtocolTCP})

	out, err := runCLI("port", "list", "vm-gip-case", "--guest-ip", "FE80::abcd")
	if err != nil {
		t.Fatalf("port list --guest-ip (case-insensitive): %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 2 { // header + 1 matching row
		t.Fatalf("expected header + 1 row, got %d from %q", len(rows), out)
	}
	if !strings.Contains(out, "fe80::ABCD") {
		t.Errorf("expected the fe80::ABCD row in output, got %q", out)
	}
}

func TestCLI_PortList_FilterByGuestIP_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListMultiGuestIPFixtures(t, s)

	out, err := runCLI("port", "list", "vm-gip", "--guest-ip", "")
	if err != nil {
		t.Fatalf("port list --guest-ip='': %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 5 { // header + 4 rules
		t.Fatalf("empty --guest-ip should be a no-op, got %d rows from %q", len(rows), out)
	}
}

func TestCLI_PortList_FilterByGuestIP_NoMatch(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListMultiGuestIPFixtures(t, s)

	out, err := runCLI("port", "list", "vm-gip", "--guest-ip", "203.0.113.1")
	if err != nil {
		t.Fatalf("port list --guest-ip (no match): %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 1 { // header only
		t.Fatalf("expected header-only output, got %d rows from %q", len(rows), out)
	}
}

func TestCLI_PortList_FilterByGuestIP_ComposesWithProtocol(t *testing.T) {
	s, _, cleanup := withTestPortForwarder(t)
	defer cleanup()
	seedPortListMultiGuestIPFixtures(t, s)

	// 192.168.100.50 has 2 rules, both TCP; protocol=udp narrows to 0.
	out, err := runCLI("port", "list", "vm-gip", "--guest-ip", "192.168.100.50", "--protocol", "udp")
	if err != nil {
		t.Fatalf("port list compose: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 1 {
		t.Fatalf("guest_ip + protocol=udp should be header-only, got %d rows from %q", len(rows), out)
	}
	// Re-narrow with TCP brings both rules back.
	out, err = runCLI("port", "list", "vm-gip", "--guest-ip", "192.168.100.50", "--protocol", "tcp")
	if err != nil {
		t.Fatalf("port list compose tcp: %v", err)
	}
	rows = tableRows(t, out)
	if len(rows) != 3 {
		t.Fatalf("guest_ip + protocol=tcp = %d rows, want 3 (header + 2) from %q", len(rows), out)
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

	// `image` and `default_user` are valid template sort axes (5.4.89,
	// 5.4.92); use a sentinel that's still unsupported so the error path
	// is exercised.
	_, err := runCLI("template", "list", "--sort", "memory")
	if err == nil {
		t.Fatal("expected error for unsupported --sort, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --sort") {
		t.Errorf("error = %v, want it to mention 'invalid --sort'", err)
	}
	// CLI rejection message must advertise the new axes so the help text
	// mirrors the API surface.
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error = %v, want it to mention `image`", err)
	}
	if !strings.Contains(err.Error(), "default_user") {
		t.Errorf("error = %v, want it to mention `default_user`", err)
	}
	if !strings.Contains(err.Error(), "os_type") {
		t.Errorf("error = %v, want it to mention `os_type`", err)
	}
	if !strings.Contains(err.Error(), "os_variant") {
		t.Errorf("error = %v, want it to mention `os_variant`", err)
	}
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("error = %v, want it to mention `description` (5.4.119)", err)
	}
}

// 5.4.102 — case-insensitive `os_type` sort axis on the template list.
// Diverges from the nil-trailing convention: empty stored os_type resolves
// to `linux` via VMTemplate.ResolvedOSType, so empty templates collate
// with explicit-linux templates rather than sinking to the tail.

func TestCLI_TemplateList_SortByOSType_AscEmptyResolvesToLinux(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "win-app", Image: "win.qcow2", OSType: types.OSTypeWindows}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "empty-tpl", Image: "rocky9.qcow2"}); err != nil { // empty → linux
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "linux-tpl", Image: "rocky9.qcow2", OSType: "linux"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "os_type")
	if err != nil {
		t.Fatalf("template list --sort os_type: %v", err)
	}
	rows := tableRows(t, out)
	// linux < windows; empty (→linux) and explicit linux interleave by id tiebreak.
	want := []string{"empty-tpl", "linux-tpl", "win-app"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByOSType_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "WinUpper", Image: "win.qcow2", OSType: "WINDOWS"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "winLower", Image: "win.qcow2", OSType: "windows"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "lin", Image: "rocky9.qcow2", OSType: "Linux"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "os_type")
	if err != nil {
		t.Fatalf("template list --sort os_type: %v", err)
	}
	rows := tableRows(t, out)
	// linux < windows; the two windows entries (regardless of case) tiebreak on id.
	want := []string{"lin", "winLower", "WinUpper"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

// 5.4.115 — case-insensitive `os_variant` sort axis on the template list.
// Mirrors the VM list `os_variant` sort axis (5.4.103) — empty stored values
// sink to the tail (no documented default, unlike `os_type`).

func TestCLI_TemplateList_SortByOSVariant_AscNilTrailing(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "srv-2025", Image: "win-2025.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-server-2025"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "win10", Image: "win-10.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-10"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "rocky", Image: "rocky9.qcow2"}); err != nil { // empty → trails
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-4", Name: "win11", Image: "win-11.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "os_variant")
	if err != nil {
		t.Fatalf("template list --sort os_variant: %v", err)
	}
	rows := tableRows(t, out)
	// Asc alphabetical: windows-10 < windows-11 < windows-server-2025; empty trails.
	want := []string{"win10", "win11", "srv-2025", "rocky"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByOSVariant_DescNilLeading(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "srv-2025", Image: "win-2025.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-server-2025"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "win10", Image: "win-10.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-10"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "rocky", Image: "rocky9.qcow2"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "os_variant", "--order", "desc")
	if err != nil {
		t.Fatalf("template list --sort os_variant --order desc: %v", err)
	}
	rows := tableRows(t, out)
	// Desc: empty heads, then windows-server-2025 > windows-10.
	want := []string{"rocky", "srv-2025", "win10"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

// 5.4.119 — case-insensitive `description` sort axis on the template list.
// Mirrors the image list `description` axis (5.4.118) one resource over;
// empty stored values sink to the tail of asc / head of desc.

func TestCLI_TemplateList_SortByDescription_Asc(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "no-desc", Image: "rocky9.qcow2", Description: ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "zeta", Image: "rocky9.qcow2", Description: "Zeta hardened"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "alpha", Image: "rocky9.qcow2", Description: "alpha bootstrap"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "description")
	if err != nil {
		t.Fatalf("template list --sort description: %v", err)
	}
	rows := tableRows(t, out)
	// Case-insensitive: alpha < zeta; empty trails in asc.
	want := []string{"alpha", "zeta", "no-desc"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByDescription_DescEmptyHeads(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "zeta", Image: "rocky9.qcow2", Description: "Zeta hardened"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "no-desc", Image: "rocky9.qcow2", Description: ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "alpha", Image: "rocky9.qcow2", Description: "alpha bootstrap"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "description", "--order", "desc")
	if err != nil {
		t.Fatalf("template list --sort description --order desc: %v", err)
	}
	rows := tableRows(t, out)
	// Desc inverts asc: empty heads, then zeta, then alpha.
	want := []string{"no-desc", "zeta", "alpha"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByImage_AscEmptyTrailing(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "no-img", Image: ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "ubuntu", Image: "ubuntu-22.04.qcow2"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "rocky", Image: "rocky9.qcow2"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "image")
	if err != nil {
		t.Fatalf("template list --sort image: %v", err)
	}
	rows := tableRows(t, out)
	// rocky9 < ubuntu; empty trails in asc.
	want := []string{"rocky", "ubuntu", "no-img"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByImage_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	// Operator pastes verbatim from a directory where case varies; the
	// case-insensitive sort must collate both into the same cohort.
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "RockyUpper", Image: "Rocky9.qcow2"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rockyLower", Image: "rocky9.qcow2"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "alpine", Image: "alpine.qcow2"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "image")
	if err != nil {
		t.Fatalf("template list --sort image: %v", err)
	}
	rows := tableRows(t, out)
	// alpine < rocky9 (case-folded); rocky tie tiebreaks on id (tmpl-1 before tmpl-3).
	want := []string{"alpine", "rockyLower", "RockyUpper"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

// 5.4.92 — case-insensitive `default_user` sort axis on the template list.
// Diverges from the VM list `default_user` axis (5.4.91): empty stored values
// sink to the tail of asc rather than collapsing to "root".

func TestCLI_TemplateList_SortByDefaultUser_AscEmptyTrailing(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "no-user", Image: "rocky9.qcow2", DefaultUser: ""}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "ubuntu", Image: "rocky9.qcow2", DefaultUser: "ubuntu"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "alice", Image: "rocky9.qcow2", DefaultUser: "ops-alice"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "default_user")
	if err != nil {
		t.Fatalf("template list --sort default_user: %v", err)
	}
	rows := tableRows(t, out)
	// ops-alice < ubuntu lex; empty trails in asc.
	want := []string{"alice", "ubuntu", "no-user"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByDefaultUser_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	// Same user with varying case should collate as a single cohort.
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "RootUpper", Image: "rocky9.qcow2", DefaultUser: "Root"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rootLower", Image: "rocky9.qcow2", DefaultUser: "root"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "alice", Image: "rocky9.qcow2", DefaultUser: "alice"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "default_user")
	if err != nil {
		t.Fatalf("template list --sort default_user: %v", err)
	}
	rows := tableRows(t, out)
	// alice < root (case-folded); root tie tiebreaks on id (tmpl-1 before tmpl-3).
	want := []string{"alice", "rootLower", "RootUpper"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByCPUs(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "ubuntu", CPUs: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "medium", Image: "ubuntu", CPUs: 4}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "large", Image: "ubuntu", CPUs: 8}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "cpus")
	if err != nil {
		t.Fatalf("template list --sort cpus: %v", err)
	}
	rows := tableRows(t, out)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want header + 3", len(rows))
	}
	want := []string{"small", "medium", "large"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByRAMMBDesc(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "tiny", Image: "ubuntu", RAMMB: 512}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "big", Image: "ubuntu", RAMMB: 8192}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "med", Image: "ubuntu", RAMMB: 2048}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "ram_mb", "--order", "desc")
	if err != nil {
		t.Fatalf("template list --sort ram_mb: %v", err)
	}
	rows := tableRows(t, out)
	want := []string{"big", "med", "tiny"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
	}
}

func TestCLI_TemplateList_SortByDiskGB(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "small", Image: "ubuntu", DiskGB: 10}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "huge", Image: "ubuntu", DiskGB: 500}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "med", Image: "ubuntu", DiskGB: 100}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--sort", "disk_gb")
	if err != nil {
		t.Fatalf("template list --sort disk_gb: %v", err)
	}
	rows := tableRows(t, out)
	want := []string{"small", "med", "huge"}
	for i, name := range want {
		if rows[i+1][1] != name {
			t.Errorf("row %d name = %q, want %q", i, rows[i+1][1], name)
		}
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
// template list --image tests
// =====================================================

func TestCLI_TemplateList_FilterByImage_ExactMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--image", "rocky9.qcow2")
	if err != nil {
		t.Fatalf("template list --image: %v", err)
	}
	if !strings.Contains(out, "rocky9-base") {
		t.Fatalf("expected rocky9-base in output, got %q", out)
	}
	if strings.Contains(out, "ubuntu-22") {
		t.Fatalf("did not expect ubuntu-22 in output, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByImage_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "Rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--image", "ROCKY9.QCOW2")
	if err != nil {
		t.Fatalf("template list --image: %v", err)
	}
	if !strings.Contains(out, "rocky9-base") {
		t.Fatalf("expected case-insensitive match for rocky9-base, got %q", out)
	}
	if strings.Contains(out, "ubuntu-22") {
		t.Fatalf("did not expect ubuntu-22 in output, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByImage_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "ubuntu-22", Image: "ubuntu.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--image", "   ")
	if err != nil {
		t.Fatalf("template list --image: %v", err)
	}
	if !strings.Contains(out, "rocky9-base") || !strings.Contains(out, "ubuntu-22") {
		t.Fatalf("expected every template (whitespace --image is a no-op), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByImage_NoMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--image", "fedora.qcow2")
	if err != nil {
		t.Fatalf("template list --image: %v", err)
	}
	if strings.Contains(out, "rocky9-base") {
		t.Fatalf("expected empty list for no-match, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByImage_ComposesWithTag(t *testing.T) {
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

	out, err := runCLI("template", "list", "--image", "rocky9.qcow2", "--tag", "prod")
	if err != nil {
		t.Fatalf("template list --image --tag: %v", err)
	}
	if !strings.Contains(out, "rocky9-prod") {
		t.Fatalf("expected rocky9-prod (intersection of image+tag), got %q", out)
	}
	if strings.Contains(out, "rocky9-qa") || strings.Contains(out, "ubuntu-prod") {
		t.Fatalf("did not expect non-intersecting templates, got %q", out)
	}
}

// --- template list --default-user (roadmap 5.4.38) ---

func TestCLI_TemplateList_FilterByDefaultUser_ExactMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "ec2-ubuntu", Image: "ubuntu.qcow2", DefaultUser: "ec2-user", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--default-user", "deploy")
	if err != nil {
		t.Fatalf("template list --default-user: %v", err)
	}
	if !strings.Contains(out, "deploy-rocky") {
		t.Fatalf("expected deploy-rocky in output, got %q", out)
	}
	if strings.Contains(out, "ec2-ubuntu") {
		t.Fatalf("did not expect ec2-ubuntu in output, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByDefaultUser_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "Deploy", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "ec2-ubuntu", Image: "ubuntu.qcow2", DefaultUser: "ec2-user", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--default-user", "DEPLOY")
	if err != nil {
		t.Fatalf("template list --default-user: %v", err)
	}
	if !strings.Contains(out, "deploy-rocky") {
		t.Fatalf("expected case-insensitive match for deploy-rocky, got %q", out)
	}
	if strings.Contains(out, "ec2-ubuntu") {
		t.Fatalf("did not expect ec2-ubuntu in output, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByDefaultUser_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "ec2-ubuntu", Image: "ubuntu.qcow2", DefaultUser: "ec2-user", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--default-user", "   ")
	if err != nil {
		t.Fatalf("template list --default-user: %v", err)
	}
	if !strings.Contains(out, "deploy-rocky") || !strings.Contains(out, "ec2-ubuntu") {
		t.Fatalf("expected every template (whitespace --default-user is a no-op), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByDefaultUser_NoMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "deploy-rocky", Image: "rocky9.qcow2", DefaultUser: "deploy", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--default-user", "admin")
	if err != nil {
		t.Fatalf("template list --default-user: %v", err)
	}
	if strings.Contains(out, "deploy-rocky") {
		t.Fatalf("expected empty list for no-match, got %q", out)
	}
}

// --- template list --os-type (5.6.8) ---

func TestCLI_TemplateList_FilterByOSType_ExactMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky-tpl", Image: "rocky9.qcow2", OSType: types.OSTypeLinux, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "win22-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-type", "windows")
	if err != nil {
		t.Fatalf("template list --os-type: %v", err)
	}
	if !strings.Contains(out, "win22-tpl") || strings.Contains(out, "rocky-tpl") {
		t.Fatalf("expected only win22-tpl, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByOSType_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "win22-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-type", "WINDOWS")
	if err != nil {
		t.Fatalf("template list --os-type: %v", err)
	}
	if !strings.Contains(out, "win22-tpl") {
		t.Fatalf("expected case-insensitive match for win22-tpl, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByOSType_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky-tpl", Image: "r.qcow2", OSType: types.OSTypeLinux, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "win22-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-type", "   ")
	if err != nil {
		t.Fatalf("template list --os-type: %v", err)
	}
	if !strings.Contains(out, "rocky-tpl") || !strings.Contains(out, "win22-tpl") {
		t.Fatalf("expected every template (whitespace --os-type is a no-op), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByOSType_LinuxMatchesEmpty(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "implicit-linux", Image: "r.qcow2", OSType: "", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "explicit-linux", Image: "r.qcow2", OSType: types.OSTypeLinux, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "win22-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-type", "linux")
	if err != nil {
		t.Fatalf("template list --os-type linux: %v", err)
	}
	if !strings.Contains(out, "implicit-linux") || !strings.Contains(out, "explicit-linux") || strings.Contains(out, "win22-tpl") {
		t.Fatalf("expected implicit-linux + explicit-linux only, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByOSType_RejectsInvalidValue(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky-tpl", Image: "r.qcow2", OSType: types.OSTypeLinux, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := runCLI("template", "list", "--os-type", "plan9")
	if err == nil {
		t.Fatal("expected error for invalid --os-type, got nil")
	}
	if !strings.Contains(err.Error(), "--os-type") {
		t.Fatalf("error should reference --os-type, got %v", err)
	}
}

// --- template list --os-variant (roadmap 5.4.67) ---
//
// Mirrors the VM list --os-variant CLI tests on the template cohort:
// case-insensitive exact-match, whitespace-trim, empty-disables, empty-stored
// excluded, and a clear --os-variant error on unknown values.

func TestCLI_TemplateList_FilterByOSVariant_ExactMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "win11-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "srv22-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-server-2022", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "linux-tpl", Image: "l.qcow2", OSType: types.OSTypeLinux, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-variant", "windows-11")
	if err != nil {
		t.Fatalf("template list --os-variant: %v", err)
	}
	if !strings.Contains(out, "win11-tpl") || strings.Contains(out, "srv22-tpl") || strings.Contains(out, "linux-tpl") {
		t.Fatalf("expected only win11-tpl, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByOSVariant_IsCaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "srv22-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-server-2022", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-variant", "WINDOWS-SERVER-2022")
	if err != nil {
		t.Fatalf("template list --os-variant case: %v", err)
	}
	if !strings.Contains(out, "srv22-tpl") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByOSVariant_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "win11-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "linux-tpl", Image: "l.qcow2", OSType: types.OSTypeLinux, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-variant", "")
	if err != nil {
		t.Fatalf("template list --os-variant empty: %v", err)
	}
	if !strings.Contains(out, "win11-tpl") || !strings.Contains(out, "linux-tpl") {
		t.Fatalf("expected empty filter to return all templates, got %q", out)
	}
}

// TestCLI_TemplateList_FilterByOSVariant_ExcludesEmptyStored mirrors the VM
// no-empty-match contract on the template cohort: unlike `--os-type linux`
// (which matches empty-stored via the documented linux default),
// `--os-variant` has no documented default so empty drops out whenever the
// filter is set.
func TestCLI_TemplateList_FilterByOSVariant_ExcludesEmptyStored(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "win11-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "win-unset", Image: "w.qcow2", OSType: types.OSTypeWindows, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-variant", "windows-11")
	if err != nil {
		t.Fatalf("template list --os-variant: %v", err)
	}
	if !strings.Contains(out, "win11-tpl") || strings.Contains(out, "win-unset") {
		t.Fatalf("expected only win11-tpl (empty-stored excluded), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByOSVariant_RejectsInvalidValue(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "win11-tpl", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := runCLI("template", "list", "--os-variant", "windows-12")
	if err == nil {
		t.Fatal("expected error for invalid --os-variant, got nil")
	}
	if !strings.Contains(err.Error(), "--os-variant") {
		t.Fatalf("error should reference --os-variant, got %v", err)
	}
}

func TestCLI_TemplateList_FilterByOSVariant_ComposesWithOSType(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "a-win11", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-11", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "b-srv22", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-server-2022", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "c-win10", Image: "w.qcow2", OSType: types.OSTypeWindows, OSVariant: "windows-10", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--os-type", "windows", "--os-variant", "windows-11")
	if err != nil {
		t.Fatalf("template list compose: %v", err)
	}
	if !strings.Contains(out, "a-win11") || strings.Contains(out, "b-srv22") || strings.Contains(out, "c-win10") {
		t.Fatalf("expected only a-win11, got %q", out)
	}
}

// --- template list --network (roadmap 5.4.45) ---

func tmplNet(names ...string) []types.NetworkAttachment {
	attachments := make([]types.NetworkAttachment, 0, len(names))
	for _, n := range names {
		attachments = append(attachments, types.NetworkAttachment{Name: n})
	}
	return attachments
}

func TestCLI_TemplateList_FilterByNetwork_ExactMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "data-tpl", Image: "rocky9.qcow2", Networks: tmplNet("data-net"), CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "storage-tpl", Image: "rocky9.qcow2", Networks: tmplNet("storage-net"), CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--network", "data-net")
	if err != nil {
		t.Fatalf("template list --network: %v", err)
	}
	if !strings.Contains(out, "data-tpl") {
		t.Fatalf("expected data-tpl in output, got %q", out)
	}
	if strings.Contains(out, "storage-tpl") {
		t.Fatalf("did not expect storage-tpl in output, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByNetwork_CaseInsensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "data-tpl", Image: "rocky9.qcow2", Networks: tmplNet("Data-Net"), CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "storage-tpl", Image: "rocky9.qcow2", Networks: tmplNet("storage-net"), CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--network", "DATA-NET")
	if err != nil {
		t.Fatalf("template list --network: %v", err)
	}
	if !strings.Contains(out, "data-tpl") {
		t.Fatalf("expected case-insensitive match for data-tpl, got %q", out)
	}
	if strings.Contains(out, "storage-tpl") {
		t.Fatalf("did not expect storage-tpl in output, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByNetwork_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "data-tpl", Image: "rocky9.qcow2", Networks: tmplNet("data-net"), CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "no-net", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--network", "   ")
	if err != nil {
		t.Fatalf("template list --network: %v", err)
	}
	if !strings.Contains(out, "data-tpl") || !strings.Contains(out, "no-net") {
		t.Fatalf("expected every template (whitespace --network is a no-op), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByNetwork_NoMatch(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "data-tpl", Image: "rocky9.qcow2", Networks: tmplNet("data-net"), CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--network", "mgmt-net")
	if err != nil {
		t.Fatalf("template list --network: %v", err)
	}
	if strings.Contains(out, "data-tpl") {
		t.Fatalf("expected empty list for no-match, got %q", out)
	}
}

// --- template list --since / --until (roadmap 5.4.31) ---

func TestCLI_TemplateList_FilterBySince(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-early", Name: "early", Image: "rocky9.qcow2", CreatedAt: day(1), UpdatedAt: day(1)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-late", Name: "late", Image: "rocky9.qcow2", CreatedAt: day(30), UpdatedAt: day(30)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--since", "2026-05-10T00:00:00Z")
	if err != nil {
		t.Fatalf("template list --since: %v", err)
	}
	if !strings.Contains(out, "late") || strings.Contains(out, "early") {
		t.Fatalf("expected only late, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByUntil(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-early", Name: "early", Image: "rocky9.qcow2", CreatedAt: day(1), UpdatedAt: day(1)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-late", Name: "late", Image: "rocky9.qcow2", CreatedAt: day(30), UpdatedAt: day(30)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--until", "2026-05-15T00:00:00Z")
	if err != nil {
		t.Fatalf("template list --until: %v", err)
	}
	if !strings.Contains(out, "early") || strings.Contains(out, "late") {
		t.Fatalf("expected only early, got %q", out)
	}
}

func TestCLI_TemplateList_FilterBySinceAndUntil(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	day := func(d int) time.Time { return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC) }
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "tmpl-1", Image: "rocky9.qcow2", CreatedAt: day(1), UpdatedAt: day(1)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-15", Name: "tmpl-15", Image: "rocky9.qcow2", CreatedAt: day(15), UpdatedAt: day(15)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-30", Name: "tmpl-30", Image: "rocky9.qcow2", CreatedAt: day(30), UpdatedAt: day(30)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--since", "2026-05-10T00:00:00Z", "--until", "2026-05-20T00:00:00Z")
	if err != nil {
		t.Fatalf("template list --since --until: %v", err)
	}
	if !strings.Contains(out, "tmpl-15") || strings.Contains(out, "tmpl-1\t") || strings.Contains(out, "tmpl-30") {
		t.Fatalf("expected only tmpl-15, got %q", out)
	}
}

func TestCLI_TemplateList_RejectsInvalidSince(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("template", "list", "--since", "yesterday")
	if err == nil || !strings.Contains(err.Error(), "invalid --since") {
		t.Fatalf("expected invalid --since error, got %v", err)
	}
}

func TestCLI_TemplateList_RejectsInvalidUntil(t *testing.T) {
	_, _, cleanup := withTestStorage(t)
	defer cleanup()

	_, err := runCLI("template", "list", "--until", "2026-13-99")
	if err == nil || !strings.Contains(err.Error(), "invalid --until") {
		t.Fatalf("expected invalid --until error, got %v", err)
	}
}

func TestCLI_TemplateList_EmptySinceOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-any", Name: "any", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--since", "  ")
	if err != nil {
		t.Fatalf("template list --since '  ': %v", err)
	}
	if !strings.Contains(out, "any") {
		t.Fatalf("whitespace --since should be no-op, got %q", out)
	}
}

// --- template list --min-cpus / --max-cpus (roadmap 5.4.51) ---

func seedCPUTemplates(t *testing.T, s interface {
	PutTemplate(*types.VMTemplate) error
}) {
	t.Helper()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tpl := range []*types.VMTemplate{
		{ID: "tmpl-small", Name: "small-tpl", Image: "rocky9.qcow2", CPUs: 2, CreatedAt: t0, UpdatedAt: t0},
		{ID: "tmpl-mid", Name: "mid-tpl", Image: "rocky9.qcow2", CPUs: 4, CreatedAt: t0, UpdatedAt: t0},
		{ID: "tmpl-big", Name: "big-tpl", Image: "rocky9.qcow2", CPUs: 16, CreatedAt: t0, UpdatedAt: t0},
	} {
		if err := s.PutTemplate(tpl); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestCLI_TemplateList_FilterByMinCpus(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedCPUTemplates(t, s)

	out, err := runCLI("template", "list", "--min-cpus", "4")
	if err != nil {
		t.Fatalf("template list --min-cpus: %v", err)
	}
	if strings.Contains(out, "small-tpl") || !strings.Contains(out, "mid-tpl") || !strings.Contains(out, "big-tpl") {
		t.Fatalf("expected mid+big (>= 4 vCPUs), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByMaxCpus(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedCPUTemplates(t, s)

	out, err := runCLI("template", "list", "--max-cpus", "4")
	if err != nil {
		t.Fatalf("template list --max-cpus: %v", err)
	}
	if !strings.Contains(out, "small-tpl") || !strings.Contains(out, "mid-tpl") || strings.Contains(out, "big-tpl") {
		t.Fatalf("expected small+mid (<= 4 vCPUs), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByCpusRange(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedCPUTemplates(t, s)

	out, err := runCLI("template", "list", "--min-cpus", "3", "--max-cpus", "8")
	if err != nil {
		t.Fatalf("template list cpu range: %v", err)
	}
	if strings.Contains(out, "small-tpl") || !strings.Contains(out, "mid-tpl") || strings.Contains(out, "big-tpl") {
		t.Fatalf("expected only mid-tpl in [3,8] vCPUs, got %q", out)
	}
}

func TestCLI_TemplateList_RejectsInvalidMinCpus(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedCPUTemplates(t, s)

	_, err := runCLI("template", "list", "--min-cpus", "lots")
	if err == nil {
		t.Fatalf("expected error for non-numeric --min-cpus")
	}
	if !strings.Contains(err.Error(), "--min-cpus") {
		t.Fatalf("expected error to mention --min-cpus, got %v", err)
	}
}

func TestCLI_TemplateList_RejectsNegativeMaxCpus(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedCPUTemplates(t, s)

	_, err := runCLI("template", "list", "--max-cpus", "-2")
	if err == nil {
		t.Fatalf("expected error for negative --max-cpus")
	}
	if !strings.Contains(err.Error(), "--max-cpus") {
		t.Fatalf("expected error to mention --max-cpus, got %v", err)
	}
}

// --- template list --min-ram-mb / --max-ram-mb (roadmap 5.4.52) ---

func seedRAMTemplates(t *testing.T, s interface {
	PutTemplate(*types.VMTemplate) error
}) {
	t.Helper()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tpl := range []*types.VMTemplate{
		{ID: "tmpl-rsmall", Name: "small-ram-tpl", Image: "rocky9.qcow2", RAMMB: 1024, CreatedAt: t0, UpdatedAt: t0},
		{ID: "tmpl-rmid", Name: "mid-ram-tpl", Image: "rocky9.qcow2", RAMMB: 4096, CreatedAt: t0, UpdatedAt: t0},
		{ID: "tmpl-rbig", Name: "big-ram-tpl", Image: "rocky9.qcow2", RAMMB: 16384, CreatedAt: t0, UpdatedAt: t0},
	} {
		if err := s.PutTemplate(tpl); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestCLI_TemplateList_FilterByMinRAM(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedRAMTemplates(t, s)

	out, err := runCLI("template", "list", "--min-ram-mb", "4096")
	if err != nil {
		t.Fatalf("template list --min-ram-mb: %v", err)
	}
	if strings.Contains(out, "small-ram-tpl") || !strings.Contains(out, "mid-ram-tpl") || !strings.Contains(out, "big-ram-tpl") {
		t.Fatalf("expected mid+big (>= 4096 MB RAM), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByMaxRAM(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedRAMTemplates(t, s)

	out, err := runCLI("template", "list", "--max-ram-mb", "4096")
	if err != nil {
		t.Fatalf("template list --max-ram-mb: %v", err)
	}
	if !strings.Contains(out, "small-ram-tpl") || !strings.Contains(out, "mid-ram-tpl") || strings.Contains(out, "big-ram-tpl") {
		t.Fatalf("expected small+mid (<= 4096 MB RAM), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByRAMRange(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedRAMTemplates(t, s)

	out, err := runCLI("template", "list", "--min-ram-mb", "2048", "--max-ram-mb", "8192")
	if err != nil {
		t.Fatalf("template list ram range: %v", err)
	}
	if strings.Contains(out, "small-ram-tpl") || !strings.Contains(out, "mid-ram-tpl") || strings.Contains(out, "big-ram-tpl") {
		t.Fatalf("expected only mid-ram-tpl in [2048,8192] MB RAM, got %q", out)
	}
}

func TestCLI_TemplateList_RejectsInvalidMinRAM(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedRAMTemplates(t, s)

	_, err := runCLI("template", "list", "--min-ram-mb", "lots")
	if err == nil {
		t.Fatalf("expected error for non-numeric --min-ram-mb")
	}
	if !strings.Contains(err.Error(), "--min-ram-mb") {
		t.Fatalf("expected error to mention --min-ram-mb, got %v", err)
	}
}

func TestCLI_TemplateList_RejectsNegativeMaxRAM(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedRAMTemplates(t, s)

	_, err := runCLI("template", "list", "--max-ram-mb", "-2")
	if err == nil {
		t.Fatalf("expected error for negative --max-ram-mb")
	}
	if !strings.Contains(err.Error(), "--max-ram-mb") {
		t.Fatalf("expected error to mention --max-ram-mb, got %v", err)
	}
}

// --- template list --min-disk-gb / --max-disk-gb (roadmap 5.4.53) ---

func seedDiskTemplates(t *testing.T, s interface {
	PutTemplate(*types.VMTemplate) error
}) {
	t.Helper()
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tpl := range []*types.VMTemplate{
		{ID: "tmpl-dsmall", Name: "small-dtpl", Image: "rocky9.qcow2", DiskGB: 10, CreatedAt: t0, UpdatedAt: t0},
		{ID: "tmpl-dmid", Name: "mid-dtpl", Image: "rocky9.qcow2", DiskGB: 50, CreatedAt: t0, UpdatedAt: t0},
		{ID: "tmpl-dbig", Name: "big-dtpl", Image: "rocky9.qcow2", DiskGB: 200, CreatedAt: t0, UpdatedAt: t0},
	} {
		if err := s.PutTemplate(tpl); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestCLI_TemplateList_FilterByMinDisk(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedDiskTemplates(t, s)

	out, err := runCLI("template", "list", "--min-disk-gb", "50")
	if err != nil {
		t.Fatalf("template list --min-disk-gb: %v", err)
	}
	if strings.Contains(out, "small-dtpl") || !strings.Contains(out, "mid-dtpl") || !strings.Contains(out, "big-dtpl") {
		t.Fatalf("expected mid+big (>= 50 GB), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByMaxDisk(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedDiskTemplates(t, s)

	out, err := runCLI("template", "list", "--max-disk-gb", "50")
	if err != nil {
		t.Fatalf("template list --max-disk-gb: %v", err)
	}
	if !strings.Contains(out, "small-dtpl") || !strings.Contains(out, "mid-dtpl") || strings.Contains(out, "big-dtpl") {
		t.Fatalf("expected small+mid (<= 50 GB), got %q", out)
	}
}

func TestCLI_TemplateList_FilterByDiskRange(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedDiskTemplates(t, s)

	out, err := runCLI("template", "list", "--min-disk-gb", "40", "--max-disk-gb", "100")
	if err != nil {
		t.Fatalf("template list disk range: %v", err)
	}
	if strings.Contains(out, "small-dtpl") || !strings.Contains(out, "mid-dtpl") || strings.Contains(out, "big-dtpl") {
		t.Fatalf("expected only mid-dtpl in [40,100] GB, got %q", out)
	}
}

func TestCLI_TemplateList_RejectsInvalidMinDisk(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedDiskTemplates(t, s)

	_, err := runCLI("template", "list", "--min-disk-gb", "lots")
	if err == nil {
		t.Fatalf("expected error for non-numeric --min-disk-gb")
	}
	if !strings.Contains(err.Error(), "--min-disk-gb") {
		t.Fatalf("expected error to mention --min-disk-gb, got %v", err)
	}
}

func TestCLI_TemplateList_RejectsNegativeMaxDisk(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()
	seedDiskTemplates(t, s)

	_, err := runCLI("template", "list", "--max-disk-gb", "-2")
	if err == nil {
		t.Fatalf("expected error for negative --max-disk-gb")
	}
	if !strings.Contains(err.Error(), "--max-disk-gb") {
		t.Fatalf("expected error to mention --max-disk-gb, got %v", err)
	}
}

// 5.4.78 — `--prefix` on `vmsmith template list`. Case-sensitive HasPrefix
// against tpl.Name; mirrors snapshot/VM/image --prefix.
func TestCLI_TemplateList_FilterByPrefix_Match(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "rocky9-base-v1", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-2", Name: "rocky9-base-v2", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-3", Name: "ubuntu-22", Image: "ubuntu.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--prefix", "rocky9-base-")
	if err != nil {
		t.Fatalf("template list --prefix: %v", err)
	}
	if !strings.Contains(out, "rocky9-base-v1") || !strings.Contains(out, "rocky9-base-v2") {
		t.Fatalf("expected both rocky9-base-* in output, got %q", out)
	}
	if strings.Contains(out, "ubuntu-22") {
		t.Fatalf("ubuntu-22 should be excluded, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-1", Name: "Rocky9-Base", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--prefix", "rocky9-base")
	if err != nil {
		t.Fatalf("template list --prefix rocky9-base: %v", err)
	}
	if strings.Contains(out, "Rocky9-Base") {
		t.Fatalf("case-sensitive non-match should exclude Rocky9-Base, got %q", out)
	}
}

func TestCLI_TemplateList_FilterByPrefix_EmptyOmitsFilter(t *testing.T) {
	s, _, cleanup := withTestStorage(t)
	defer cleanup()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-a", Name: "alpha", Image: "rocky9.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.PutTemplate(&types.VMTemplate{ID: "tmpl-b", Name: "bravo", Image: "ubuntu.qcow2", CreatedAt: t0, UpdatedAt: t0}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := runCLI("template", "list", "--prefix", "  ")
	if err != nil {
		t.Fatalf("template list --prefix '  ': %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "bravo") {
		t.Fatalf("whitespace --prefix should be no-op, got %q", out)
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
	if !strings.Contains(err.Error(), "delivery_status") {
		t.Fatalf("error = %q, want it to advertise 'delivery_status'", err.Error())
	}
}

// 5.4.98 — delivery_status sort axis forwarded to the daemon.
func TestCLI_WebhookList_ForwardsDeliveryStatusSort(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--sort", "  Delivery_Status  ", "--order", "DESC"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	// CLI normalises mixed-case + whitespace into the canonical lowercase axis.
	if !strings.Contains(state.lastQuery, "sort=delivery_status") {
		t.Fatalf("expected sort=delivery_status, got %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "order=desc") {
		t.Fatalf("expected order=desc, got %q", state.lastQuery)
	}
}

// 5.4.114 — active sort axis forwarded to the daemon. Whitespace +
// case-folding on the --sort value (the daemon validates server-side;
// the CLI just lowercases + trims before forwarding).
func TestCLI_WebhookList_ForwardsActiveSort(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--sort", "  ACTIVE  ", "--order", "desc"); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if !strings.Contains(state.lastQuery, "sort=active") {
		t.Fatalf("expected sort=active, got %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "order=desc") {
		t.Fatalf("expected order=desc, got %q", state.lastQuery)
	}
}

// TestCLI_WebhookList_InvalidSortAdvertisesActive asserts the client-side
// rejection lists active in the error envelope so operators discover the
// new 5.4.114 axis from the error path.
func TestCLI_WebhookList_InvalidSortAdvertisesActive(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)
	_, err := runCLI("webhook", "list", "--api-url", srv.URL, "--sort", "garbage")
	if err == nil {
		t.Fatal("expected client-side rejection of --sort garbage")
	}
	if !strings.Contains(err.Error(), "active") {
		t.Fatalf("invalid --sort message should advertise active, got %v", err)
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

// =====================================================
// webhook list --url-prefix tests (5.4.83)
// =====================================================

func TestCLI_WebhookList_ForwardsURLPrefix(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--url-prefix", "https://hooks.slack.com/"); err != nil {
		t.Fatalf("webhook list --url-prefix: %v", err)
	}
	// strings.Contains the URL-encoded form is annoying; check after decoding.
	if !strings.Contains(state.lastQuery, "url_prefix=") {
		t.Fatalf("query missing url_prefix=: %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "hooks.slack.com") {
		t.Fatalf("query missing host substring: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_LowercasesURLPrefix(t *testing.T) {
	// The CLI lowercases the value so the daemon receives a canonical needle
	// regardless of shell quoting noise. Matches the case-insensitive URL
	// haystack in WebhookMatchesSearch.
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--url-prefix", "  HTTPS://HOOKS.SLACK.COM/  "); err != nil {
		t.Fatalf("webhook list --url-prefix: %v", err)
	}
	if !strings.Contains(state.lastQuery, "https") || strings.Contains(state.lastQuery, "HTTPS") {
		t.Fatalf("expected lowercased url_prefix in query, got %q", state.lastQuery)
	}
	if !strings.Contains(state.lastQuery, "hooks.slack.com") {
		t.Fatalf("expected hooks.slack.com host in query, got %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_EmptyURLPrefixOmitsParam(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--url-prefix", ""); err != nil {
		t.Fatalf("webhook list: %v", err)
	}
	if strings.Contains(state.lastQuery, "url_prefix=") {
		t.Fatalf("empty url-prefix must not be forwarded: %q", state.lastQuery)
	}
}

// =====================================================
// webhook list --since / --until tests
// =====================================================

func TestCLI_WebhookList_ForwardsSince(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--since", "2026-05-01T00:00:00Z"); err != nil {
		t.Fatalf("webhook list --since: %v", err)
	}
	if !strings.Contains(state.lastQuery, "since=2026-05-01") {
		t.Fatalf("query missing since=2026-05-01: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ForwardsUntil(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--until", "2026-05-20T00:00:00Z"); err != nil {
		t.Fatalf("webhook list --until: %v", err)
	}
	if !strings.Contains(state.lastQuery, "until=2026-05-20") {
		t.Fatalf("query missing until=2026-05-20: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ForwardsSinceAndUntil(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--since", "2026-05-01T00:00:00Z", "--until", "2026-05-20T00:00:00Z"); err != nil {
		t.Fatalf("webhook list --since --until: %v", err)
	}
	if !strings.Contains(state.lastQuery, "since=2026-05-01") || !strings.Contains(state.lastQuery, "until=2026-05-20") {
		t.Fatalf("query missing since/until: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_RejectsInvalidSince(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL, "--since", "yesterday")
	if err == nil {
		t.Fatalf("expected error for invalid --since, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --since") {
		t.Fatalf("error = %q, want it to mention 'invalid --since'", err.Error())
	}
}

func TestCLI_WebhookList_RejectsInvalidUntil(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL, "--until", "2026-13-99")
	if err == nil {
		t.Fatalf("expected error for invalid --until, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --until") {
		t.Fatalf("error = %q, want it to mention 'invalid --until'", err.Error())
	}
}

func TestCLI_WebhookList_EmptySinceOmitsParam(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL, "--since", "  "); err != nil {
		t.Fatalf("webhook list --since '  ': %v", err)
	}
	if strings.Contains(state.lastQuery, "since=") {
		t.Fatalf("whitespace-only --since must not be forwarded: %q", state.lastQuery)
	}
}

// =====================================================
// webhook list --last-delivery-since / --last-delivery-until tests (5.4.61)
// =====================================================

func TestCLI_WebhookList_ForwardsLastDeliverySince(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--last-delivery-since", "2026-05-01T00:00:00Z"); err != nil {
		t.Fatalf("webhook list --last-delivery-since: %v", err)
	}
	if !strings.Contains(state.lastQuery, "last_delivery_since=2026-05-01") {
		t.Fatalf("query missing last_delivery_since=2026-05-01: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ForwardsLastDeliveryUntil(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--last-delivery-until", "2026-05-20T00:00:00Z"); err != nil {
		t.Fatalf("webhook list --last-delivery-until: %v", err)
	}
	if !strings.Contains(state.lastQuery, "last_delivery_until=2026-05-20") {
		t.Fatalf("query missing last_delivery_until=2026-05-20: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ForwardsLastDeliveryRange(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--last-delivery-since", "2026-05-01T00:00:00Z",
		"--last-delivery-until", "2026-05-20T00:00:00Z"); err != nil {
		t.Fatalf("webhook list --last-delivery-since --last-delivery-until: %v", err)
	}
	if !strings.Contains(state.lastQuery, "last_delivery_since=2026-05-01") ||
		!strings.Contains(state.lastQuery, "last_delivery_until=2026-05-20") {
		t.Fatalf("query missing last_delivery_since/until: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_RejectsInvalidLastDeliverySince(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--last-delivery-since", "yesterday")
	if err == nil {
		t.Fatalf("expected error for invalid --last-delivery-since, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --last-delivery-since") {
		t.Fatalf("error = %q, want it to mention 'invalid --last-delivery-since'", err.Error())
	}
}

func TestCLI_WebhookList_RejectsInvalidLastDeliveryUntil(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--last-delivery-until", "2026-13-99")
	if err == nil {
		t.Fatalf("expected error for invalid --last-delivery-until, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --last-delivery-until") {
		t.Fatalf("error = %q, want it to mention 'invalid --last-delivery-until'", err.Error())
	}
}

func TestCLI_WebhookList_EmptyLastDeliveryFlagsOmitParams(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--last-delivery-since", "  ",
		"--last-delivery-until", "  "); err != nil {
		t.Fatalf("webhook list with whitespace last-delivery flags: %v", err)
	}
	if strings.Contains(state.lastQuery, "last_delivery_since=") ||
		strings.Contains(state.lastQuery, "last_delivery_until=") {
		t.Fatalf("whitespace-only last-delivery flags must not be forwarded: %q", state.lastQuery)
	}
}

// =====================================================
// webhook list --delivery-status tests (5.4.35)
// =====================================================

func TestCLI_WebhookList_ForwardsDeliveryStatus(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--delivery-status", "failing"); err != nil {
		t.Fatalf("webhook list --delivery-status: %v", err)
	}
	if !strings.Contains(state.lastQuery, "delivery_status=failing") {
		t.Fatalf("query missing delivery_status=failing: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_DeliveryStatus_NormalisesCaseAndWhitespace(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--delivery-status", "  HEALTHY  "); err != nil {
		t.Fatalf("webhook list --delivery-status: %v", err)
	}
	if !strings.Contains(state.lastQuery, "delivery_status=healthy") {
		t.Fatalf("expected normalised 'delivery_status=healthy' in query: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_RejectsInvalidDeliveryStatus(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--delivery-status", "alive")
	if err == nil {
		t.Fatalf("expected error for invalid --delivery-status, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --delivery-status") {
		t.Fatalf("error = %q, want it to mention 'invalid --delivery-status'", err.Error())
	}
}

func TestCLI_WebhookList_EmptyDeliveryStatusOmitsParam(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--delivery-status", "  "); err != nil {
		t.Fatalf("webhook list --delivery-status '  ': %v", err)
	}
	if strings.Contains(state.lastQuery, "delivery_status=") {
		t.Fatalf("whitespace-only --delivery-status must not be forwarded: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ForwardsActiveTrue(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--active", "true"); err != nil {
		t.Fatalf("webhook list --active true: %v", err)
	}
	if !strings.Contains(state.lastQuery, "active=true") {
		t.Fatalf("query missing active=true: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_ForwardsActiveFalse(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--active", "false"); err != nil {
		t.Fatalf("webhook list --active false: %v", err)
	}
	if !strings.Contains(state.lastQuery, "active=false") {
		t.Fatalf("query missing active=false: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_Active_NormalisesAliasesAndCase(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	// "1" alias + surrounding whitespace + mixed case all normalise to true.
	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--active", "  1  "); err != nil {
		t.Fatalf("webhook list --active '  1  ': %v", err)
	}
	if !strings.Contains(state.lastQuery, "active=true") {
		t.Fatalf("expected normalised 'active=true' in query: %q", state.lastQuery)
	}
}

func TestCLI_WebhookList_RejectsInvalidActive(t *testing.T) {
	srv, _ := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	_, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--active", "maybe")
	if err == nil {
		t.Fatalf("expected error for invalid --active, got nil")
	}
	if !strings.Contains(err.Error(), "invalid --active") {
		t.Fatalf("error = %q, want it to mention 'invalid --active'", err.Error())
	}
}

func TestCLI_WebhookList_EmptyActiveOmitsParam(t *testing.T) {
	srv, state := newFakeWebhookListDaemon(t, http.StatusOK, `[]`)

	if _, err := runCLI("webhook", "list", "--api-url", srv.URL,
		"--active", "  "); err != nil {
		t.Fatalf("webhook list --active '  ': %v", err)
	}
	if strings.Contains(state.lastQuery, "active=") {
		t.Fatalf("whitespace-only --active must not be forwarded: %q", state.lastQuery)
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

// --- snapshot list --since / --until (roadmap 5.4.28) ---

func TestCLI_SnapshotList_FilterBySince(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap-since", Name: "host"})
	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-since", Name: "early", CreatedAt: day(1)})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-since", Name: "late", CreatedAt: day(30)})

	out, err := runCLI("snapshot", "list", "vm-snap-since", "--since", "2026-05-10T00:00:00Z")
	if err != nil {
		t.Fatalf("snapshot list --since: %v", err)
	}
	if !strings.Contains(out, "late") || strings.Contains(out, "early") {
		t.Fatalf("expected only late, got %q", out)
	}
}

func TestCLI_SnapshotList_FilterByUntil(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap-until", Name: "host"})
	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-until", Name: "early", CreatedAt: day(1)})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-until", Name: "late", CreatedAt: day(30)})

	out, err := runCLI("snapshot", "list", "vm-snap-until", "--until", "2026-05-15T00:00:00Z")
	if err != nil {
		t.Fatalf("snapshot list --until: %v", err)
	}
	if !strings.Contains(out, "early") || strings.Contains(out, "late") {
		t.Fatalf("expected only early, got %q", out)
	}
}

func TestCLI_SnapshotList_FilterBySinceAndUntil(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap-range", Name: "host"})
	day := func(d int) time.Time {
		return time.Date(2026, 5, d, 12, 0, 0, 0, time.UTC)
	}
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-range", Name: "snap-1", CreatedAt: day(1)})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-range", Name: "snap-15", CreatedAt: day(15)})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-range", Name: "snap-30", CreatedAt: day(30)})

	out, err := runCLI("snapshot", "list", "vm-snap-range",
		"--since", "2026-05-10T00:00:00Z", "--until", "2026-05-20T00:00:00Z")
	if err != nil {
		t.Fatalf("snapshot list --since --until: %v", err)
	}
	if !strings.Contains(out, "snap-15") || strings.Contains(out, "snap-1\t") || strings.Contains(out, "snap-30") {
		t.Fatalf("expected only snap-15, got %q", out)
	}
}

func TestCLI_SnapshotList_RejectsInvalidSince(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-snap-bad", Name: "host"})

	_, err := runCLI("snapshot", "list", "vm-snap-bad", "--since", "yesterday")
	if err == nil || !strings.Contains(err.Error(), "invalid --since") {
		t.Fatalf("expected invalid --since error, got %v", err)
	}
}

func TestCLI_SnapshotList_RejectsInvalidUntil(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-snap-bad-until", Name: "host"})

	_, err := runCLI("snapshot", "list", "vm-snap-bad-until", "--until", "2026-13-99")
	if err == nil || !strings.Contains(err.Error(), "invalid --until") {
		t.Fatalf("expected invalid --until error, got %v", err)
	}
}

func TestCLI_SnapshotList_EmptySinceOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap-empty", Name: "host"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-empty", Name: "any", CreatedAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)})

	out, err := runCLI("snapshot", "list", "vm-snap-empty", "--since", "  ")
	if err != nil {
		t.Fatalf("snapshot list --since '  ': %v", err)
	}
	if !strings.Contains(out, "any") {
		t.Fatalf("whitespace --since should be no-op, got %q", out)
	}
}

// 5.4.75 — `--prefix` on `vmsmith snapshot list`. Case-sensitive HasPrefix
// against snap.Name; mirrors the `--prefix` selector on `snapshot delete`.
func TestCLI_SnapshotList_FilterByPrefix_Match(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap-prefix"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix", Name: "auto-nightly-1"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix", Name: "auto-nightly-2"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix", Name: "manual-rollback"})

	out, err := runCLI("snapshot", "list", "vm-snap-prefix", "--prefix", "auto-nightly-")
	if err != nil {
		t.Fatalf("snapshot list --prefix: %v", err)
	}
	if !strings.Contains(out, "auto-nightly-1") || !strings.Contains(out, "auto-nightly-2") {
		t.Fatalf("expected both auto-nightly-* in output, got %q", out)
	}
	if strings.Contains(out, "manual-rollback") {
		t.Fatalf("manual-rollback should be excluded, got %q", out)
	}
}

func TestCLI_SnapshotList_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap-prefix-case"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-case", Name: "Auto-Daily"})

	out, err := runCLI("snapshot", "list", "vm-snap-prefix-case", "--prefix", "auto-daily")
	if err != nil {
		t.Fatalf("snapshot list --prefix auto-daily: %v", err)
	}
	if strings.Contains(out, "Auto-Daily") {
		t.Fatalf("case-sensitive non-match should exclude Auto-Daily, got %q", out)
	}
}

func TestCLI_SnapshotList_FilterByPrefix_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-snap-prefix-empty"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-empty", Name: "snap-a"})
	mock.SeedSnapshot(&types.Snapshot{VMID: "vm-snap-prefix-empty", Name: "snap-b"})

	out, err := runCLI("snapshot", "list", "vm-snap-prefix-empty", "--prefix", "  ")
	if err != nil {
		t.Fatalf("snapshot list --prefix '  ': %v", err)
	}
	if !strings.Contains(out, "snap-a") || !strings.Contains(out, "snap-b") {
		t.Fatalf("whitespace --prefix should be no-op, got %q", out)
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

// --- 5.4.76 — `--prefix` on `vmsmith vm list`. Case-sensitive HasPrefix on
// vm.Name; mirrors the 5.4.75 snapshot list --prefix selector.

func TestCLI_VMList_FilterByPrefix_Match(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "web-prod-1", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "web-prod-2", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-3", Name: "db-prod-1", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--prefix", "web-prod-")
	if err != nil {
		t.Fatalf("vm list --prefix: %v", err)
	}
	if !strings.Contains(out, "web-prod-1") || !strings.Contains(out, "web-prod-2") {
		t.Fatalf("expected both web-prod-* in output, got %q", out)
	}
	if strings.Contains(out, "db-prod-1") {
		t.Fatalf("db-prod-1 should be excluded, got %q", out)
	}
}

func TestCLI_VMList_FilterByPrefix_IsCaseSensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "Web-Prod-1", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--prefix", "web-prod-")
	if err != nil {
		t.Fatalf("vm list --prefix: %v", err)
	}
	if strings.Contains(out, "Web-Prod-1") {
		t.Fatalf("case-sensitive non-match should exclude Web-Prod-1, got %q", out)
	}
}

func TestCLI_VMList_FilterByPrefix_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "vm-a", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "vm-b", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--prefix", "  ")
	if err != nil {
		t.Fatalf("vm list --prefix '  ': %v", err)
	}
	if !strings.Contains(out, "vm-a") || !strings.Contains(out, "vm-b") {
		t.Fatalf("whitespace --prefix should be no-op, got %q", out)
	}
}

// 5.4.79 — NAT static IP filter on CLI vm list.

func TestCLI_VMList_FilterByNATStaticIP_Match(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatStaticIP: "192.168.100.50/24"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatStaticIP: "192.168.100.51/24"}})

	out, err := runCLI("vm", "list", "--nat-static-ip", "192.168.100.50")
	if err != nil {
		t.Fatalf("vm list --nat-static-ip: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected alpha in output, got %q", out)
	}
	if strings.Contains(out, "beta") {
		t.Fatalf("beta should be excluded, got %q", out)
	}
}

func TestCLI_VMList_FilterByNATStaticIP_ExcludesDHCP(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatStaticIP: "192.168.100.50/24"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--nat-static-ip", "192.168.100.50")
	if err != nil {
		t.Fatalf("vm list --nat-static-ip: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected alpha in output, got %q", out)
	}
	if strings.Contains(out, "beta") {
		t.Fatalf("DHCP VM beta should be excluded, got %q", out)
	}
}

func TestCLI_VMList_FilterByNATStaticIP_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatStaticIP: "fe80::ABCD/64"}})

	out, err := runCLI("vm", "list", "--nat-static-ip", "fe80::abcd/64")
	if err != nil {
		t.Fatalf("vm list --nat-static-ip: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByNATStaticIP_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatStaticIP: "192.168.100.50/24"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--nat-static-ip", "  ")
	if err != nil {
		t.Fatalf("vm list --nat-static-ip '  ': %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("whitespace --nat-static-ip should be no-op, got %q", out)
	}
}

// 5.4.80 — NAT gateway filter on CLI vm list.

func TestCLI_VMList_FilterByNATGateway_Match(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatGateway: "192.168.100.1"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatGateway: "10.0.0.1"}})

	out, err := runCLI("vm", "list", "--nat-gateway", "192.168.100.1")
	if err != nil {
		t.Fatalf("vm list --nat-gateway: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected alpha in output, got %q", out)
	}
	if strings.Contains(out, "beta") {
		t.Fatalf("beta should be excluded, got %q", out)
	}
}

func TestCLI_VMList_FilterByNATGateway_ExcludesEmpty(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatGateway: "192.168.100.1"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--nat-gateway", "192.168.100.1")
	if err != nil {
		t.Fatalf("vm list --nat-gateway: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected alpha in output, got %q", out)
	}
	if strings.Contains(out, "beta") {
		t.Fatalf("VM with empty NatGateway beta should be excluded, got %q", out)
	}
}

func TestCLI_VMList_FilterByNATGateway_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatGateway: "FE80::1"}})

	out, err := runCLI("vm", "list", "--nat-gateway", "fe80::1")
	if err != nil {
		t.Fatalf("vm list --nat-gateway: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByNATGateway_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", Spec: types.VMSpec{CPUs: 1, RAMMB: 512, NatGateway: "192.168.100.1"}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--nat-gateway", "  ")
	if err != nil {
		t.Fatalf("vm list --nat-gateway '  ': %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("whitespace --nat-gateway should be no-op, got %q", out)
	}
}

// 5.4.81 — Runtime IP filter on CLI vm list. Mirrors the NAT gateway shape
// but matches the discovered v.IP field (the value printed in the table's
// IP column), covering DHCP-assigned VMs that --nat-static-ip cannot.

func TestCLI_VMList_FilterByIP_Match(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", IP: "192.168.100.99", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--ip", "192.168.100.42")
	if err != nil {
		t.Fatalf("vm list --ip: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected alpha in output, got %q", out)
	}
	if strings.Contains(out, "beta") {
		t.Fatalf("beta should be excluded, got %q", out)
	}
}

func TestCLI_VMList_FilterByIP_ExcludesEmpty(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}}) // empty IP

	out, err := runCLI("vm", "list", "--ip", "192.168.100.42")
	if err != nil {
		t.Fatalf("vm list --ip: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected alpha in output, got %q", out)
	}
	if strings.Contains(out, "beta") {
		t.Fatalf("VM with empty IP beta should be excluded, got %q", out)
	}
}

func TestCLI_VMList_FilterByIP_CaseInsensitive(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "FE80::42", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--ip", "fe80::42")
	if err != nil {
		t.Fatalf("vm list --ip: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Fatalf("expected case-insensitive match, got %q", out)
	}
}

func TestCLI_VMList_FilterByIP_EmptyOmitsFilter(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()

	mock.SeedVM(&types.VM{ID: "vm-1", Name: "alpha", IP: "192.168.100.42", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})
	mock.SeedVM(&types.VM{ID: "vm-2", Name: "beta", Spec: types.VMSpec{CPUs: 1, RAMMB: 512}})

	out, err := runCLI("vm", "list", "--ip", "  ")
	if err != nil {
		t.Fatalf("vm list --ip '  ': %v", err)
	}
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("whitespace --ip should be no-op, got %q", out)
	}
}
