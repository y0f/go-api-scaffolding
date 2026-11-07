package testutil

import (
	"net/url"
	"strings"
	"testing"
)

func TestDatabaseURLPreservesConnectionButOverridesDatabase(t *testing.T) {
	got, err := databaseURL("postgresql://user:p%40ss@localhost:5432/admin?sslmode=require&database=production&dbname=production", "forge_test_123")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	if u.Path != "/forge_test_123" || u.Host != "localhost:5432" || password != "p@ss" || u.Query().Get("sslmode") != "require" {
		t.Fatalf("connection settings were not preserved: %s", got)
	}
	if u.Query().Has("database") || u.Query().Has("dbname") {
		t.Fatal("admin database query override escaped into an isolated test")
	}
}

func TestDatabaseURLRejectsUnsupportedOrInvalidInputWithoutCredentials(t *testing.T) {
	for _, input := range []string{"host=localhost password=secret", "https://user:secret@localhost/admin", "postgres://user:secret@%invalid/admin", "postgres:///admin"} {
		if _, err := databaseURL(input, "isolated"); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid input must be rejected without echoing credentials: %v", err)
		}
	}
}
