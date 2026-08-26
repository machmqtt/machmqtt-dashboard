package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	nats "github.com/nats-io/nats.go"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/testutil/natstest"
)

// These tests pin the dashboard's parity with the bridge's v1.2 metrics
// contract. testdata/machmqtt_v12_metrics.txt is the bridge's OWN Prometheus
// exposition: it was produced by handing a fully-populated metrics.Snapshot
// (every field distinct and non-zero, all family gates on, all three reason
// maps populated, non-uniform histogram buckets with observations above the
// last bound) to the broker's real renderers. Every family name, label key,
// label escaping and number format in it is therefore the broker's, not a
// hand-written approximation of it.

const v12Fixture = "testdata/machmqtt_v12_metrics.txt"

func parseV12Fixture(t *testing.T) *MQTTMetrics {
	t.Helper()
	body, err := os.ReadFile(v12Fixture)
	if err != nil {
		t.Fatal(err)
	}
	return parsePrometheusMetrics(string(body))
}

// TestParseV12FixtureScalarFields asserts every scalar field added for v1.2
// carries its own distinct value from the fixture, so a mapping wired to the
// wrong family or the wrong label cannot pass. The two umbrella counters are
// included because their values are derived, not free: the fixture's
// connections_rejected_total is the sum of the eight reasons the broker sums
// (mem_budget excluded), and AuthFailure is the sum of the eleven auth reasons.
func TestParseV12FixtureScalarFields(t *testing.T) {
	m := parseV12Fixture(t)

	tests := []struct {
		field string
		got   int64
		want  int64
	}{
		{"ConnectionsMax", m.ConnectionsMax, 1015},
		{"RejectedMemBudget", m.RejectedMemBudget, 1071},
		{"AuthFailureTrackerEntries", m.AuthFailureTrackerEntries, 1232},
		{"HookPanics", m.HookPanics, 1505},
		{"HookVetoes", m.HookVetoes, 1512},
		{"SysTreePublished", m.SysTreePublished, 1519},
		{"SysPublishBlocked", m.SysPublishBlocked, 1526},
		{"PublishRefusedTopic", m.PublishRefusedTopic, 1527},
		{"SharedConsumerRecreated", m.SharedConsumerRecreated, 1491},
		{"SubscribeConsumerFailures", m.SubscribeConsumerFailures, 1436},
		{"SubscribeConsumerRetries", m.SubscribeConsumerRetries, 1443},
		{"JetStreamAPIErrors", m.JetStreamAPIErrors, 1994},
		// Renamed from machmqtt_jetstream_api_total by the broker: the _total
		// suffix falsely implied counter semantics for a gauge that holds a
		// cumulative total. The old name is no longer emitted at all, so the old
		// field read a permanent zero against any current broker.
		{"JetStreamAPIRequests", m.JetStreamAPIRequests, 2001},
		{"JetStreamHealthProbeFailures", m.JetStreamHealthProbeFailures, 2008},
		{"StreamEnsureRetries", m.StreamEnsureRetries, 2015},
		{"StreamEnsureStalls", m.StreamEnsureStalls, 2022},
		{"NATSConnected", m.NATSConnected, 1},
		{"JetStreamDegraded", m.JetStreamDegraded, 1},
		{"ConsumersAwaitingReattach", m.ConsumersAwaitingReattach, 2029},
		{"ReattachSweepDurationMs", m.ReattachSweepDurationMs, 2036},
		{"ConsumerDeletedUnderConsume", m.ConsumerDeletedUnderConsume, 1498},
		{"WSProtocolViolations", m.WSProtocolViolations, 2142},
		{"QoS2SyncPersistFailed", m.QoS2SyncPersistFailed, 1582},
		{"WillVerifyFailures", m.WillVerifyFailures, 1694},
		{"SubscribeFlushFailures", m.SubscribeFlushFailures, 1701},
		{"InboundBytes", m.InboundBytes, 1743},
		{"WillPersistFailedWrite", m.WillPersistFailedWrite, 2114},
		{"WillPersistFailedQueueFull", m.WillPersistFailedQueueFull, 2121},
		{"RetainPersistFailedPut", m.RetainPersistFailedPut, 2128},
		{"RetainPersistFailedDelete", m.RetainPersistFailedDelete, 2135},
		{"SessionPersistPanics", m.SessionPersistPanics, 2002},
		{"CredentialExpiryDisconnects", m.CredentialExpiryDisconnects, 2009},
		{"MTLSIdentityFallbackLicense", m.MTLSIdentityFallbackLicense, 2016},
		{"MTLSIdentityFallbackNoMatch", m.MTLSIdentityFallbackNoMatch, 2023},
		{"MTLSIdentityFallbackNoCert", m.MTLSIdentityFallbackNoCert, 2030},
		{"OTelHistogramSkewClamped", m.OTelHistogramSkewClamped, 2037},
		{"OAuth2TokenCacheEvictions", m.OAuth2TokenCacheEvictions, 2058},
		{"ClusterLeaseRevisionRegressions", m.ClusterLeaseRevisionRegressions, 1890},
		{"ClusterHeartbeatPublishFailures", m.ClusterHeartbeatPublishFailures, 2149},
		{"QoS2SyncPersistDurationCount", m.QoS2SyncPersistDurationCount, 1421},
		// The umbrella deliberately excludes mem_budget, so it must NOT equal
		// the sum of all nine reasons (8757 + 1071).
		{"ConnectionsRejected", m.ConnectionsRejected, 8757},
		{"AuthFailure", m.AuthFailure, 13013},
		// machmqtt #160: PUBLISH-rejected and op-queue-dropped counters. Both
		// broker families emit ONLY labeled series (no umbrella line on the
		// wire), so PublishRejectedState and OpQueueDropped are sums the
		// parser computes client-side — same shape as AuthFailure above.
		{"PublishRejectedStateConnecting", m.PublishRejectedStateConnecting, 9211},
		{"PublishRejectedStateAuthenticating", m.PublishRejectedStateAuthenticating, 9212},
		{"PublishRejectedStateDisconnecting", m.PublishRejectedStateDisconnecting, 9213},
		{"PublishRejectedStateClosed", m.PublishRejectedStateClosed, 9214},
		{"PublishRejectedStateOther", m.PublishRejectedStateOther, 9215},
		{"PublishRejectedState", m.PublishRejectedState, 9211 + 9212 + 9213 + 9214 + 9215},
		{"PublishRejectedQoS0", m.PublishRejectedQoS0, 9221},
		{"PublishRejectedQoS1", m.PublishRejectedQoS1, 9222},
		{"PublishRejectedQoS2", m.PublishRejectedQoS2, 9223},
		{"PublishRejectedQoS3", m.PublishRejectedQoS3, 9224},
		{"OpQueueDroppedCloseRace", m.OpQueueDroppedCloseRace, 9231},
		{"OpQueueDroppedPoolFull", m.OpQueueDroppedPoolFull, 9232},
		{"OpQueueDroppedHandlerError", m.OpQueueDroppedHandlerError, 9233},
		{"OpQueueDroppedSlotClosed", m.OpQueueDroppedSlotClosed, 9234},
		{"OpQueueDroppedOther", m.OpQueueDroppedOther, 9235},
		{"OpQueueDroppedWorkerAbort", m.OpQueueDroppedWorkerAbort, 30011},
		{"OpQueueDropped", m.OpQueueDropped, 9231 + 9232 + 9233 + 9234 + 9235 + 30011},
		// #164: the eight v1.2 fields the parity gate flagged, plus the
		// max_connections denominator (machmqtt#192). jetstream_available is a
		// LIVE gauge (state), jetstream_transitions its counter counterpart.
		{"DrainAckUnwritable", m.DrainAckUnwritable, 30012},
		{"JetStreamAvailable", m.JetStreamAvailable, 1},
		{"JetStreamTransitions", m.JetStreamTransitions, 30013},
		{"WillStaleClearAttempted", m.WillStaleClearAttempted, 30014},
		{"WillStaleClearSkipped", m.WillStaleClearSkipped, 30015},
		{"MsgsRedeliverySuppressed", m.MsgsRedeliverySuppressed, 30016},
		{"RetainedDeliveryTruncated", m.RetainedDeliveryTruncated, 30017},
		{"MaxConnections", m.MaxConnections, 30018},
		// The remainder of the cross-repo parity gap: byte, op-queue shedding,
		// dispatch-batching, process-descriptor, Go-allocation, QoS 2 purge and
		// session-signing families. Each carries its own distinct fixture value,
		// so a case wired to the wrong family or the wrong label cannot pass.
		{"BytesSent", m.BytesSent, 20029},
		{"OpShedQoS0PerConnBytes", m.OpShedQoS0PerConnBytes, 20036},
		{"OpShedQoS0TotalBytes", m.OpShedQoS0TotalBytes, 20043},
		{"OpShedQoS0Depth", m.OpShedQoS0Depth, 20050},
		{"OpDispatchBatches", m.OpDispatchBatches, 20057},
		{"OpDispatchMessages", m.OpDispatchMessages, 20064},
		{"OpSuspendEvents", m.OpSuspendEvents, 20071},
		{"ProcessOpenFDs", m.ProcessOpenFDs, 20078},
		{"ProcessMaxFDs", m.ProcessMaxFDs, 20085},
		{"GoAllocBytesTotal", m.GoAllocBytesTotal, 20092},
		{"SessionQoS2PurgeFailuresSyncConnect", m.SessionQoS2PurgeFailuresSyncConnect, 20141},
		{"SessionQoS2PurgeFailuresAsyncDeath", m.SessionQoS2PurgeFailuresAsyncDeath, 20148},
		{"SessionVerifyFailures", m.SessionVerifyFailures, 20155},
		{"SessionUnsignedAccepted", m.SessionUnsignedAccepted, 20162},
		{"SessionSigningKeyPresent", m.SessionSigningKeyPresent, 20169},
		{"SessionSigningRequired", m.SessionSigningRequired, 20176},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.field, tc.got, tc.want)
		}
	}

	if m.QoS2SyncPersistDurationSumSeconds != 0.5 {
		t.Errorf("QoS2SyncPersistDurationSumSeconds = %v, want 0.5", m.QoS2SyncPersistDurationSumSeconds)
	}
	// The broker renders histogram sums with %g, so a large sum arrives in
	// exponent form ("1.2345675e+06") — proof the value parser accepts it.
	if m.JSPublishDurationSumSeconds != 1234567.5 {
		t.Errorf("JSPublishDurationSumSeconds = %v, want 1234567.5", m.JSPublishDurationSumSeconds)
	}
}

