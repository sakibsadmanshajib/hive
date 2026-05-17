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
	"strings"

	"github.com/google/uuid"
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
