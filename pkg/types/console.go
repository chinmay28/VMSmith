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
