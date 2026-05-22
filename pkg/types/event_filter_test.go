package types

import "testing"

func TestEventMatchesStreamFilter_EmptyMatchesEverything(t *testing.T) {
	evt := &Event{ID: "1", Type: "vm.started", VMID: "vm-1", Source: "libvirt", Severity: "info"}
	if !EventMatchesStreamFilter(evt, EventStreamFilter{}) {
		t.Fatalf("empty filter should match every non-nil event")
	}
}

func TestEventMatchesStreamFilter_NilEventNeverMatches(t *testing.T) {
	if EventMatchesStreamFilter(nil, EventStreamFilter{}) {
		t.Fatalf("nil event should never match")
	}
	if EventMatchesStreamFilter(nil, EventStreamFilter{VMID: "vm-1"}) {
		t.Fatalf("nil event should never match (with filter)")
	}
}

func TestEventMatchesStreamFilter_VMIDExact(t *testing.T) {
	evt := &Event{VMID: "vm-1"}
	if !EventMatchesStreamFilter(evt, EventStreamFilter{VMID: "vm-1"}) {
		t.Fatalf("expected match on exact VMID")
	}
	if EventMatchesStreamFilter(evt, EventStreamFilter{VMID: "vm-2"}) {
		t.Fatalf("different VMID should not match")
	}
	// Case-sensitive (mirrors store contract).
	if EventMatchesStreamFilter(&Event{VMID: "VM-1"}, EventStreamFilter{VMID: "vm-1"}) {
		t.Fatalf("VMID match should be case-sensitive")
	}
}

func TestEventMatchesStreamFilter_TypeAndSourceAndSeverityActorResourceID(t *testing.T) {
	evt := &Event{Type: "vm.started", Source: "libvirt", Severity: "info", Actor: "ops-alice", ResourceID: "snap-1"}
	cases := []struct {
		name   string
		filter EventStreamFilter
		want   bool
	}{
		{"type-match", EventStreamFilter{Type: "vm.started"}, true},
		{"type-miss", EventStreamFilter{Type: "vm.stopped"}, false},
		{"source-match", EventStreamFilter{Source: "libvirt"}, true},
		{"source-miss", EventStreamFilter{Source: "app"}, false},
		{"severity-match", EventStreamFilter{Severity: "info"}, true},
		{"severity-miss", EventStreamFilter{Severity: "error"}, false},
		{"actor-match", EventStreamFilter{Actor: "ops-alice"}, true},
		{"actor-miss-case", EventStreamFilter{Actor: "Ops-Alice"}, false},
		{"resource-match", EventStreamFilter{ResourceID: "snap-1"}, true},
		{"resource-miss", EventStreamFilter{ResourceID: "snap-2"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EventMatchesStreamFilter(evt, tc.filter); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEventMatchesStreamFilter_TypePrefixCaseInsensitive(t *testing.T) {
	evt := &Event{Type: "Snapshot.Created"}
	// Caller pre-lowercases the prefix, so this models the handler's behavior.
	if !EventMatchesStreamFilter(evt, EventStreamFilter{TypePrefix: "snapshot."}) {
		t.Fatalf("type prefix should match case-insensitively")
	}
	if EventMatchesStreamFilter(evt, EventStreamFilter{TypePrefix: "vm."}) {
		t.Fatalf("non-matching prefix should not match")
	}
	// Full type matches itself.
	if !EventMatchesStreamFilter(&Event{Type: "vm.started"}, EventStreamFilter{TypePrefix: "vm.started"}) {
		t.Fatalf("full type should still match as prefix of itself")
	}
}

func TestEventMatchesStreamFilter_SearchDelegatesToEventMatchesSearch(t *testing.T) {
	evt := &Event{Message: "VM Bastion started", Type: "vm.started"}
	if !EventMatchesStreamFilter(evt, EventStreamFilter{Search: "bastion"}) {
		t.Fatalf("search should match lowercased substring in message")
	}
	if EventMatchesStreamFilter(evt, EventStreamFilter{Search: "totally-absent"}) {
		t.Fatalf("non-matching search needle should not match")
	}
}

func TestEventMatchesStreamFilter_AllFieldsMustMatch(t *testing.T) {
	evt := &Event{Type: "vm.started", VMID: "vm-1", Source: "libvirt"}
	if !EventMatchesStreamFilter(evt, EventStreamFilter{Type: "vm.started", VMID: "vm-1", Source: "libvirt"}) {
		t.Fatalf("all matching fields should match")
	}
	if EventMatchesStreamFilter(evt, EventStreamFilter{Type: "vm.started", VMID: "vm-1", Source: "app"}) {
		t.Fatalf("one mismatching field should make the whole filter fail")
	}
}

func TestEventStreamFilter_HasAny(t *testing.T) {
	if (EventStreamFilter{}).HasAny() {
		t.Fatalf("zero filter should report HasAny()=false")
	}
	cases := []EventStreamFilter{
		{VMID: "x"}, {Type: "x"}, {Source: "x"}, {Severity: "x"},
		{Actor: "x"}, {ResourceID: "x"}, {TypePrefix: "x"}, {Search: "x"},
	}
	for i, f := range cases {
		if !f.HasAny() {
			t.Fatalf("case %d: expected HasAny()=true for %+v", i, f)
		}
	}
}
