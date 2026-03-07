package network

import (
	"fmt"

	"github.com/vmsmith/vmsmith/internal/config"
	"libvirt.org/go/libvirt"
)

// Manager handles network creation and port forwarding.
type Manager struct {
	conn *libvirt.Connect
	cfg  *config.Config
}

// NewManager creates a new network manager.
func NewManager(conn *libvirt.Connect, cfg *config.Config) *Manager {
	return &Manager{conn: conn, cfg: cfg}
}

// EnsureNetwork creates the vmsmith NAT network if it doesn't exist.
func (m *Manager) EnsureNetwork() error {
	// Check if network already exists
	net, err := m.conn.LookupNetworkByName(m.cfg.Network.Name)
	if err == nil {
		defer net.Free()
		// Network exists; make sure it's active
		active, _ := net.IsActive()
		if !active {
			return net.Create()
		}
		return nil
	}

	// Create the network
	xml := m.networkXML()
	net, err = m.conn.NetworkDefineXML(xml)
	if err != nil {
		return fmt.Errorf("defining network: %w", err)
	}
	defer net.Free()

	if err := net.SetAutostart(true); err != nil {
		return fmt.Errorf("setting autostart: %w", err)
	}

	if err := net.Create(); err != nil {
		return fmt.Errorf("starting network: %w", err)
	}

	return nil
}

func (m *Manager) networkXML() string {
	return fmt.Sprintf(`<network>
  <name>%s</name>
  <forward mode='nat'>
    <nat>
      <port start='1024' end='65535'/>
    </nat>
  </forward>
  <bridge name='vmsmith0' stp='on' delay='0'/>
  <ip address='192.168.100.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='%s' end='%s'/>
    </dhcp>
  </ip>
</network>`, m.cfg.Network.Name, m.cfg.Network.DHCPStart, m.cfg.Network.DHCPEnd)
}
