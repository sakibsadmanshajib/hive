// Package authz — cross-tenant guard.
//
// RequireOwnTenant compares the resolved tenant on the request context
// (set by the auth middleware via auth.WithUser) against a requested
// tenant id from the request path/body. On mismatch it invokes an
// audit callback with the action label "CROSS_TENANT_ATTEMPT" and
// returns ErrForbidden. The callback shape avoids importing the audit
// package here to prevent an import cycle; the HTTP boundary wires
// audit.Log into the closure.
package authz

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// ErrForbidden is returned when the requested tenant does not match
// the authenticated principal's tenant, or when no tenant is resolved
// on the context.
var ErrForbidden = errors.New("authz: forbidden")

// AuditFunc is the minimal callback shape so this package stays free
// of a dependency on the audit package. The caller wires the real
// audit emitter at the HTTP boundary.
type AuditFunc func(action string)

// RequireOwnTenant fails when the request's tenant does not match the
// caller's resolved tenant id, or when either id is uuid.Nil. It calls
// auditFn with "CROSS_TENANT_ATTEMPT" on denial; the caller is
// responsible for emitting the actual audit row.
func RequireOwnTenant(ctx context.Context, requested uuid.UUID, auditFn AuditFunc) error {
	resolved := auth.TenantID(ctx)
	if resolved != uuid.Nil && resolved == requested {
		return nil
	}
	if auditFn != nil {
		auditFn("CROSS_TENANT_ATTEMPT")
	}
	return ErrForbidden
}
