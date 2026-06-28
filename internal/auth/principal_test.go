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
		{"editor has write", Principal{Roles: []string{"editor"}}, "widgets:write", true},
		{"unknown role lacks write", Principal{Roles: []string{"viewer"}}, "widgets:write", false},
		{"explicit scope grants", Principal{Scopes: []string{"widgets:write"}}, "widgets:write", true},
		{"empty principal denied", Principal{}, "widgets:write", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.principal.HasPermission(tt.permission); got != tt.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tt.permission, got, tt.want)
			}
		})
	}
}
