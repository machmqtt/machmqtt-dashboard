package ws

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
	log     *slog.Logger
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[*Client]bool),
		log:     log,
	}
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Broadcast serializes the message once, pre-frames it as a WebSocket
// PreparedMessage, and fans the single shared frame out to every client
// subscribed to env. This removes the per-client JSON re-encode cost that
// previously grew linearly with the number of connected viewers.
func (h *Hub) Broadcast(env string, msgType string, data any) {
	raw, err := json.Marshal(Message{Type: msgType, Env: env, Data: data})
	if err != nil {
		h.log.Error("ws broadcast marshal", "err", err)
		return
	}
	pm, err := websocket.NewPreparedMessage(websocket.TextMessage, raw)
	if err != nil {
		h.log.Error("ws broadcast prepare", "err", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if c.Env() == env {
			select {
			case c.send <- pm:
			default:
				// Drop message if client is slow.
			}
		}
	}
}
