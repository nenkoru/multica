package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// KioskResolver validates a kiosk token and returns its workspace ID.
type KioskResolver func(ctx context.Context, token string) (workspaceID string, err error)

// kioskRecheckInterval bounds how long a revoked kiosk token keeps its socket.
const kioskRecheckInterval = time.Minute

// kioskChangedFrame is the only event a kiosk connection receives. It tells
// the dashboard to refetch its aggregate and carries no workspace content.
var kioskChangedFrame = []byte(`{"type":"tv:changed"}`)

// kioskEventPrefixes are the event families that can change the TV aggregate.
var kioskEventPrefixes = []string{"issue:", "issue_labels:", "issue_status:", "task:", "epic:", "sprint:", "agent:", "label:"}

// kioskProjection maps an outbound message to what a kiosk client may see:
// the change signal for relevant events, pong for its own ping, else nil.
func kioskProjection(message []byte) []byte {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(message, &head); err != nil {
		return nil
	}
	if head.Type == "pong" {
		return message
	}
	// Streamed agent output is the bulk of task traffic and never changes
	// the aggregate.
	if head.Type == "task:message" || head.Type == "task:progress" {
		return nil
	}
	for _, prefix := range kioskEventPrefixes {
		if strings.HasPrefix(head.Type, prefix) {
			return kioskChangedFrame
		}
	}
	return nil
}

// HandleKioskWebSocket upgrades a TV dashboard connection authenticated by a
// kiosk token (cookie or bearer header). The connection joins its
// workspace's room but only ever receives kioskProjection output, and is
// closed within kioskRecheckInterval after the token is revoked.
func HandleKioskWebSocket(hub *Hub, token string, resolve KioskResolver, w http.ResponseWriter, r *http.Request) {
	if token == "" {
		http.Error(w, `{"error":"kiosk token required"}`, http.StatusUnauthorized)
		return
	}
	workspaceID, err := resolve(r.Context(), token)
	if err != nil || workspaceID == "" {
		http.Error(w, `{"error":"invalid kiosk token"}`, http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("kiosk websocket upgrade failed", "error", err)
		return
	}
	conn.SetReadLimit(inboundReadLimit)
	slog.Info("kiosk websocket connected", "workspace_id", workspaceID)

	client := &Client{
		hub:         hub,
		conn:        conn,
		send:        make(chan []byte, 256),
		workspaceID: workspaceID,
		kiosk:       true,
	}
	hub.register <- client

	go client.writePump()
	go client.readPump()
	go func() {
		ticker := time.NewTicker(kioskRecheckInterval)
		defer ticker.Stop()
		for range ticker.C {
			hub.mu.RLock()
			_, alive := hub.clients[client]
			hub.mu.RUnlock()
			if !alive {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			ws, err := resolve(ctx, token)
			cancel()
			if err == nil && ws == workspaceID {
				continue
			}
			// Only a definite rejection closes the socket; closing an
			// already-closed connection is a no-op.
			if err != nil && !IsKioskRejected(err) {
				continue
			}
			slog.Info("kiosk websocket closed: token no longer valid", "workspace_id", workspaceID)
			conn.Close()
			return
		}
	}()
}

// KioskRejectedError marks a resolver failure that means the token is not
// valid (as opposed to a transient lookup error).
type KioskRejectedError struct{}

func (KioskRejectedError) Error() string { return "kiosk token rejected" }

// IsKioskRejected reports whether err is a definite token rejection.
func IsKioskRejected(err error) bool {
	_, ok := err.(KioskRejectedError)
	return ok
}
