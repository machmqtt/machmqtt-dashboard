package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

// fullCluster returns a cluster with every nullable field populated so all five
// marshal sites in marshalClusterFields are reached.
func fullCluster() *Cluster {
	return &Cluster{
		Name:          "full",
		Servers:       []config.Server{{URL: "http://localhost:8222"}},
		MQTTBridges:   []config.MQTTBridge{{Name: "b1"}},
		MQTTDiscovery: &config.MQTTDiscoveryConfig{},
		TLS:           &config.TLSConfig{},
		NATSConn:      &config.NATSConnConfig{},
	}
}

// failOnNthMarshal returns a json.Marshal stand-in that fails on the nth call
// and delegates to the real json.Marshal otherwise.
func failOnNthMarshal(n int) func(any) ([]byte, error) {
	calls := 0
	return func(v any) ([]byte, error) {
		calls++
		if calls == n {
			return nil, errors.New("marshal boom")
		}
		return json.Marshal(v)
	}
}

func swapMarshal(t *testing.T, fn func(any) ([]byte, error)) {
	t.Helper()
	orig := jsonMarshal
	jsonMarshal = fn
	t.Cleanup(func() { jsonMarshal = orig })
}

func TestMarshalClusterFieldsErrorBranches(t *testing.T) {
	cases := []struct {
		nth      int
		wantText string
	}{
		{1, "marshal servers"},
		{2, "marshal mqtt_bridges"},
		{3, "marshal mqtt_discovery"},
		{4, "marshal tls"},
		{5, "marshal nats_conn"},
	}
	for _, tc := range cases {
		t.Run(tc.wantText, func(t *testing.T) {
			swapMarshal(t, failOnNthMarshal(tc.nth))
			_, err := marshalClusterFields(fullCluster())
			if err == nil {
				t.Fatalf("expected error when marshal #%d fails", tc.nth)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantText)
			}
		})
	}
}

func TestCreateClusterPropagatesMarshalError(t *testing.T) {
	s := testStore(t)
	swapMarshal(t, failOnNthMarshal(1)) // fail marshalling servers
	err := s.CreateCluster(fullCluster())
	if err == nil || !strings.Contains(err.Error(), "marshal servers") {
		t.Fatalf("CreateCluster err = %v, want marshal servers error", err)
	}
}

func TestUpdateClusterPropagatesMarshalError(t *testing.T) {
	s := testStore(t)
	c := fullCluster()
	if err := s.CreateCluster(c); err != nil {
		t.Fatalf("seed CreateCluster: %v", err)
	}
	swapMarshal(t, failOnNthMarshal(1))
	if err := s.UpdateCluster(c); err == nil || !strings.Contains(err.Error(), "marshal servers") {
		t.Fatalf("UpdateCluster err = %v, want marshal servers error", err)
	}
}

func TestGenerateClusterIDEntropyError(t *testing.T) {
	// generateClusterID wraps a crypto/rand failure. The real crypto/rand cannot
	// return an error on this platform, so we drive the path through the randRead
	// seam.
	origRead := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	t.Cleanup(func() { randRead = origRead })

	if _, err := generateClusterID(); err == nil || !strings.Contains(err.Error(), "generate cluster id") {
		t.Fatalf("generateClusterID err = %v, want generate cluster id error", err)
	}

	// And that CreateCluster surfaces it.
	s := testStore(t)
	if err := s.CreateCluster(fullCluster()); err == nil || !strings.Contains(err.Error(), "generate cluster id") {
		t.Fatalf("CreateCluster err = %v, want generate cluster id error", err)
	}
}
