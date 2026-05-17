package auth

import (
	"context"

	"github.com/google/uuid"
)

// User is the authenticated principal attached to a request context.
// Email may be empty when the principal is identified by a hashed
// API key rather than a Supabase JWT.
type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Role     string
	Email    string
}

type ctxKey struct{}

// WithUser returns a derived context carrying the principal. Callers
// downstream of authentication middleware MUST go through this helper
// rather than stuffing fields onto context with raw string keys.
//
// The principal is shallow-copied into a freshly allocated *User so
// post-call mutation by the caller cannot alter the authenticated
// identity mid-request. A nil input is forwarded as-is so UserFrom
// reports the missing-principal case consistently.
func WithUser(ctx context.Context, u *User) context.Context {
	if u == nil {
		return context.WithValue(ctx, ctxKey{}, (*User)(nil))
	}
	snapshot := *u
	return context.WithValue(ctx, ctxKey{}, &snapshot)
}

// UserFrom returns the principal previously stored by WithUser. The
// boolean reports whether a User was found (independent of whether the
// underlying pointer is nil).
func UserFrom(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*User)
	return u, ok
}

// TenantID returns the tenant id on the request context. Callers MUST go
// through this getter; reading tenant_id from any other source is blocked
// by lint (tools/lint-no-direct-tenant-id.mjs).
func TenantID(ctx context.Context) uuid.UUID {
	if u, ok := UserFrom(ctx); ok && u != nil {
		return u.TenantID
	}
	return uuid.Nil
}
