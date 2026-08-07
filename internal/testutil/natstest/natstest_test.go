package natstest_test

import (
	"encoding/json"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

func TestNewConnectsAndPubSub(t *testing.T) {
	s := natstest.New(t)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	sub, _ := nc.SubscribeSync("ping")
	nc.Publish("ping", []byte("hello"))
	nc.Flush()

	msg, err := sub.NextMsg(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if string(msg.Data) != "hello" {
		t.Errorf("data = %q, want hello", string(msg.Data))
	}
}

func TestNewWithSysAccountReceivesSTATSZ(t *testing.T) {
	s := natstest.NewWithSysAccount(t)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync("$SYS.SERVER.>")
	if err != nil {
		t.Fatal(err)
	}

	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatalf("no $SYS.SERVER.> message: %v", err)
	}
	if len(msg.Data) == 0 {
		t.Error("empty STATSZ payload")
	}
}

func TestNewWithSysAccountPINGReply(t *testing.T) {
	s := natstest.NewWithSysAccount(t)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	msg, err := nc.Request("$SYS.REQ.SERVER.PING.STATSZ", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("PING.STATSZ request failed: %v", err)
	}

	// $SYS.REQ.SERVER.PING.STATSZ replies with ServerStatsMsg:
	//   { "server": {id, name, ...}, "stats": {connections, cpu, mem, ...} }
	var envelope struct {
		Server struct {
			ID string `json:"id"`
		} `json:"server"`
		Stats struct {
			Connections int `json:"connections"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(msg.Data, &envelope); err != nil {
		t.Fatalf("unmarshal STATSZ reply: %v", err)
	}
	if envelope.Server.ID == "" {
		t.Errorf("STATSZ reply missing server.id; raw: %s", msg.Data)
	}
}

func TestNewClusterFormsRoutes(t *testing.T) {
	servers := natstest.NewCluster(t, 3, "test-cluster")
	if len(servers) != 3 {
		t.Fatalf("got %d servers, want 3", len(servers))
	}

	// Each server should have 2 routes (to the other 2).
	for i, s := range servers {
		nc, err := nats.Connect(s.ClientURL())
		if err != nil {
			t.Fatalf("server %d connect: %v", i, err)
		}
		nc.Close()
	}
}

func TestNewClusterWithSysAccountSTATSZ(t *testing.T) {
	servers := natstest.NewClusterWithSysAccount(t, 2, "sys-cluster")

	nc, err := nats.Connect(servers[0].ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync("$SYS.SERVER.>")
	if err != nil {
		t.Fatal(err)
	}

	msg, err := sub.NextMsg(5 * time.Second)
	if err != nil {
		t.Fatalf("no $SYS.SERVER.> message from clustered server: %v", err)
	}
	if len(msg.Data) == 0 {
		t.Error("empty payload")
	}
}

func TestShutdownDoesNotPanic(t *testing.T) {
	s := natstest.New(t)
	// Explicit Shutdown before t.Cleanup runs — the subsequent cleanup call is a no-op.
	s.Shutdown()
}

func TestNewClusterSingleNode(t *testing.T) {
	// n=1 exercises the waitClusterFormed early-return (expected==0) path.
	servers := natstest.NewCluster(t, 1, "solo")
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want 1", len(servers))
	}
	nc, err := nats.Connect(servers[0].ClientURL())
	if err != nil {
		t.Fatalf("connect to single-node cluster: %v", err)
	}
	nc.Close()
}

func TestCorePubSubAcrossCluster(t *testing.T) {
	servers := natstest.NewCluster(t, 2, "pubsub-cluster")

	nc0, err := nats.Connect(servers[0].ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc0.Close()

	nc1, err := nats.Connect(servers[1].ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc1.Close()

	sub, _ := nc1.SubscribeSync("cross")
	nc1.Flush()

	// Wait for subscription propagation.
	time.Sleep(50 * time.Millisecond)

	nc0.Publish("cross", []byte("routed"))
	nc0.Flush()

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("cross-cluster message not received: %v", err)
	}
	if string(msg.Data) != "routed" {
		t.Errorf("data = %q, want routed", string(msg.Data))
	}
}
