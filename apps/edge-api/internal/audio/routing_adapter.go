package audio

import (
	"context"
	"fmt"

	"github.com/hivegpt/hive/apps/edge-api/internal/inference"
)

// RoutingAdapter adapts *inference.RoutingClient to the audio.RoutingInterface.
type RoutingAdapter struct {
	inner *inference.RoutingClient
}

// NewRoutingAdapter wraps an inference.RoutingClient for use with the audio Handler.
func NewRoutingAdapter(inner *inference.RoutingClient) *RoutingAdapter {
	return &RoutingAdapter{inner: inner}
}

// SelectRoute calls the routing client with audio-specific capability flags.
func (a *RoutingAdapter) SelectRoute(ctx context.Context, input RouteInput) (RouteResult, error) {
	result, err := a.inner.SelectRoute(ctx, inference.SelectRouteInput{
		AliasID: input.AliasID,
		NeedTTS: input.NeedTTS,
		NeedSTT: input.NeedSTT,
	})
	if err != nil {
		return RouteResult{}, fmt.Errorf("audio: select route: %w", err)
	}
	return RouteResult{
		AliasID:          result.AliasID,
		LiteLLMModelName: result.LiteLLMModelName,
	}, nil
}