// TestParseV12FixtureReasonMaps covers the third hex-coded reason family added
// in v1.2 alongside the two the dashboard already read.
func TestParseV12FixtureReasonMaps(t *testing.T) {
	m := parseV12Fixture(t)

	tests := []struct {
		field string
		got   map[string]int64
		want  map[string]int64
	}{
		{"ConnackRejectedByReason", m.ConnackRejectedByReason, map[string]int64{"0x81": 31, "0x88": 32, "0x8C": 33}},
		{"SubackRejectedByReason", m.SubackRejectedByReason, map[string]int64{"0x87": 41, "0x8F": 42, "0xA2": 43}},
		{"DisconnectsSentByReason", m.DisconnectsSentByReason, map[string]int64{"0x82": 51, "0x8E": 52, "0x93": 53}},
	}
	for _, tc := range tests {
		if !reflect.DeepEqual(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.field, tc.got, tc.want)
		}
	}
}

// TestParseV12FixtureSubObjects asserts the license, reactor and pool groups
// are reconstructed field-for-field from the exposition, including the
// per-slot series and the escaped cluster-HMAC source IDs.
func TestParseV12FixtureSubObjects(t *testing.T) {
	m := parseV12Fixture(t)

	if m.License == nil {
		t.Fatal("License is nil, want the whole license group parsed")
	}
	licTests := []struct {
		field string
		got   int64
		want  int64
	}{
		{"Valid", m.License.Valid, 1},
		// Status 4 is the highest code the broker defines; it must survive as a
		// number rather than being clamped to the older 0..3 range.
		{"Status", m.License.Status, 4},
		{"ExpiresTimestamp", m.License.ExpiresTimestamp, 1893456000},
		{"ConnectionsLimit", m.License.ConnectionsLimit, 5000},
		{"ConnectionsGlobal", m.License.ConnectionsGlobal, 4321},
		{"MaxQoS", m.License.MaxQoS, 2},
		{"Instances", m.License.Instances, 3},
		{"Degraded", m.License.Degraded, 1},
		{"BlockConfirmed", m.License.BlockConfirmed, 1},
		{"CapacityClamped", m.License.CapacityClamped, 1},
		{"ClampFloor", m.License.ClampFloor, 250},
		{"PeerDiscrepancy", m.License.PeerDiscrepancy, 1},
		{"PermissionViolations", m.License.PermissionViolations, 77},
		{"HeartbeatPublishFailures", m.License.HeartbeatPublishFailures, 88},
	}
	for _, tc := range licTests {
		if tc.got != tc.want {
			t.Errorf("License.%s = %d, want %d", tc.field, tc.got, tc.want)
		}
	}
	if !m.License.HasExpiry {
		t.Error("License.HasExpiry = false, want true (the expires_timestamp family was rendered)")
	}

	if m.Reactor == nil {
		t.Fatal("Reactor is nil, want the whole reactor group parsed")
	}
	reactorTests := []struct {
		field string
		got   int64
		want  int64
	}{
		{"TaskQueueDepth", m.Reactor.TaskQueueDepth, 910},
		{"TaskQueueDepthMax", m.Reactor.TaskQueueDepthMax, 911},
		{"LoopPanics", m.Reactor.LoopPanics, 912},
		{"ReadContinuations", m.Reactor.ReadContinuations, 913},
		{"WriteBackpressure", m.Reactor.WriteBackpressure, 914},
		{"FeedWriteOverflows", m.Reactor.FeedWriteOverflows, 915},
		{"FeedReadOverflows", m.Reactor.FeedReadOverflows, 917},
		{"LoopDeaths", m.Reactor.LoopDeaths, 916},
		{"RegisteredSlots", m.Reactor.RegisteredSlots, 20183},
		{"StaleEvents", m.Reactor.StaleEvents, 20190},
	}
	for _, tc := range reactorTests {
		if tc.got != tc.want {
			t.Errorf("Reactor.%s = %d, want %d", tc.field, tc.got, tc.want)
		}
	}

	if m.Pool == nil {
		t.Fatal("Pool is nil, want the whole pool group parsed")
	}
	poolTests := []struct {
		field string
		got   int64
		want  int64
	}{
		{"Size", m.Pool.Size, 4},
		{"BufferedBytes", m.Pool.BufferedBytes, 920},
		{"BufferedBytesMax", m.Pool.BufferedBytesMax, 921},
	}
	for _, tc := range poolTests {
		if tc.got != tc.want {
			t.Errorf("Pool.%s = %d, want %d", tc.field, tc.got, tc.want)
		}
	}
	// Slot 2 is absent from the fixture: the slot= label is the identity, so a
	// gap must not shift the remaining slots' values.
	wantSlots := []MQTTMetricsPoolSlot{
		{Slot: 0, BufferedBytes: 930, OutMsgs: 931, InMsgs: 932},
		{Slot: 1, BufferedBytes: 940, OutMsgs: 941, InMsgs: 942},
		{Slot: 3, BufferedBytes: 950, OutMsgs: 951, InMsgs: 952},
	}
	if !reflect.DeepEqual(m.Pool.Slots, wantSlots) {
		t.Errorf("Pool.Slots = %+v, want %+v", m.Pool.Slots, wantSlots)
	}

	// The broker escapes source instance IDs with the exposition format's own
	// rules, so these came off the wire as peer-b\\, peer-c\"q\\z and
	// peer-d\ntail and must be unescaped back to their original bytes.
	wantHMAC := []MQTTHMACFailure{
		{SourceInstanceID: `peer-a`, Count: 61},
		{SourceInstanceID: `peer-b\`, Count: 62},
		{SourceInstanceID: `peer-c"q\z`, Count: 63},
		{SourceInstanceID: "peer-d\ntail", Count: 64},
	}
	if !reflect.DeepEqual(m.ClusterHMACFailures, wantHMAC) {
		t.Errorf("ClusterHMACFailures = %+v, want %+v", m.ClusterHMACFailures, wantHMAC)
	}
}

// TestParseV12FixtureFamilyGates asserts the four booleans the broker uses to
// gate whole families are recovered from the poll path, where no metric line
// for the gate itself exists.
func TestParseV12FixtureFamilyGates(t *testing.T) {
	m := parseV12Fixture(t)

	gates := []struct {
		field string
		got   bool
	}{
		{"AuthWebhookActive", m.AuthWebhookActive},
		{"ClusterEnabled", m.ClusterEnabled},
		{"BridgeUp", m.BridgeUp},
		{"SessionsUp", m.SessionsUp},
	}
	for _, g := range gates {
		if !g.got {
			t.Errorf("%s = false, want true (its gated families are in the fixture)", g.field)
		}
	}
}

// TestParseSparseBodyLeavesGroupsAbsent is the negative half of the gating
// rule: a broker with no license manager, no reactor and no pool renders none
// of those families, and the dashboard must report them absent rather than
// present-and-zero — the same distinction the pushed payload makes by omitting
// the objects.
func TestParseSparseBodyLeavesGroupsAbsent(t *testing.T) {
	m := parsePrometheusMetrics("machmqtt_connections_active 3\n")

	if m.License != nil {
		t.Errorf("License = %+v, want nil", m.License)
	}
	if m.Reactor != nil {
		t.Errorf("Reactor = %+v, want nil", m.Reactor)
	}
	if m.Pool != nil {
		t.Errorf("Pool = %+v, want nil", m.Pool)
	}
	if m.ClusterHMACFailures != nil {
		t.Errorf("ClusterHMACFailures = %+v, want nil", m.ClusterHMACFailures)
	}
	for _, g := range []struct {
		field string
		got   bool
	}{
		{"AuthWebhookActive", m.AuthWebhookActive},
		{"ClusterEnabled", m.ClusterEnabled},
		{"BridgeUp", m.BridgeUp},
		{"SessionsUp", m.SessionsUp},
	} {
		if g.got {
			t.Errorf("%s = true, want false (no gated family was rendered)", g.field)
		}
	}

	// A license with no expiry: the broker omits expires_timestamp entirely, so
	// HasExpiry must stay false and the timestamp must not be invented.
	m = parsePrometheusMetrics("machmqtt_license_valid 1\nmachmqtt_license_status 1\n")
	if m.License == nil {
		t.Fatal("License is nil, want non-nil once a license family appeared")
	}
	if m.License.HasExpiry || m.License.ExpiresTimestamp != 0 {
		t.Errorf("License.HasExpiry/ExpiresTimestamp = %v/%d, want false/0",
			m.License.HasExpiry, m.License.ExpiresTimestamp)
	}
}

// TestParseV12FixtureHistogramBucketsRoundTrip is the bucket-semantics proof:
// the broker stores RAW per-bucket counts and renders them CUMULATIVELY, so
// parse(render(snapshot)) must return the raw arrays the snapshot held. The
// fixture's histograms all have observations above the last bound (count >
// sum(buckets)), which is exactly the case a parser that back-fills the last
// bucket from the +Inf series would corrupt.
func TestParseV12FixtureHistogramBucketsRoundTrip(t *testing.T) {
	m := parseV12Fixture(t)

	tests := []struct {
		family string
		got    [MQTTHistogramBucketCount]int64
		count  int64
		want   [MQTTHistogramBucketCount]int64
	}{
		{"publish_latency", m.PublishLatencyBuckets, m.PublishLatencyCount,
			[9]int64{11, 12, 15, 0, 27, 36, 47, 60, 75}},
		{"auth_duration", m.AuthDurationBuckets, m.AuthDurationCount,
			[9]int64{22, 24, 30, 0, 54, 72, 94, 120, 150}},
		{"auth_webhook_duration", m.AuthWebhookDurationBuckets, m.AuthWebhookDurationCount,
			[9]int64{33, 36, 45, 0, 81, 108, 141, 180, 225}},
		{"jetstream_publish_duration", m.JSPublishDurationBuckets, m.JSPublishDurationCount,
			[9]int64{44, 48, 60, 0, 108, 144, 188, 240, 300}},
		{"qos2_sync_persist_duration", m.QoS2SyncPersistDurationBuckets, m.QoS2SyncPersistDurationCount,
			[9]int64{55, 60, 75, 0, 135, 180, 235, 300, 375}},
		{"subscribe_duration", m.SubscribeDurationBuckets, m.SubscribeDurationCount,
			[9]int64{66, 72, 90, 0, 162, 216, 282, 360, 450}},
		{"dispatch_wait", m.DispatchWaitBuckets, m.DispatchWaitCount,
			[9]int64{77, 84, 105, 0, 189, 252, 329, 420, 525}},
		{"tls_handshake_duration", m.TLSHandshakeDurationBuckets, m.TLSHandshakeDurationCount,
			[9]int64{88, 96, 120, 0, 216, 288, 376, 480, 600}},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s buckets = %v, want %v (raw, not cumulative)", tc.family, tc.got, tc.want)
		}
		var sum int64
		for _, v := range tc.got {
			sum += v
		}
		if sum >= tc.count {
			t.Errorf("%s: sum(buckets)=%d count=%d — the fixture's above-last-bound "+
				"observations were folded into a bucket", tc.family, sum, tc.count)
		}
	}

	// The adversarial shape the fixture cannot show: every explicit bucket
	// empty while the +Inf total is large. Back-filling the last bucket from
	// +Inf would report 900 observations at or below 5s that never happened.
	body := renderHistogram("machmqtt_publish_latency_seconds", [9]int64{}, 900, 4.5)
	empty := parsePrometheusMetrics(body)
	if empty.PublishLatencyBuckets != ([9]int64{}) {
		t.Errorf("all-overflow histogram buckets = %v, want all zero", empty.PublishLatencyBuckets)
	}
	if empty.PublishLatencyCount != 900 {
		t.Errorf("all-overflow histogram count = %d, want 900", empty.PublishLatencyCount)
	}
}

