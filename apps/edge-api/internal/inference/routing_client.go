package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

// ErrRouteNotFound wraps a SelectRoute failure caused by the control-plane
// returning 404 (the alias itself does not resolve to any route). Callers
// use errors.Is against this to tell a genuine "unknown model" apart from
// a transport failure or unexpected upstream status, which should surface
// as a provider-blind 5xx rather than 404 (#289 review).
var ErrRouteNotFound = errors.New("routing: alias not found")

// ErrModelNotEntitled wraps a SelectRoute failure caused by the control-plane
// returning 403: the alias resolves, but the requesting tenant is not entitled
// to it (an admin hid it, or it is restricted and the tenant holds no grant).
// Callers surface this as a 403 refusal; folding it into the generic branch
// would report an admin policy decision as a transient routing outage.
var ErrModelNotEntitled = errors.New("routing: model not entitled for tenant")

// SelectRouteInput mirrors the control-plane routing.SelectionInput.
type SelectRouteInput struct {
	AliasID string `json:"alias_id"`
	// TenantID is set by SelectRoute from the authenticated request context.
	// Callers must not populate it; whatever they set is overwritten.
	TenantID            string `json:"tenant_id,omitempty"`
	NeedResponses       bool   `json:"need_responses"`
	NeedChatCompletions bool   `json:"need_chat_completions"`
	NeedEmbeddings      bool   `json:"need_embeddings"`
	NeedStreaming       bool   `json:"need_streaming"`
	NeedReasoning       bool   `json:"need_reasoning"`
	NeedImageGeneration bool   `json:"need_image_generation"`
	NeedImageEdit       bool   `json:"need_image_edit"`
	NeedTTS             bool   `json:"need_tts"`
	NeedSTT             bool   `json:"need_stt"`
	// RequireToolCapable restricts routing to tool-capable routes only.
	RequireToolCapable bool     `json:"require_tool_capable,omitempty"`
	AllowedAliases     []string `json:"allowed_aliases,omitempty"`
	AllowedProviders   []string `json:"allowed_providers,omitempty"`
}

// SelectRouteResult mirrors the control-plane routing.SelectionResult.
type SelectRouteResult struct {
	AliasID          string   `json:"alias_id"`
	RouteID          string   `json:"route_id"`
	LiteLLMModelName string   `json:"litellm_model_name"`
	Provider         string   `json:"provider"`
	FallbackRouteIDs []string `json:"fallback_route_ids"`
}

// RoutingClient calls the control-plane routing endpoint.
type RoutingClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewRoutingClient creates a new RoutingClient.
func NewRoutingClient(baseURL string) *RoutingClient {
	return &RoutingClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// SelectRoute calls POST /internal/routing/select on the control-plane.
//
// The tenant is bound here, from the authenticated request context, rather than
// by each caller. Every JWT-session inference path (chat dispatch, RAG chat,
// audio, images, embeddings) funnels through this method, so binding it once
// means a new transport cannot forget to pass the tenant and silently receive a
// route for a model the tenant is not entitled to. Any TenantID the caller set
// is discarded so a handler cannot widen its own scope.
//
// An API-key request has no JWT principal on the context and therefore sends no
// tenant: api_keys hang off accounts, which are not tenant-scoped, and those
// requests are governed by the key policy allowlist instead.
func (c *RoutingClient) SelectRoute(ctx context.Context, input SelectRouteInput) (SelectRouteResult, error) {
	input.TenantID = ""
	if tenantID := auth.TenantID(ctx); tenantID != uuid.Nil {
		input.TenantID = tenantID.String()
	}

	body, err := json.Marshal(input)
	if err != nil {
		return SelectRouteResult{}, fmt.Errorf("routing: marshal input: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/routing/select", bytes.NewReader(body))
	if err != nil {
		return SelectRouteResult{}, fmt.Errorf("routing: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SelectRouteResult{}, fmt.Errorf("routing: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode == http.StatusNotFound {
		return SelectRouteResult{}, fmt.Errorf("%w: %s", ErrRouteNotFound, strings.TrimSpace(string(respBody)))
	}
	if resp.StatusCode == http.StatusForbidden {
		return SelectRouteResult{}, fmt.Errorf("%w: alias %s", ErrModelNotEntitled, input.AliasID)
	}
	if resp.StatusCode != http.StatusOK {
		return SelectRouteResult{}, fmt.Errorf("routing: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result SelectRouteResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return SelectRouteResult{}, fmt.Errorf("routing: decode result: %w", err)
	}

	return result, nil
}
