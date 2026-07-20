package vm

import (
	"context"
	"testing"

	"github.com/vmsmith/vmsmith/pkg/types"
)

func newTestMulti(t *testing.T) (*MultiHostManager, *MockManager, *MockManager) {
	t.Helper()
	local := NewMockManager()
	remote := NewMockManager()
	multi, err := NewMultiHostManager("local", map[string]Manager{
		"local": local,
		"hv2":   remote,
	})
	if err != nil {
		t.Fatalf("NewMultiHostManager: %v", err)
	}
	return multi, local, remote
}

func TestMultiHost_CreateRoutesByHost(t *testing.T) {
	multi, local, remote := newTestMulti(t)
	ctx := context.Background()

	vmLocal, err := multi.Create(ctx, types.VMSpec{Name: "on-local", Image: "img"})
	if err != nil {
		t.Fatalf("create local: %v", err)
	}
	if vmLocal.Spec.Host != "local" {
		t.Errorf("local vm host = %q, want local (stamped default)", vmLocal.Spec.Host)
	}

	vmRemote, err := multi.Create(ctx, types.VMSpec{Name: "on-hv2", Image: "img", Host: "hv2"})
	if err != nil {
		t.Fatalf("create remote: %v", err)
	}
	if vmRemote.Spec.Host != "hv2" {
		t.Errorf("remote vm host = %q, want hv2", vmRemote.Spec.Host)
	}

	if local.VMCount() != 1 || remote.VMCount() != 1 {
		t.Errorf("placement counts = local:%d remote:%d, want 1/1", local.VMCount(), remote.VMCount())
	}
}

func TestMultiHost_CreateUnknownHostRejected(t *testing.T) {
	multi, _, _ := newTestMulti(t)

	_, err := multi.Create(context.Background(), types.VMSpec{Name: "x", Image: "img", Host: "nope"})
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*types.APIError)
	if !ok || apiErr.Code != "invalid_host" {
		t.Fatalf("err = %v, want invalid_host APIError", err)
	}
}

func TestMultiHost_LifecycleRoutesToOwningHost(t *testing.T) {
	multi, local, remote := newTestMulti(t)
	ctx := context.Background()

	vmRemote, err := multi.Create(ctx, types.VMSpec{Name: "on-hv2", Image: "img", Host: "hv2"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Injecting an error on the LOCAL host must not affect ops on the
	// remote-placed VM — proof the call routed to hv2.
	local.StopErr = types.NewAPIError("boom", "local must not be called")
	if err := multi.Stop(ctx, vmRemote.ID); err != nil {
		t.Fatalf("stop should route to hv2: %v", err)
	}
	got, err := multi.Get(ctx, vmRemote.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != types.VMStateStopped {
		t.Errorf("state = %q, want stopped", got.State)
	}
	_ = remote
}

func TestMultiHost_ListAggregatesAcrossHosts(t *testing.T) {
	multi, _, _ := newTestMulti(t)
	ctx := context.Background()

	if _, err := multi.Create(ctx, types.VMSpec{Name: "a", Image: "img"}); err != nil {
		t.Fatal(err)
	}
	if _, err := multi.Create(ctx, types.VMSpec{Name: "b", Image: "img", Host: "hv2"}); err != nil {
		t.Fatal(err)
	}

	vms, err := multi.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(vms) != 2 {
		t.Fatalf("list len = %d, want 2", len(vms))
	}
	hosts := map[string]bool{}
	for _, v := range vms {
		hosts[v.Spec.Host] = true
	}
	if !hosts["local"] || !hosts["hv2"] {
		t.Errorf("hosts seen = %v, want local + hv2", hosts)
	}
}

func TestMultiHost_SnapshotsRouteToOwningHost(t *testing.T) {
	multi, local, _ := newTestMulti(t)
	ctx := context.Background()

	vmRemote, err := multi.Create(ctx, types.VMSpec{Name: "on-hv2", Image: "img", Host: "hv2"})
	if err != nil {
		t.Fatal(err)
	}
	local.CreateSnapshotErr = types.NewAPIError("boom", "local must not be called")

	if _, err := multi.CreateSnapshot(ctx, vmRemote.ID, types.SnapshotSpec{Name: "s1"}); err != nil {
		t.Fatalf("snapshot should route to hv2: %v", err)
	}
	snaps, err := multi.ListSnapshots(ctx, vmRemote.ID)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %v, %v", snaps, err)
	}
}

func TestMultiHost_HostReachable(t *testing.T) {
	multi, _, remote := newTestMulti(t)
	ctx := context.Background()

	if !multi.HostReachable(ctx, "local") || !multi.HostReachable(ctx, "hv2") {
		t.Error("both mock hosts should be reachable")
	}
	remote.ListErr = types.NewAPIError("down", "conn lost")
	if multi.HostReachable(ctx, "hv2") {
		t.Error("hv2 should be unreachable with ListErr injected")
	}
	if multi.HostReachable(ctx, "unknown") {
		t.Error("unknown host should be unreachable")
	}
}

func TestMultiHost_DefaultHostMustExist(t *testing.T) {
	if _, err := NewMultiHostManager("missing", map[string]Manager{"local": NewMockManager()}); err == nil {
		t.Fatal("expected error for missing default host")
	}
}

func TestMultiHost_HostNamesDeterministic(t *testing.T) {
	multi, _, _ := newTestMulti(t)
	names := multi.HostNames()
	if len(names) != 2 || names[0] != "local" || names[1] != "hv2" {
		t.Fatalf("names = %v, want [local hv2]", names)
	}
	if multi.DefaultHostName() != "local" {
		t.Fatalf("default = %q", multi.DefaultHostName())
	}
}
