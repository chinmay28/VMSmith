package network

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// PortForwarder manages iptables-based NAT port forwarding rules.
type PortForwarder struct {
	store *store.Store
}

// NewPortForwarder creates a new port forwarder.
func NewPortForwarder(store *store.Store) *PortForwarder {
	return &PortForwarder{store: store}
}

// Add creates a new port forwarding rule: host_port -> guest_ip:guest_port.
func (pf *PortForwarder) Add(vmID string, hostPort, guestPort int, guestIP string, proto types.Protocol) (*types.PortForward, error) {
	if proto == "" {
		proto = types.ProtocolTCP
	}

	// Apply the iptables rule
	if err := pf.applyRule("add", hostPort, guestPort, guestIP, string(proto)); err != nil {
		return nil, err
	}

	rule := &types.PortForward{
		ID:        fmt.Sprintf("pf-%d", time.Now().UnixNano()),
		VMID:      vmID,
		HostPort:  hostPort,
		GuestPort: guestPort,
		GuestIP:   guestIP,
		Protocol:  proto,
	}

	if err := pf.store.PutPortForward(rule); err != nil {
		// Try to roll back the iptables rule
		pf.applyRule("remove", hostPort, guestPort, guestIP, string(proto))
		return nil, fmt.Errorf("persisting port forward: %w", err)
	}

	return rule, nil
}

// Remove deletes a port forwarding rule.
func (pf *PortForwarder) Remove(id string) error {
	forwards, err := pf.store.ListPortForwards("")
	if err != nil {
		return err
	}

	for _, fwd := range forwards {
		if fwd.ID == id {
			if err := pf.applyRule("remove", fwd.HostPort, fwd.GuestPort, fwd.GuestIP, string(fwd.Protocol)); err != nil {
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
		if err := pf.applyRule("add", fwd.HostPort, fwd.GuestPort, fwd.GuestIP, string(fwd.Protocol)); err != nil {
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

	// DNAT rule: redirect incoming traffic on hostPort to guest
	dnatCmd := exec.Command("iptables",
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
