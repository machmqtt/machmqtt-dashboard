package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/noodlebit/machmqtt-dashboard/internal/api"
	"github.com/noodlebit/machmqtt-dashboard/internal/auth"
	"github.com/noodlebit/machmqtt-dashboard/internal/collector"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
	"github.com/noodlebit/machmqtt-dashboard/internal/ws"
)

const dashboardURL = "https://127.0.0.1:18443"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dataDir, err := os.MkdirTemp("", "machmqtt-enterprise-browser-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dataDir) }()

	st, err := store.Open(dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	if _, err := st.CreateUser("local-admin", "local-password", store.RoleAdmin); err != nil {
		return err
	}
	if _, err := st.CreateUser("fry", "local-fry-password", store.RoleAdmin); err != nil {
		return err
	}

	ldapConfig := config.LDAPAuthConfig{
		URL: "ldap://127.0.0.1:1389", AllowPlaintext: true,
		BindDN: "cn=admin,dc=planetexpress,dc=com", BindPassword: "GoodNewsEveryone",
		UserBaseDN: "ou=people,dc=planetexpress,dc=com", UserFilter: "(uid={username})",
		UsernameAttribute: "uid", SubjectAttribute: "entryUUID", DisplayNameAttribute: "cn", EmailAttribute: "mail",
		GroupAttribute: "memberOf", AdminGroups: []string{"ship_crew"}, Timeout: 2 * time.Second,
	}
	oidcConfig := config.OIDCAuthConfig{
		IssuerURL: "http://127.0.0.1:5556/dex", ClientID: "dashboard-client", ClientSecret: "dashboard-secret",
		Scopes: []string{"groups"}, UsernameClaim: "preferred_username", DisplayNameClaim: "name",
		EmailClaim: "email", GroupsClaim: "groups", AdminGroups: []string{"ship_crew"},
	}
	authConfig := config.AuthenticationConfig{PublicURL: dashboardURL, Providers: []config.AuthProviderConfig{
		{Name: "openldap", Type: "ldap", LDAP: &ldapConfig},
		{Name: "dex", Type: "oidc", OIDC: &oidcConfig},
	}}
	providers, err := auth.BuildProviderSet(authConfig, st)
	if err != nil {
		return err
	}
	a := auth.NewWithProviderSet(st, "enterprise-browser-session-secret", true, providers)
	defer a.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a.SetLogger(logger)

	cfg := &config.Config{PollInterval: 5 * time.Second, Environments: []config.Environment{{
		Name: "browser-test", Servers: []config.Server{{URL: "http://127.0.0.1:18222"}},
	}}}
	manager, err := collector.NewManager(cfg, nil, logger, st)
	if err != nil {
		return err
	}
	srv := api.NewServer(a, manager, ws.NewHub(logger), logger, "enterprise-browser", cfg, nil, st, nil)
	certificate, err := selfSignedCertificate()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:18443")
	if err != nil {
		return err
	}
	tlsListener := tlsListener(listener, certificate)
	httpServer := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(tlsListener) }()
	fmt.Println("enterprise browser fixture ready")

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(shutdown)
	select {
	case <-shutdown:
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpServer.Shutdown(ctx)
}

func tlsListener(listener net.Listener, certificate tls.Certificate) net.Listener {
	return tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
}

func selfSignedCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}
