package network

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// PortForwarder manages iptables-based NAT port forwarding rules.
type PortForwarder struct {
	store       *store.Store
	applyRuleFn func(action string, hostPort, guestPort int, guestIP, proto string) error
}

// NewPortForwarder creates a new port forwarder.
func NewPortForwarder(store *store.Store) *PortForwarder {
	pf := &PortForwarder{store: store}
	pf.applyRuleFn = pf.applyRule
	return pf
}

// SetApplyRuleFunc replaces the iptables-applying function. Intended for
// tests that need to exercise add/remove flows without invoking real
// iptables. Pass nil to restore the default real-iptables implementation.
func (pf *PortForwarder) SetApplyRuleFunc(fn func(action string, hostPort, guestPort int, guestIP, proto string) error) {
	if fn == nil {
		pf.applyRuleFn = pf.applyRule
		return
	}
	pf.applyRuleFn = fn
}

// AddOptions carries optional metadata for Add. Description is a free-form
// string capped at the API boundary (256 chars).
type AddOptions struct {
	Description string
}

// Add creates a new port forwarding rule: host_port -> guest_ip:guest_port.
func (pf *PortForwarder) Add(vmID string, hostPort, guestPort int, guestIP string, proto types.Protocol, opts AddOptions) (*types.PortForward, error) {
	if proto == "" {
		proto = types.ProtocolTCP
	}
	if err := types.ValidatePortForward(hostPort, guestPort, proto); err != nil {
		return nil, err
	}

	existing, err := pf.store.ListPortForwards("")
	if err != nil {
		return nil, fmt.Errorf("listing existing port forwards: %w", err)
	}
	for _, fwd := range existing {
		if fwd.HostPort == hostPort && fwd.Protocol == proto {
			return nil, types.NewAPIError("port_forward_conflict", fmt.Sprintf("host port %d/%s is already forwarded", hostPort, proto))
		}
	}

	// Apply the iptables rule
	if err := pf.applyRuleFn("add", hostPort, guestPort, guestIP, string(proto)); err != nil {
		return nil, err
	}

	rule := &types.PortForward{
		ID:          fmt.Sprintf("pf-%d", time.Now().UnixNano()),
		VMID:        vmID,
		HostPort:    hostPort,
		GuestPort:   guestPort,
		GuestIP:     guestIP,
		Protocol:    proto,
		Description: opts.Description,
	}

	if err := pf.store.PutPortForward(rule); err != nil {
		// Try to roll back the iptables rule
		pf.applyRuleFn("remove", hostPort, guestPort, guestIP, string(proto))
		return nil, fmt.Errorf("persisting port forward: %w", err)
	}

	return rule, nil
}

// UpdateOptions carries the editable metadata for an existing port-forward
// rule. Description is a pointer so callers can distinguish between "leave
// untouched" (nil) and "clear" (empty string).
type UpdateOptions struct {
	Description *string
}

// Update mutates the metadata of an existing port-forward rule. Today only
// the description is editable; the underlying iptables 5-tuple
// (host_port/guest_port/guest_ip/protocol) is immutable, since changing any
// of those would require deleting + recreating the iptables rule and
// rotating the rule's stable ID. Returns the updated record. If the id is
// not known, returns a typed `resource_not_found` error so callers can map
// it to HTTP 404 without scanning libvirt-specific text.
func (pf *PortForwarder) Update(id string, opts UpdateOptions) (*types.PortForward, error) {
	forwards, err := pf.store.ListPortForwards("")
	if err != nil {
		return nil, fmt.Errorf("listing port forwards: %w", err)
	}

	for _, fwd := range forwards {
		if fwd.ID != id {
			continue
		}
		if opts.Description != nil {
			fwd.Description = strings.TrimSpace(*opts.Description)
		}
		if err := pf.store.PutPortForward(fwd); err != nil {
			return nil, fmt.Errorf("persisting port forward: %w", err)
		}
		return fwd, nil
	}

	return nil, types.NewAPIError("resource_not_found", "port forward "+id+" not found")
}

// Remove deletes a port forwarding rule.
func (pf *PortForwarder) Remove(id string) error {
	forwards, err := pf.store.ListPortForwards("")
	if err != nil {
		return err
	}

	for _, fwd := range forwards {
		if fwd.ID == id {
			if err := pf.applyRuleFn("remove", fwd.HostPort, fwd.GuestPort, fwd.GuestIP, string(fwd.Protocol)); err != nil {
				return err
			}
			return pf.store.DeletePortForward(id)
		}
	}

	return fmt.Errorf("port forward %s not found", id)
}

// RestoreAll re-applies all stored port forwarding rules (used on daemon startup).
func (pf *PortForwarder) RestoreAll() error {
	forwards, err := pf.store.ListPortForwards("")
	if err != nil {
		return err
	}

	for _, fwd := range forwards {
		if err := pf.applyRuleFn("add", fwd.HostPort, fwd.GuestPort, fwd.GuestIP, string(fwd.Protocol)); err != nil {
			fmt.Printf("warning: failed to restore port forward %s: %v\n", fwd.ID, err)
		}
	}

	return nil
}

// List returns port forwards, optionally filtered by VM.
func (pf *PortForwarder) List(vmID string) ([]*types.PortForward, error) {
	return pf.store.ListPortForwards(vmID)
}

func (pf *PortForwarder) applyRule(action string, hostPort, guestPort int, guestIP, proto string) error {
	var iptAction string
	switch action {
	case "add":
		iptAction = "-A"
	case "remove":
		iptAction = "-D"
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	// DNAT rule: redirect incoming traffic on hostPort to guest.
	// -w 5: wait up to 5 s for the xtables lock instead of blocking forever
	// (libvirt holds the lock while managing its own NAT rules).
	dnatCmd := exec.Command("iptables",
		"-w", "5",
		"-t", "nat",
		iptAction, "PREROUTING",
		"-p", proto,
		"--dport", fmt.Sprintf("%d", hostPort),
		"-j", "DNAT",
		"--to-destination", fmt.Sprintf("%s:%d", guestIP, guestPort),
	)
	if out, err := dnatCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables DNAT: %s: %w", string(out), err)
	}

	// Allow forwarded traffic
	fwdCmd := exec.Command("iptables",
		"-w", "5",
		iptAction, "FORWARD",
		"-p", proto,
		"-d", guestIP,
		"--dport", fmt.Sprintf("%d", guestPort),
		"-j", "ACCEPT",
	)
	if out, err := fwdCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables FORWARD: %s: %w", string(out), err)
	}

	return nil
}