// TestV12FixtureHistogramBoundsMatchBroker pins MQTTHistogramBounds — the
// dashboard's copy of the broker's bucket bounds — against the le= labels the
// broker actually rendered. Without this, a broker that changed its bounds (or
// added a tenth bucket) would leave every bucket array silently zero: the
// le= values would simply stop matching and unknown series are ignored by
// design. Every histogram family must carry the same bounds in the same order,
// then +Inf.
func TestV12FixtureHistogramBoundsMatchBroker(t *testing.T) {
	body, err := os.ReadFile(v12Fixture)
	if err != nil {
		t.Fatal(err)
	}

	want := make([]string, 0, MQTTHistogramBucketCount+1)
	for _, bound := range MQTTHistogramBounds {
		want = append(want, fmt.Sprintf("%g", bound))
	}
	want = append(want, "+Inf")

	got := map[string][]string{}
	for _, line := range strings.Split(string(body), "\n") {
		name, _ := parseMetricLine(strings.TrimSpace(line))
		if !strings.HasSuffix(name, "_bucket") {
			continue
		}
		family := strings.TrimSuffix(name, "_bucket")
		got[family] = append(got[family], extractLabel(line, "le"))
	}

	if len(got) != len(histogramFamilies) {
		t.Errorf("fixture has %d histogram families, dashboard tracks %d: %v vs %v",
			len(got), len(histogramFamilies), got, histogramFamilies)
	}
	for _, family := range histogramFamilies {
		if !reflect.DeepEqual(got[family], want) {
			t.Errorf("%s le= labels = %v, want %v (MQTTHistogramBounds no longer "+
				"matches the broker's bounds)", family, got[family], want)
		}
	}
}

