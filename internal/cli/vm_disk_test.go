package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestCLI_VMMoveDisk(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "d", State: types.VMStateStopped, DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2"})

	out, err := runCLI("vm", "move-disk", "vm-1", "bulk")
	if err != nil {
		t.Fatalf("move-disk: %v (out: %s)", err, out)
	}
	if !strings.Contains(out, `moved to location "bulk"`) || !strings.Contains(out, "/mnt/bulk/vm-1/disk.qcow2") {
		t.Errorf("out = %q", out)
	}
}

func TestCLI_VMMoveDisk_ToPath(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "d", State: types.VMStateStopped, DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2"})

	out, err := runCLI("vm", "move-disk", "vm-1", "/mnt/nvme")
	if err != nil {
		t.Fatalf("move-disk: %v (out: %s)", err, out)
	}
	if !strings.Contains(out, "/mnt/nvme/vm-1/disk.qcow2") {
		t.Errorf("out = %q", out)
	}
	if _, err := runCLI("vm", "move-disk", "vm-1", "/etc/vms"); err == nil || !strings.Contains(err.Error(), "system directory") {
		t.Fatalf("denied path err = %v", err)
	}
}

func TestCLI_VMMoveDisk_Errors(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "d", State: types.VMStateRunning, DiskPath: "/var/lib/vmsmith/vms/vm-1/disk.qcow2"})
	mock.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}

	if _, err := runCLI("vm", "move-disk", "vm-1", "bulk"); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("running vm err = %v", err)
	}
	if _, err := runCLI("vm", "move-disk", "vm-1", "nope"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unknown location err = %v", err)
	}
	if _, err := runCLI("vm", "move-disk", "vm-1"); err == nil {
		t.Fatal("missing location arg accepted")
	}
}

func TestCLI_VMCreate_DiskLocation(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.DiskLocations = map[string]string{"bulk": "/mnt/bulk"}

	out, err := runCLI("vm", "create", "placed", "--image", "img", "--disk-location", "bulk")
	if err != nil {
		t.Fatalf("create: %v (out: %s)", err, out)
	}
	vms, _ := mock.List(context.Background())
	if len(vms) != 1 || vms[0].Spec.DiskLocation != "bulk" || !strings.HasPrefix(vms[0].DiskPath, "/mnt/bulk/") {
		t.Fatalf("vms = %+v", vms)
	}
}

func TestCLI_HostStorage(t *testing.T) {
	body := `[{"name":"default","kind":"default","path":"/var/lib/vmsmith/vms","default":true,"available":true,"total_bytes":1099511627776,"free_bytes":536870912000,"vm_count":3},
{"name":"bulk","kind":"configured","path":"/mnt/bulk","default":false,"available":false,"error":"not mounted","total_bytes":0,"free_bytes":0,"vm_count":0},
{"name":"/media/alice/USB","kind":"discovered","path":"/media/alice/USB","default":false,"available":true,"warning":"/media/alice (mode 0750) is not traversable","total_bytes":1073741824,"free_bytes":1073741824,"vm_count":0}]`
	srv, state := newFakeHostDaemon(t, http.StatusOK, body)

	out, err := runCLI("host", "storage", "--api-url", srv.URL)
	if err != nil {
		t.Fatalf("host storage: %v\nout=%s", err, out)
	}
	if state.lastPath != "/api/v1/host/storage-locations" {
		t.Fatalf("path = %q", state.lastPath)
	}
	for _, want := range []string{"NAME", "KIND", "FREE", "default", "500.0 GiB", "1.0 TiB", "bulk", "configured", "unavailable: not mounted",
		"discovered", "/media/alice/USB", "warning: /media/alice (mode 0750)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	out, err = runCLI("host", "storage", "--api-url", srv.URL, "--json")
	if err != nil || !strings.Contains(out, `"name":"bulk"`) {
		t.Fatalf("--json out=%s err=%v", out, err)
	}
}
