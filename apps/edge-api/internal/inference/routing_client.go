package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SelectRouteInput mirrors the control-plane routing.SelectionInput.
type SelectRouteInput struct {
	AliasID              string `json:"alias_id"`
	NeedResponses        bool   `json:"need_responses"`
	NeedChatCompletions  bool   `json:"need_chat_completions"`
	NeedEmbeddings       bool   `json:"need_embeddings"`
	NeedStreaming         bool   `json:"need_streaming"`
	NeedReasoning        bool   `json:"need_reasoning"`
	AllowedAliases       []string `json:"allowed_aliases,omitempty"`
	AllowedProviders     []string `json:"allowed_providers,omitempty"`
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
func (c *RoutingClient) SelectRoute(ctx context.Context, input SelectRouteInput) (SelectRouteResult, error) {
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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SelectRouteResult{}, fmt.Errorf("routing: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		return SelectRouteResult{}, fmt.Errorf("routing: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result SelectRouteResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return SelectRouteResult{}, fmt.Errorf("routing: decode result: %w", err)
	}

	return result, nil
}
