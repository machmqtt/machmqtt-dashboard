package collector

import (
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestMQTTBridgesReturnsRequestLocalSlice(t *testing.T) {
	c := &Collector{mqttBridges: []MQTTBridgeInstance{
		{IP: "10.0.0.2", ConfiguredName: "second"},
		{IP: "10.0.0.1", ConfiguredName: "first"},
	}}

	got := c.MQTTBridges()
	got[0].ConfiguredName = "changed"
	sort.Slice(got, func(i, j int) bool { return got[i].IP < got[j].IP })

	again := c.MQTTBridges()
	if again[0].ConfiguredName != "second" || again[0].IP != "10.0.0.2" {
		t.Fatalf("caller mutated collector-owned bridge state: %+v", again)
	}
}

func TestCollectorOperationalStatsAreCompleteAndDefensive(t *testing.T) {
	c := &Collector{
		snapshot:        &Snapshot{Timestamp: time.Now().Add(-time.Second)},
		endpointFailure: make(map[string]uint64),
	}
	c.polls.Store(3)
	c.partialPolls.Store(1)
	c.discoverySkips.Store(2)
	c.lastPollNS.Store(int64(time.Millisecond))
	c.lastSuccessUnix.Store(time.Now().Unix())
	c.mqttDiscovering.Store(true)
	c.recordEndpointFailure("varz")
	stats := c.operationalStats()
	if stats.Polls != 3 || stats.PartialPolls != 1 || stats.DiscoverySkips != 2 || !stats.Discovering || stats.SnapshotAgeNanos <= 0 || stats.EndpointFailures["varz"] != 1 {
		t.Fatalf("stats=%+v", stats)
	}
	stats.EndpointFailures["varz"] = 99
	if c.operationalStats().EndpointFailures["varz"] != 1 {
		t.Fatal("endpoint failure metrics leaked mutable state")
	}
	manager := &Manager{collectors: map[string]*Collector{"test": c}}
	if got := manager.OperationalStats()["test"]; got.Polls != 3 {
		t.Fatalf("manager stats=%+v", got)
	}
}

func TestMQTTBridgesConcurrentPublicationAndMutation(t *testing.T) {
	c := &Collector{}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if worker == 0 {
					c.mqttMu.Lock()
					c.mqttBridges = []MQTTBridgeInstance{{IP: fmt.Sprintf("10.0.0.%d", i%255)}}
					c.mqttMu.Unlock()
					continue
				}
				bridges := c.MQTTBridges()
				if len(bridges) > 0 {
					bridges[0].ConfiguredName = "request-local"
				}
			}
		}(worker)
	}
	wg.Wait()
}
