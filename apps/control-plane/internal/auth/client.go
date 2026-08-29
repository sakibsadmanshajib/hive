package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
	ID               string       `json:"id"`
	Email            string       `json:"email"`
	EmailConfirmedAt *string      `json:"email_confirmed_at"`
	AppMetadata      appMetadata  `json:"app_metadata"`
	UserMetadata     userMetadata `json:"user_metadata"`
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

	// Tenant scope, in order of preference.
	//
	// First the tenant_id claim on the bearer. public.custom_access_token_hook
	// resolves it inside the database from one snapshot of ACTIVE tenant_users
	// joined to non-archived tenants, honouring selected_tenant_id only when it
	// appears in that snapshot and otherwise falling back to the first active
	// membership, and omits the claim entirely for a user with none. It is
	// therefore already validated at issue time, and the token carrying it is
	// signed by GoTrue.
	//
	// Reading it here is safe specifically because this line sits below the
	// status check above: Supabase has just accepted this exact token and
	// resolved su.ID from it, which is the signature check. tenantClaimFromToken
	// itself verifies nothing, so it must never be lifted above that point or
	// called on a token from anywhere but the validated Authorization header.
	// The sub match below binds the claim to the user Supabase returned.
	//
	// Then selected_tenant_id, kept because a control-plane may serve a token
	// issued before the hook existed. That field is user-mutable metadata, not
	// an authorization claim: it is writable through GoTrue's PUT /auth/v1/user.
	//
	// Either way the membership check below decides. Anything unverified
	// collapses to uuid.Nil, which every consumer treats as "no tenant" and
	// denies on.
	tenantID := tenantClaimFromToken(bearerToken, userID)
	if tenantID == uuid.Nil && su.UserMetadata.SelectedTenantID != "" {
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

// tenantClaims is the subset of the access token payload control-plane reads.
// role, tenants and owui_role are also present and deliberately not read here:
// authorization decisions belong to the services that own them, and a second
// reader of role would compete with platform.TenantRoleService.
type tenantClaims struct {
	Sub      string `json:"sub"`
	TenantID string `json:"tenant_id"`
}

// tenantClaimFromToken returns the tenant_id claim carried by bearerToken when
// that claim is a uuid and the token's sub is expectedSub. It returns uuid.Nil
// for every other input, including a bearer that is not a JWT at all.
//
// UNVERIFIED READ. This function checks no signature and must never be the
// thing that decides a token is genuine. Its only caller reads it after
// Supabase has accepted the same token through GET /auth/v1/user and returned
// the user id passed as expectedSub, so the authenticity of the token, and the
// binding of this claim to that user, are both already established. Calling it
// anywhere else would let a caller mint their own tenant scope.
func tenantClaimFromToken(bearerToken string, expectedSub uuid.UUID) uuid.UUID {
	segments := strings.Split(bearerToken, ".")
	if len(segments) != 3 {
		return uuid.Nil
	}

	// GoTrue emits unpadded base64url; the padded variant of the same alphabet
	// is accepted as well rather than assumed away, because a decode failure
	// here silently drops the tenant and reads as a permission bug rather than
	// a parse bug. StdEncoding is deliberately not tried: its `+` and `/`
	// alphabet is not valid in a JWT segment, so accepting it would only widen
	// what this parses without widening what GoTrue can have issued.
	payload, err := base64.RawURLEncoding.DecodeString(segments[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(segments[1])
		if err != nil {
			return uuid.Nil
		}
	}

	var claims tenantClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return uuid.Nil
	}

	// Compare as parsed uuids, not as raw strings. Both sides are authored by
	// Supabase for any token that reaches this line, so a case or padding
	// difference between GoTrue's claim rendering and PostgREST's user-id
	// rendering is not an attack, but it would silently strand a legitimate
	// tenant. Parsing normalises that away. The failure direction is unchanged
	// either way: a mismatch denies.
	sub, err := uuid.Parse(claims.Sub)
	if err != nil || sub != expectedSub {
		return uuid.Nil
	}

	tenantID, err := uuid.Parse(claims.TenantID)
	if err != nil {
		return uuid.Nil
	}
	return tenantID
}
