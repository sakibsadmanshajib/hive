package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// MembershipCheckFunc reports whether userID holds an ACTIVE membership on
// tenantID, joined to a tenant that is not archived. The predicate must match
// the one in public.custom_access_token_hook exactly, so that control-plane and
// the token hook agree on what a live membership is.
//
// Optional. A nil check leaves the previous behaviour in place, which unit
// tests that do not carry a database rely on.
type MembershipCheckFunc func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error)

// Client performs Supabase Auth API calls on behalf of callers.
type Client struct {
	supabaseURL string
	anonKey     string
	httpClient  *http.Client
	// membershipCheck validates the self-asserted selected tenant. See
	// WithMembershipCheck.
	membershipCheck MembershipCheckFunc
}

// NewClient returns a configured Supabase auth client.
func NewClient(supabaseURL, anonKey string) *Client {
	return &Client{
		supabaseURL: supabaseURL,
		anonKey:     anonKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// WithMembershipCheck returns a copy of the client that validates the tenant a
// caller selected before trusting it.
//
// This closes a real cross-tenant read. Viewer.TenantID is sourced from
// user_metadata.selected_tenant_id, and user_metadata is writable by the user
// themselves through GoTrue's PUT /auth/v1/user, so any authenticated caller
// could previously assert an arbitrary tenant id. public.custom_access_token_hook
// validates the same field against ACTIVE memberships before it will put a
// tenant_id claim in a token, but control-plane never read the claim: it read
// the raw metadata and skipped that validation entirely. Every consumer of
// Viewer.TenantID then treated a non-nil value as authoritative, checking only
// that it was not uuid.Nil (see internal/marketplace/http.go serveAdmin and
// internal/featuregate/admin.go requireTenant). The reachable consequence was
// /api/v1/catalog/models, which is OptionalRequire rather than admin gated, so
// an ordinary signed-in user could read another tenant's model visibility list
// by naming that tenant's id.
//
// An unvalidated tenant resolves to uuid.Nil, which every consumer already
// fails closed on: catalog narrows to public aliases only, and the admin
// surfaces return 400. Denying rather than broadening is the invariant.
func (c *Client) WithMembershipCheck(check MembershipCheckFunc) *Client {
	cloned := *c
	cloned.membershipCheck = check
	return &cloned
}

// supabaseUserResponse is the shape returned by GET /auth/v1/user.
type supabaseUserResponse struct {
	ID               string         `json:"id"`
	Email            string         `json:"email"`
	EmailConfirmedAt *string        `json:"email_confirmed_at"`
	AppMetadata      appMetadata    `json:"app_metadata"`
	UserMetadata     userMetadata   `json:"user_metadata"`
}

type userMetadata struct {
	FullName         string `json:"full_name"`
	SelectedTenantID string `json:"selected_tenant_id"`
}

type appMetadata struct {
	HiveEmailVerified *bool `json:"hive_email_verified"`
}

// LookupUser calls GET ${SUPABASE_URL}/auth/v1/user with the caller bearer token
// and returns a resolved Viewer.
func (c *Client) LookupUser(ctx context.Context, bearerToken string) (Viewer, error) {
	url := c.supabaseURL + "/auth/v1/user"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Viewer{}, fmt.Errorf("auth: build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("apikey", c.anonKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Viewer{}, fmt.Errorf("auth: lookup user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return Viewer{}, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return Viewer{}, fmt.Errorf("auth: unexpected status %d from Supabase", resp.StatusCode)
	}

	var su supabaseUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&su); err != nil {
		return Viewer{}, fmt.Errorf("auth: decode user response: %w", err)
	}

	userID, err := uuid.Parse(su.ID)
	if err != nil {
		return Viewer{}, fmt.Errorf("auth: parse user id: %w", err)
	}

	emailVerified := su.EmailConfirmedAt != nil && *su.EmailConfirmedAt != ""
	if su.AppMetadata.HiveEmailVerified != nil {
		emailVerified = *su.AppMetadata.HiveEmailVerified
	}

	// selected_tenant_id is user-mutable metadata, not an authorization claim.
	// Parse it, then confirm the user actually holds a live membership on it
	// before letting it into the Viewer. Anything unverified collapses to
	// uuid.Nil, which every consumer treats as "no tenant" and denies on.
	tenantID := uuid.Nil
	if su.UserMetadata.SelectedTenantID != "" {
		if parsed, err := uuid.Parse(su.UserMetadata.SelectedTenantID); err == nil {
			tenantID = parsed
		}
	}
	if tenantID != uuid.Nil && c.membershipCheck != nil {
		member, err := c.membershipCheck(ctx, userID, tenantID)
		if err != nil {
			// Fail closed. A membership lookup that cannot be completed must
			// not be read as a grant. The caller still gets an authenticated
			// Viewer, just an untenanted one, so account-scoped routes keep
			// working while tenant-scoped routes deny.
			slog.ErrorContext(ctx, "auth: tenant membership check failed, denying selected tenant",
				slog.String("user_id", userID.String()),
				slog.String("err", err.Error()))
			tenantID = uuid.Nil
		} else if !member {
			slog.WarnContext(ctx, "auth: rejected self-asserted tenant with no active membership",
				slog.String("user_id", userID.String()))
			tenantID = uuid.Nil
		}
	}

	return Viewer{
		UserID:        userID,
		TenantID:      tenantID,
		Email:         su.Email,
		EmailVerified: emailVerified,
		FullName:      su.UserMetadata.FullName,
	}, nil
}

// ErrUnauthorized is returned when Supabase rejects the bearer token.
var ErrUnauthorized = fmt.Errorf("auth: unauthorized")
