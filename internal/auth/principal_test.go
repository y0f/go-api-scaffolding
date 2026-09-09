package auth

import "testing"

func TestPrincipalHasPermission(t *testing.T) {
	saved := rolePermissions
	rolePermissions = map[string][]string{"editor": {"things:write"}}
	t.Cleanup(func() { rolePermissions = saved })

	tests := []struct {
		name       string
		principal  Principal
		permission string
		want       bool
	}{
		{"role grants", Principal{Roles: []string{"editor"}}, "things:write", true},
		{"unknown role lacks", Principal{Roles: []string{"viewer"}}, "things:write", false},
		{"explicit scope grants", Principal{Scopes: []string{"things:write"}}, "things:write", true},
		{"empty principal denied", Principal{}, "things:write", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.principal.HasPermission(tt.permission); got != tt.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tt.permission, got, tt.want)
			}
		})
	}
}
