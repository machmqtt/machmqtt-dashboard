package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait         = 10 * time.Second
	pongWait          = 60 * time.Second
	defaultPingPeriod = 54 * time.Second
	maxMsgSize        = 512
	sendBufLen        = 64
	// maxConsecutiveDrops force-closes a client that has missed this many
	// broadcasts in a row without a single successful delivery (two full send
	// buffers behind). Such a client is genuinely stuck, not momentarily bursty;
	// closing it lets the browser's reconnect logic resync from a fresh snapshot
	// instead of showing permanently-frozen data.
	maxConsecutiveDrops = 2 * sendBufLen
)

type Message struct {
	Type string `json:"type"`
	Env  string `json:"env"`
	Data any    `json:"data"`
}

type subscribeMsg struct {
	Subscribe string `json:"subscribe"`
}

type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan *websocket.PreparedMessage
	mu   sync.RWMutex
	env  string
	log  *slog.Logger
	// pingPeriod is how often the write pump sends a keepalive ping. It is a
	// per-client field (defaulting to defaultPingPeriod) so tests can shorten it
	// before Run starts the write pump, avoiding a shared-global data race.
	pingPeriod       time.Duration
	dropped          atomic.Uint64 // cumulative drops (surfaced in admin health)
	consecutiveDrops atomic.Uint64 // resets on a successful delivery; triggers force-close
	done             chan struct{} // closed once on teardown to unblock the write pump
	closeOnce        sync.Once
}

// markDropped records an outbound message dropped because the client's send
// buffer was full (a slow/stalled viewer). It warns on the first drop so a
// persistently-backed-up client is visible without spamming a line per drop, and
// force-closes the client after a sustained run of drops so the browser can
// reconnect and resync.
func (c *Client) markDropped() {
	if c.dropped.Add(1) == 1 {
		c.log.Warn("ws dropping messages to slow client", "env", c.Env())
	}
	if c.consecutiveDrops.Add(1) >= maxConsecutiveDrops {
		c.log.Warn("ws force-closing stuck client after sustained drops; browser will reconnect", "env", c.Env())
		c.teardown()
	}
}

// noteDelivered resets the consecutive-drop streak after a successful send, so a
// client that is keeping up is never force-closed.
func (c *Client) noteDelivered() {
	c.consecutiveDrops.Store(0)
}

// teardown closes the client exactly once: it closes done (so the write pump
// exits immediately instead of lingering until its next ping) and closes the
// connection (so the read pump unblocks and unregisters). Safe to call from any
// goroutine and with a nil conn (unit tests).
func (c *Client) teardown() {
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			c.conn.Close()
		}
	})
}

func (c *Client) Env() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.env
}

func (c *Client) setEnv(env string) {
	c.mu.Lock()
	c.env = env
	c.mu.Unlock()
}

func NewClient(hub *Hub, conn *websocket.Conn, log *slog.Logger) *Client {
	return &Client{
		hub:        hub,
		conn:       conn,
		send:       make(chan *websocket.PreparedMessage, sendBufLen),
		log:        log,
		pingPeriod: defaultPingPeriod,
		done:       make(chan struct{}),
	}
}

func (c *Client) Run() {
	c.hub.Register(c)
	go c.writePump()
	c.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.teardown()
	}()

	c.conn.SetReadLimit(maxMsgSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.log.Warn("ws read error", "err", err)
			}
			return
		}

		var sub subscribeMsg
		if json.Unmarshal(msg, &sub) == nil && sub.Subscribe != "" {
			c.setEnv(sub.Subscribe)
			c.hub.notifySubscribe(c, sub.Subscribe)
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(c.pingPeriod)
	defer func() {
		ticker.Stop()
		c.teardown()
	}()

	for {
		select {
		case <-c.done:
			// Force-closed (stuck client) or the read pump exited — stop
			// immediately instead of lingering until the next ping.
			return
		case pm := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			// c.send is never closed (Unregister only removes the client from the
			// hub map), so a receive here is always a real message.
			if err := c.conn.WritePreparedMessage(pm); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
