package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/grishalr/courier-social/internal/oauth"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// NotifHub manages WebSocket connections per DID.
type NotifHub struct {
	mu       sync.RWMutex
	conns    map[string][]*websocket.Conn
	sessions *oauth.SessionStore
}

func NewNotifHub(sessions *oauth.SessionStore) *NotifHub {
	return &NotifHub{
		conns:    make(map[string][]*websocket.Conn),
		sessions: sessions,
	}
}

// Subscribe upgrades an HTTP request to a WebSocket for a given DID.
// Requires first-message auth: client must send {"token":"..."} within 10s.
func (h *NotifHub) Subscribe(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	if did == "" {
		http.Error(w, "did required", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade error: %v", err)
		return
	}

	// First-message auth: client must send token within 10 seconds
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "auth timeout"))
		conn.Close()
		log.Printf("ws: %s auth timeout", did)
		return
	}
	conn.SetReadDeadline(time.Time{}) // clear deadline

	var authMsg struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(msg, &authMsg); err != nil || authMsg.Token == "" {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid auth"))
		conn.Close()
		return
	}

	session, err := h.sessions.GetSession(r.Context(), authMsg.Token)
	if err != nil || session.DID != did {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "unauthorized"))
		conn.Close()
		log.Printf("ws: %s unauthorized (token DID mismatch)", did)
		return
	}

	h.mu.Lock()
	h.conns[did] = append(h.conns[did], conn)
	h.mu.Unlock()

	log.Printf("ws: %s connected (authenticated)", did)

	// Keep connection alive — read loop (handles pings/close)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}

	// Cleanup on disconnect
	h.mu.Lock()
	conns := h.conns[did]
	for i, c := range conns {
		if c == conn {
			h.conns[did] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	h.mu.Unlock()
	conn.Close()
	log.Printf("ws: %s disconnected", did)
}

// Broadcast sends a notification to all WebSocket connections for a DID.
func (h *NotifHub) Broadcast(did string, notif interface{}) {
	h.mu.RLock()
	conns := h.conns[did]
	h.mu.RUnlock()

	if len(conns) == 0 {
		return
	}

	data, err := json.Marshal(notif)
	if err != nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	alive := make([]*websocket.Conn, 0, len(conns))
	for _, conn := range conns {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			conn.Close()
			continue
		}
		alive = append(alive, conn)
	}
	h.conns[did] = alive
}
