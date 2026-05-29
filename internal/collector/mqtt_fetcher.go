package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const mqttFetchTimeout = 5 * time.Second

// MQTTBridgeFetcher fetches data from a MachMQTT bridge admin API.
type MQTTBridgeFetcher struct {
	client      *http.Client
	baseURL     string
	bearerToken string
	name        string
}

func NewMQTTBridgeFetcher(baseURL, name, bearerToken string) *MQTTBridgeFetcher {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &MQTTBridgeFetcher{
		client:      &http.Client{Transport: transport, Timeout: 10 * time.Second},
		baseURL:     baseURL,
		name:        name,
		bearerToken: bearerToken,
	}
}

func (f *MQTTBridgeFetcher) fetch(ctx context.Context, path string, out any) error {
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("fetch %s: status %d: %s", path, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

func (f *MQTTBridgeFetcher) FetchReadyz(ctx context.Context) (*MQTTReadyz, error) {
	var r MQTTReadyz
	return &r, f.fetch(ctx, "/readyz", &r)
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parsePrometheusMetrics(string(body)), nil
}

// FetchStatus fetches readyz + diag/nats + metrics for a quick overview.
func (f *MQTTBridgeFetcher) FetchStatus(ctx context.Context) *MQTTBridgeStatus {
	status := &MQTTBridgeStatus{Name: f.name, URL: f.baseURL}

	readyz, err := f.FetchReadyz(ctx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Ready = readyz.Status == "ready"
	status.Draining = readyz.Status == "draining"
	status.Connections = readyz.Connections
	status.NATSConnected = readyz.NATSConnected

	if diag, err := f.FetchDiagNATS(ctx); err == nil {
		status.NATS = diag
	}

	if pool, err := f.FetchPool(ctx); err == nil {
		status.Pool = pool
	}

	if metrics, err := f.FetchMetrics(ctx); err == nil {
		status.Metrics = metrics
	}

	// Check if /connz is available.
	if connz, err := f.FetchConnz(ctx, 1, 0); err == nil {
		status.ConnzAvailable = true
		status.TotalConnections = connz.Total
	}

	return status
}
