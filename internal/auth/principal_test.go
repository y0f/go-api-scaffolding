package auth

import "testing"

func TestPrincipalHasPermission(t *testing.T) {
	tests := []struct {
		name       string
		principal  Principal
		permission string
		want       bool
	}{
		{"admin has write", Principal{Roles: []string{"admin"}}, "widgets:write", true},
		{"viewer lacks write", Principal{Roles: []string{"viewer"}}, "widgets:write", false},
		{"viewer has read", Principal{Roles: []string{"viewer"}}, "widgets:read", true},
		{"explicit scope grants", Principal{Scopes: []string{"widgets:write"}}, "widgets:write", true},
		{"empty principal denied", Principal{}, "widgets:read", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.principal.HasPermission(tt.permission); got != tt.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tt.permission, got, tt.want)
			}
		})
	}
}
