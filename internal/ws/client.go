package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 54 * time.Second
	maxMsgSize = 512
	sendBufLen = 64
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
		hub:  hub,
		conn: conn,
		send: make(chan *websocket.PreparedMessage, sendBufLen),
		log:  log,
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
		c.conn.Close()
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
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case pm, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
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
