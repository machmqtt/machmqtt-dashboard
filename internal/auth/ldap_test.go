package auth

import (
	"testing"

	ldap "github.com/go-ldap/ldap/v3"
	"github.com/noodlebit/machmqtt-dashboard/internal/config"
)

func TestLDAPDomainMatching(t *testing.T) {
	provider := &LDAPProvider{match: config.AuthMatchConfig{Domains: []string{"example.com"}}}
	if !provider.matches("alice@example.com") {
		t.Fatal("matching UPN domain was rejected")
	}
	if provider.matches("alice@other.example") {
		t.Fatal("non-matching UPN domain was accepted")
	}
	if !provider.matches("alice") {
		t.Fatal("unqualified username should be looked up in provider order")
	}
}

func TestLDAPSubjectEncoding(t *testing.T) {
	textEntry := &ldap.Entry{Attributes: []*ldap.EntryAttribute{{Name: "entryUUID", ByteValues: [][]byte{[]byte("uuid-123")}}}}
	if got := ldapSubject(textEntry, "entryUUID"); got != "uuid-123" {
		t.Errorf("text subject = %q", got)
	}
	binaryEntry := &ldap.Entry{Attributes: []*ldap.EntryAttribute{{Name: "objectGUID", ByteValues: [][]byte{{0x00, 0xff, 0x10}}}}}
	if got := ldapSubject(binaryEntry, "objectGUID"); got != "AP8Q" {
		t.Errorf("binary subject = %q", got)
	}
}
