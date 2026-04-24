package inference

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
)

// UsageAccumulator tracks token usage across SSE streaming chunks.
type UsageAccumulator struct {
	InputTokens      int64
	OutputTokens     int64
	ReasoningTokens  int64
	CachedTokens     int64
	TotalTokens      int64
	HasUsage         bool
}

// Accumulate copies usage fields from a chunk if present.
func (a *UsageAccumulator) Accumulate(chunk ChatCompletionChunk) {
	if chunk.Usage == nil {
		return
	}
	a.HasUsage = true
	a.InputTokens = chunk.Usage.PromptTokens
	a.OutputTokens = chunk.Usage.CompletionTokens
	a.TotalTokens = chunk.Usage.TotalTokens
	if chunk.Usage.CompletionTokensDetails != nil {
		a.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
	}
	if chunk.Usage.PromptTokensDetails != nil {
		a.CachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
	}
}

// ToUsageResponse constructs a UsageResponse from accumulated values.
func (a *UsageAccumulator) ToUsageResponse() *UsageResponse {
	u := &UsageResponse{
		PromptTokens:     a.InputTokens,
		CompletionTokens: a.OutputTokens,
		TotalTokens:      a.TotalTokens,
		CompletionTokensDetails: &CompletionTokensDetails{
			ReasoningTokens: a.ReasoningTokens,
		},
		PromptTokensDetails: &PromptTokensDetails{
			CachedTokens: a.CachedTokens,
		},
	}
	return u
}

