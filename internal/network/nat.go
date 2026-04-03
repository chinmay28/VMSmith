package network

import (
	"encoding/xml"
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

// Close releases the underlying libvirt connection.
func (m *Manager) Close() error {
	if m == nil || m.conn == nil {
		return nil
	}
	_, err := m.conn.Close()
	return err
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

// AddDHCPHost adds a static DHCP host entry so dnsmasq always assigns ip to
// mac. The update takes effect immediately on the live network and is
// persisted to the network definition, so it survives daemon restarts.
func (m *Manager) AddDHCPHost(mac, ip, name string) error {
	net, err := m.conn.LookupNetworkByName(m.cfg.Network.Name)
	if err != nil {
		return fmt.Errorf("looking up network: %w", err)
	}
	defer net.Free()

	hostXML := fmt.Sprintf(`<host mac='%s' name='%s' ip='%s'/>`, mac, name, ip)
	return net.Update(
		libvirt.NETWORK_UPDATE_COMMAND_ADD_LAST,
		libvirt.NETWORK_SECTION_IP_DHCP_HOST,
		-1, hostXML,
		libvirt.NETWORK_UPDATE_AFFECT_LIVE|libvirt.NETWORK_UPDATE_AFFECT_CONFIG,
	)
}

// RemoveDHCPHostByName removes the static DHCP host reservation whose name
// matches vmName.  This is used to clean up stale reservations left by a
// previously failed VM creation.  Errors and missing entries are silently
// ignored.
func (m *Manager) RemoveDHCPHostByName(vmName string) {
	net, err := m.conn.LookupNetworkByName(m.cfg.Network.Name)
	if err != nil {
		return
	}
	defer net.Free()

	xmlStr, err := net.GetXMLDesc(0)
	if err != nil {
		return
	}

	// Find a host entry whose name matches.
	type hostEntry struct {
		MAC  string `xml:"mac,attr"`
		Name string `xml:"name,attr"`
		IP   string `xml:"ip,attr"`
	}
	type dhcpBlock struct {
		Hosts []hostEntry `xml:"host"`
	}
	type ipElem struct {
		DHCP dhcpBlock `xml:"dhcp"`
	}
	type networkXML struct {
		IPs []ipElem `xml:"ip"`
	}

	var parsed networkXML
	if err := xml.Unmarshal([]byte(xmlStr), &parsed); err != nil {
		return
	}
	for _, ipEl := range parsed.IPs {
		for _, h := range ipEl.DHCP.Hosts {
			if h.Name == vmName && h.MAC != "" {
				hostXML := fmt.Sprintf(`<host mac='%s' name='%s' ip='%s'/>`, h.MAC, h.Name, h.IP)
				net.Update( //nolint:errcheck
					libvirt.NETWORK_UPDATE_COMMAND_DELETE,
					libvirt.NETWORK_SECTION_IP_DHCP_HOST,
					-1, hostXML,
					libvirt.NETWORK_UPDATE_AFFECT_LIVE|libvirt.NETWORK_UPDATE_AFFECT_CONFIG,
				)
			}
		}
	}
}

// RemoveDHCPHost removes the static DHCP host reservation for the given MAC.
// Errors are ignored — the entry may not exist.
func (m *Manager) RemoveDHCPHost(mac, ip string) {
	net, err := m.conn.LookupNetworkByName(m.cfg.Network.Name)
	if err != nil {
		return
	}
	defer net.Free()

	hostXML := fmt.Sprintf(`<host mac='%s' ip='%s'/>`, mac, ip)
	net.Update( //nolint:errcheck
		libvirt.NETWORK_UPDATE_COMMAND_DELETE,
		libvirt.NETWORK_SECTION_IP_DHCP_HOST,
		-1, hostXML,
		libvirt.NETWORK_UPDATE_AFFECT_LIVE|libvirt.NETWORK_UPDATE_AFFECT_CONFIG,
	)
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
