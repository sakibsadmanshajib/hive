package images

import (
	"context"
	"errors"
	"fmt"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// ErrAccountNotProvisioned reports an API-key principal whose account has no
// resolvable tenant (AuthResult.TenantID, sourced from
// authz.AuthSnapshot.TenantID, is empty or unparseable). SelectRoute fails
// closed on this before ever calling the routing client, the same way
// inference.Orchestrator.selectRoute does for chat/completions (D-030,
// PR #620): without a tenant the tenant-scoped entitlement check inside
// control-plane's routing.Service.SelectRoute cannot run at all, so admitting
// the request would silently reopen the gap that check exists to close.
var ErrAccountNotProvisioned = errors.New("images: account has no tenant")

// RoutingAdapter adapts *inference.RoutingClient to the images.RoutingInterface.
type RoutingAdapter struct {
	inner *inference.RoutingClient
}

// NewRoutingAdapter wraps an inference.RoutingClient for use with the images Handler.
func NewRoutingAdapter(inner *inference.RoutingClient) *RoutingAdapter {
	return &RoutingAdapter{inner: inner}
}

// SelectRoute calls the routing client with image-specific capability flags.
//
// input.TenantID is bound onto ctx here (via auth.WithUser, the same
// context key inference.RoutingClient.SelectRoute already reads first via
// auth.TenantID) before delegating to the routing client. Without this, the
// images endpoints never put a tenant on ctx at all, so RoutingClient.SelectRoute
// sent an empty tenant_id, which control-plane parsed as uuid.Nil and treated
// as "skip the tenant-scoped entitlement gate", making a tenant-restricted
// alias reachable through /v1/images/* by any API key even though the same
// alias is correctly refused on chat (#623). A key whose account has no
// resolvable tenant fails closed here, before the routing client is ever
// called, rather than falling back to unfiltered access.
func (a *RoutingAdapter) SelectRoute(ctx context.Context, input RouteInput) (RouteResult, error) {
	// authz.ParseTenantID rather than a local uuid.Parse: it is the same
	// account_not_provisioned check and log every other fail-closed path
	// uses (authz.AuthSnapshot.TenantUUID), so this one cannot drift out of
	// step with it the way it already had (found live 2026-08-28). The
	// local ErrAccountNotProvisioned below is kept: this function's error
	// text and type are unchanged, only the check and its visibility are shared.
	tenantID, err := authz.ParseTenantID(input.TenantID, input.AccountID, input.APIKeyID)
	if err != nil {
		return RouteResult{}, fmt.Errorf("images: select route: %w", ErrAccountNotProvisioned)
	}
	ctx = auth.WithUser(ctx, &auth.User{TenantID: tenantID})

	result, err := a.inner.SelectRoute(ctx, inference.SelectRouteInput{
		AliasID:             input.AliasID,
		NeedImageGeneration: input.NeedImageGeneration,
		NeedImageEdit:       input.NeedImageEdit,
	})
	if err != nil {
		return RouteResult{}, fmt.Errorf("images: select route: %w", err)
	}
	return RouteResult{
		AliasID:          result.AliasID,
		LiteLLMModelName: result.LiteLLMModelName,
	}, nil
}
