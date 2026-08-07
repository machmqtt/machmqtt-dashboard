package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const mqttFetchTimeout = 5 * time.Second
const mqttMaxResponseBytes = 8 << 20

var mqttTransport = &http.Transport{
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   5 * time.Second,
	ResponseHeaderTimeout: 5 * time.Second,
}

// MQTTBridgeFetcher fetches data from a MachMQTT bridge admin API.
type MQTTBridgeFetcher struct {
	client      *http.Client
	baseURL     string
	bearerToken string
	name        string
}

// sharedMQTTTransport is reused across all bridge fetchers. NewMQTTBridgeFetcher
// is called once per bridge per discovery cycle, so a per-fetcher transport
// would never reuse a connection and would churn idle-conn pools every poll.
// http.Transport is safe for concurrent use and pools connections per host.
var sharedMQTTTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	IdleConnTimeout:     30 * time.Second,
	TLSHandshakeTimeout: 5 * time.Second,
}

func NewMQTTBridgeFetcher(baseURL, name, bearerToken string) *MQTTBridgeFetcher {
	return &MQTTBridgeFetcher{
		client:      &http.Client{Transport: sharedMQTTTransport, Timeout: 10 * time.Second},
		baseURL:     baseURL,
		name:        name,
		bearerToken: bearerToken,
	}
}

func (f *MQTTBridgeFetcher) fetch(ctx context.Context, path string, out any) error {
	return f.fetchAccepting(ctx, path, out)
}

