// loadgen drives traffic for the demo topology so the dashboard has something
// lively to show. Two modes:
//
//	-mode mqtt  : per-edge MQTT 5 traffic against one MachMQTT broker. Persistent
//	              subscribers on test/# and publishers on test/<prefix>/<id>.
//	              Lights up that bridge's metrics + its edge server's msg rates.
//	-mode nats  : cross-edge core-NATS flow. Connects to every edge's client port,
//	              subscribes each to flow.>, and publishes flow.<i> from each edge.
//	              Messages route edge->hub->edge, lighting the leaf + route links
//	              in the topology graph (the "back and forth" flow).
//
// With -spiky the publish rate alternates between short high-rate bursts and
// longer quiet windows, so the rate charts show spikes instead of a flat line.
// Client IDs are prefixed (-prefix) so brokers never collide on duplicate IDs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/nats-io/nats.go"
)

func main() {
	mode := flag.String("mode", "mqtt", "mqtt | nats")
	urlStr := flag.String("url", "mqtt://127.0.0.1:1883", "mqtt mode: broker URL")
	urlsStr := flag.String("urls", "", "nats mode: comma-separated edge NATS URLs")
	prefix := flag.String("prefix", "e", "client-id / subject prefix (keeps brokers from colliding)")
	nSubs := flag.Int("subs", 2, "mqtt mode: persistent subscribers")
	nPubs := flag.Int("pubs", 3, "publishers (per edge in nats mode)")
	qos := flag.Int("qos", 1, "mqtt QoS (0,1,2)")
	low := flag.Float64("low", 4, "quiet-window rate (msgs/sec per publisher)")
	high := flag.Float64("high", 180, "burst-window rate (msgs/sec per publisher)")
	spiky := flag.Bool("spiky", true, "alternate bursts and quiet windows")
	dur := flag.Duration("duration", 0, "run duration (0 = until interrupted)")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; log.Println("signal received, stopping"); cancel() }()
	if *dur > 0 {
		go func() {
			select {
			case <-time.After(*dur):
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	switch *mode {
	case "mqtt":
		runMQTT(ctx, *urlStr, *prefix, *nSubs, *nPubs, *qos, *low, *high, *spiky)
	case "nats":
		runNATS(ctx, *urlsStr, *prefix, *nPubs, *low, *high, *spiky)
	default:
		log.Fatalf("unknown -mode %q (want mqtt|nats)", *mode)
	}
}

// pacer emits a tick at a rate that alternates between low and high when spiky.
// It re-randomizes burst/quiet windows so publishers don't march in lockstep.
func pacer(ctx context.Context, low, high float64, spiky bool, seed int64, fn func()) {
	rng := rand.New(rand.NewSource(seed))
	burst := false
	deadline := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		now := time.Now()
		if spiky && now.After(deadline) {
			burst = !burst
			if burst {
				deadline = now.Add(time.Duration(2500+rng.Intn(3500)) * time.Millisecond) // 2.5–6s burst
			} else {
				deadline = now.Add(time.Duration(4000+rng.Intn(6000)) * time.Millisecond) // 4–10s quiet
			}
		}
		rate := low
		if !spiky || burst {
			rate = high
		}
		if !spiky {
			rate = high
		}
		fn()
		if rate <= 0 {
			rate = 1
		}
		sleep := time.Duration(float64(time.Second) / rate)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
	}
}

func runMQTT(ctx context.Context, urlStr, prefix string, nSubs, nPubs, qos int, low, high float64, spiky bool) {
	u, err := url.Parse(urlStr)
	if err != nil {
		log.Fatalf("parse url: %v", err)
	}
	var received, sent atomic.Int64
	var cms []*autopaho.ConnectionManager
	var mu sync.Mutex
	add := func(cm *autopaho.ConnectionManager) { mu.Lock(); cms = append(cms, cm); mu.Unlock() }

	for i := 0; i < nSubs; i++ {
		i := i
		cfg := autopaho.ClientConfig{
			ServerUrls: []*url.URL{u}, KeepAlive: 20, CleanStartOnInitialConnection: true,
			OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
				_, _ = cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: "test/#", QoS: byte(qos)}}})
			},
			ClientConfig: paho.ClientConfig{
				ClientID: fmt.Sprintf("%s-sub-%d", prefix, i),
				OnPublishReceived: []func(paho.PublishReceived) (bool, error){
					func(paho.PublishReceived) (bool, error) { received.Add(1); return true, nil },
				},
			},
		}
		cm, err := autopaho.NewConnection(ctx, cfg)
		if err != nil {
			log.Fatalf("%s-sub-%d connect: %v", prefix, i, err)
		}
		if err := cm.AwaitConnection(ctx); err != nil {
			log.Fatalf("%s-sub-%d await: %v", prefix, i, err)
		}
		add(cm)
	}
	log.Printf("[%s] %d subscribers on test/# (QoS %d)", prefix, nSubs, qos)

	var wg sync.WaitGroup
	for i := 0; i < nPubs; i++ {
		i := i
		cfg := autopaho.ClientConfig{
			ServerUrls: []*url.URL{u}, KeepAlive: 20, CleanStartOnInitialConnection: true,
			ClientConfig: paho.ClientConfig{ClientID: fmt.Sprintf("%s-pub-%d", prefix, i)},
		}
		cm, err := autopaho.NewConnection(ctx, cfg)
		if err != nil {
			log.Fatalf("%s-pub-%d connect: %v", prefix, i, err)
		}
		if err := cm.AwaitConnection(ctx); err != nil {
			log.Fatalf("%s-pub-%d await: %v", prefix, i, err)
		}
		add(cm)
		wg.Add(1)
		go func() {
			defer wg.Done()
			topic := fmt.Sprintf("test/%s/%d", prefix, i)
			var n int64
			pacer(ctx, low, high, spiky, time.Now().UnixNano()+int64(i*7919), func() {
				n++
				if _, err := cm.Publish(ctx, &paho.Publish{Topic: topic, QoS: byte(qos), Payload: []byte(fmt.Sprintf("msg %d", n))}); err == nil {
					sent.Add(1)
				}
			})
		}()
	}
	log.Printf("[%s] %d publishers on test/%s/<id> (spiky=%v low=%.0f high=%.0f)", prefix, nPubs, prefix, spiky, low, high)

	statsLoop(ctx, prefix, &sent, &received)
	dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dcancel()
	for _, cm := range cms {
		_ = cm.Disconnect(dctx)
	}
	wg.Wait()
}

