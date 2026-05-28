package collector

import (
	"strconv"
	"strings"
)

func parsePrometheusMetrics(body string) *MQTTMetrics {
	m := &MQTTMetrics{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		name, value := parseMetricLine(line)
		switch {
		case name == "machmqtt_connections_active":
			m.ConnectionsActive = parseInt(value)
		case name == "machmqtt_connections_total":
			m.ConnectionsTotal = parseInt(value)
		case name == "machmqtt_connections_rejected_total":
			m.ConnectionsRejected = parseInt(value)
		case name == "machmqtt_ws_connections_active":
			m.WSConnectionsActive = parseInt(value)
		case name == "machmqtt_ws_connections_total":
			m.WSConnectionsTotal = parseInt(value)
		case name == "machmqtt_auth_success_total":
			m.AuthSuccess = parseInt(value)
		case name == "machmqtt_auth_failure_total":
			// machmqtt emits this only as per-reason labeled series
			// (reason="bad_credentials|enhanced|locked|other") with no
			// unlabeled aggregate, so sum the buckets into the total.
			m.AuthFailure += parseInt(value)
		case strings.HasPrefix(line, `machmqtt_messages_received_total{qos="0"}`):
			m.MsgsRecvQoS0 = parseInt(value)
		case strings.HasPrefix(line, `machmqtt_messages_received_total{qos="1"}`):
			m.MsgsRecvQoS1 = parseInt(value)
		case strings.HasPrefix(line, `machmqtt_messages_received_total{qos="2"}`):
			m.MsgsRecvQoS2 = parseInt(value)
		case strings.HasPrefix(line, `machmqtt_messages_sent_total{qos="0"}`):
			m.MsgsSentQoS0 = parseInt(value)
		case strings.HasPrefix(line, `machmqtt_messages_sent_total{qos="1"}`):
			m.MsgsSentQoS1 = parseInt(value)
		case strings.HasPrefix(line, `machmqtt_messages_sent_total{qos="2"}`):
			m.MsgsSentQoS2 = parseInt(value)
		case name == "machmqtt_subscribes_total":
			m.Subscribes = parseInt(value)
		case name == "machmqtt_unsubscribes_total":
			m.Unsubscribes = parseInt(value)
		case name == "machmqtt_keepalive_timeouts_total":
			m.KeepaliveTimeouts = parseInt(value)
		case name == "machmqtt_pool_publishes_total":
			m.PoolPublishes = parseInt(value)
		case name == "machmqtt_pool_subscribes_total":
			m.PoolSubscribes = parseInt(value)
		case name == "machmqtt_nats_disconnects_total":
			m.NATSDisconnects = parseInt(value)
		case name == "machmqtt_nats_reconnects_total":
			m.NATSReconnects = parseInt(value)
		}
	}
	return m
}

func parseMetricLine(line string) (name, value string) {
	idx := strings.IndexByte(line, '{')
	if idx >= 0 {
		name = line[:idx]
		end := strings.LastIndexByte(line, '}')
		if end >= 0 && end+1 < len(line) {
			value = strings.TrimSpace(line[end+1:])
		}
	} else {
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			name = parts[0]
			value = parts[1]
		}
	}
	return
}

func parseInt(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