// executeStreaming runs the full streaming inference lifecycle:
// authorize -> route -> validate -> attempt -> reserve -> dispatch -> relay SSE -> finalize.
func (o *Orchestrator) executeStreaming(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	endpoint string,
	body []byte,
	model string,
	aliasID string,
	needFlags NeedFlags,
	estimatedCredits int64,
	includeUsage bool,
	reasoningEffort *string,
	dispatch dispatchFunc,
) error {
	// 1. Authorize
	authHeader := r.Header.Get("Authorization")
	snapshot, headers, authErr := o.authorizer.Authorize(ctx, authHeader, model, estimatedCredits, 0, 0)
	if authErr != nil {
		status := http.StatusUnauthorized
		if authErr.Error.Type == "insufficient_quota" {
			status = http.StatusTooManyRequests
		} else if authErr.Error.Code != nil && *authErr.Error.Code == "model_not_found" {
			status = http.StatusNotFound
		}
		if authErr.Error.Code != nil && *authErr.Error.Code == "rate_limit_exceeded" {
			apierrors.WriteRateLimitError(w, authErr.Error.Message, authErr.Error.Code, headers)
			return nil
		}
		apierrors.WriteError(w, status, authErr.Error.Type, authErr.Error.Message, authErr.Error.Code)
		return nil
	}

	// 2. Select route
	route, err := o.routing.SelectRoute(ctx, SelectRouteInput{
		AliasID:             model,
		NeedChatCompletions: needFlags.NeedChatCompletions,
		NeedResponses:       needFlags.NeedResponses,
		NeedEmbeddings:      needFlags.NeedEmbeddings,
		NeedStreaming:        needFlags.NeedStreaming,
		NeedReasoning:        needFlags.NeedReasoning,
	})
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found") {
			writeModelNotFoundError(w, model)
			return nil
		}
		if strings.Contains(errMsg, "no eligible") || strings.Contains(errMsg, "capability") {
			code := "capability_mismatch"
			apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("No route supports the requested capabilities for model '%s'.", model), &code)
			return nil
		}
		code := "routing_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error",
			"Failed to select a route for this request.", &code)
		return nil
	}

	// 3. Validate reasoning capability
	// If the route was selected without NeedReasoning, it may not support reasoning.
	routeSupportsReasoning := needFlags.NeedReasoning // route was selected with this requirement
	if !validateReasoningCapability(w, model, reasoningEffort, routeSupportsReasoning) {
		return nil
	}

	// 4. Start attempt
	requestID := uuid.New().String()
	attempt, err := o.accounting.StartAttempt(ctx, StartAttemptInput{
		AccountID:     snapshot.AccountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		Endpoint:      endpoint,
		ModelAlias:    model,
		Status:        "streaming",
		APIKeyID:      snapshot.KeyID,
	})
	if err != nil {
		log.Printf("inference: start attempt failed (non-fatal): %v", err)
	}

	// 5. Create reservation
	reservation, err := o.accounting.CreateReservation(ctx, CreateReservationInput{
		AccountID:        snapshot.AccountID,
		RequestID:        requestID,
		AttemptNumber:    1,
		APIKeyID:         snapshot.KeyID,
		Endpoint:         endpoint,
		ModelAlias:       model,
		EstimatedCredits: estimatedCredits,
		PolicyMode:       "strict",
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "budget") || strings.Contains(err.Error(), "insufficient") {
			code := "insufficient_quota"
			apierrors.WriteError(w, http.StatusTooManyRequests, "insufficient_quota",
				"You exceeded your current quota, please check your plan and billing details.", &code)
			return nil
		}
		log.Printf("inference: create reservation failed (non-fatal): %v", err)
	}

	// Set up defer for reservation cleanup on unexpected exit.
	finalized := false
	accumulator := &UsageAccumulator{}
	defer func() {
		if !finalized && reservation.ID != "" {
			// Customer-favoring: release with whatever tokens we accumulated.
			_ = o.accounting.ReleaseReservation(context.Background(), ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "client_disconnect",
			})
			// Record interrupted usage event.
			o.recordInterruptedEvent(context.Background(), snapshot, attempt, requestID, endpoint, model, accumulator)
		}
	}()

	// 6. Dispatch to LiteLLM with bounded retry on 429/5xx (safe: no bytes
	// have been written to the client yet at this point).
	resp, err := dispatchWithRetry(ctx, route.LiteLLMModelName, body, dispatch)
	if err != nil {
		if reservation.ID != "" {
			_ = o.accounting.ReleaseReservation(ctx, ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "upstream_error",
			})
			finalized = true
		}
		apierrors.WriteProviderBlindUpstreamError(w, model, http.StatusBadGateway, err.Error())
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if reservation.ID != "" {
			_ = o.accounting.ReleaseReservation(ctx, ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "upstream_error",
			})
			finalized = true
		}
		o.recordErrorEvent(ctx, snapshot, attempt, requestID, endpoint, model, resp.StatusCode, string(upstreamBody))
		apierrors.WriteProviderBlindUpstreamError(w, model, resp.StatusCode, string(upstreamBody))
		return nil
	}

	// 7. Assert Flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		if reservation.ID != "" {
			_ = o.accounting.ReleaseReservation(ctx, ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "internal_error",
			})
			finalized = true
		}
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error",
			"Streaming not supported by server.", &code)
		return nil
	}

	// 8. Set SSE headers and commit response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 9. Relay SSE chunks
	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for large chunks
	scanner.Buffer(make([]byte, 64*1024), 512*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "data: [DONE]" {
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			break
		}

		if strings.HasPrefix(line, "data: ") {
			jsonData := line[6:]
			var chunk ChatCompletionChunk
			if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
				// Rewrite model to alias ID
				chunk.Model = aliasID
				// Accumulate usage if present
				accumulator.Accumulate(chunk)
				// Re-marshal sanitized chunk
				sanitized, marshalErr := json.Marshal(chunk)
				if marshalErr == nil {
					fmt.Fprintf(w, "data: %s\n\n", sanitized)
					flusher.Flush()
					continue
				}
			}
			// Fallback: pass through the original line
			fmt.Fprintf(w, "%s\n\n", line)
			flusher.Flush()
			continue
		}

		if line == "" {
			fmt.Fprint(w, "\n")
			flusher.Flush()
			continue
		}

		// Pass through event: lines and other SSE fields
		fmt.Fprintf(w, "%s\n", line)
		flusher.Flush()
	}

	// 10. Synthesize terminal usage chunk if requested but upstream didn't send one
	if includeUsage && !accumulator.HasUsage {
		synth := ChatCompletionChunk{
			ID:      "chatcmpl-" + uuid.New().String(),
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   aliasID,
			Choices: []ChunkChoice{},
			Usage:   accumulator.ToUsageResponse(),
		}
		synthJSON, err := json.Marshal(synth)
		if err == nil {
			fmt.Fprintf(w, "data: %s\n\n", synthJSON)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	// 11. Finalize reservation
	if reservation.ID != "" {
		usage := accumulator.ToUsageResponse()
		actualCredits := estimatedCredits
		if accumulator.HasUsage {
			actualCredits = usage.TotalTokens
		}
		_ = o.accounting.FinalizeReservation(ctx, FinalizeReservationInput{
			AccountID:              snapshot.AccountID,
			ReservationID:          reservation.ID,
			ActualCredits:          actualCredits,
			TerminalUsageConfirmed: accumulator.HasUsage,
			Status:                 "completed",
		})
		finalized = true
	}

	o.recordCompletedEvent(ctx, snapshot, attempt, requestID, endpoint, model, accumulator.ToUsageResponse())

	return nil
}

func (o *Orchestrator) recordInterruptedEvent(ctx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, requestID, endpoint, model string, acc *UsageAccumulator) {
	input := RecordEventInput{
		AccountID:        snapshot.AccountID,
		RequestAttemptID: attempt.ID,
		APIKeyID:         snapshot.KeyID,
		RequestID:        requestID,
		EventType:        "interrupted",
		Endpoint:         endpoint,
		ModelAlias:       model,
		Status:           "interrupted",
	}
	if acc != nil && acc.HasUsage {
		input.InputTokens = acc.InputTokens
		input.OutputTokens = acc.OutputTokens
		input.HiveCreditDelta = acc.TotalTokens
		input.CacheReadTokens = acc.CachedTokens
	}
	_ = o.accounting.RecordUsageEvent(ctx, input)
}
