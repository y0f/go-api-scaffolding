// Package auth verifies bearer tokens and resolves them to a Principal that
// carries roles and permissions for authorization checks. It supports JWKS
// (OIDC), a static RSA public key, and an ephemeral development key.
package auth

import "context"

// Principal is the authenticated caller derived from a verified token.
type Principal struct {
	Subject string
	Roles   []string
	Scopes  []string
}

// rolePermissions is the built-in RBAC policy. Replace it with a policy engine
// (OPA, Casbin) by swapping the implementation of HasPermission. Reads are
// public in this scaffold (see api/openapi.yaml), so only write permissions are
// granted here; add read permissions when you protect the read endpoints.
var rolePermissions = map[string][]string{
	"admin":  {"widgets:write"},
	"editor": {"widgets:write"},
}

func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasPermission is true when any of the principal's roles grants the permission
// or the permission is present as an explicit token scope.
func (p Principal) HasPermission(permission string) bool {
	for _, s := range p.Scopes {
		if s == permission {
			return true
		}
	}
	for _, role := range p.Roles {
		for _, granted := range rolePermissions[role] {
			if granted == permission {
				return true
			}
		}
	}
	return false
}

type contextKey struct{}

// holder lets the authenticator publish the resolved principal to the
// downstream handler. The request validator does not propagate a new context,
// so a shared pointer placed in the context before validation is used instead.
type holder struct {
	principal *Principal
}

// withHolder seeds the context with an empty principal holder. Call it before
// the request-validation middleware runs.
func withHolder(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, &holder{})
}

// withPrincipal returns a context carrying p as the authenticated principal, for
// middleware that resolves the principal up front rather than through the
// seed-then-fill OpenAPI validation flow.
func withPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, &holder{principal: &p})
}

func holderFrom(ctx context.Context) (*holder, bool) {
	h, ok := ctx.Value(contextKey{}).(*holder)
	return h, ok
}

// PrincipalFrom returns the authenticated principal, or false if the request
// was not authenticated.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	h, ok := holderFrom(ctx)
	if !ok || h.principal == nil {
		return Principal{}, false
	}
	return *h.principal, true
}
