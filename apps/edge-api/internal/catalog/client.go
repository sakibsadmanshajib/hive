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

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// CatalogPricing mirrors control-plane's catalog.CatalogPricing across the
// HTTP boundary.
//
// The input and output prices are POINTERS because a variable-price alias
// genuinely has none and control-plane sends JSON null for both. This is not
// cosmetic: a plain int64 target makes encoding/json reject the whole payload,
// and this struct is decoded as part of the catalog snapshot that backs
// /v1/models, so one nulled alias would have taken down model listing for every
// model rather than just its own.
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

	return snapshot, nil
}
