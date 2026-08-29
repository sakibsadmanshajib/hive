package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

// Model mirrors control-plane's catalog.PublicModel across the HTTP boundary
// and is re-serialised verbatim as one entry of GET /v1/models.
//
// Name and Description are the alias's display name and one-sentence summary.
// They are additive fields outside the OpenAI contract, carried so the chat
// model picker can show what each alias is for instead of a bare slug; see the
// control-plane type for the full reasoning. Both are `omitempty` in both
// directions, so an alias with neither produces the exact payload this endpoint
// served before they existed.
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created"`
	OwnedBy     string `json:"owned_by"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// CatalogPricing mirrors control-plane's catalog.CatalogPricing across the
// HTTP boundary.
//
// The input and output prices are POINTERS because a variable-price alias
// genuinely has none and control-plane sends JSON null for both.
//
// This is load-bearing, and for the opposite reason to the database side.
// Scanning a SQL NULL into a non-pointer int64 is a hard pgx error, so that
// boundary fails loudly on its own. encoding/json does NOT behave that way:
// unmarshalling JSON null into a non-pointer int64 is a documented no-op that
// leaves the field at its zero value and returns no error. Verified against
// Go's own decoder on 2026-08-22 rather than assumed.
//
// So with a plain int64 here, a variable-price alias would have arrived
// silently priced at 0 credits, which is indistinguishable from free and is
// exactly the shape that billed nothing for three days in July. The pointer is
// what makes the absence visible.
type CatalogPricing struct {
	InputPriceCredits      *int64 `json:"input_price_credits"`
	OutputPriceCredits     *int64 `json:"output_price_credits"`
	CacheReadPriceCredits  *int64 `json:"cache_read_price_credits,omitempty"`
	CacheWritePriceCredits *int64 `json:"cache_write_price_credits,omitempty"`
	PricingMode            string `json:"pricing_mode"`
}

type CatalogModel struct {
	ID               string         `json:"id"`
	DisplayName      string         `json:"display_name"`
	Summary          string         `json:"summary"`
	CapabilityBadges []string       `json:"capability_badges"`
	Pricing          CatalogPricing `json:"pricing"`
	Lifecycle        string         `json:"lifecycle"`
}

type Snapshot struct {
	Models  []Model        `json:"models"`
	Catalog []CatalogModel `json:"catalog"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return &Client{
		baseURL: trimmed,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// FetchSnapshot returns the global catalog snapshot (no tenant filtering).
func (c *Client) FetchSnapshot(ctx context.Context) (Snapshot, error) {
	return c.fetchSnapshot(ctx, "/internal/catalog/snapshot")
}

// FetchSnapshotForTenant returns the snapshot filtered to one tenant's model
// entitlement, so a model list matches what that tenant may actually invoke.
// The tenant travels as a path segment; the endpoint is shared-secret gated.
func (c *Client) FetchSnapshotForTenant(ctx context.Context, tenantID uuid.UUID) (Snapshot, error) {
	return c.fetchSnapshot(ctx, "/internal/catalog/snapshot/tenant/"+tenantID.String())
}

func (c *Client) fetchSnapshot(ctx context.Context, path string) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("catalog client: build snapshot request: %w", err)
	}
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("catalog client: fetch snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Snapshot{}, fmt.Errorf("catalog client: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var snapshot Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("catalog client: decode snapshot: %w", err)
	}

	if snapshot.Models == nil {
		snapshot.Models = []Model{}
	}
	if snapshot.Catalog == nil {
		snapshot.Catalog = []CatalogModel{}
	}

	// Customer-facing boundary: everything below this line is re-serialised
	// verbatim by GET /v1/models and GET /catalog/models, so the guard sits
	// here, on the single funnel both handlers share, rather than in either
	// handler (issue #1284; see providerblind.go).
	return redactSnapshot(snapshot), nil
}