// renderHistogram reproduces the broker's histogram exposition for one family:
// cumulative bucket series over the standard bounds, then +Inf (the total),
// _sum and _count.
func renderHistogram(name string, raw [MQTTHistogramBucketCount]int64, count int64, sum float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# TYPE %s histogram\n", name)
	var cum int64
	for i, bound := range MQTTHistogramBounds {
		cum += raw[i]
		fmt.Fprintf(&b, "%s_bucket{le=\"%g\"} %d\n", name, bound, cum)
	}
	fmt.Fprintf(&b, "%s_bucket{le=\"+Inf\"} %d\n", name, count)
	fmt.Fprintf(&b, "%s_sum %g\n", name, sum)
	fmt.Fprintf(&b, "%s_count %d\n", name, count)
	return b.String()
}

// TestParseV12FixtureLeavesNoFieldBehind is the completeness guard: every field
// in the fixture's source snapshot was set non-zero, so any MQTTMetrics field
// still holding its zero value after the parse is a family the dashboard is not
// reading. It fails for fields added later without a parser branch, which a
// hand-listed table cannot do.
func TestParseV12FixtureLeavesNoFieldBehind(t *testing.T) {
	m := parseV12Fixture(t)
	walkForZeroFields(t, reflect.ValueOf(*m), "MQTTMetrics")
}

