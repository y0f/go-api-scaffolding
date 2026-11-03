package main

import (
	"os"
	"strings"
	"testing"
)

// migrate only needs FORGE_DB_URL, so unrelated API settings (auth in
// production, the admin token) must not stop it before it reaches the database.
func TestMigrateIgnoresUnrelatedConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
	}{
		{"production without auth", map[string]string{"FORGE_ENV": "production"}},
		{"admin enabled without token", map[string]string{"FORGE_ADMIN_ENABLED": "true"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FORGE_DB_URL", "postgres://x:y@127.0.0.1:1/db")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			old := os.Args
			os.Args = []string{"migrate", "status"}
			defer func() { os.Args = old }()
			err := run()
			if err != nil && !strings.Contains(err.Error(), "run \"status\"") {
				t.Fatalf("migrate failed on non-database config before connecting: %v", err)
			}
		})
	}
}
