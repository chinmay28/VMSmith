package types

import "testing"

func TestEventMatchesSearch_EmptyQueryMatchesEverything(t *testing.T) {
	e := &Event{Type: "vm.started", Message: "ok"}
	if !EventMatchesSearch(e, "") {
		t.Fatal("empty query should match")
	}
}

func TestEventMatchesSearch_NilEventNeverMatches(t *testing.T) {
	if EventMatchesSearch(nil, "anything") {
		t.Fatal("nil event must not match")
	}
}

func TestEventMatchesSearch_MatchesMessage(t *testing.T) {
	e := &Event{Type: "vm.started", Message: "Started vm web-prod-01"}
	if !EventMatchesSearch(e, "web-prod") {
		t.Fatal("substring of message should match")
	}
}

func TestEventMatchesSearch_MatchesType(t *testing.T) {
	e := &Event{Type: "snapshot.created", Message: "ok"}
	if !EventMatchesSearch(e, "snapshot") {
		t.Fatal("substring of type should match")
	}
}

func TestEventMatchesSearch_MatchesAttributes(t *testing.T) {
	e := &Event{
		Type:    "port_forward.added",
		Message: "ok",
		Attributes: map[string]string{
			"host_port":  "22001",
			"guest_port": "22",
			"protocol":   "tcp",
		},
	}
	if !EventMatchesSearch(e, "22001") {
		t.Fatal("substring inside attribute value should match")
	}
}

func TestEventMatchesSearch_MatchesActorAndVMIDAndResourceID(t *testing.T) {
	e := &Event{
		Type:       "vm.created",
		Actor:      "ops-alice",
		VMID:       "vm-1741234567890123",
		ResourceID: "img-rocky9",
	}
	if !EventMatchesSearch(e, "alice") {
		t.Fatal("actor substring should match")
	}
	if !EventMatchesSearch(e, "1741234") {
		t.Fatal("vm_id substring should match")
	}
	if !EventMatchesSearch(e, "rocky9") {
		t.Fatal("resource_id substring should match")
	}
}

func TestEventMatchesSearch_CallerLowercasesQuery(t *testing.T) {
	// Contract: callers must lower-case the needle. Verify the helper
	// honours that — same-case substring matches, but a mixed-case needle
	// won't because we don't lower it inside.
	e := &Event{Message: "DHCP exhausted"}
	if !EventMatchesSearch(e, "dhcp") {
		t.Fatal("lower-cased substring should match")
	}
	if EventMatchesSearch(e, "DHCP") {
		t.Fatal("uppercase needle must not match — caller is responsible for lowercasing")
	}
}

func TestEventMatchesSearch_NoMatch(t *testing.T) {
	e := &Event{Type: "vm.started", Message: "ok", Source: "libvirt", Severity: "info"}
	if EventMatchesSearch(e, "needle-not-present") {
		t.Fatal("unrelated query should not match")
	}
}

func TestEventMatchesSearch_EmptyHaystackFieldsAreSkipped(t *testing.T) {
	// All fields blank but Type — searching for "" still matches because
	// of the empty-query short-circuit, but a non-empty query that doesn't
	// hit Type must not crash on the nil Attributes map.
	e := &Event{Type: "vm.started"}
	if EventMatchesSearch(e, "stopped") {
		t.Fatal("unrelated query must not match")
	}
	if !EventMatchesSearch(e, "started") {
		t.Fatal("type substring should match")
	}
}