func walkForZeroFields(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		f, name := v.Field(i), path+"."+typ.Field(i).Name
		// The Uncurated capture fields are dashboard-local, not part of the
		// broker mirror: a fully curated fixture leaves them legitimately empty
		// (that emptiness is itself asserted by TestParseV12FixtureFullyCurated).
		if strings.HasPrefix(typ.Field(i).Name, "Uncurated") {
			continue
		}
		switch f.Kind() {
		case reflect.Array:
			// Individual buckets may legitimately be empty; an all-zero array
			// means the series was never read.
			if f.IsZero() {
				t.Errorf("%s is all zero — no bucket series was parsed", name)
			}
		case reflect.Pointer:
			if f.IsNil() {
				t.Errorf("%s is nil — its family group was not parsed", name)
				continue
			}
			walkForZeroFields(t, f.Elem(), name)
		case reflect.Slice, reflect.Map:
			if f.Len() == 0 {
				t.Errorf("%s is empty — its family was not parsed", name)
				continue
			}
			// Descend into the LAST element: the first pool slot's slot= label is
			// legitimately 0, while every field of the fixture's last entry is
			// non-zero.
			if last := f.Len() - 1; f.Kind() == reflect.Slice && f.Index(last).Kind() == reflect.Struct {
				walkForZeroFields(t, f.Index(last), fmt.Sprintf("%s[%d]", name, last))
			}
		case reflect.Struct:
			walkForZeroFields(t, f, name)
		default:
			if f.IsZero() {
				t.Errorf("%s is zero — its family was not parsed", name)
			}
		}
	}
}

