package webhooks

import (
	"errors"
	"net"
	"testing"
)

func staticResolver(ip string) func(string) ([]net.IP, error) {
	return func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP(ip)}, nil
	}
}

func TestValidateTarget_RejectsNonHTTPScheme(t *testing.T) {
	_, err := validateTarget("ftp://example.com/x", nil, nil)
	if !errors.Is(err, ErrInvalidScheme) {
		t.Fatalf("err = %v, want ErrInvalidScheme", err)
	}
}

func TestValidateTarget_RejectsLoopback(t *testing.T) {
	_, err := validateTarget("http://localhost/hook", nil, staticResolver("127.0.0.1"))
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, want ErrSSRFBlocked", err)
	}
}

func TestValidateTarget_RejectsLinkLocalMetadata(t *testing.T) {
	_, err := validateTarget("http://metadata.example/x", nil, staticResolver("169.254.169.254"))
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, want ErrSSRFBlocked", err)
	}
}

func TestValidateTarget_RejectsVMNATRange(t *testing.T) {
	_, err := validateTarget("http://nat.example/x", nil, staticResolver("192.168.100.10"))
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, want ErrSSRFBlocked", err)
	}
}

func TestValidateTarget_RejectsIPv6Loopback(t *testing.T) {
	_, err := validateTarget("http://v6.example/x", nil, staticResolver("::1"))
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("err = %v, want ErrSSRFBlocked", err)
	}
}

func TestValidateTarget_AllowsPublicAddress(t *testing.T) {
	_, err := validateTarget("https://example.com/hook", nil, staticResolver("93.184.216.34"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestValidateTarget_AllowedHostBypassesDeny(t *testing.T) {
	// 127.0.0.1 normally blocked; allow-list opts it back in.
	_, err := validateTarget("http://localhost:9000/hook",
		[]string{"localhost"},
		staticResolver("127.0.0.1"))
	if err != nil {
		t.Fatalf("unexpected err with allow-list: %v", err)
	}
}

func TestValidateTarget_DNSFailureRejected(t *testing.T) {
	failing := func(string) ([]net.IP, error) { return nil, errors.New("nxdomain") }
	_, err := validateTarget("http://nope.example/x", nil, failing)
	if err == nil {
		t.Fatalf("expected resolution error")
	}
}
