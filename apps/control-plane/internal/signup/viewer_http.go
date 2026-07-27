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
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// ViewerHandler serves the authenticated reconcile endpoint.
type ViewerHandler struct{ prov *Provisioner }

// NewViewerHandler constructs the handler over the same Provisioner the webhook
// uses, so there is exactly one implementation of the provisioning write.
func NewViewerHandler(prov *Provisioner) *ViewerHandler { return &ViewerHandler{prov: prov} }

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
