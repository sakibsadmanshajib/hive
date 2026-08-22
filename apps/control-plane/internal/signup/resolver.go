// Package signup resolves the tenant a new sign-in/sign-up belongs to.
//
// Two strategies are tried in priority order:
//  1. Invite token — explicit user choice (Phase 19 invite flow).
//  2. Email-domain mapping — EnterpriseEdge default (tenant.domain).
//
// If neither resolves, ErrNoMatch is returned and the caller should reject
// the sign-in with NO_TENANT until an administrator invites the user.
package signup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoMatch indicates neither the invite token nor the email domain
// mapped to a known tenant. Callers should treat this as NO_TENANT.
var ErrNoMatch = errors.New("signup: no tenant match")

// Input captures the signals available at sign-in time.
type Input struct {
	Email       string
	InviteToken string
}

// LookupFunc resolves a single key (invite token or email domain) to a
// tenant id. It must return ErrNoMatch when no row is found and any other
// error for transient/unexpected failures (so the resolver can surface
// them instead of silently falling through).
type LookupFunc func(ctx context.Context, key string) (uuid.UUID, error)

// ResolverDeps is the dependency surface — kept as plain function fields
// so tests can stub each strategy without an interface ceremony.
type ResolverDeps struct {
	InviteLookup LookupFunc
	DomainLookup LookupFunc
}

// Resolver picks the tenant id for a sign-up/sign-in attempt.
type Resolver struct {
	deps ResolverDeps
}

// NewResolver constructs a Resolver. Either lookup may be nil; a nil
// strategy is simply skipped.
func NewResolver(deps ResolverDeps) *Resolver { return &Resolver{deps: deps} }

// Resolve picks the tenant id in priority order: invite token first
// (explicit user choice), then email-domain mapping (EnterpriseEdge
// default), then ErrNoMatch (sign-in is rejected with NO_TENANT until
// an admin invites the user).
//
// Non-ErrNoMatch errors short-circuit so transient lookup failures are
// not masked by the fallback.
func (r *Resolver) Resolve(ctx context.Context, in Input) (uuid.UUID, error) {
	if in.InviteToken != "" && r.deps.InviteLookup != nil {
		id, err := r.deps.InviteLookup(ctx, in.InviteToken)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, ErrNoMatch) {
			return uuid.Nil, err
		}
	}
	if r.deps.DomainLookup != nil {
		if at := strings.IndexByte(in.Email, '@'); at >= 0 && at < len(in.Email)-1 {
			domain := strings.ToLower(in.Email[at+1:])
			id, err := r.deps.DomainLookup(ctx, domain)
			if err == nil {
				return id, nil
			}
			if !errors.Is(err, ErrNoMatch) {
				return uuid.Nil, err
			}
		}
	}
	return uuid.Nil, ErrNoMatch
}

// Message formats for the pgx-backed lookups below. Kept as constants next to
// their only call sites so the wrapped error text is auditable in one place.
const (
	errInviteLookup = "signup invite lookup: %w"
	errDomainLookup = "signup domain lookup: %w"
)

// inviteLookupQuery resolves an unconsumed, unexpired invite token to its
// tenant. Expiry and consumption are part of the predicate rather than checked
// afterwards, so a stale token resolves to nothing at all.
const inviteLookupQuery = `
	SELECT tenant_id
	  FROM public.tenant_invites
	 WHERE token = $1
	   AND consumed_at IS NULL
	   AND expires_at > now()
`

// domainLookupQuery maps a registered email domain to its tenant.
//
// REGISTERED, not verified. Nothing proves the tenant that claimed a domain
// controls its DNS zone or any mailbox in it, and this query is the whole of the
// check. That is safe only because writing to public.tenant_email_domains is an
// administrator operation: migration 20260822_01 revoked INSERT and DELETE from
// `authenticated`, after PR #993 made the sweep attach identities to a claimed
// domain automatically. Before that revocation any signed-in user could claim
// `gmail.com` on their own personal tenant, since `domain` is the primary key and
// claims were first come first served, and thereafter every gmail.com signup
// would have been given a membership in a stranger's tenant.
//
// If self-service domain registration is ever wanted, it needs a real ownership
// proof (a DNS TXT record or a challenge to postmaster@) in front of the insert.
// Do not reintroduce the grant without one.
const domainLookupQuery = `
	SELECT tenant_id
	  FROM public.tenant_email_domains
	 WHERE domain = $1
`

// NewPgxResolver builds the production Resolver over a pgx pool.
//
// It lives here rather than in cmd/server so that every caller resolves a
// tenant with the same SQL. There are three provisioning entry points now (the
// legacy Supabase webhook, the console route and the reconciler sweep), and a
// test or a second binary that hand-rolled these two queries would be free to
// drift from the predicate the running server actually applies.
//
// Only "no eligible row" collapses to ErrNoMatch. A transient database failure
// surfaces, so the webhook answers 500 and Supabase retries, and the sweep
// counts a fault rather than recording a terminal no-tenant determination.
func NewPgxResolver(pool *pgxpool.Pool) *Resolver {
	return NewResolver(ResolverDeps{
		InviteLookup: pgxLookup(pool, inviteLookupQuery, errInviteLookup),
		DomainLookup: pgxLookup(pool, domainLookupQuery, errDomainLookup),
	})
}

func pgxLookup(pool *pgxpool.Pool, query, wrap string) LookupFunc {
	return func(ctx context.Context, key string) (uuid.UUID, error) {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, query, key).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, ErrNoMatch
			}
			return uuid.Nil, fmt.Errorf(wrap, err)
		}
		return id, nil
	}
}
