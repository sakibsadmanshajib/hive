package images

import (
	"context"
	"fmt"

	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
)

// RoutingAdapter adapts *inference.RoutingClient to the images.RoutingInterface.
type RoutingAdapter struct {
	inner *inference.RoutingClient
}

// NewRoutingAdapter wraps an inference.RoutingClient for use with the images Handler.
func NewRoutingAdapter(inner *inference.RoutingClient) *RoutingAdapter {
	return &RoutingAdapter{inner: inner}
}

// SelectRoute calls the routing client with image-specific capability flags.
func (a *RoutingAdapter) SelectRoute(ctx context.Context, input RouteInput) (RouteResult, error) {
	result, err := a.inner.SelectRoute(ctx, inference.SelectRouteInput{
		AliasID:             input.AliasID,
		NeedImageGeneration: input.NeedImageGeneration,
		NeedImageEdit:       input.NeedImageEdit,
	})
	if err != nil {
		return RouteResult{}, fmt.Errorf("images: select route: %w", err)
	}
	return RouteResult{
		AliasID:          result.AliasID,
		LiteLLMModelName: result.LiteLLMModelName,
	}, nil
}