// TestExtractLabelHardening covers the label-extraction rules the v1.2
// exposition depends on: values escaped per the exposition format, and whole-key
// matching so a key that is a suffix of another key does not answer for it.
func TestExtractLabelHardening(t *testing.T) {
	tests := []struct {
		name string
		line string
		key  string
		want string
	}{
		{"plain", `foo{reason="bar"} 1`, "reason", "bar"},
		{"escaped quote", `foo{id="a\"b"} 1`, "id", `a"b`},
		{"escaped backslash", `foo{id="a\\b"} 1`, "id", `a\b`},
		{"trailing backslash", `foo{id="a\\"} 1`, "id", `a\`},
		{"escaped newline", `foo{id="a\nb"} 1`, "id", "a\nb"},
		// A chained-replacement unescaper turns this into a newline; a
		// single-pass one keeps the literal two characters.
		{"backslash then n", `foo{id="a\\nb"} 1`, "id", `a\nb`},
		{"brace inside value", `foo{id="a}b",k="v"} 1`, "k", "v"},
		// source_instance_id ends with instance_id: asking for the shorter key
		// must not answer with the longer key's value.
		{"suffix collision", `foo{source_instance_id="peer"} 1`, "instance_id", ""},
		{"suffix collision reversed", `foo{instance_id="own",source_instance_id="peer"} 1`, "source_instance_id", "peer"},
		{"second label", `foo{a="1",reason="bar"} 1`, "reason", "bar"},
		{"spaced separator", `foo{a="1", reason="bar"} 1`, "reason", "bar"},
		{"absent key", `foo{a="1"} 1`, "reason", ""},
		{"no labels", `foo 1`, "reason", ""},
		{"value containing the key name", `foo{a="reason=\"x\""} 1`, "reason", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractLabel(tc.line, tc.key); got != tc.want {
				t.Errorf("extractLabel(%q, %q) = %q, want %q", tc.line, tc.key, got, tc.want)
			}
		})
	}
}

// TestParseMetricLineValueForms covers the sample formats the exposition
// permits but the broker's own %d/%g rendering does not always produce: float
// forms for integral counters, and an optional trailing timestamp on both
// labelled and unlabelled lines.
func TestParseMetricLineValueForms(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantName  string
		wantValue string
		wantInt   int64
	}{
		{"plain int", `foo_total 42`, "foo_total", "42", 42},
		{"exponent form", `foo_total 1e+06`, "foo_total", "1e+06", 1000000},
		{"decimal form", `foo_total 1.5`, "foo_total", "1.5", 1},
		{"trailing timestamp", `foo_total 42 1699999999000`, "foo_total", "42", 42},
		{"labelled", `foo_total{a="b"} 42`, "foo_total", "42", 42},
		{"labelled trailing timestamp", `foo_total{a="b"} 42 1699999999000`, "foo_total", "42", 42},
		{"labelled exponent form", `foo_total{a="b"} 1e+06`, "foo_total", "1e+06", 1000000},
		{"brace in label value", `foo_total{a="}"} 7`, "foo_total", "7", 7},
		{"no value", `foo_total`, "", "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			name, value := parseMetricLine(tc.line)
			if name != tc.wantName || value != tc.wantValue {
				t.Errorf("parseMetricLine(%q) = (%q, %q), want (%q, %q)",
					tc.line, name, value, tc.wantName, tc.wantValue)
			}
			if got := parseInt(value); got != tc.wantInt {
				t.Errorf("parseInt(%q) = %d, want %d", value, got, tc.wantInt)
			}
		})
	}

	// End to end through the real switch: an exponent-form counter carrying a
	// trailing timestamp must still land in its field.
	m := parsePrometheusMetrics("machmqtt_bytes_received_total 1e+06 1699999999000\n")
	if m.InboundBytes != 1000000 {
		t.Errorf("InboundBytes = %d, want 1000000", m.InboundBytes)
	}
}

// TestMQTTSubscriberV12NestedMetrics runs a broker-shaped v1.2 push payload
// through the real NATS subscriber and asserts the nested objects, bucket
// arrays, reason maps and family-gate booleans survive the round trip — the
// push half of the same contract the fixture pins for the poll half.
func TestMQTTSubscriberV12NestedMetrics(t *testing.T) {
	s := natstest.New(t)
	sub := newMQTTSubscriber()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sub.run(ctx, &config.NATSConnConfig{URLs: []string{s.ClientURL()}, SubjectPrefix: "$MQTT5"})
	waitSubscriberConnected(t, sub)

	nc, err := nats.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	raw := `{
		"v": 1,
		"instance_name": "v12-bridge",
		"instance_id": "id-v12",
		"nats": {"connected": true, "server_name": "n1"},
		"pool": {"size": 2, "slots": [{"index": 0, "connected": true}]},
		"metrics": {
			"connections_active": 4,
			"connections_max": 91,
			"rejected_mem_budget": 92,
			"hook_panics": 93,
			"hook_vetoes": 94,
			"sys_tree_published": 95,
			"sys_publish_blocked": 96,
			"publish_refused_topic": 97,
			"legacy_named_consumers": 101,
			"subscribe_consumer_failures": 104,
			"subscribe_consumer_retries": 105,
			"jetstream_api_errors": 106,
			"jetstream_api_requests": 107,
			"jetstream_health_probe_failures": 108,
			"stream_ensure_retries": 109,
			"stream_ensure_stalls": 110,
			"nats_connected": 1,
			"jetstream_degraded": 1,
			"consumers_awaiting_reattach": 111,
			"reattach_sweep_duration_ms": 112,
			"shared_consumer_recreated": 102,
			"consumer_deleted_under_consume": 103,
			"inbound_bytes": 97,
			"session_persist_panics": 98,
			"cluster_lease_revision_regressions": 99,
			"cluster_heartbeat_publish_failures": 100,
			"consumer_pending_messages": 2,
			"suback_rejected_by_reason": {"0x87": 41, "0xA2": 43},
			"publish_latency_buckets": [1, 2, 3, 0, 5, 6, 7, 8, 9],
			"publish_latency_count": 50,
			"qos2_sync_persist_duration_count": 11,
			"qos2_sync_persist_duration_sum_seconds": 0.25,
			"license": {"valid": 1, "status": 4, "has_expiry": true,
				"expires_timestamp": 1893456000, "connections_limit": 5000,
				"clamp_floor": 250, "permission_violations": 77},
			"reactor": {"task_queue_depth": 910, "read_continuations": 913, "loop_deaths": 916},
			"pool": {"size": 4, "buffered_bytes": 920, "buffered_bytes_max": 921,
				"slots": [{"slot": 0, "buffered_bytes": 930, "out_msgs": 931, "in_msgs": 932},
				          {"slot": 3, "buffered_bytes": 950, "out_msgs": 951, "in_msgs": 952}]},
			"cluster_hmac_failures": [{"source_instance_id": "peer-a", "count": 61},
			                          {"source_instance_id": "peer-b\\", "count": 62}],
			"auth_webhook_active": true,
			"cluster_enabled": true,
			"bridge_up": true,
			"sessions_up": true
		}
	}`
	nc.Publish("$MQTT5.metrics.v12-bridge", []byte(raw))
	nc.Flush()
	waitForBridges(t, sub, 1)

	m := sub.Bridges()[0].Status.Metrics
	if m == nil {
		t.Fatal("Metrics is nil")
	}

	scalars := []struct {
		field string
		got   int64
		want  int64
	}{
		{"ConnectionsMax", m.ConnectionsMax, 91},
		{"RejectedMemBudget", m.RejectedMemBudget, 92},
		{"HookPanics", m.HookPanics, 93},
		{"HookVetoes", m.HookVetoes, 94},
		{"SysTreePublished", m.SysTreePublished, 95},
		{"SysPublishBlocked", m.SysPublishBlocked, 96},
		{"PublishRefusedTopic", m.PublishRefusedTopic, 97},
		{"LegacyNamedConsumers", m.LegacyNamedConsumers, 101},
		{"SubscribeConsumerFailures", m.SubscribeConsumerFailures, 104},
		{"SubscribeConsumerRetries", m.SubscribeConsumerRetries, 105},
		{"JetStreamAPIErrors", m.JetStreamAPIErrors, 106},
		{"JetStreamAPIRequests", m.JetStreamAPIRequests, 107},
		{"JetStreamHealthProbeFailures", m.JetStreamHealthProbeFailures, 108},
		{"StreamEnsureRetries", m.StreamEnsureRetries, 109},
		{"StreamEnsureStalls", m.StreamEnsureStalls, 110},
		{"NATSConnected", m.NATSConnected, 1},
		{"JetStreamDegraded", m.JetStreamDegraded, 1},
		{"ConsumersAwaitingReattach", m.ConsumersAwaitingReattach, 111},
		{"ReattachSweepDurationMs", m.ReattachSweepDurationMs, 112},
		{"SharedConsumerRecreated", m.SharedConsumerRecreated, 102},
		{"ConsumerDeletedUnderConsume", m.ConsumerDeletedUnderConsume, 103},
		{"InboundBytes", m.InboundBytes, 97},
		{"SessionPersistPanics", m.SessionPersistPanics, 98},
		{"ClusterLeaseRevisionRegressions", m.ClusterLeaseRevisionRegressions, 99},
		{"ClusterHeartbeatPublishFailures", m.ClusterHeartbeatPublishFailures, 100},
		{"QoS2SyncPersistDurationCount", m.QoS2SyncPersistDurationCount, 11},
	}
	for _, tc := range scalars {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.field, tc.got, tc.want)
		}
	}

	// Auto-discovery is a holding pen, not a destination: once a key has a typed
	// field, UnmarshalJSON must stop routing it to Uncurated. These three landed
	// as uncurated when the broker added them and have since been curated.
	for _, key := range []string{
		"legacy_named_consumers", "shared_consumer_recreated", "consumer_deleted_under_consume",
		"subscribe_consumer_failures", "subscribe_consumer_retries", "jetstream_api_errors",
		"jetstream_api_requests", "jetstream_health_probe_failures", "stream_ensure_retries",
		"stream_ensure_stalls", "nats_connected", "jetstream_degraded",
		"consumers_awaiting_reattach", "reattach_sweep_duration_ms",
	} {
		if value, ok := m.Uncurated[key]; ok {
			t.Errorf("%s is curated but was still captured as uncurated (%v)", key, value)
		}
	}

	// The push path carries raw bucket counts verbatim — no cumulative
	// differencing, which is what makes the poll path's conversion necessary.
	if want := [9]int64{1, 2, 3, 0, 5, 6, 7, 8, 9}; m.PublishLatencyBuckets != want {
		t.Errorf("PublishLatencyBuckets = %v, want %v", m.PublishLatencyBuckets, want)
	}
	if want := (map[string]int64{"0x87": 41, "0xA2": 43}); !reflect.DeepEqual(m.SubackRejectedByReason, want) {
		t.Errorf("SubackRejectedByReason = %v, want %v", m.SubackRejectedByReason, want)
	}

	if m.License == nil {
		t.Fatal("License is nil")
	}
	if m.License.Status != 4 || !m.License.HasExpiry || m.License.ExpiresTimestamp != 1893456000 ||
		m.License.ClampFloor != 250 || m.License.PermissionViolations != 77 {
		t.Errorf("License = %+v, want status 4 / has_expiry / ts 1893456000 / floor 250 / violations 77", *m.License)
	}
	if m.Reactor == nil {
		t.Fatal("Reactor is nil")
	}
	if m.Reactor.TaskQueueDepth != 910 || m.Reactor.ReadContinuations != 913 || m.Reactor.LoopDeaths != 916 {
		t.Errorf("Reactor = %+v, want depth 910 / continuations 913 / deaths 916", *m.Reactor)
	}
	if m.Pool == nil {
		t.Fatal("Pool is nil")
	}
	wantSlots := []MQTTMetricsPoolSlot{
		{Slot: 0, BufferedBytes: 930, OutMsgs: 931, InMsgs: 932},
		{Slot: 3, BufferedBytes: 950, OutMsgs: 951, InMsgs: 952},
	}
	if m.Pool.Size != 4 || m.Pool.BufferedBytes != 920 || m.Pool.BufferedBytesMax != 921 ||
		!reflect.DeepEqual(m.Pool.Slots, wantSlots) {
		t.Errorf("Pool = %+v, want size 4 / 920 / 921 / slots %+v", *m.Pool, wantSlots)
	}
	wantHMAC := []MQTTHMACFailure{
		{SourceInstanceID: `peer-a`, Count: 61},
		{SourceInstanceID: `peer-b\`, Count: 62},
	}
	if !reflect.DeepEqual(m.ClusterHMACFailures, wantHMAC) {
		t.Errorf("ClusterHMACFailures = %+v, want %+v", m.ClusterHMACFailures, wantHMAC)
	}
	if !m.AuthWebhookActive || !m.ClusterEnabled || !m.BridgeUp || !m.SessionsUp {
		t.Errorf("family gates = %v/%v/%v/%v, want all true",
			m.AuthWebhookActive, m.ClusterEnabled, m.BridgeUp, m.SessionsUp)
	}

	// The bridge's own /pool view is a separate object and must not be
	// overwritten by the metrics-path pool group.
	if p := sub.Bridges()[0].Status.Pool; p == nil || p.Size != 2 || len(p.Slots) != 1 {
		t.Errorf("Status.Pool = %+v, want the payload's own pool object (size 2, 1 slot)", p)
	}
}

// TestParseV12FixtureFullyCurated pins that a body containing only curated
// families captures nothing: the Uncurated maps exist for genuinely unknown
// metrics, not as a duplicate of the typed fields.
func TestParseV12FixtureFullyCurated(t *testing.T) {
	m := parseV12Fixture(t)
	if len(m.Uncurated) != 0 {
		t.Errorf("Uncurated = %v, want empty for a fully curated body", m.Uncurated)
	}
	if len(m.UncuratedHelp) != 0 {
		t.Errorf("UncuratedHelp = %v, want empty for a fully curated body", m.UncuratedHelp)
	}
}

// TestParsePrometheusUncuratedCapture covers the scrape-path capture of
// machmqtt families this build has no curated field for: plain and labeled
// series are recorded verbatim with the broker's HELP text, unknown histogram
// buckets and non-machmqtt families stay out, and known families never leak in.
func TestParsePrometheusUncuratedCapture(t *testing.T) {
	body := `# HELP machmqtt_future_widget_total Widgets processed by a feature this dashboard predates.
# TYPE machmqtt_future_widget_total counter
machmqtt_future_widget_total 123
# TYPE machmqtt_future_by_kind_total counter
machmqtt_future_by_kind_total{kind="a"} 4
machmqtt_future_by_kind_total{kind="b"} 5
# TYPE machmqtt_future_latency_seconds histogram
machmqtt_future_latency_seconds_bucket{le="0.1"} 7
machmqtt_future_latency_seconds_sum 0.5
machmqtt_future_latency_seconds_count 7
# TYPE machmqtt_connections_active gauge
machmqtt_connections_active 3
not_a_machmqtt_metric 9
`
	m := parsePrometheusMetrics(body)

	want := map[string]float64{
		"machmqtt_future_widget_total":            123,
		`machmqtt_future_by_kind_total{kind="a"}`: 4,
		`machmqtt_future_by_kind_total{kind="b"}`: 5,
		// The unknown histogram's sum/count are plain unknown series; its
		// _bucket lines are deliberately not captured (the bucket case swallows
		// them before the default runs).
		"machmqtt_future_latency_seconds_sum":   0.5,
		"machmqtt_future_latency_seconds_count": 7,
	}
	if len(m.Uncurated) != len(want) {
		t.Errorf("Uncurated has %d entries, want %d: %v", len(m.Uncurated), len(want), m.Uncurated)
	}
	for k, v := range want {
		if m.Uncurated[k] != v {
			t.Errorf("Uncurated[%q] = %v, want %v", k, m.Uncurated[k], v)
		}
	}
	if _, leaked := m.Uncurated["machmqtt_connections_active"]; leaked {
		t.Error("a curated family leaked into Uncurated")
	}
	if m.ConnectionsActive != 3 {
		t.Errorf("ConnectionsActive = %d, want 3 (curated parsing must be unaffected)", m.ConnectionsActive)
	}
	if got := m.UncuratedHelp["machmqtt_future_widget_total"]; got != "Widgets processed by a feature this dashboard predates." {
		t.Errorf("UncuratedHelp = %q, want the broker's HELP text", got)
	}
	if _, ok := m.UncuratedHelp["machmqtt_future_by_kind_total"]; ok {
		t.Error("help recorded for a family that has no HELP line")
	}
}

// TestMQTTMetricsUnmarshalCapturesUnknownKeys covers the push-path capture:
// unknown numeric top-level keys in the metrics object land in Uncurated,
// non-numeric unknowns are skipped, and typed fields decode as before.
func TestMQTTMetricsUnmarshalCapturesUnknownKeys(t *testing.T) {
	raw := `{
		"connections_active": 6,
		"counter_added_in_future": 999,
		"ratio_added_in_future": 0.25,
		"object_added_in_future": {"nested": 1},
		"string_added_in_future": "x"
	}`
	var m MQTTMetrics
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.ConnectionsActive != 6 {
		t.Errorf("ConnectionsActive = %d, want 6", m.ConnectionsActive)
	}
	want := map[string]float64{"counter_added_in_future": 999, "ratio_added_in_future": 0.25}
	if len(m.Uncurated) != len(want) {
		t.Errorf("Uncurated has %d entries, want %d: %v", len(m.Uncurated), len(want), m.Uncurated)
	}
	for k, v := range want {
		if m.Uncurated[k] != v {
			t.Errorf("Uncurated[%q] = %v, want %v", k, m.Uncurated[k], v)
		}
	}
}
