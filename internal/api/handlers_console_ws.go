package api

import (
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/vmsmith/vmsmith/internal/console"
	"github.com/vmsmith/vmsmith/pkg/types"
)

const consoleWriteWait = 10 * time.Second

type activeConsoleSession struct {
	vmID string
	ws   *websocket.Conn
	once sync.Once
}

func (s *activeConsoleSession) close() {
	s.once.Do(func() {
		if s.ws != nil {
			_ = s.ws.Close()
		}
	})
}

var consoleUpgrader = websocket.Upgrader{
	Subprotocols: []string{"binary"},
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// ProxyConsole handles GET /api/v1/vms/{vmID}/console.
//
// It validates a single-use ticket, resolves the live VNC endpoint for the VM,
// upgrades the HTTP connection to a websocket using the `binary` subprotocol,
// and then proxies raw RFB bytes between the browser and the loopback VNC
// socket.
func (s *Server) ProxyConsole(w http.ResponseWriter, r *http.Request) {
	if s.consoleStore == nil {
		writeAPIError(w, http.StatusServiceUnavailable, types.NewAPIError("service_unavailable", "console subsystem is not enabled on this daemon"))
		return
	}
	if s.tlsEnabled && r.TLS == nil {
		writeAPIError(w, http.StatusForbidden, types.NewAPIError("mixed_content_blocked", "console websocket requires wss when TLS is enabled"))
		return
	}
	if s.consoleConfig.MaxConcurrentSessions > 0 && s.consoleSessionCount() >= s.consoleConfig.MaxConcurrentSessions {
		writeAPIError(w, http.StatusTooManyRequests, types.NewAPIError("console_session_limit_reached", "maximum concurrent console sessions reached"))
		return
	}

	vmID := chi.URLParam(r, "vmID")
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		writeAPIError(w, http.StatusUnauthorized, types.NewAPIError("unauthorized", "missing console ticket"))
		return
	}

	apiKey, err := s.consoleStore.ConsumeTicket(ticket, vmID)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, sanitizeConsoleTicketError(err))
		return
	}
	_ = apiKey

	endpoint, err := s.vmManager.GetConsoleEndpoint(r.Context(), vmID, types.ConsoleIntentVNC)
	if err != nil {
		status := http.StatusInternalServerError
		if apiErr, ok := sanitizeManagerError(err).(*types.APIError); ok {
			switch apiErr.Code {
			case "resource_not_found":
				status = http.StatusNotFound
			case "vm_not_running":
				status = http.StatusConflict
			case "console_unavailable":
				status = http.StatusServiceUnavailable
			}
		}
		writeAPIError(w, status, sanitizeManagerError(err))
		return
	}

	targetConn, err := net.DialTimeout("tcp", net.JoinHostPort(endpoint.Host, itoa(endpoint.Port)), consoleWriteWait)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, types.NewAPIError("console_unreachable", "failed to reach vm console"))
		return
	}

	wsConn, err := consoleUpgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = targetConn.Close()
		return
	}

	session := &activeConsoleSession{vmID: vmID, ws: wsConn}
	s.registerConsoleSession(session)
	defer s.unregisterConsoleSession(session)
	defer session.close()
	defer targetConn.Close()

	wsConn.SetReadLimit(1 << 20)

	errCh := make(chan error, 2)
	go func() { errCh <- proxyConsoleWebSocketToTCP(wsConn, targetConn, time.Duration(s.consoleConfig.IdleTimeoutSeconds)*time.Second) }()
	go func() { errCh <- proxyConsoleTCPToWebSocket(targetConn, wsConn, time.Duration(s.consoleConfig.IdleTimeoutSeconds)*time.Second) }()

	var maxSession <-chan time.Time
	if s.consoleConfig.MaxSessionSeconds > 0 {
		timer := time.NewTimer(time.Duration(s.consoleConfig.MaxSessionSeconds) * time.Second)
		defer timer.Stop()
		maxSession = timer.C
	}

	select {
	case <-r.Context().Done():
	case <-s.shutdownNotify:
	case <-maxSession:
	case <-errCh:
	}
}

func sanitizeConsoleTicketError(err error) error {
	switch {
	case errors.Is(err, console.ErrTicketExpired):
		return types.NewAPIError("unauthorized", "console ticket has expired")
	case errors.Is(err, console.ErrTicketVMMismatch), errors.Is(err, console.ErrTicketNotFound):
		return types.NewAPIError("unauthorized", "invalid console ticket")
	default:
		return types.NewAPIError("unauthorized", "invalid console ticket")
	}
}

func proxyConsoleWebSocketToTCP(wsConn *websocket.Conn, targetConn net.Conn, idleTimeout time.Duration) error {
	for {
		if idleTimeout > 0 {
			_ = wsConn.SetReadDeadline(time.Now().Add(idleTimeout))
		}
		msgType, payload, err := wsConn.ReadMessage()
		if err != nil {
			return err
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		if idleTimeout > 0 {
			_ = targetConn.SetWriteDeadline(time.Now().Add(idleTimeout))
		}
		if _, err := targetConn.Write(payload); err != nil {
			return err
		}
	}
}

func proxyConsoleTCPToWebSocket(targetConn net.Conn, wsConn *websocket.Conn, idleTimeout time.Duration) error {
	buf := make([]byte, 32*1024)
	for {
		if idleTimeout > 0 {
			_ = targetConn.SetReadDeadline(time.Now().Add(idleTimeout))
		}
		n, err := targetConn.Read(buf)
		if n > 0 {
			_ = wsConn.SetWriteDeadline(time.Now().Add(consoleWriteWait))
			if writeErr := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return err
			}
			if n == 0 {
				return err
			}
		}
	}
}

func (s *Server) registerConsoleSession(session *activeConsoleSession) {
	s.consoleSessionsMu.Lock()
	defer s.consoleSessionsMu.Unlock()
	if s.consoleSessions[session.vmID] == nil {
		s.consoleSessions[session.vmID] = make(map[*activeConsoleSession]struct{})
	}
	s.consoleSessions[session.vmID][session] = struct{}{}
}

func (s *Server) unregisterConsoleSession(session *activeConsoleSession) {
	s.consoleSessionsMu.Lock()
	defer s.consoleSessionsMu.Unlock()
	delete(s.consoleSessions[session.vmID], session)
	if len(s.consoleSessions[session.vmID]) == 0 {
		delete(s.consoleSessions, session.vmID)
	}
}

func (s *Server) consoleSessionCount() int {
	s.consoleSessionsMu.Lock()
	defer s.consoleSessionsMu.Unlock()
	count := 0
	for _, sessions := range s.consoleSessions {
		count += len(sessions)
	}
	return count
}