// fetchAccepting performs a GET and decodes the body on 200 or on any status in
// alsoDecode; every other status is an error. Only /readyz passes alsoDecode:
// its non-ready states answer 503 with the state in the body, so treating that
// as a failure would report a live bridge as unreachable. All other endpoints
// go through fetch and keep the strict 200-only contract.
func (f *MQTTBridgeFetcher) fetchAccepting(ctx context.Context, path string, out any, alsoDecode ...int) error {
	ctx, cancel := context.WithTimeout(ctx, mqttFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+path, nil)
	if err != nil {
		return err
	}
	if f.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.bearerToken)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	decodable := resp.StatusCode == http.StatusOK
	for _, code := range alsoDecode {
		if resp.StatusCode == code {
			decodable = true
			break
		}
	}
	if !decodable {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("fetch %s: status %d: %s", path, resp.StatusCode, body)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, mqttMaxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > mqttMaxResponseBytes {
		return fmt.Errorf("fetch %s: response exceeds %d bytes", path, mqttMaxResponseBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// getWithStatus performs a GET and returns the bridge's HTTP status code. On a
// 200 it decodes the body into out; on any other status it returns the code
// with a nil error so callers can relay it (e.g. 409 = feature disabled,
// 404 = unsupported on an older bridge). A transport error returns (0, err).
func (f *MQTTBridgeFetcher) getWithStatus(ctx context.Context, path string, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, mqttFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	if f.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.bearerToken)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("get %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// PostAdmin sends a POST to a bridge admin endpoint and returns the bridge's
// HTTP status code and raw (length-capped) response body. reqBody may be nil
// for body-less actions. A transport error returns (0, nil, err). The caller
// relays the status/body so the UI can distinguish 403 (endpoint disabled),
// 409 (cluster not enabled) and 404 (unsupported on an older bridge).
func (f *MQTTBridgeFetcher) PostAdmin(ctx context.Context, path string, reqBody []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, mqttFetchTimeout)
	defer cancel()

	var rdr io.Reader
	if len(reqBody) > 0 {
		rdr = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if f.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.bearerToken)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("post %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, body, nil
}

// FetchCluster fetches the bridge's cluster summary. The returned int is the
// bridge's HTTP status (200 ok, 409 clustering not enabled, 404 unsupported).
func (f *MQTTBridgeFetcher) FetchCluster(ctx context.Context) (*MQTTCluster, int, error) {
	var c MQTTCluster
	code, err := f.getWithStatus(ctx, "/admin/cluster", &c)
	if err != nil || code != http.StatusOK {
		return nil, code, err
	}
	return &c, code, nil
}

// FetchClusterInspect locates and inspects a single client across the cluster.
// The returned int is the bridge's HTTP status (200 found, 404 not found,
// 409 not clustered, 429 busy).
func (f *MQTTBridgeFetcher) FetchClusterInspect(ctx context.Context, clientID string) (*MQTTClusterInspect, int, error) {
	var ins MQTTClusterInspect
	code, err := f.getWithStatus(ctx, "/admin/cluster/inspect?client_id="+url.QueryEscape(clientID), &ins)
	if err != nil || code != http.StatusOK {
		return nil, code, err
	}
	return &ins, code, nil
}

// ReadyzState maps a bridge /readyz status string onto the mutually exclusive
// states the dashboard renders. An unrecognised status (including "not ready")
// yields all-false: the bridge answered, it just isn't in a state this build
// names. Shared by the poll path and the per-bridge readyz proxy so the two
// cannot drift.
func ReadyzState(status string) (ready, draining, jetStreamDegraded bool) {
	return status == "ready", status == "draining", status == "jetstream-degraded"
}

// FetchReadyz reads the bridge's readiness state. A 503 is a valid, decodable
// answer here — "draining", "jetstream-degraded" and "not ready" all report it —
// so only a transport failure or an unexpected status yields an error.
func (f *MQTTBridgeFetcher) FetchReadyz(ctx context.Context) (*MQTTReadyz, error) {
	var r MQTTReadyz
	return &r, f.fetchAccepting(ctx, "/readyz", &r, http.StatusServiceUnavailable)
}

func (f *MQTTBridgeFetcher) FetchConnz(ctx context.Context, limit, offset int) (*MQTTConnz, error) {
	path := fmt.Sprintf("/connz?limit=%d&offset=%d", limit, offset)
	var c MQTTConnz
	return &c, f.fetch(ctx, path, &c)
}

func (f *MQTTBridgeFetcher) FetchConnzClient(ctx context.Context, clientID string) (*MQTTConnz, error) {
	path := "/connz?mqtt_client=" + url.QueryEscape(clientID)
	var c MQTTConnz
	return &c, f.fetch(ctx, path, &c)
}

func (f *MQTTBridgeFetcher) FetchDiagNATS(ctx context.Context) (*MQTTNATSDiag, error) {
	var d MQTTNATSDiag
	return &d, f.fetch(ctx, "/diag/nats", &d)
}

func (f *MQTTBridgeFetcher) FetchDiag(ctx context.Context) (*MQTTDiag, error) {
	var d MQTTDiag
	return &d, f.fetch(ctx, "/diag", &d)
}

func (f *MQTTBridgeFetcher) FetchLicense(ctx context.Context) (*MQTTLicense, error) {
	var l MQTTLicense
	return &l, f.fetch(ctx, "/license", &l)
}

func (f *MQTTBridgeFetcher) FetchPool(ctx context.Context) (*MQTTPool, error) {
	var p MQTTPool
	return &p, f.fetch(ctx, "/pool", &p)
}

// FetchMetrics fetches the Prometheus text metrics and parses key values.
func (f *MQTTBridgeFetcher) FetchMetrics(ctx context.Context) (*MQTTMetrics, error) {
	ctx, cancel := context.WithTimeout(ctx, mqttFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.baseURL+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	if f.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+f.bearerToken)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, mqttMaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > mqttMaxResponseBytes {
		return nil, fmt.Errorf("metrics response exceeds %d bytes", mqttMaxResponseBytes)
	}

	return parsePrometheusMetrics(string(body)), nil
}

func CloseMQTTIdleConnections() { mqttTransport.CloseIdleConnections() }

// FetchStatus fetches readyz + diag/nats + metrics for a quick overview.
func (f *MQTTBridgeFetcher) FetchStatus(ctx context.Context) *MQTTBridgeStatus {
	status := &MQTTBridgeStatus{Name: f.name, URL: f.baseURL}

	readyz, err := f.FetchReadyz(ctx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Ready, status.Draining, status.JetStreamDegraded = ReadyzState(readyz.Status)
	status.NATSConnected = readyz.NATSConnected

	if diag, err := f.FetchDiagNATS(ctx); err == nil {
		status.NATS = diag
	}

	if pool, err := f.FetchPool(ctx); err == nil {
		status.Pool = pool
	}

	if metrics, err := f.FetchMetrics(ctx); err == nil {
		status.Metrics = metrics
		// The bridge's /readyz has no connection count, so the active-client
		// figure comes from the metrics snapshot (matching the NATS-push path,
		// which sets this from the same counter). /connz total below is a
		// fallback for bridges whose metrics endpoint is unavailable.
		status.Connections = int(metrics.ConnectionsActive)
	}

	// Check if /connz is available.
	if connz, err := f.FetchConnz(ctx, 1, 0); err == nil {
		status.ConnzAvailable = true
		status.TotalConnections = connz.Total
		if status.Connections == 0 {
			status.Connections = int(connz.Total)
		}
	}

	return status
}
