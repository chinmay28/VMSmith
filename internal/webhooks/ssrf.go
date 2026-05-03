package webhooks

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// ErrSSRFBlocked is returned when a target URL resolves to a denied address
// (loopback, link-local, the cloud-metadata endpoint, or the VM NAT range).
var ErrSSRFBlocked = errors.New("webhook target blocked by SSRF policy")

// ErrInvalidScheme is returned when a target URL is not http or https.
var ErrInvalidScheme = errors.New("webhook URL must use http or https")

// defaultDenyCIDRs returns the always-blocked address ranges.  This list
// matches the contract in docs/ARCHITECTURE.md (loopback, link-local,
// cloud-metadata, VM NAT range).  AllowedHosts in DispatcherConfig can opt
// specific FQDNs back in for testing.
func defaultDenyCIDRs() []*net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",      // IPv4 loopback
		"::1/128",          // IPv6 loopback
		"169.254.0.0/16",   // IPv4 link-local incl. 169.254.169.254 metadata
		"fe80::/10",        // IPv6 link-local
		"192.168.100.0/24", // VMSmith default NAT subnet
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

// validateTarget parses rawURL, runs the scheme + SSRF checks, and resolves
// the host against the optional allow-list and the default deny-list.
//
// allowedHosts is a case-insensitive set of hostnames that bypass the SSRF
// check.  This is intended for local testing (e.g. "localhost") where the
// operator has explicitly opted in.
//
// resolveIPs is overridable in tests; production callers should pass
// net.LookupIP.
//
// The returned []net.IP is the set of resolved-and-verified addresses.  Callers
// should pin the subsequent HTTP connect to this set (see pinnedDialer) to
// close the DNS-rebinding window between validation and connect.  Returns nil
// for the IP slice when the host bypassed the check via allowedHosts; in that
// case the caller intentionally trusts whatever the system resolver returns.
func validateTarget(rawURL string, allowedHosts []string, resolveIPs func(string) ([]net.IP, error)) (*url.URL, []net.IP, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, ErrInvalidScheme
	}
	if u.Host == "" {
		return nil, nil, errors.New("webhook URL is missing host")
	}

	host := u.Hostname()
	for _, h := range allowedHosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return u, nil, nil
		}
	}

	if resolveIPs == nil {
		resolveIPs = net.LookupIP
	}
	ips, err := resolveIPs(host)
	if err != nil || len(ips) == 0 {
		return nil, nil, errors.New("webhook target host did not resolve")
	}

	deny := defaultDenyCIDRs()
	for _, ip := range ips {
		if ip.IsUnspecified() || ip.IsMulticast() {
			return nil, nil, ErrSSRFBlocked
		}
		for _, n := range deny {
			if n.Contains(ip) {
				return nil, nil, ErrSSRFBlocked
			}
		}
	}
	return u, ips, nil
}