func runNATS(ctx context.Context, urlsStr, prefix string, nPubs int, low, high float64, spiky bool) {
	if urlsStr == "" {
		log.Fatal("nats mode requires -urls")
	}
	urls := strings.Split(urlsStr, ",")
	var received, sent atomic.Int64
	conns := make([]*nats.Conn, 0, len(urls))
	for i, raw := range urls {
		nc, err := nats.Connect(strings.TrimSpace(raw), nats.Name(fmt.Sprintf("%s-flow-%d", prefix, i)), nats.MaxReconnects(-1))
		if err != nil {
			log.Fatalf("nats connect %s: %v", raw, err)
		}
		// Every edge subscribes to the full flow.> space; cross-edge messages
		// route through the hub, lighting leaf + route links.
		if _, err := nc.Subscribe("flow.>", func(*nats.Msg) { received.Add(1) }); err != nil {
			log.Fatalf("subscribe flow.>: %v", err)
		}
		conns = append(conns, nc)
	}
	for _, nc := range conns {
		_ = nc.Flush()
	}
	log.Printf("[nats] %d edges subscribed to flow.> ; publishing cross-edge (spiky=%v)", len(conns), spiky)

	var wg sync.WaitGroup
	for ei, nc := range conns {
		for p := 0; p < nPubs; p++ {
			ei, p, nc := ei, p, nc
			wg.Add(1)
			go func() {
				defer wg.Done()
				subj := fmt.Sprintf("flow.edge%d.%d", ei, p)
				payload := []byte(strings.Repeat("x", 64))
				pacer(ctx, low, high, spiky, time.Now().UnixNano()+int64(ei*131+p*17), func() {
					if err := nc.Publish(subj, payload); err == nil {
						sent.Add(1)
					}
				})
			}()
		}
	}
	statsLoop(ctx, "nats", &sent, &received)
	for _, nc := range conns {
		_ = nc.Drain()
	}
	wg.Wait()
}

func statsLoop(ctx context.Context, tag string, sent, received *atomic.Int64) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] final: published=%d delivered=%d", tag, sent.Load(), received.Load())
			return
		case <-t.C:
			log.Printf("[%s] published=%d delivered=%d", tag, sent.Load(), received.Load())
		}
	}
}
