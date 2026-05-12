package types

import "testing"

func TestPortForwardMatchesSearch_EmptyQueryMatchesAll(t *testing.T) {
	pf := &PortForward{ID: "vm-1/2222", HostPort: 2222, GuestPort: 22, Protocol: ProtocolTCP}
	if !PortForwardMatchesSearch(pf, "") {
		t.Fatalf("empty query should match every port forward")
	}
}

func TestPortForwardMatchesSearch_NilRuleNeverMatches(t *testing.T) {
	if PortForwardMatchesSearch(nil, "anything") {
		t.Fatalf("nil port forward must not match a non-empty query")
	}
}

func TestPortForwardMatchesSearch_DescriptionSubstring(t *testing.T) {
	pf := &PortForward{
		ID:          "vm-1/8080",
		HostPort:    8080,
		GuestPort:   80,
		Protocol:    ProtocolTCP,
		Description: "Public HTTP ingress for web tier",
	}
	if !PortForwardMatchesSearch(pf, "http") {
		t.Fatalf("expected description substring to match")
	}
	if !PortForwardMatchesSearch(pf, "ingress") {
		t.Fatalf("expected description substring to match")
	}
}

func TestPortForwardMatchesSearch_ProtocolSubstring(t *testing.T) {
	pf := &PortForward{HostPort: 53, GuestPort: 53, Protocol: ProtocolUDP}
	if !PortForwardMatchesSearch(pf, "udp") {
		t.Fatalf("expected protocol udp to match")
	}
	if PortForwardMatchesSearch(pf, "tcp") {
		t.Fatalf("did not expect tcp needle to match a udp rule")
	}
}

func TestPortForwardMatchesSearch_HostPortSubstring(t *testing.T) {
	pf := &PortForward{HostPort: 22022, GuestPort: 22, Protocol: ProtocolTCP}
	if !PortForwardMatchesSearch(pf, "22022") {
		t.Fatalf("expected exact host port match")
	}
	if !PortForwardMatchesSearch(pf, "2202") {
		t.Fatalf("expected host port substring to match")
	}
}

func TestPortForwardMatchesSearch_GuestPortSubstring(t *testing.T) {
	pf := &PortForward{HostPort: 6443, GuestPort: 6443, Protocol: ProtocolTCP}
	if !PortForwardMatchesSearch(pf, "6443") {
		t.Fatalf("expected guest port match")
	}
}

func TestPortForwardMatchesSearch_GuestIPSubstring(t *testing.T) {
	pf := &PortForward{
		HostPort:  9090,
		GuestPort: 9090,
		GuestIP:   "192.168.100.42",
		Protocol:  ProtocolTCP,
	}
	if !PortForwardMatchesSearch(pf, "100.42") {
		t.Fatalf("expected guest IP substring to match")
	}
}

func TestPortForwardMatchesSearch_CallerLowercasesQuery(t *testing.T) {
	// PortForwardMatchesSearch lowercases the haystack but trusts the
	// caller to have lowercased the needle (see the API/CLI handlers).
	pf := &PortForward{
		HostPort:    8080,
		GuestPort:   80,
		Protocol:    ProtocolTCP,
		Description: "Public HTTP Ingress",
	}
	if PortForwardMatchesSearch(pf, "HTTP") {
		t.Fatalf("expected uppercase needle to miss; caller is responsible for lowercasing")
	}
	if !PortForwardMatchesSearch(pf, "http") {
		t.Fatalf("expected lowercase needle to hit case-insensitive haystack")
	}
}

func TestPortForwardMatchesSearch_SkipsEmptyDescription(t *testing.T) {
	pf := &PortForward{HostPort: 8080, GuestPort: 80, Protocol: ProtocolTCP}
	if PortForwardMatchesSearch(pf, "alpha-not-here") {
		t.Fatalf("unrelated query must not match")
	}
	if !PortForwardMatchesSearch(pf, "8080") {
		t.Fatalf("host port should still match when description is empty")
	}
}

func TestPortForwardMatchesSearch_NoMatch(t *testing.T) {
	pf := &PortForward{
		HostPort:    8080,
		GuestPort:   80,
		Protocol:    ProtocolTCP,
		Description: "production frontend",
		GuestIP:     "192.168.100.10",
	}
	if PortForwardMatchesSearch(pf, "needle-not-present") {
		t.Fatalf("unrelated query should not match")
	}
}

func TestPortForwardMatchesSearch_IDAndVMIDNotInHaystack(t *testing.T) {
	// ID is `{vmID}/{hostPort}` and VMID is the URL scope. A needle that
	// only appears in those fields (e.g., a substring of the VM id's
	// unix-nano tail) must not match.
	pf := &PortForward{
		ID:        "vm-1741234567890123/2222",
		VMID:      "vm-1741234567890123",
		HostPort:  2222,
		GuestPort: 22,
		Protocol:  ProtocolTCP,
	}
	if PortForwardMatchesSearch(pf, "1741234") {
		t.Fatalf("VM-id substring must not match — IDs are excluded from haystack")
	}
}
