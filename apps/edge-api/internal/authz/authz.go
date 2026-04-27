package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AuthSnapshot is the edge-side representation of the control-plane-projected
// authorization data. Matches the control-plane's AuthSnapshot JSON schema.
type AuthSnapshot struct {
	KeyID                 string      `json:"key_id"`
	AccountID             string      `json:"account_id"`
	Status                string      `json:"status"`
	ExpiresAt             *string     `json:"expires_at,omitempty"`
	AllowAllModels        bool        `json:"allow_all_models"`
	AllowedAliases        []string    `json:"allowed_aliases"`
	BudgetKind            string      `json:"budget_kind"`
	BudgetLimitCredits    *int64      `json:"budget_limit_credits,omitempty"`
	BudgetConsumedCredits int64       `json:"budget_consumed_credits"`
	BudgetReservedCredits int64       `json:"budget_reserved_credits"`
	BudgetAnchorAt        *string     `json:"budget_anchor_at,omitempty"`
	AccountRatePolicy     *RatePolicy `json:"account_rate_policy,omitempty"`
	KeyRatePolicy         *RatePolicy `json:"key_rate_policy,omitempty"`
	PolicyVersion         int64       `json:"policy_version"`
}

// RatePolicy is the edge-side rate-limit projection for one scope.
type RatePolicy struct {
	RateLimitRPM          int                       `json:"rate_limit_rpm"`
	RateLimitTPM          int                       `json:"rate_limit_tpm"`
	RollingFiveHourLimit  int64                     `json:"rolling_five_hour_limit"`
	WeeklyLimit           int64                     `json:"weekly_limit"`
	FreeTokenWeightTenths int                       `json:"free_token_weight_tenths"`
	TierOverrides         map[string]TierOverridePol `json:"tier_overrides,omitempty"`
}

// TierOverridePol is the edge-side projection of a per-tier RPM/TPM override
// supplied by the control-plane via api_key_rate_policies.tier_overrides.
type TierOverridePol struct {
	RPM int `json:"rpm"`
	TPM int `json:"tpm"`
}

// Resolver fetches AuthSnapshots from the control plane.
type Resolver struct {
	controlPlaneBaseURL string
	httpClient          *http.Client
}

// NewResolver creates a new Resolver.
func NewResolver(controlPlaneBaseURL string) *Resolver {
	return &Resolver{
		controlPlaneBaseURL: strings.TrimRight(controlPlaneBaseURL, "/"),
		httpClient:          &http.Client{Timeout: 5 * time.Second},
	}
}

// Resolve sends a token hash to the control plane's internal resolver
// and returns the projected AuthSnapshot.
func (r *Resolver) Resolve(ctx context.Context, tokenHash string) (AuthSnapshot, error) {
	body := fmt.Sprintf(`{"token_hash":%q}`, tokenHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.controlPlaneBaseURL+"/internal/apikeys/resolve",
		strings.NewReader(body),
	)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("authz: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return AuthSnapshot{}, fmt.Errorf("authz: resolve: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return AuthSnapshot{}, fmt.Errorf("authz: resolve status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var snapshot AuthSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return AuthSnapshot{}, fmt.Errorf("authz: decode snapshot: %w", err)
	}

	return snapshot, nil
}

// CheckResult holds the outcome of an authorization check.
type CheckResult struct {
	Allowed  bool
	DenyCode string
	DenyMsg  string
	Snapshot AuthSnapshot
}

// CheckAccess validates a request against the auth snapshot.
// Returns a CheckResult indicating whether the request is allowed.
func CheckAccess(snapshot AuthSnapshot, requestedAlias string, estimatedCredits int64) CheckResult {
	// 1. Key must be active.
	if snapshot.Status != "active" {
		return CheckResult{
			Allowed:  false,
			DenyCode: "invalid_api_key",
			DenyMsg:  fmt.Sprintf("API key is %s", snapshot.Status),
		}
	}

	// 2. Check expiry.
	if snapshot.ExpiresAt != nil {
		exp, err := time.Parse(time.RFC3339, *snapshot.ExpiresAt)
		if err == nil && exp.Before(time.Now()) {
			return CheckResult{
				Allowed:  false,
				DenyCode: "invalid_api_key",
				DenyMsg:  "API key has expired",
			}
		}
	}

	// 3. Check model access (skip if all models allowed).
	if !snapshot.AllowAllModels && requestedAlias != "" {
		allowed := false
		for _, a := range snapshot.AllowedAliases {
			if a == "*" || a == requestedAlias {
				allowed = true
				break
			}
		}
		if !allowed {
			return CheckResult{
				Allowed:  false,
				DenyCode: "model_not_allowed",
				DenyMsg:  fmt.Sprintf("Model '%s' is not allowed by this API key's policy", requestedAlias),
			}
		}
	}

	// 4. Check budget (if applicable).
	if snapshot.BudgetKind != "none" && snapshot.BudgetLimitCredits != nil {
		used := snapshot.BudgetConsumedCredits + snapshot.BudgetReservedCredits + estimatedCredits
		if used > *snapshot.BudgetLimitCredits {
			return CheckResult{
				Allowed:  false,
				DenyCode: "budget_exceeded",
				DenyMsg:  "API key budget has been exceeded",
			}
		}
	}

	return CheckResult{
		Allowed:  true,
		Snapshot: snapshot,
	}
}
