package auth

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ber "github.com/go-asn1-ber/asn1-ber"
	ldap "github.com/go-ldap/ldap/v3"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
	"github.com/noodlebit/machmqtt-dashboard/internal/store"
)

type ldapIntegrationServer struct {
	listener         net.Listener
	serviceDN        string
	servicePassword  string
	userDN           string
	userPassword     string
	username         string
	userGroups       []string
	nestedGroups     []string
	userCount        int
	searchError      bool
	groupSearchError bool
	omitSubject      bool
	omitUsername     bool

	mu         sync.Mutex
	operations []string
}

func startLDAPIntegrationServer(t *testing.T, useTLS bool) (*ldapIntegrationServer, string, string) {
	t.Helper()
	var listener net.Listener
	caFile := ""
	var err error
	if useTLS {
		certificateListener, listenErr := net.Listen("tcp4", "127.0.0.1:0")
		if listenErr != nil {
			t.Fatal(listenErr)
		}
		certificateSource := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		certificateSource.Listener = certificateListener
		certificateSource.StartTLS()
		certificate := certificateSource.TLS.Certificates[0]
		certificateDER := certificate.Certificate[0]
		certificateSource.Close()
		listener, err = tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		})
		if err != nil {
			t.Fatal(err)
		}
		caFile = filepath.Join(t.TempDir(), "ldap-ca.pem")
		if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
	}
	server := &ldapIntegrationServer{
		listener:        listener,
		serviceDN:       "CN=Dashboard Bind,OU=Service Accounts,DC=example,DC=com",
		servicePassword: "service-password",
		userDN:          "CN=Alice,OU=People,DC=example,DC=com",
		userPassword:    "directory-password",
		username:        "alice",
		userGroups:      []string{"CN=Dashboard Viewers,OU=Groups,DC=example,DC=com"},
		userCount:       1,
	}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	scheme := "ldap://"
	if useTLS {
		scheme = "ldaps://"
	}
	return server, scheme + listener.Addr().String(), caFile
}

func (s *ldapIntegrationServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn)
	}
}

func (s *ldapIntegrationServer) serveConn(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		request, err := ber.ReadPacket(reader)
		if err != nil || len(request.Children) < 2 {
			return
		}
		messageID := request.Children[0].Value
		op := request.Children[1]
		switch op.Tag {
		case ldap.ApplicationBindRequest:
			dn := packetString(op.Children[1])
			password := op.Children[2].Data.String()
			s.record("bind:" + dn)
			s.mu.Lock()
			serviceDN, servicePassword := s.serviceDN, s.servicePassword
			userDN, userPassword := s.userDN, s.userPassword
			s.mu.Unlock()
			code := ldap.LDAPResultSuccess
			if (dn != serviceDN || password != servicePassword) && (dn != userDN || password != userPassword) {
				code = ldap.LDAPResultInvalidCredentials
			}
			_ = writeLDAPResult(conn, messageID, ldap.ApplicationBindResponse, code)
		case ldap.ApplicationSearchRequest:
			baseDN := packetString(op.Children[0])
			s.record("search:" + baseDN)
			s.mu.Lock()
			searchError := s.searchError
			groupSearchError := s.groupSearchError
			nestedGroups := append([]string(nil), s.nestedGroups...)
			userGroups := append([]string(nil), s.userGroups...)
			username, userDN, userCount := s.username, s.userDN, s.userCount
			omitSubject, omitUsername := s.omitSubject, s.omitUsername
			s.mu.Unlock()
			isGroupSearch := strings.EqualFold(baseDN, "OU=Groups,DC=example,DC=com")
			if searchError || (isGroupSearch && groupSearchError) {
				_ = writeLDAPResult(conn, messageID, ldap.ApplicationSearchResultDone, ldap.LDAPResultOperationsError)
				continue
			}
			if isGroupSearch {
				for _, group := range nestedGroups {
					_ = writeLDAPEntry(conn, messageID, group, map[string][][]byte{
						"cn": {[]byte(firstRDN(group))},
					})
				}
			} else if strings.Contains(string(request.Bytes()), username) {
				for i := 0; i < userCount; i++ {
					dn := userDN
					if i > 0 {
						dn = "CN=Alice Duplicate,OU=People,DC=example,DC=com"
					}
					groupValues := make([][]byte, 0, len(userGroups))
					for _, group := range userGroups {
						groupValues = append(groupValues, []byte(group))
					}
					attributes := map[string][][]byte{
						"displayName": {[]byte("Alice Directory")},
						"mail":        {[]byte("alice@example.com")},
						"memberOf":    groupValues,
					}
					if !omitUsername {
						attributes["sAMAccountName"] = [][]byte{[]byte(username)}
					}
					if !omitSubject {
						attributes["entryUUID"] = [][]byte{[]byte("ldap-subject-123")}
					}
					_ = writeLDAPEntry(conn, messageID, dn, attributes)
				}
			}
			_ = writeLDAPResult(conn, messageID, ldap.ApplicationSearchResultDone, ldap.LDAPResultSuccess)
		case ldap.ApplicationUnbindRequest:
			return
		default:
			return
		}
	}
}

func (s *ldapIntegrationServer) record(operation string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations = append(s.operations, operation)
}

func (s *ldapIntegrationServer) operationLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.operations...)
}

func (s *ldapIntegrationServer) configure(fn func(*ldapIntegrationServer)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func packetString(packet *ber.Packet) string {
	if value, ok := packet.Value.(string); ok {
		return value
	}
	return packet.Data.String()
}

func writeLDAPResult(conn net.Conn, messageID any, applicationTag ber.Tag, resultCode int) error {
	response := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP Response")
	response.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, messageID, "Message ID"))
	result := ber.Encode(ber.ClassApplication, ber.TypeConstructed, applicationTag, nil, "Result")
	result.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, resultCode, "Result Code"))
	result.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "Matched DN"))
	result.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", "Diagnostic Message"))
	response.AppendChild(result)
	_, err := conn.Write(response.Bytes())
	return err
}

