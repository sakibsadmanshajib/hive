package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type CatalogPricing struct {
	InputPriceCredits      int64  `json:"input_price_credits"`
	OutputPriceCredits     int64  `json:"output_price_credits"`
	CacheReadPriceCredits  *int64 `json:"cache_read_price_credits,omitempty"`
	CacheWritePriceCredits *int64 `json:"cache_write_price_credits,omitempty"`
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

func (c *Client) FetchSnapshot(ctx context.Context) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/catalog/snapshot", nil)
	if err != nil {
		return Snapshot{}, fmt.Errorf("catalog client: build snapshot request: %w", err)
	}

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
