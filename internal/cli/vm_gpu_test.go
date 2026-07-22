package cli

import (
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestCLI_VMGPUAttach_StoppedVM(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateStopped})

	out, err := runCLI("vm", "gpu", "attach", "vm-1", "01:00.0")
	if err != nil {
		t.Fatalf("attach: %v (out: %s)", err, out)
	}
	if !strings.Contains(out, "GPU 01:00.0 attached to g") {
		t.Errorf("out = %q", out)
	}
	if !strings.Contains(out, "0000:01:00.0") {
		t.Errorf("normalized address missing: %q", out)
	}
}

func TestCLI_VMGPUAttach_RunningRefusedWithoutForce(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateRunning})

	_, err := runCLI("vm", "gpu", "attach", "vm-1", "01:00.0")
	if err == nil || !strings.Contains(err.Error(), "force") {
		t.Fatalf("err = %v, want force-gate refusal", err)
	}

	out, err := runCLI("vm", "gpu", "attach", "vm-1", "01:00.0", "--force-attach")
	if err != nil {
		t.Fatalf("forced attach: %v", err)
	}
	if !strings.Contains(out, "reboot the guest") {
		t.Errorf("expected reboot warning, out = %q", out)
	}
}

func TestCLI_VMGPUDetach(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{
		ID: "vm-1", Name: "g", State: types.VMStateStopped,
		Spec: types.VMSpec{GPUs: []string{"0000:01:00.0"}},
	})

	out, err := runCLI("vm", "gpu", "detach", "vm-1", "0000:01:00.0")
	if err != nil {
		t.Fatalf("detach: %v (out: %s)", err, out)
	}
	if !strings.Contains(out, "No GPUs remain attached") {
		t.Errorf("out = %q", out)
	}
}

func TestCLI_VMGPUDetach_NotAttached(t *testing.T) {
	mock, cleanup := withMockVM(t)
	defer cleanup()
	mock.SeedVM(&types.VM{ID: "vm-1", Name: "g", State: types.VMStateStopped})

	_, err := runCLI("vm", "gpu", "detach", "vm-1", "01:00.0")
	if err == nil || !strings.Contains(err.Error(), "not attached") {
		t.Fatalf("err = %v, want not-attached error", err)
	}
}
