package payments

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	xeBaseURL        = "https://xecdapi.xe.com"
	fxCacheKey       = "fx:usd_bdt:mid_rate"
	fxCacheTTL       = 5 * time.Minute
	xeRequestTimeout = 10 * time.Second
)

// FXCache is an abstraction over the rate cache store to allow test substitution.
type FXCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

// redisFXCache wraps *redis.Client to implement FXCache.
type redisFXCache struct {
	client *redis.Client
}

func (r *redisFXCache) Get(ctx context.Context, key string) (string, error) {
	return r.client.Get(ctx, key).Result()
}

func (r *redisFXCache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}

// FXService fetches USD/BDT exchange rates from XE API with cache fallback
// and optional admin override.
type FXService struct {
	httpClient        *http.Client
	xeAccountID       string
	xeAPIKey          string
	cache             FXCache
	adminOverrideRate string
	baseURL           string // overridable for tests
}

// NewFXService creates a production FXService backed by Redis cache.
func NewFXService(httpClient *http.Client, xeAccountID, xeAPIKey string, redisClient *redis.Client) *FXService {
	var cache FXCache
	if redisClient != nil {
		cache = &redisFXCache{client: redisClient}
	}
	return &FXService{
		httpClient:  httpClient,
		xeAccountID: xeAccountID,
		xeAPIKey:    xeAPIKey,
		cache:       cache,
		baseURL:     xeBaseURL,
	}
}

// newFXServiceWithBaseURL creates an FXService with a custom base URL (used in tests).
func newFXServiceWithBaseURL(httpClient *http.Client, xeAccountID, xeAPIKey string, cache FXCache, baseURL string) *FXService {
	return &FXService{
		httpClient:  httpClient,
		xeAccountID: xeAccountID,
		xeAPIKey:    xeAPIKey,
		cache:       cache,
		baseURL:     baseURL,
	}
}

// SetAdminOverride sets or clears the admin override rate.
// Pass an empty string to clear the override.
func (f *FXService) SetAdminOverride(rate string) {
	f.adminOverrideRate = rate
}

// xeConvertResponse is the JSON shape returned by XE convert_from.json.
type xeConvertResponse struct {
	To []struct {
		Quotecurrency string  `json:"quotecurrency"`
		Mid           float64 `json:"mid"`
	} `json:"to"`
}

// FetchUSDToBDT returns the current USD/BDT mid-rate and the data source.
//
// Priority:
//  1. Admin override (if set)
//  2. XE API (cached on success)
//  3. Redis cache fallback
//  4. ErrFXUnavailable
func (f *FXService) FetchUSDToBDT(ctx context.Context) (midRate string, sourceAPI string, err error) {
	// 1. Admin override takes precedence.
	if f.adminOverrideRate != "" {
		return f.adminOverrideRate, "admin_override", nil
	}

	// 2. Try XE API.
	rate, xeErr := f.fetchFromXE(ctx)
	if xeErr == nil {
		// Cache the fresh rate.
		if f.cache != nil {
			_ = f.cache.Set(ctx, fxCacheKey, rate, fxCacheTTL)
		}
		return rate, "xe", nil
	}

	// 3. Fall back to cache.
	if f.cache != nil {
		cached, cacheErr := f.cache.Get(ctx, fxCacheKey)
		if cacheErr == nil && cached != "" {
			return cached, "cache", nil
		}
	}

	return "", "", ErrFXUnavailable
}

// fetchFromXE performs the HTTP request to the XE convert_from API.
func (f *FXService) fetchFromXE(ctx context.Context) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, xeRequestTimeout)
	defer cancel()

	url := fmt.Sprintf("%s/v1/convert_from.json/?from=USD&to=BDT&amount=1", f.baseURL)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("fx: build request: %w", err)
	}
	req.SetBasicAuth(f.xeAccountID, f.xeAPIKey)

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fx: xe request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fx: xe returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("fx: read body: %w", err)
	}

	var xeResp xeConvertResponse
	if err := json.Unmarshal(body, &xeResp); err != nil {
		return "", fmt.Errorf("fx: parse response: %w", err)
	}

	if len(xeResp.To) == 0 {
		return "", fmt.Errorf("fx: no rates in XE response")
	}

	// Format as a clean decimal string.
	rate := fmt.Sprintf("%g", xeResp.To[0].Mid)
	return rate, nil
}

// CreateSnapshot fetches the current rate, computes the effective rate (mid * 1.05),
// persists an FXSnapshot row, and returns it.
func (f *FXService) CreateSnapshot(ctx context.Context, repo Repository, accountID uuid.UUID) (FXSnapshot, error) {
	midRate, sourceAPI, err := f.FetchUSDToBDT(ctx)
	if err != nil {
		return FXSnapshot{}, fmt.Errorf("fx: fetch rate: %w", err)
	}

	// Use math/big to avoid float64 corruption.
	// effectiveRate = midRate * (105/100)
	midRat := new(big.Rat)
	if _, ok := midRat.SetString(midRate); !ok {
		return FXSnapshot{}, fmt.Errorf("fx: invalid mid rate value %q", midRate)
	}
	feeMultiplier := big.NewRat(105, 100)
	effectiveRat := new(big.Rat).Mul(midRat, feeMultiplier)

	// Format to 6 decimal places.
	effectiveFloat, _ := effectiveRat.Float64()
	effectiveRate := fmt.Sprintf("%.6f", effectiveFloat)

	now := time.Now().UTC()
	snap := FXSnapshot{
		ID:            uuid.New(),
		AccountID:     accountID,
		BaseCurrency:  "USD",
		QuoteCurrency: "BDT",
		MidRate:       midRate,
		FeeRate:       FXFeeRate,
		EffectiveRate: effectiveRate,
		SourceAPI:     sourceAPI,
		FetchedAt:     now,
		CreatedAt:     now,
	}

	if err := repo.InsertFXSnapshot(ctx, snap); err != nil {
		return FXSnapshot{}, fmt.Errorf("fx: insert snapshot: %w", err)
	}

	return snap, nil
}
