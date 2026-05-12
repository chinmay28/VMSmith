package types

import "testing"

func pfIDs(pfs []*PortForward) []string {
	out := make([]string, len(pfs))
	for i, p := range pfs {
		out[i] = p.ID
	}
	return out
}

func TestSortPortForwards_ByID_Asc(t *testing.T) {
	pfs := []*PortForward{
		{ID: "vm-1/22002", HostPort: 22002},
		{ID: "vm-1/22000", HostPort: 22000},
		{ID: "vm-1/22001", HostPort: 22001},
	}
	SortPortForwards(pfs, PortForwardSortID, SortOrderAsc)
	want := []string{"vm-1/22000", "vm-1/22001", "vm-1/22002"}
	got := pfIDs(pfs)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortPortForwards_ByHostPort_Desc(t *testing.T) {
	pfs := []*PortForward{
		{ID: "vm-1/22000", HostPort: 22000},
		{ID: "vm-1/22002", HostPort: 22002},
		{ID: "vm-1/22001", HostPort: 22001},
	}
	SortPortForwards(pfs, PortForwardSortHostPort, SortOrderDesc)
	want := []string{"vm-1/22002", "vm-1/22001", "vm-1/22000"}
	got := pfIDs(pfs)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortPortForwards_ByGuestPort_TiebreaksOnID(t *testing.T) {
	pfs := []*PortForward{
		{ID: "vm-1/22003", HostPort: 22003, GuestPort: 22},
		{ID: "vm-1/22002", HostPort: 22002, GuestPort: 22},
		{ID: "vm-1/22001", HostPort: 22001, GuestPort: 80},
	}
	SortPortForwards(pfs, PortForwardSortGuestPort, SortOrderAsc)
	// guest_port 22 < 80, equal-22 pair tiebreaks on id
	want := []string{"vm-1/22002", "vm-1/22003", "vm-1/22001"}
	got := pfIDs(pfs)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortPortForwards_ByProtocol_TiebreaksOnID(t *testing.T) {
	pfs := []*PortForward{
		{ID: "vm-1/22002", HostPort: 22002, Protocol: ProtocolUDP},
		{ID: "vm-1/22001", HostPort: 22001, Protocol: ProtocolTCP},
		{ID: "vm-1/22003", HostPort: 22003, Protocol: ProtocolTCP},
	}
	SortPortForwards(pfs, PortForwardSortProtocol, SortOrderAsc)
	// "tcp" < "udp"; tcp pair tiebreaks on id
	want := []string{"vm-1/22001", "vm-1/22003", "vm-1/22002"}
	got := pfIDs(pfs)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortPortForwards_ByDescription_CaseInsensitive(t *testing.T) {
	pfs := []*PortForward{
		{ID: "vm-1/22003", HostPort: 22003, Description: "Web frontend"},
		{ID: "vm-1/22001", HostPort: 22001, Description: "ssh jumpbox"},
		{ID: "vm-1/22002", HostPort: 22002, Description: "metrics scrape"},
	}
	SortPortForwards(pfs, PortForwardSortDescription, SortOrderAsc)
	// case-insensitive: "metrics..." < "ssh..." < "web..."
	want := []string{"vm-1/22002", "vm-1/22001", "vm-1/22003"}
	got := pfIDs(pfs)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortPortForwards_UnknownFieldFallsBackToID(t *testing.T) {
	pfs := []*PortForward{
		{ID: "vm-1/22003", HostPort: 22003},
		{ID: "vm-1/22001", HostPort: 22001},
		{ID: "vm-1/22002", HostPort: 22002},
	}
	SortPortForwards(pfs, "guest_ip", SortOrderAsc)
	want := []string{"vm-1/22001", "vm-1/22002", "vm-1/22003"}
	got := pfIDs(pfs)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: id = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSortPortForwards_StableEqualKeys(t *testing.T) {
	// Two independent sorts on equal-key data must produce the same order so
	// repeated requests return deterministic results.
	build := func() []*PortForward {
		return []*PortForward{
			{ID: "vm-1/22003", HostPort: 22003, Description: "shared"},
			{ID: "vm-1/22001", HostPort: 22001, Description: "shared"},
			{ID: "vm-1/22004", HostPort: 22004, Description: "shared"},
			{ID: "vm-1/22002", HostPort: 22002, Description: "shared"},
		}
	}
	a, b := build(), build()
	SortPortForwards(a, PortForwardSortDescription, SortOrderAsc)
	SortPortForwards(b, PortForwardSortDescription, SortOrderAsc)
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("idx %d: a=%q b=%q — equal-description tie not deterministic", i, a[i].ID, b[i].ID)
		}
	}
}
