package collector

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

const fetchTimeout = 3 * time.Second

type Fetcher struct {
	client *http.Client
}

func NewFetcher(tlsCfg *config.TLSConfig) (*Fetcher, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	if tlsCfg != nil {
		if tlsCfg.Insecure {
			fmt.Fprintln(os.Stderr, "WARNING: TLS certificate verification is disabled (insecure: true). This is vulnerable to man-in-the-middle attacks.")
		}
		tc := &tls.Config{InsecureSkipVerify: tlsCfg.Insecure, MinVersion: tls.VersionTLS12}
		if tlsCfg.CAFile != "" {
			caCert, err := os.ReadFile(tlsCfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("read CA file: %w", err)
			}
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(caCert)
			tc.RootCAs = pool
		}
		transport.TLSClientConfig = tc
	}

	return &Fetcher{
		client: &http.Client{Transport: transport},
	}, nil
}

func (f *Fetcher) fetch(ctx context.Context, baseURL, path string, params url.Values, out any) error {
	return f.fetchWithTimeout(ctx, fetchTimeout, baseURL, path, params, out)
}

// fetchWithTimeout performs a GET + JSON decode with a per-request timeout. Most
// callers use fetch (the default 3s timeout); the heavier connz+subs detail
// endpoints pass a longer timeout.
func (f *Fetcher) fetchWithTimeout(ctx context.Context, timeout time.Duration, baseURL, path string, params url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	u := baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
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

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (f *Fetcher) FetchVarz(ctx context.Context, baseURL string) (*Varz, error) {
	var v Varz
	return &v, f.fetch(ctx, baseURL, "/varz", nil, &v)
}

func (f *Fetcher) FetchConnz(ctx context.Context, baseURL string, limit, offset int, sort, acc, state, filterSubject string) (*Connz, error) {
	params := url.Values{}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		params.Set("offset", fmt.Sprintf("%d", offset))
	}
	if sort != "" {
		params.Set("sort", sort)
	}
	if acc != "" {
		params.Set("acc", acc)
	}
	if state != "" {
		params.Set("state", state)
	}
	if filterSubject != "" {
		params.Set("subs", "true")
		params.Set("filter_subject", filterSubject)
	}
	var c Connz
	return &c, f.fetch(ctx, baseURL, "/connz", params, &c)
}

func (f *Fetcher) FetchConnzWithSubs(ctx context.Context, baseURL string, limit int) (*Connz, error) {
	params := url.Values{"subs": {"true"}}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	var c Connz
	return &c, f.fetch(ctx, baseURL, "/connz", params, &c)
}

func (f *Fetcher) FetchConnzWithSubsFiltered(ctx context.Context, baseURL string, limit int, filterSubject string) (*Connz, error) {
	params := url.Values{"subs": {"true"}}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if filterSubject != "" {
		params.Set("filter_subject", filterSubject)
	}
	var c Connz
	return &c, f.fetch(ctx, baseURL, "/connz", params, &c)
}

func (f *Fetcher) FetchConnzSubsDetail(ctx context.Context, baseURL string, limit int) (*Connz, error) {
	return f.fetchConnzSubs(ctx, baseURL, "detail", limit, "")
}

func (f *Fetcher) FetchConnzSubsDetailFiltered(ctx context.Context, baseURL string, limit int, filterSubject string) (*Connz, error) {
	return f.fetchConnzSubs(ctx, baseURL, "detail", limit, filterSubject)
}

func (f *Fetcher) fetchConnzSubs(ctx context.Context, baseURL, subsMode string, limit int, filterSubject string) (*Connz, error) {
	params := url.Values{"subs": {subsMode}}
	if limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", limit))
	}
	if filterSubject != "" {
		params.Set("filter_subject", filterSubject)
	}
	var c Connz
	// connz+subs detail is heavier than the other endpoints, so allow a longer
	// timeout than the default fetch.
	if err := f.fetchWithTimeout(ctx, 10*time.Second, baseURL, "/connz", params, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (f *Fetcher) FetchRoutez(ctx context.Context, baseURL string) (*Routez, error) {
	var r Routez
	return &r, f.fetch(ctx, baseURL, "/routez", nil, &r)
}

func (f *Fetcher) FetchGatewayz(ctx context.Context, baseURL string) (*Gatewayz, error) {
	var g Gatewayz
	return &g, f.fetch(ctx, baseURL, "/gatewayz", nil, &g)
}

func (f *Fetcher) FetchLeafz(ctx context.Context, baseURL string) (*Leafz, error) {
	var l Leafz
	return &l, f.fetch(ctx, baseURL, "/leafz", nil, &l)
}

func (f *Fetcher) FetchSubsz(ctx context.Context, baseURL string) (*SubszResp, error) {
	var s SubszResp
	return &s, f.fetch(ctx, baseURL, "/subsz", nil, &s)
}

func (f *Fetcher) FetchJSInfo(ctx context.Context, baseURL string) (*JSInfo, error) {
	// config=true is required for nats-server to include each stream's and
	// consumer's config block (storage, replicas, limits, retention, deliver/ack
	// policy, filter subject). Without it those come back null and the JetStream
	// page can't show stream settings or consumer policies.
	params := url.Values{"streams": {"true"}, "consumers": {"true"}, "config": {"true"}}
	var j JSInfo
	return &j, f.fetch(ctx, baseURL, "/jsz", params, &j)
}

func (f *Fetcher) FetchAccountz(ctx context.Context, baseURL string) (*Accountz, error) {
	var a Accountz
	return &a, f.fetch(ctx, baseURL, "/accountz", nil, &a)
}

func (f *Fetcher) FetchAccountDetail(ctx context.Context, baseURL, account string) (*Accountz, error) {
	params := url.Values{"acc": {account}}
	var a Accountz
	return &a, f.fetch(ctx, baseURL, "/accountz", params, &a)
}

func (f *Fetcher) FetchHealthz(ctx context.Context, baseURL string) (*HealthStatus, error) {
	var h HealthStatus
	return &h, f.fetch(ctx, baseURL, "/healthz", nil, &h)
}
