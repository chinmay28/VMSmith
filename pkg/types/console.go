package types

import "time"

// ConsoleTicket is the wire format returned by
// POST /api/v1/vms/{id}/console/ticket. The ticket is a single-use, short-TTL
// token the caller passes to the (forthcoming) console websocket endpoint.
type ConsoleTicket struct {
	Ticket       string    `json:"ticket"`
	ExpiresAt    time.Time `json:"expires_at"`
	WebsocketURL string    `json:"websocket_url"`
}

// ConsoleIntent identifies which console flavour the caller wants when
// asking the VM manager for an endpoint.  Tickets in 5.1.2 carry this
// value too so a VNC ticket cannot be redirected to the serial console.
type ConsoleIntent string

const (
	// ConsoleIntentVNC selects the graphical VNC console exposed via
	// the libvirt domain's `<graphics type='vnc'>` element.
	ConsoleIntentVNC ConsoleIntent = "vnc"
	// ConsoleIntentSerial selects the text serial console exposed via
	// the libvirt domain's `<console type='pty'>` element.
	ConsoleIntentSerial ConsoleIntent = "serial"
)

// Valid reports whether the intent is a value the manager understands.
func (i ConsoleIntent) Valid() bool {
	switch i {
	case ConsoleIntentVNC, ConsoleIntentSerial:
		return true
	}
	return false
}

// ConsoleEndpoint describes how the daemon's console proxy can reach a
// VM's interactive console.  For VNC it carries a TCP host/port pair
// (typically loopback); for serial it carries the pty path libvirt has
// allocated.  Exactly one of (Host, Port) or Path is populated for any
// given intent.
type ConsoleEndpoint struct {
	Intent ConsoleIntent `json:"intent"`
	// Host is the VNC listen address (typically "127.0.0.1") for the
	// vnc intent.  Empty for serial.
	Host string `json:"host,omitempty"`
	// Port is the VNC TCP port for the vnc intent.  Zero for serial.
	Port int `json:"port,omitempty"`
	// Path is the pty path (e.g. "/dev/pts/3") for the serial intent.
	// Empty for vnc.
	Path string `json:"path,omitempty"`
}
