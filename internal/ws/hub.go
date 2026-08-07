package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

type Hub struct {
	mu            sync.RWMutex
	clients       map[*Client]bool
	log           *slog.Logger
	onSubscribe   func(c *Client, env string)
	dropped       atomic.Uint64
	subscribed    atomic.Uint64
	disconnects   atomic.Uint64
	writeFailures atomic.Uint64
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{
		clients: make(map[*Client]bool),
		log:     log,
	}
}

// SetOnSubscribe registers a hook called when a client subscribes to an env.
// Used to immediately push the current snapshot to the new subscriber.
func (h *Hub) SetOnSubscribe(fn func(c *Client, env string)) {
	h.mu.Lock()
	h.onSubscribe = fn
	h.mu.Unlock()
}

// notifySubscribe is called from readPump after a client changes its env.
func (h *Hub) notifySubscribe(c *Client, env string) {
	h.mu.RLock()
	fn := h.onSubscribe
	h.mu.RUnlock()
	if fn != nil {
		fn(c, env)
	}
}

// prepare serializes a message and pre-frames it as a WebSocket
// PreparedMessage. It returns nil (and logs) on marshal/prepare failure so both
// SendTo and Broadcast share a single encode path.
func (h *Hub) prepare(env, msgType string, data any) *websocket.PreparedMessage {
	raw, err := json.Marshal(Message{Type: msgType, Env: env, Data: data})
	if err != nil {
		h.log.Error("ws marshal", "err", err)
		return nil
	}
	pm, err := websocket.NewPreparedMessage(websocket.TextMessage, raw)
	if err != nil {
		h.log.Error("ws prepare", "err", err)
		return nil
	}
	return pm
}

// deliver queues a prepared message to a client, recording a drop if the
// client's send buffer is full.
func deliver(c *Client, pm *websocket.PreparedMessage) {
	select {
	case c.send <- pm:
		c.noteDelivered()
	default:
		c.markDropped()
	}
}

// SendTo serializes a message and delivers it to a single client.
func (h *Hub) SendTo(c *Client, env string, msgType string, data any) {
	pm := h.prepare(env, msgType, data)
	if pm == nil {
		return
	}
	deliver(c, pm)
}

// ClientCount returns the number of currently-connected WebSocket clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// DroppedTotal returns the cumulative number of outbound messages dropped to
// slow clients across all currently-connected clients (surfaced in admin health
// so a persistently-stale viewer is visible to operators, not just in a log line).
func (h *Hub) DroppedTotal() uint64 {
	return h.dropped.Load()
}

// StaleClientCount returns how many currently-connected clients have dropped at
// least one message (i.e. are or were backed up).
func (h *Hub) StaleClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for c := range h.clients {
		if c.dropped.Load() > 0 {
			n++
		}
	}
	return n
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	h.clients[c] = true
	n := len(h.clients)
	h.mu.Unlock()
	h.log.Info("ws client connected", "clients", n)
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	_, existed := h.clients[c]
	delete(h.clients, c)
	n := len(h.clients)
	h.mu.Unlock()
	if existed {
		h.disconnects.Add(1)
	}
	h.log.Info("ws client disconnected", "clients", n)
}

func (h *Hub) recordSubscription() { h.subscribed.Add(1) }
func (h *Hub) recordWriteFailure() { h.writeFailures.Add(1) }

// Broadcast serializes the message once, pre-frames it as a WebSocket
// PreparedMessage, and fans the single shared frame out to every client
// subscribed to env. This removes the per-client JSON re-encode cost that
// previously grew linearly with the number of connected viewers.
func (h *Hub) Broadcast(env string, msgType string, data any) {
	pm := h.prepare(env, msgType, data)
	if pm == nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if c.Env() == env {
			deliver(c, pm)
		}
	}
}

type Stats struct {
	Connected      int
	Dropped        uint64
	Subscriptions  uint64
	Disconnects    uint64
	WriteFailures  uint64
	SendQueueDepth int
}

func (h *Hub) Stats() Stats {
	h.mu.RLock()
	defer h.mu.RUnlock()
	depth := 0
	for client := range h.clients {
		depth += len(client.send)
	}
	return Stats{Connected: len(h.clients), Dropped: h.dropped.Load(), Subscriptions: h.subscribed.Load(), Disconnects: h.disconnects.Load(), WriteFailures: h.writeFailures.Load(), SendQueueDepth: depth}
}
