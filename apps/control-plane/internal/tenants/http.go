// Package tenants exposes HTTP handlers for tenant CRUD and the
// authenticated user's "switch active tenant" endpoint. The Switch handler
// is the cross-tenant enforcement point: every membership lookup failure is
// audited as CROSS_TENANT_ATTEMPT at CRITICAL severity, every successful
// switch is audited as TENANT_SWITCH at INFO severity.
//
// See docs/superpowers/plans/2026-05-16-phase-19-02-identity-and-auth.md
// Task 4 for the spec.
package tenants

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// User is the minimal authenticated principal needed by tenant handlers.
// It is injected into request context by the auth middleware and read via
// UserFrom. Phase 18's authz.Actor is the richer RBAC-aware actor; this
// struct intentionally stays narrow so the switch endpoint can run before
// a full Actor (with role/admin overlay) is even resolvable.
//
// TODO(phase-19-plan-03): collapse this duplicate User type with
// apps/edge-api/internal/auth.User. They share a contract by accident —
// Plan 03 introduces a shared identity package both sides depend on.
type User struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	Role     string
}

type ctxKey struct{}

// WithUser returns a derived context carrying the authenticated user.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFrom extracts the authenticated user previously attached via WithUser.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

// Deps holds the runtime dependencies for the tenants handler.
type Deps struct {
	Pool  *pgxpool.Pool
	Audit *audit.Logger
}

// Handler serves /v1/tenants/* routes.
type Handler struct{ deps Deps }

// NewHandler constructs a tenants Handler. Incomplete dependencies are
// logged at construction time so the misconfiguration is observable in
// startup logs; the runtime guard inside Switch still rejects requests
// with 503 if any required dep is nil.
func NewHandler(deps Deps) *Handler {
	if deps.Pool == nil || deps.Audit == nil {
		log.Printf("tenants: NewHandler constructed with incomplete deps (pool=%v audit=%v); Switch will fail-closed with 503",
			deps.Pool != nil, deps.Audit != nil)
	}
	return &Handler{deps: deps}
}

type switchBody struct {
	TenantID uuid.UUID `json:"tenant_id"`
}

// Switch changes the authenticated user's active tenant. The user must have
// an ACTIVE membership in the target tenant; otherwise the attempt is
// recorded as CROSS_TENANT_ATTEMPT at CRITICAL severity and the call
// returns 403 with code CROSS_TENANT.
//
// On success, auth.users.raw_user_meta_data.selected_tenant_id is updated
// and a TENANT_SWITCH audit row is emitted at INFO severity.
func (h *Handler) Switch(w http.ResponseWriter, r *http.Request) {
	// Fail-closed on incomplete deps. Returning 503 keeps the cross-tenant
	// audit invariant intact: we never enter the membership-check path
	// without a working pool + audit logger, so a misconfiguration cannot
	// silently allow a switch nor lose a CROSS_TENANT_ATTEMPT event.
	if h.deps.Pool == nil || h.deps.Audit == nil {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "service unavailable")
		return
	}
	user, ok := UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing user context")
		return
	}
	var body switchBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TenantID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "tenant_id required")
		return
	}

	// Atomic membership-check + metadata-update. A prior SELECT/UPDATE
	// split allowed a TOCTOU window where a concurrently-revoked
	// membership would still get the selected_tenant_id pinned. The
	// WHERE...EXISTS guard ties the write to the membership predicate
	// in a single statement; RowsAffected distinguishes the two
	// outcomes (0 → not a member, 1 → switched).
	tag, err := h.deps.Pool.Exec(r.Context(),
		`UPDATE auth.users
		    SET raw_user_meta_data = COALESCE(raw_user_meta_data, '{}'::jsonb)
		      || jsonb_build_object('selected_tenant_id', $1::text)
		  WHERE id = $2
		    AND EXISTS (
		      SELECT 1 FROM public.tenant_users
		       WHERE user_id   = $2
		         AND tenant_id = $1
		         AND status    = 'ACTIVE'
		    )`,
		body.TenantID, user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "membership lookup failed")
		return
	}
	if tag.RowsAffected() == 0 {
		_ = h.deps.Audit.Log(r.Context(), audit.Event{
			TenantID: user.TenantID,
			Actor:    audit.Actor{ID: user.ID, Type: audit.ActorUser},
			Action:   "CROSS_TENANT_ATTEMPT",
			Severity: audit.SeverityCritical,
			Before:   map[string]string{"requested_tenant_id": body.TenantID.String()},
		})
		writeError(w, http.StatusForbidden, "CROSS_TENANT", "not a member of the requested tenant")
		return
	}

	_ = h.deps.Audit.Log(r.Context(), audit.Event{
		TenantID: body.TenantID,
		Actor:    audit.Actor{ID: user.ID, Type: audit.ActorUser},
		Action:   "TENANT_SWITCH",
		Severity: audit.SeverityInfo,
		Before:   map[string]string{"from": user.TenantID.String()},
		After:    map[string]string{"to": body.TenantID.String()},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg, "type": typeFor(status)},
	})
}

func typeFor(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case status == http.StatusForbidden:
		return "FORBIDDEN"
	case status == http.StatusBadRequest:
		return "INVALID_REQUEST"
	case status >= 500:
		return "INTERNAL"
	default:
		return "INTERNAL"
	}
}
