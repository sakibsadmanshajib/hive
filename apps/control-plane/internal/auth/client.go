package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Client performs Supabase Auth API calls on behalf of callers.
type Client struct {
	supabaseURL string
	anonKey     string
	httpClient  *http.Client
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

// supabaseUserResponse is the shape returned by GET /auth/v1/user.
type supabaseUserResponse struct {
	ID               string         `json:"id"`
	Email            string         `json:"email"`
	EmailConfirmedAt *string        `json:"email_confirmed_at"`
	AppMetadata      appMetadata    `json:"app_metadata"`
	UserMetadata     userMetadata   `json:"user_metadata"`
}

type userMetadata struct {
	FullName string `json:"full_name"`
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

	return Viewer{
		UserID:        userID,
		Email:         su.Email,
		EmailVerified: emailVerified,
		FullName:      su.UserMetadata.FullName,
	}, nil
}

// ErrUnauthorized is returned when Supabase rejects the bearer token.
var ErrUnauthorized = fmt.Errorf("auth: unauthorized")
