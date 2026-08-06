package ws

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newPreparedMsg builds a trivial prepared message for filling send buffers.
func newPreparedMsg(t *testing.T) *websocket.PreparedMessage {
	t.Helper()
	pm, err := websocket.NewPreparedMessage(websocket.TextMessage, []byte(`x`))
	if err != nil {
		t.Fatal(err)
	}
	return pm
}

// assertOneQueued asserts exactly one prepared message is waiting on the
// client's send channel and drains it. PreparedMessage hides its payload, so
// payload assertions live in the live-connection tests instead.
func assertOneQueued(t *testing.T, c *Client) {
	t.Helper()
	if len(c.send) != 1 {
		t.Fatalf("queued = %d, want 1", len(c.send))
	}
	<-c.send
}

func TestHubRegisterUnregisterCount(t *testing.T) {
	h := NewHub(testLog())
	if h.ClientCount() != 0 {
		t.Fatalf("new hub count = %d, want 0", h.ClientCount())
	}
	c := NewClient(h, nil, testLog())
	h.Register(c)
	if h.ClientCount() != 1 {
		t.Fatalf("after register count = %d, want 1", h.ClientCount())
	}
	h.Unregister(c)
	if h.ClientCount() != 0 {
		t.Fatalf("after unregister count = %d, want 0", h.ClientCount())
	}
}

func TestHubBroadcastRoutesByEnv(t *testing.T) {
	h := NewHub(testLog())
	a := NewClient(h, nil, testLog())
	a.setEnv("alpha")
	b := NewClient(h, nil, testLog())
	b.setEnv("beta")
	h.Register(a)
	h.Register(b)

	h.Broadcast("alpha", "overview", map[string]int{"x": 1})

	if len(a.send) != 1 {
		t.Errorf("alpha client queue = %d, want 1", len(a.send))
	}
	if len(b.send) != 0 {
		t.Errorf("beta client queue = %d, want 0 (different env)", len(b.send))
	}
}

func TestHubBroadcastDropsWhenBufferFull(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())
	c.setEnv("env")
	h.Register(c)

	// Saturate the send buffer.
	for i := 0; i < sendBufLen; i++ {
		c.send <- newPreparedMsg(t)
	}
	h.Broadcast("env", "overview", map[string]int{"x": 1})

	if got := c.dropped.Load(); got != 1 {
		t.Errorf("dropped = %d, want 1", got)
	}
	if got := h.DroppedTotal(); got != 1 {
		t.Errorf("hub dropped total = %d, want 1", got)
	}
	if got := h.StaleClientCount(); got != 1 {
		t.Errorf("stale clients = %d, want 1", got)
	}
	h.recordSubscription()
	h.recordWriteFailure()
	stats := h.Stats()
	if stats.Connected != 1 || stats.Dropped != 1 || stats.Subscriptions != 1 || stats.WriteFailures != 1 || stats.SendQueueDepth != sendBufLen {
		t.Fatalf("unexpected hub stats: %+v", stats)
	}
}

func TestHubBroadcastMarshalErrorIsSafe(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())
	c.setEnv("env")
	h.Register(c)

	// A channel value can't be JSON-marshalled, so Broadcast must bail before
	// queueing anything and must not panic.
	h.Broadcast("env", "overview", make(chan int))

	if len(c.send) != 0 {
		t.Errorf("queue = %d, want 0 after marshal failure", len(c.send))
	}
}

func TestHubSendTo(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())
	h.Register(c)

	h.SendTo(c, "env", "health", map[string]string{"status": "ok"})
	assertOneQueued(t, c)
}

func TestHubSendToDropsWhenBufferFull(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())
	for i := 0; i < sendBufLen; i++ {
		c.send <- newPreparedMsg(t)
	}
	h.SendTo(c, "env", "health", map[string]string{"status": "ok"})
	if got := c.dropped.Load(); got != 1 {
		t.Errorf("dropped = %d, want 1", got)
	}
}

