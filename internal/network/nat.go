package network

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
			// A previous daemon run may have left an orphaned dnsmasq
			// holding 192.168.100.1:53. Kill it so Create() can start fresh.
			m.killStaleDnsmasq()
			return net.Create()
		}
		return nil
	}

	// Define and start the network
	xml := m.networkXML()
	net, err = m.conn.NetworkDefineXML(xml)
	if err != nil {
		return fmt.Errorf("defining network: %w", err)
	}
	defer net.Free()

	if err := net.SetAutostart(true); err != nil {
		return fmt.Errorf("setting autostart: %w", err)
	}

	m.killStaleDnsmasq()
	if err := net.Create(); err != nil {
		return fmt.Errorf("starting network: %w", err)
	}

	return nil
}

// killStaleDnsmasq terminates any orphaned dnsmasq left over from a previous
// daemon run. Libvirt writes a PID file when it starts dnsmasq; if the daemon
// was killed without a clean shutdown that process may still be running and
// holding the network address, preventing a fresh Create() from succeeding.
func (m *Manager) killStaleDnsmasq() {
	pidFile := fmt.Sprintf("/run/libvirt/network/%s.pid", m.cfg.Network.Name)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid := strings.TrimSpace(string(data))
	if pid != "" {
		exec.Command("kill", pid).Run() //nolint:errcheck
	}
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
