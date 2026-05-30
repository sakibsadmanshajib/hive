// Package identity exposes the authenticated "finalize email verification"
// endpoint (issue #112).
//
// Background: the web-console /auth/callback edge route used to hold
// SUPABASE_SERVICE_ROLE_KEY and PUT auth.users app_metadata directly. That put
// a god-key in a public edge bundle and failed *silently* when the key was
// absent (the write was guarded by `if (process.env.SUPABASE_SERVICE_ROLE_KEY)`
// and `.catch(() => undefined)`), so email verification quietly never persisted.
//
// This handler moves the privileged write into the control-plane, which already
// holds a service-role-privileged DB pool. The edge only forwards the user's
// session bearer; the handler flips hive_email_verified for the *authenticated*
// caller and only when Supabase has already confirmed the email
// (email_confirmed_at IS NOT NULL — enforced by the injected write), so a caller
// cannot self-verify an unconfirmed address. Any failure is loud (500), never a
// silent no-op.
package identity

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// FinalizeFunc sets hive_email_verified=true for the user IFF Supabase has
// already confirmed the email, returning the number of rows affected (0 means
// the email is not confirmed or the user does not exist). It is the only path
// that touches the service-role-privileged pool, so it is injected to keep the
// handler unit-testable without a database.
type FinalizeFunc func(ctx context.Context, userID uuid.UUID) (int64, error)

// Deps wires the handler. Audit is optional (best-effort); FinalizeEmailVerified
// is required and its absence is treated as a loud misconfiguration.
type Deps struct {
	Audit                 *audit.Logger
	FinalizeEmailVerified FinalizeFunc
}

// Handler serves POST /api/v1/accounts/current/email-verification/finalize.
type Handler struct{ deps Deps }

// NewHandler constructs the finalize handler.
func NewHandler(deps Deps) *Handler { return &Handler{deps: deps} }

// ServeHTTP finalizes email verification for the authenticated viewer.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	// Fail loud on misconfiguration rather than silently reporting success —
	// the exact failure mode that #112 set out to eliminate.
	if h == nil || h.deps.FinalizeEmailVerified == nil {
		log.Print("identity: finalize handler invoked without FinalizeEmailVerified wired (misconfigured)")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "service unavailable"})
		return
	}

	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok || viewer.UserID == uuid.Nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing user context"})
		return
	}

	rows, err := h.deps.FinalizeEmailVerified(r.Context(), viewer.UserID)
	if err != nil {
		// Full error to the process log only; the client gets a generic message
		// (it may embed SQL/DSN fragments). Audit carries a classification only.
		log.Printf("identity: finalize email verification failed user=%s: %v", viewer.UserID, err)
		h.audit(r.Context(), viewer.UserID, "EMAIL_VERIFY_FINALIZE_ERROR", audit.SeverityError,
			map[string]string{"error": "finalize_write"})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to finalize verification"})
		return
	}
	if rows == 0 {
		// Supabase has not confirmed the email (or the user is unknown). Never
		// treat this as success — that would let a caller self-verify.
		h.audit(r.Context(), viewer.UserID, "EMAIL_VERIFY_NOT_CONFIRMED", audit.SeverityWarning, nil)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email is not confirmed"})
		return
	}

	h.audit(r.Context(), viewer.UserID, "EMAIL_VERIFIED", audit.SeverityInfo, nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) audit(ctx context.Context, userID uuid.UUID, action string, sev audit.Severity, before map[string]string) {
	if h.deps.Audit == nil {
		return
	}
	_ = h.deps.Audit.Log(ctx, audit.Event{
		Actor:    audit.Actor{ID: userID, Type: audit.ActorUser},
		Action:   action,
		Severity: sev,
		Before:   before,
	})
}

func writeJSON(w http.ResponseWriter, status int, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