func TestHubSendToMarshalErrorIsSafe(t *testing.T) {
	h := NewHub(testLog())
	c := NewClient(h, nil, testLog())
	h.SendTo(c, "env", "health", make(chan int))
	if len(c.send) != 0 {
		t.Errorf("queue = %d, want 0 after marshal failure", len(c.send))
	}
}

func TestHubOnSubscribeHook(t *testing.T) {
	h := NewHub(testLog())

	// notifySubscribe with no hook registered must be a no-op (no panic).
	h.notifySubscribe(NewClient(h, nil, testLog()), "env")

	var gotEnv string
	var gotClient *Client
	c := NewClient(h, nil, testLog())
	h.SetOnSubscribe(func(cl *Client, env string) {
		gotClient = cl
		gotEnv = env
	})
	h.notifySubscribe(c, "myenv")

	if gotEnv != "myenv" {
		t.Errorf("hook env = %q, want myenv", gotEnv)
	}
	if gotClient != c {
		t.Error("hook received wrong client")
	}
}

func TestMarkDroppedCounts(t *testing.T) {
	c := NewClient(NewHub(testLog()), nil, testLog())
	c.markDropped()
	c.markDropped()
	if got := c.dropped.Load(); got != 2 {
		t.Errorf("dropped = %d, want 2", got)
	}
}

func TestClientEnvSetGet(t *testing.T) {
	c := NewClient(NewHub(testLog()), nil, testLog())
	if c.Env() != "" {
		t.Errorf("initial env = %q, want empty", c.Env())
	}
	c.setEnv("prod")
	if c.Env() != "prod" {
		t.Errorf("env = %q, want prod", c.Env())
	}
}

// --- Live-connection tests exercise Run / readPump / writePump end-to-end. ---

// wsTestServer spins up an httptest server that upgrades each connection and
// runs a ws.Client. The connected client is delivered on the returned channel.
func wsTestServer(t *testing.T, h *Hub) (*httptest.Server, chan *Client) {
	return wsTestServerCfg(t, h, nil)
}

