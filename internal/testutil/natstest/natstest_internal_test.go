package natstest

import (
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstestpkg "github.com/nats-io/nats-server/v2/test"
)

// TestWaitClusterFormedRetriesUntilRouteUp exercises waitClusterFormed's
// polling retry path: it must observe an incomplete mesh on its first poll
// (formed=false → break → sleep) and keep polling until the route comes up.
//
// The black-box NewCluster path can never hit this branch because servers are
// started sequentially and loopback routes form in sub-millisecond time, so the
// very first poll always sees a complete mesh. Here we deliberately start the
// second node *after* a delay longer than one poll interval, guaranteeing the
// first poll sees zero routes and the retry/sleep branch runs before the route
// finally forms and waitClusterFormed returns.
func TestWaitClusterFormedRetriesUntilRouteUp(t *testing.T) {
	// Seed node: started immediately so it has a cluster listener to dial.
	seedOpts := natstestpkg.DefaultTestOptions
	seedOpts.Port = -1
	seedOpts.ServerName = "retry-0"
	seedOpts.Cluster.Name = "retry-cluster"
	seedOpts.Cluster.Host = "127.0.0.1"
	seedOpts.Cluster.Port = -1
	seed := run(t, &seedOpts)

	addr := seed.s.ClusterAddr()
	if addr == nil {
		t.Fatal("seed server has no cluster listener address")
	}

	// Second node: constructed but NOT started yet, routed at the seed. Until
	// Start() runs it reports 0 routes, so the first poll must fail.
	secondOpts := natstestpkg.DefaultTestOptions
	secondOpts.Port = -1
	secondOpts.ServerName = "retry-1"
	secondOpts.Cluster.Name = "retry-cluster"
	secondOpts.Cluster.Host = "127.0.0.1"
	secondOpts.Cluster.Port = -1
	secondOpts.Routes = natsserver.RoutesFromStr(
		fmt.Sprintf("nats://127.0.0.1:%d", addr.Port),
	)
	// RunServer disables route pooling/compression for the test package; mirror
	// that so the constructed node matches a run()-started peer.
	secondOpts.Cluster.PoolSize = -1
	secondOpts.NoLog = true

	ns, err := natsserver.NewServer(&secondOpts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(ns.Shutdown)
	second := &Server{s: ns}

	if got := second.s.NumRoutes(); got != 0 {
		t.Fatalf("unstarted second node NumRoutes = %d, want 0 (precondition for retry)", got)
	}

	// Bring the second node up after a delay longer than the 25ms poll interval
	// so waitClusterFormed is guaranteed to observe an unformed mesh first.
	go func() {
		time.Sleep(100 * time.Millisecond)
		ns.Start()
	}()

	start := time.Now()
	waitClusterFormed(t, []*Server{seed, second})
	elapsed := time.Since(start)

	// It must have waited for the delayed start (proving the retry loop ran),
	// not returned instantly.
	if elapsed < 75*time.Millisecond {
		t.Errorf("waitClusterFormed returned after %v, expected it to poll past the delayed start (retry path not exercised)", elapsed)
	}

	// Mesh is now formed: each node has a route to the other.
	if got := seed.s.NumRoutes(); got < 1 {
		t.Errorf("seed NumRoutes = %d, want >= 1 after formation", got)
	}
	if got := second.s.NumRoutes(); got < 1 {
		t.Errorf("second NumRoutes = %d, want >= 1 after formation", got)
	}
}
