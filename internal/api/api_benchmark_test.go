package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

func BenchmarkConnectionMergeAndSort100K(b *testing.B) {
	results := make([]connzServerResult, 4)
	for server := range results {
		results[server].serverID = fmt.Sprintf("server-%d", server)
		results[server].total = 25_000
		results[server].conns = make([]collector.ConnInfo, 25_000)
		for index := range results[server].conns {
			results[server].conns[index] = collector.ConnInfo{ServerID: results[server].serverID, Cid: uint64(25_000 - index)}
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		connections, total, failures, partial := flattenConnz(results)
		if len(connections) != 100_000 || total != 100_000 || failures != 0 || partial {
			b.Fatal("unexpected flattened result")
		}
	}
}

func BenchmarkSubscriptionCacheRead50K(b *testing.B) {
	rows := make([]subRow, 50_000)
	server := &Server{subsCacheData: map[string]*subsCacheEntry{
		"benchmark": {rows: rows, fetchedAt: time.Now()},
	}}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if got, _ := server.getSubsRows(b.Context(), "benchmark"); len(got) != len(rows) {
			b.Fatalf("rows=%d", len(got))
		}
	}
}

func BenchmarkAuthenticatedEnvironmentsAPI(b *testing.B) {
	st, err := store.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	user, err := st.CreateUser("benchmark", "benchmark-password", store.RoleViewer)
	if err != nil {
		b.Fatal(err)
	}
	a := auth.New(st, "benchmark-signing-key", false)
	defer a.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{PollInterval: 5 * time.Second, Environments: []config.Environment{{Name: "benchmark"}}}
	manager, err := collector.NewManager(cfg, nil, logger, st)
	if err != nil {
		b.Fatal(err)
	}
	srv := NewServer(a, manager, nil, logger, "benchmark", cfg, nil, st, nil)
	token, err := a.IssueToken(user)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		req := httptest.NewRequest(http.MethodGet, "/api/environments", nil)
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		response := httptest.NewRecorder()
		srv.Handler().ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			b.Fatalf("status=%d", response.Code)
		}
	}
}
