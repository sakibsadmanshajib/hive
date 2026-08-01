// Package signup resolves and provisions the tenant membership a new sign-up
// belongs to.
//
// Wired at POST /internal/auth/user-created. Supabase Database Webhooks fire on
// auth.users insert; this handler verifies the shared-secret header
// (constant-time compare) and then hands off to Provisioner.Reconcile, which
// owns the resolution and the writes. See reconcile.go for that implementation
// and for why the console has a second, authenticated entry point into it.
//
// Status mapping is chosen for Supabase's retry semantics. A resolved
// membership and a determination that no tenant claims the user are both 204,
// because neither improves on a retry. Only a transient or unexpected fault is
// 500, which is the reply that makes Supabase retry with backoff.
package signup

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// EnsureGroupFunc creates (or returns the id of) an OWUI group with the
// given name. Implementations should be idempotent.
type EnsureGroupFunc func(ctx context.Context, name string) (string, error)

// AddUserFunc adds the given email to the given OWUI group id.
type AddUserFunc func(ctx context.Context, groupID, email string) error

// DisposableCheckFunc reports whether the given email belongs to a disposable
// (throwaway) provider. It is the server-side backstop for issue #116: the
// public web-console precheck is the first line of defence, but a scripted
// signup can write auth.users directly via the Supabase API and trigger this
// webhook without ever calling the precheck. Returning true stops provisioning
// (so no tenant membership and no free-credit grant is created) before any
// database read or write. Optional; nil disables the backstop.
type DisposableCheckFunc func(email string) (bool, error)

// WebhookDeps wires the handler to its collaborators. SharedSecret is
// required; the rest are validated at request time so an unauthorized
// caller is rejected before any nil-pointer panics.
type WebhookDeps struct {
	Pool        *pgxpool.Pool
	Resolver    *Resolver
	EnsureGroup EnsureGroupFunc
	AddUser     AddUserFunc
	Audit       *audit.Logger
	// DisposableCheck is an optional disposable-domain backstop (issue #116).
	DisposableCheck DisposableCheckFunc
	// SelfServeTenants enables personal-tenant provisioning for a signup that
	// no existing tenant claims (issue #625). True on Hive Cloud, where an
	// account signing itself up is an org of one and must end up usable. False
	// on Hive Enterprise, whose posture is that membership is administered, so
	// an unclaimed signup stays at OutcomeNoTenant exactly as before. Wired in
	// cmd/server/main.go from the same switch that already picks
	// licensing.CloudSource over licensing.FileSource, so there is no second
	// source of truth for deployment posture.
	SelfServeTenants bool
	SharedSecret     string
}

// Webhook implements http.Handler for POST /internal/auth/user-created.
type Webhook struct {
	deps WebhookDeps
	prov *Provisioner
}

// NewWebhook constructs a Webhook. Validation of optional deps happens
// inside Reconcile so misconfiguration is observable as a 500 rather
// than a startup panic — the secret check still runs first.
func NewWebhook(deps WebhookDeps) *Webhook {
	return &Webhook{deps: deps, prov: NewProvisioner(deps)}
}

type webhookBody struct {
	UserID      uuid.UUID `json:"user_id"`
	Email       string    `json:"email"`
	InviteToken string    `json:"invite_token,omitempty"`
}

// emailAuditToken returns an opaque, one-way token suitable for audit logs.
// It is the hex-encoded SHA-256 of the lowercased, trimmed email address,
// prefixed with "email_sha256:" so the field is self-describing. The domain
// portion is also included separately so operators can correlate by provider
// without recovering the full address.
//
// Neither the raw local-part nor the full email ever appears in an audit row.
func emailAuditToken(email string) (token, domain string) {
	norm := strings.ToLower(strings.TrimSpace(email))
	sum := sha256.Sum256([]byte(norm))
	token = "email_sha256:" + hex.EncodeToString(sum[:])
	at := strings.LastIndexByte(norm, '@')
	if at >= 0 {
		domain = norm[at+1:]
	}
	return token, domain
}

// ServeHTTP handles the Supabase webhook.
func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Fail-closed on missing shared secret. constant_time_compare("","")
	// returns 1, so an empty SharedSecret would let an unauthenticated
	// caller (also sending empty header) past the auth check. Treat the
	// misconfiguration as a 500 so an operator notices rather than
	// silently exposing the endpoint.
	if h == nil || h.deps.SharedSecret == "" {
		http.Error(w, `{"error":"misconfigured"}`, http.StatusInternalServerError)
		return
	}
	if subtle.ConstantTimeCompare(
		[]byte(r.Header.Get("X-Hive-Signup-Secret")),
		[]byte(h.deps.SharedSecret),
	) != 1 {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var body webhookBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}
	if body.UserID == uuid.Nil || body.Email == "" {
		http.Error(w, `{"error":"missing user_id or email"}`, http.StatusBadRequest)
		return
	}

	// TODO(phase-19-plan-03): persist delivery idempotency key (Supabase
	// webhook id header) so retries cannot double-provision tenant_users.
	// Until then the (tenant_id, user_id) primary key plus ON CONFLICT DO
	// NOTHING is what makes a redelivery harmless.
	_, err := h.prov.Reconcile(r.Context(), ReconcileInput{
		UserID:      body.UserID,
		Email:       body.Email,
		InviteToken: body.InviteToken,
	})
	if err != nil {
		// Reconcile has already audited the classification and logged the raw
		// error. 500 so Supabase retries with backoff.
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	// Provisioned and no-tenant are both terminal for the webhook: retrying
	// neither improves a success nor changes a determination that no tenant
	// claims this address.
	w.WriteHeader(http.StatusNoContent)
}