func writeLDAPEntry(conn net.Conn, messageID any, dn string, attributes map[string][][]byte) error {
	response := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "LDAP Response")
	response.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, messageID, "Message ID"))
	entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultEntry, nil, "Search Entry")
	entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, dn, "Object Name"))
	attributeList := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attributes")
	for name, values := range attributes {
		attribute := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Attribute")
		attribute.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, name, "Name"))
		valueSet := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "Values")
		for _, value := range values {
			valueSet.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, string(value), "Value"))
		}
		attribute.AppendChild(valueSet)
		attributeList.AppendChild(attribute)
	}
	entry.AppendChild(attributeList)
	response.AppendChild(entry)
	_, err := conn.Write(response.Bytes())
	return err
}

func firstRDN(dn string) string {
	value := strings.SplitN(dn, ",", 2)[0]
	parts := strings.SplitN(value, "=", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return value
}

func ldapIntegrationConfig(url, caFile string) config.LDAPAuthConfig {
	return config.LDAPAuthConfig{
		URL:                  url,
		CAFile:               caFile,
		BindDN:               "CN=Dashboard Bind,OU=Service Accounts,DC=example,DC=com",
		BindPassword:         "service-password",
		UserBaseDN:           "OU=People,DC=example,DC=com",
		UserFilter:           "(sAMAccountName={username})",
		UsernameAttribute:    "sAMAccountName",
		SubjectAttribute:     "entryUUID",
		DisplayNameAttribute: "displayName",
		EmailAttribute:       "mail",
		GroupAttribute:       "memberOf",
		ViewerGroups:         []string{"Dashboard Viewers"},
		Timeout:              2 * time.Second,
	}
}

func TestLDAPProviderLDAPSIntegration(t *testing.T) {
	server, ldapURL, caFile := startLDAPIntegrationServer(t, true)
	st := testStoreForAuth(t)
	provider, err := NewLDAPProvider("corporate-ad", config.AuthMatchConfig{}, ldapIntegrationConfig(ldapURL, caFile), st)
	if err != nil {
		t.Fatal(err)
	}
	user, result, err := provider.Authenticate(context.Background(), "alice", "directory-password")
	if err != nil {
		t.Fatal(err)
	}
	if result != ProviderAuthenticated || user.AuthProvider != "corporate-ad" || user.Role != store.RoleViewer {
		t.Fatalf("result = %v, user = %+v", result, user)
	}
	operations := server.operationLog()
	if len(operations) < 3 || operations[0] != "bind:"+server.serviceDN || !strings.HasPrefix(operations[1], "search:") || operations[2] != "bind:"+server.userDN {
		t.Fatalf("LDAP operations = %v", operations)
	}
}

func TestLDAPProviderNestedADGroupIntegration(t *testing.T) {
	server, ldapURL, _ := startLDAPIntegrationServer(t, false)
	server.configure(func(s *ldapIntegrationServer) {
		s.userGroups = nil
		s.nestedGroups = []string{"CN=Dashboard Admins,OU=Groups,DC=example,DC=com"}
	})
	cfg := ldapIntegrationConfig(ldapURL, "")
	cfg.AllowPlaintext = true
	cfg.NestedActiveDirectory = true
	cfg.GroupBaseDN = "OU=Groups,DC=example,DC=com"
	cfg.AdminGroups = []string{"Dashboard Admins"}
	cfg.ViewerGroups = nil
	provider, err := NewLDAPProvider("active-directory", config.AuthMatchConfig{}, cfg, testStoreForAuth(t))
	if err != nil {
		t.Fatal(err)
	}
	user, result, err := provider.Authenticate(context.Background(), "alice", "directory-password")
	if err != nil || result != ProviderAuthenticated || user.Role != store.RoleAdmin {
		t.Fatalf("result = %v, user = %+v, err = %v", result, user, err)
	}
}

func TestLDAPProviderProtocolFailuresAndLocalFallback(t *testing.T) {
	server, ldapURL, _ := startLDAPIntegrationServer(t, false)
	cfg := ldapIntegrationConfig(ldapURL, "")
	cfg.AllowPlaintext = true
	st := testStoreForAuth(t)
	local, err := st.CreateUser("local-admin", "local-password", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewLDAPProvider("corporate-ad", config.AuthMatchConfig{}, cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	a := NewWithProviders(st, "test-secret-key", false, []PasswordProvider{provider})
	user, err := a.AuthenticatePassword(context.Background(), "local-admin", "local-password")
	if err != nil || user.ID != local.ID {
		t.Fatalf("local fallback user = %+v, err = %v", user, err)
	}
	if _, result, err := provider.Authenticate(context.Background(), "alice", "wrong-password"); err != nil || result != ProviderRejected {
		t.Fatalf("bad password result = %v, err = %v", result, err)
	}
	server.configure(func(s *ldapIntegrationServer) { s.userCount = 2 })
	if _, _, err := provider.Authenticate(context.Background(), "alice", "directory-password"); err == nil {
		t.Fatal("expected ambiguous LDAP search error")
	}
	server.configure(func(s *ldapIntegrationServer) {
		s.userCount = 1
		s.searchError = true
	})
	if _, _, err := provider.Authenticate(context.Background(), "alice", "directory-password"); err == nil {
		t.Fatal("expected LDAP search error")
	}
}

func testStoreForAuth(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