// wsTestServerCfg is like wsTestServer but applies configure to each client
// before Run starts the pumps — used to shorten the ping period deterministically
// (configure runs before the writePump goroutine, so there's no data race).
func wsTestServerCfg(t *testing.T, h *Hub, configure func(*Client)) (*httptest.Server, chan *Client) {
	t.Helper()
	clientCh := make(chan *Client, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		c := NewClient(h, conn, testLog())
		if configure != nil {
			configure(c)
		}
		clientCh <- c
		c.Run()
	}))
	t.Cleanup(srv.Close)
	return srv, clientCh
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestClientSubscribeAndReceiveBroadcast(t *testing.T) {
	h := NewHub(testLog())
	subCh := make(chan string, 1)
	h.SetOnSubscribe(func(c *Client, env string) { subCh <- env })

	srv, _ := wsTestServer(t, h)
	conn := dial(t, srv)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"subscribe":"envX"}`)); err != nil {
		t.Fatal(err)
	}

	select {
	case env := <-subCh:
		if env != "envX" {
			t.Fatalf("subscribe env = %q, want envX", env)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onSubscribe hook never fired")
	}

	// Wait until the hub has the client registered with the right env.
	waitFor(t, func() bool { return h.ClientCount() == 1 })

	h.Broadcast("envX", "overview", map[string]any{"server_count": 3})

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read broadcast: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Type != "overview" || msg.Env != "envX" {
		t.Errorf("msg = %+v, want type=overview env=envX", msg)
	}
}

func TestClientIgnoresNonSubscribeMessages(t *testing.T) {
	h := NewHub(testLog())
	subCh := make(chan string, 4)
	h.SetOnSubscribe(func(c *Client, env string) { subCh <- env })

	srv, clientCh := wsTestServer(t, h)
	conn := dial(t, srv)
	c := <-clientCh

	// Invalid JSON and a JSON object without a "subscribe" key must both be
	// ignored without changing the client's env or firing the hook.
	conn.WriteMessage(websocket.TextMessage, []byte(`not json`))
	conn.WriteMessage(websocket.TextMessage, []byte(`{"other":"value"}`))
	conn.WriteMessage(websocket.TextMessage, []byte(`{"subscribe":""}`))

	// Give the read pump time to process; then confirm no subscribe fired.
	time.Sleep(100 * time.Millisecond)
	select {
	case env := <-subCh:
		t.Fatalf("unexpected subscribe fired for env %q", env)
	default:
	}
	if c.Env() != "" {
		t.Errorf("env = %q, want empty", c.Env())
	}
}

func TestClientCleanCloseUnregisters(t *testing.T) {
	h := NewHub(testLog())
	srv, _ := wsTestServer(t, h)
	conn := dial(t, srv)
	waitFor(t, func() bool { return h.ClientCount() == 1 })

	// Clean close handshake: readPump should return via the
	// CloseNormalClosure path and unregister the client.
	conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	conn.Close()

	waitFor(t, func() bool { return h.ClientCount() == 0 })
}

func TestClientAbruptCloseUnregisters(t *testing.T) {
	h := NewHub(testLog())
	srv, _ := wsTestServer(t, h)
	conn := dial(t, srv)
	waitFor(t, func() bool { return h.ClientCount() == 1 })

	// Drop the TCP connection without a close handshake. readPump sees an
	// unexpected close (1006) and still unregisters the client.
	conn.UnderlyingConn().Close()

	waitFor(t, func() bool { return h.ClientCount() == 0 })
}

func TestClientWritePumpSendsPing(t *testing.T) {
	h := NewHub(testLog())
	// Shorten this client's ping period so the write pump's ticker branch fires
	// quickly. Set before Run via the configure hook to avoid any data race.
	srv, _ := wsTestServerCfg(t, h, func(c *Client) {
		c.pingPeriod = 20 * time.Millisecond
	})
	conn := dial(t, srv)

	pinged := make(chan struct{}, 1)
	conn.SetPingHandler(func(appData string) error {
		select {
		case pinged <- struct{}{}:
		default:
		}
		// Echo a pong so the server-side client's pong handler runs (resetting its
		// read deadline). WriteControl is safe to call concurrently with reads.
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})

	// ReadMessage drives the ping handler.
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	select {
	case <-pinged:
	case <-time.After(2 * time.Second):
		t.Fatal("write pump never sent a ping")
	}
}

// upgradedServerConn returns the server side of a freshly-upgraded WebSocket
// connection (the client side is dialed and registered for cleanup). It does
// NOT run Client.Run, so a test can drive the pumps directly.
func upgradedServerConn(t *testing.T) *websocket.Conn {
	t.Helper()
	connCh := make(chan *websocket.Conn, 1)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connCh <- conn
	}))
	t.Cleanup(srv.Close)
	dial(t, srv)
	select {
	case conn := <-connCh:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("server never received an upgraded connection")
		return nil
	}
}

func runUntilExit(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("write pump did not exit")
	}
}

func TestClientWritePumpExitsOnSendWriteError(t *testing.T) {
	c := NewClient(NewHub(testLog()), upgradedServerConn(t), testLog())
	c.conn.Close() // subsequent writes fail
	c.send <- newPreparedMsg(t)
	// The send case writes the queued message, the write fails, writePump returns
	// and runs its deferred cleanup.
	runUntilExit(t, c.writePump)
}

func TestClientWritePumpExitsOnPingWriteError(t *testing.T) {
	c := NewClient(NewHub(testLog()), upgradedServerConn(t), testLog())
	c.pingPeriod = time.Millisecond
	c.conn.Close() // subsequent writes fail
	// With nothing queued, the ticker fires first; the ping write fails and
	// writePump returns.
	runUntilExit(t, c.writePump)
}

// waitFor polls cond up to 2s, failing the test if it never becomes true.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}
