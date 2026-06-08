// Package natstest provides in-process NATS server helpers for integration tests.
package natstest

import (
	"fmt"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
)

// Server wraps a running in-process NATS server.
type Server struct {
	s   *natsserver.Server
	url string
}

// ClientURL returns the nats:// URL suitable for nats.Connect.
func (s *Server) ClientURL() string { return s.url }

// Shutdown stops the server immediately.
func (s *Server) Shutdown() { s.s.Shutdown() }

// New starts a single NATS server on a random port and registers a cleanup
// that shuts it down when t finishes. No auth, no system account.
func New(t *testing.T) *Server {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	return run(t, &opts)
}

// NewWithSysAccount starts a server identical to New but with the global
// account ($G) designated as the system account. Any connecting client can
// subscribe to $SYS.SERVER.*.STATSZ and issue $SYS.REQ.SERVER.PING.*
// requests — exactly what the dashboard's $SYS-based server collector needs.
// No additional credentials are required.
func NewWithSysAccount(t *testing.T) *Server {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.SystemAccount = "$G"
	return run(t, &opts)
}

// NewCluster starts n servers wired into a named cluster and returns them in
// order. All servers share the same cluster route mesh. n must be ≥ 1.
func NewCluster(t *testing.T, n int, clusterName string) []*Server {
	t.Helper()
	return newCluster(t, n, clusterName, false)
}

// NewClusterWithSysAccount is like NewCluster but with the system account
// enabled on all nodes (same $G pattern as NewWithSysAccount).
func NewClusterWithSysAccount(t *testing.T, n int, clusterName string) []*Server {
	t.Helper()
	return newCluster(t, n, clusterName, true)
}

func newCluster(t *testing.T, n int, clusterName string, sysAccount bool) []*Server {
	t.Helper()
	if n < 1 {
		t.Fatal("natstest: cluster size must be >= 1")
	}

	servers := make([]*Server, 0, n)
	var firstClusterPort int

	for i := range n {
		opts := natstest.DefaultTestOptions
		opts.Port = -1
		opts.ServerName = fmt.Sprintf("%s-%d", clusterName, i)
		opts.Cluster.Name = clusterName
		opts.Cluster.Host = "127.0.0.1"
		opts.Cluster.Port = -1
		if sysAccount {
			opts.SystemAccount = "$G"
		}

		if firstClusterPort != 0 {
			// Wire subsequent servers to route through the first one.
			opts.Routes = natsserver.RoutesFromStr(
				fmt.Sprintf("nats://127.0.0.1:%d", firstClusterPort),
			)
		}

		srv := run(t, &opts)
		servers = append(servers, srv)

		if firstClusterPort == 0 {
			if addr := srv.s.ClusterAddr(); addr != nil {
				firstClusterPort = addr.Port
			}
		}
	}

	waitClusterFormed(t, servers)
	return servers
}

// waitClusterFormed polls until every server in the slice has NumRoutes() ==
// len(servers)-1, or fails the test after a 5s deadline.
func waitClusterFormed(t *testing.T, servers []*Server) {
	t.Helper()
	expected := len(servers) - 1
	if expected == 0 {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		formed := true
		for _, s := range servers {
			if s.s.NumRoutes() < expected {
				formed = false
				break
			}
		}
		if formed {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("natstest: cluster did not form within 5s")
}

func run(t *testing.T, opts *natsserver.Options) *Server {
	t.Helper()
	ns := natstest.RunServer(opts)
	t.Cleanup(ns.Shutdown)
	return &Server{s: ns, url: ns.ClientURL()}
}
