package signup

// POST /api/v1/viewer/tenant-provision
//
// The console-driven entry into Provisioner.Reconcile. It exists so tenant
// membership provisioning is guaranteed by code that ships with this
// repository, rather than by a Supabase Database Webhook an operator has to
// remember to create in a dashboard. The webhook path stays wired and
// unchanged; this is the belt to its braces.
//
// Security posture. This endpoint is the ONLY thing a token with no tenant
// claim is meant to be able to do, so its input surface is deliberately
// minimal:
//
//   - The identity comes exclusively from auth.ViewerFromContext, which the
//     auth middleware populates only after Supabase has validated the bearer
//     token. Nothing is read from the request body, and a body is not even
//     decoded, so a caller cannot name a different user.
//   - No tenant id is accepted. The tenant is resolved server side from an
//     unconsumed invite token or a registered email domain, so a caller cannot
//     name a tenant to join.
//   - No invite token is accepted either. Accepting one here would let any
//     signed-in user attach themselves to any tenant whose token they obtained;
//     invitation redemption keeps its own audited endpoint.
//   - The response is one of two enum values. It never echoes a tenant id, a
//     tenant name, or a count, so it cannot be used to probe which tenants or
//     domains exist.
//
// The endpoint is therefore safe to expose to a tenant-less principal, which is
// the whole point: that principal has no other reachable surface.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// AllowFunc reports whether the given subject may make another provisioning
// attempt. A non-nil return rejects the attempt. Both over-quota and
// limiter-unavailable map to the same retryable 429, matching how
// internal/signupguard/http.go treats its own limiter, so the caller does not
// have to distinguish them and this package stays free of a signupguard import.
//
// Optional. A nil AllowFunc disables throttling, which is what unit tests and
// any deployment without Redis get.
type AllowFunc func(ctx context.Context, subject string) error

// ViewerHandler serves the authenticated reconcile endpoint.
type ViewerHandler struct {
	prov  *Provisioner
	allow AllowFunc
}

// NewViewerHandler constructs the handler over the same Provisioner the webhook
// uses, so there is exactly one implementation of the provisioning write.
//
// allow throttles per user id. Provisioning is a write path, so it must not be
// hammerable by an authenticated caller even though each call is idempotent and
// individually cheap.
func NewViewerHandler(prov *Provisioner, allow AllowFunc) *ViewerHandler {
	return &ViewerHandler{prov: prov, allow: allow}
}

type tenantProvisionResponse struct {
	Status Outcome `json:"status"`
}

// ServeHTTP implements http.Handler. Mount behind the auth middleware's
// Require, which guarantees a validated Supabase token.
func (h *ViewerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeViewerJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeViewerJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	// A validated Supabase user always has an id; an address can in principle
	// be absent (phone-only signup), and resolution is email-domain based, so
	// treat a missing address as an unresolvable tenant rather than an error.
	if viewer.Email == "" {
		writeViewerJSON(w, http.StatusOK, tenantProvisionResponse{Status: OutcomeNoTenant})
		return
	}

	// Throttle on the authenticated user id rather than the client IP. The
	// caller is authenticated, so the user id is the stable subject, and it
	// cannot be rotated by moving networks. Deliberately after the auth and
	// method checks so an unauthenticated or wrong-method request cannot
	// consume somebody's quota.
	if h.allow != nil {
		if err := h.allow(r.Context(), viewer.UserID.String()); err != nil {
			slog.WarnContext(r.Context(), "signup: viewer reconcile throttled",
				slog.String("user_id", viewer.UserID.String()))
			w.Header().Set("Retry-After", "60")
			writeViewerJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many requests"})
			return
		}
	}

	outcome, err := h.prov.Reconcile(r.Context(), ReconcileInput{
		UserID: viewer.UserID,
		Email:  viewer.Email,
		// InviteToken intentionally left empty. See the package comment above.
	})
	if err != nil {
		// Reconcile has already audited the classification and logged the raw
		// error. The client gets a fixed string: this response reaches an
		// unprovisioned browser session, so it must not carry SQL fragments,
		// DSN substrings, or upstream provider detail.
		slog.ErrorContext(r.Context(), "signup: viewer reconcile failed",
			slog.String("user_id", viewer.UserID.String()),
			slog.String("err", err.Error()))
		writeViewerJSON(w, http.StatusInternalServerError, map[string]string{"error": "provisioning unavailable"})
		return
	}

	writeViewerJSON(w, http.StatusOK, tenantProvisionResponse{Status: outcome})
}

func writeViewerJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
