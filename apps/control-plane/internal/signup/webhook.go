// Package signup webhook handler.
//
// Wired at POST /internal/auth/user-created. Supabase Database Webhooks
// fire on auth.users insert; this handler:
//  1. verifies the shared-secret header (constant-time compare);
//  2. resolves the tenant (invite token → email domain);
//  3. inserts tenant_users(MEMBER, ACTIVE) idempotently;
//  4. provisions the OWUI group + adds the user;
//  5. emits audit-log entries for each stage.
//
// On NO_TENANT we reply 204 (success to Supabase) after auditing a
// AUTH_SIGNIN_FAILURE_NO_TENANT — retrying the webhook would not help.
// On provision failure we reply 500 so Supabase retries.
package signup

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
// database write. Optional; nil disables the backstop.
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
	SharedSecret    string
}

// Webhook implements http.Handler for POST /internal/auth/user-created.
type Webhook struct{ deps WebhookDeps }

// NewWebhook constructs a Webhook. Validation of optional deps happens
// inside ServeHTTP so misconfiguration is observable as a 500 rather
// than a startup panic — the secret check still runs first.
func NewWebhook(deps WebhookDeps) *Webhook { return &Webhook{deps: deps} }

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

	ctx := r.Context()
	emailToken, emailDomain := emailAuditToken(body.Email)

	// Disposable-domain backstop (issue #116). Runs before any DB write so a
	// throwaway signup never reaches tenant provisioning or a free-credit grant.
	// Reply 204 (terminal) so Supabase does not retry — the address will not
	// become eligible on a later attempt. The audit detail carries a hash token
	// and domain only, never the raw email. A check error is treated as "not
	// disposable" and logged, so a bug in the list never blocks all signups
	// (availability over a soft abuse signal at this backstop layer).
	if h.deps.DisposableCheck != nil {
		blocked, checkErr := h.deps.DisposableCheck(body.Email)
		if checkErr != nil {
			log.Printf("signup: disposable check error user=%s: %v", body.UserID, checkErr)
		} else if blocked {
			if h.deps.Audit != nil {
				_ = h.deps.Audit.Log(ctx, audit.Event{
					Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
					Action:   "AUTH_SIGNUP_DISPOSABLE_BLOCKED",
					Severity: audit.SeverityWarning,
					Before:   map[string]string{"stage": "disposable", "email_sha256": emailToken, "domain": emailDomain},
				})
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	if h.deps.Pool == nil || h.deps.Resolver == nil || h.deps.Audit == nil {
		http.Error(w, `{"error":"misconfigured"}`, http.StatusInternalServerError)
		return
	}
	// TODO(phase-19-plan-03): persist delivery idempotency key (Supabase
	// webhook id header) so retries cannot double-provision tenant_users.
	tenantID, err := h.deps.Resolver.Resolve(ctx, Input{Email: body.Email, InviteToken: body.InviteToken})
	if err != nil {
		// Distinguish "no eligible tenant" (terminal — 204 so Supabase
		// stops retrying) from any other resolver failure (transient or
		// programmer error — 500 so Supabase retries with backoff and
		// the operator sees an ERROR audit entry).
		if errors.Is(err, ErrNoMatch) {
			_ = h.deps.Audit.Log(ctx, audit.Event{
				Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
				Action:   "AUTH_SIGNIN_FAILURE_NO_TENANT",
				Severity: audit.SeverityWarning,
				Before:   map[string]string{"email_sha256": emailToken, "domain": emailDomain},
			})
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Audit payload carries a fixed classification string only — the
		// real error (which may embed SQL fragments, DSN substrings, or
		// upstream provider details) is wrapped and surfaces via the
		// process log below. auditor_ro must not read raw pgx/fmt errors.
		log.Printf("signup: resolver error user=%s: %v", body.UserID, fmt.Errorf("resolver_transient: %w", err))
		_ = h.deps.Audit.Log(ctx, audit.Event{
			Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
			Action:   "AUTH_SIGNIN_FAILURE_NO_TENANT",
			Severity: audit.SeverityError,
			Before:   map[string]string{"email_sha256": emailToken, "domain": emailDomain, "stage": "resolver_error", "error": "resolver_transient"},
		})
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}

	if err := h.provision(ctx, tenantID, body, emailToken, emailDomain); err != nil {
		// See comment above — class only in audit, full error to log.
		log.Printf("signup: provision failed user=%s tenant=%s: %v", body.UserID, tenantID, fmt.Errorf("provision_db: %w", err))
		_ = h.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
			Action:   "AUTH_SIGNUP_SUCCESS",
			Severity: audit.SeverityError,
			Before:   map[string]string{"email_sha256": emailToken, "domain": emailDomain, "stage": "provision_failed", "error": "provision_db"},
		})
		http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Webhook) provision(ctx context.Context, tenantID uuid.UUID, body webhookBody, emailToken, emailDomain string) error {
	_, err := h.deps.Pool.Exec(ctx, `
		INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		VALUES ($1, $2, 'MEMBER', 'ACTIVE')
		ON CONFLICT DO NOTHING
	`, tenantID, body.UserID)
	if err != nil {
		return fmt.Errorf("insert tenant_users: %w", err)
	}

	_ = h.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
		Action:   "AUTH_SIGNUP_SUCCESS",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"email_sha256": emailToken, "domain": emailDomain},
	})
	_ = h.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
		Action:   "TENANT_USER_ADD",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"role": "MEMBER"},
	})

	if h.deps.EnsureGroup == nil || h.deps.AddUser == nil {
		log.Printf("signup: OWUI provisioning skipped (deps not configured) user=%s tenant=%s",
			body.UserID, tenantID)
		return nil
	}
	groupName := "tenant_" + tenantID.String()
	groupID, err := h.deps.EnsureGroup(ctx, groupName)
	if err != nil {
		// Audit payload carries the classification only; the raw OWUI
		// upstream error (which may echo back Authorization headers on
		// some 401/403 paths) goes to the process log, never to
		// auditor_ro-readable rows.
		log.Printf("signup: owui ensure group tenant=%s: %v", tenantID, fmt.Errorf("owui_ensure_group: %w", err))
		_ = h.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Action:   "OWUI_GROUP_CREATE_FAILURE",
			Severity: audit.SeverityError,
			Before:   map[string]string{"name": groupName, "error": "owui_ensure_group"},
		})
		return fmt.Errorf("ensure group: %w", err)
	}
	if err := h.deps.AddUser(ctx, groupID, body.Email); err != nil {
		log.Printf("signup: owui add user tenant=%s group=%s: %v", tenantID, groupID, fmt.Errorf("owui_add_user: %w", err))
		_ = h.deps.Audit.Log(ctx, audit.Event{
			TenantID: tenantID,
			Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
			Action:   "OWUI_GROUP_ADD_FAILURE",
			Severity: audit.SeverityError,
			Before:   map[string]string{"group_id": groupID, "email_sha256": emailToken, "domain": emailDomain, "error": "owui_add_user"},
		})
		return fmt.Errorf("add user: %w", err)
	}
	_ = h.deps.Audit.Log(ctx, audit.Event{
		TenantID: tenantID,
		Actor:    audit.Actor{ID: body.UserID, Type: audit.ActorUser},
		Action:   "OWUI_GROUP_ADD_SUCCESS",
		Severity: audit.SeverityInfo,
		After:    map[string]string{"group_id": groupID, "email_sha256": emailToken, "domain": emailDomain},
	})
	return nil
}
