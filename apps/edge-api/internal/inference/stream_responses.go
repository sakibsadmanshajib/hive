package inference

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// responsesEventTranslator tracks state for translating chat-completions SSE chunks
// into Responses API lifecycle events.
type responsesEventTranslator struct {
	responseID       string
	aliasID          string
	created          int64
	started          bool
	outputItemAdded  bool
	contentPartAdded bool
	outputItems      []ResponseOutputItem
	currentContent   strings.Builder
	usageAccumulator UsageAccumulator
	finishReason     *string
	msgID            string
}

// executeResponsesStreaming runs the Responses API streaming lifecycle:
// authorize -> route -> attempt -> reserve -> dispatch -> translate events -> finalize.
func (o *Orchestrator) executeResponsesStreaming(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	req ResponsesRequest,
	model string,
	needFlags NeedFlags,
	estimatedCredits int64,
) {
	// 1. Authorize
	authHeader := r.Header.Get("Authorization")
	snapshot, headers, authErr := o.authorizer.Authorize(ctx, authHeader, model, estimatedCredits, 0, 0)
	if authErr != nil {
		// Third copy of this switch found during PR #903 security review
		// (orchestrator.go and stream.go had already been fixed to call the
		// shared helper; this one was missed). Route through
		// apierrors.WriteAuthFailure like every other Authorize call site
		// instead of a third copy of its logic.
		apierrors.WriteAuthFailure(w, authErr, headers)
		return
	}

	// 2. Select route
	route, err := o.selectRoute(ctx, snapshot, SelectRouteInput{
		AliasID:             model,
		NeedResponses:       needFlags.NeedResponses,
		NeedChatCompletions: needFlags.NeedChatCompletions,
		NeedStreaming:       needFlags.NeedStreaming,
		NeedReasoning:       needFlags.NeedReasoning,
	})
	if err != nil {
		if errors.Is(err, ErrAccountNotProvisioned) {
			writeAccountNotProvisionedError(w)
			return
		}
		if errors.Is(err, ErrModelNotEntitled) {
			writeModelNotEntitledError(w, model)
			return
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found") {
			writeModelNotFoundError(w, model)
			return
		}
		if strings.Contains(errMsg, "no eligible") || strings.Contains(errMsg, "capability") {
			code := "capability_mismatch"
			apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("No route supports the requested capabilities for model '%s'.", model), &code)
			return
		}
		code := "routing_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error",
			"Failed to select a route for this request.", &code)
		return
	}

	// 3. Validate reasoning capability
	if !validateResponsesReasoningCapability(w, model, req.Reasoning, needFlags.NeedReasoning) {
		return
	}

	// 3b. Refuse an alias this endpoint cannot price, before any hold is taken
	// and before a provider is ever reached (#688, D-034).
	if !requireTokenPricing(w, route, EndpointResponses, model) {
		return
	}

	// 3c. Bound the request for a variable-price alias, before dispatch. Its
	// hold is only provably sufficient below a known request size and a known
	// completion ceiling; see EnforceVariablePriceBounds. A pass-through for
	// every fixed-price alias.
	boundedBody, withinBounds := EnforceVariablePriceBounds(w, route, EndpointResponses, model, body)
	if !withinBounds {
		return
	}
	body = boundedBody

	// Reasoning headroom, same contract as the sync path's step 2d (issue
	// #1171): inflate the ceiling fields present by the pool reserve so
	// hidden reasoning spends the reserve. Applied before the reservation and
	// before any byte reaches the client. Headroom only here; the sync-path
	// zero-content guard does not apply mid-stream.
	if headroomBody, inflated := applyReasoningHeadroom(body, EndpointResponses, route.ReasoningReserveTokens); inflated {
		body = headroomBody
	}

	// 4. Start attempt
	requestID := uuid.New().String()
	attempt, err := o.accounting.StartAttempt(ctx, StartAttemptInput{
		AccountID:     snapshot.AccountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		Endpoint:      EndpointResponses,
		ModelAlias:    model,
		Status:        "streaming",
		APIKeyID:      snapshot.KeyID,
	})
	if err != nil {
		log.Printf("inference: start attempt failed (non-fatal): %v", err)
	}

	// 5. Create reservation
	reservation, err := o.accounting.CreateReservation(ctx, CreateReservationInput{
		AccountID:     snapshot.AccountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		APIKeyID:      snapshot.KeyID,
		Endpoint:      EndpointResponses,
		ModelAlias:    model,
		// A variable-price alias raises this from its catalog row; a fixed
		// one keeps the flat endpoint default. See ReservationCredits.
		EstimatedCredits: ReservationCredits(route, estimatedCredits),
		PolicyMode:       "strict",
	})
	if err != nil && refuseOnReservationFailure(w, EndpointResponses, model, err) {
		return
	}

	// Set up defer for reservation settlement. This is the single settlement
	// site for a streaming request: it always runs, whether the stream ends
	// normally or the client disconnects mid-stream, and it always dispatches
	// on its own background context (see settleStream) rather than the ctx
	// this function was called with. translator is declared here (before its
	// fields are filled in at step 9) so the defer can read its accumulated
	// content regardless of which exit path fires.
	finalized := false
	acc := &UsageAccumulator{}
	translator := &responsesEventTranslator{aliasID: model}
	defer func() {
		if finalized {
			return
		}
		finalized = o.settleStream(ctx, snapshot, attempt, reservation, route, requestID, EndpointResponses, model, acc, string(body), translator.currentContent.String())
	}()

	// 6. Dispatch to LiteLLM (always with stream_options for usage) with
	// bounded retry on 429/5xx; safe because no bytes have been written
	// to the client yet at this point.
	resp, err := dispatchWithRetry(ctx, route.LiteLLMModelName, body, o.litellm.ChatCompletion)
	if err != nil {
		if reservation.ID != "" {
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, "upstream_error")
		}
		apierrors.WriteProviderBlindUpstreamError(w, model, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if reservation.ID != "" {
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, "upstream_error")
		}
		o.recordErrorEvent(ctx, snapshot, attempt, requestID, EndpointResponses, model, resp.StatusCode, string(upstreamBody))
		apierrors.WriteProviderBlindUpstreamError(w, model, resp.StatusCode, string(upstreamBody))
		return
	}

	// 7. Assert Flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		if reservation.ID != "" {
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, "internal_error")
		}
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error",
			"Streaming not supported by server.", &code)
		return
	}

	// 8. Set SSE headers and commit response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 9. Build state machine (translator was declared earlier, before the
	// settlement defer, so fill in the remaining fields here).
	translator.responseID = "resp_" + uuid.New().String()
	translator.created = time.Now().Unix()
	translator.msgID = "msg_" + uuid.New().String()

	writeSSEEvent := func(eventType string, data any) {
		dataJSON, err := json.Marshal(data)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, dataJSON)
		flusher.Flush()
	}

	// 10. Scan and translate upstream SSE chunks
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 512*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "data: [DONE]" {
			// Emit response.completed event instead of [DONE].
			translator.emitCompleted(w, flusher, acc, req)
			break
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		jsonData := line[6:]
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			continue
		}

		// A parsed frame is about to be relayed (translated) to the caller, so
		// delivery is proven even when nothing accumulates -- a tool-call-only
		// Responses turn used to release free as upstream_error (#1215).
		acc.HasForwardedChunk = true

		// Clamp upstream-zero completion_tokens against the content streamed
		// so far. Usage chunks typically arrive last with empty choices, so
		// translator.currentContent already holds the full response body.
		if chunk.Usage != nil {
			clampZeroCompletionUsage(chunk.Usage, []string{translator.currentContent.String()}, chunk.ID, model, EndpointResponses)
			// Same capture as executeStreaming. Without it this path can only
			// ever fail closed for a variable-price alias, because settleStream
			// reads these bytes and ChatCompletionChunk has already discarded
			// the cost field on the way in.
			if route.Pricing.IsUpstreamActual() {
				acc.RawUsageChunk = append(acc.RawUsageChunk[:0], jsonData...)
			}
		}

		// Accumulate usage if present.
		acc.Accumulate(chunk, model)

		// Emit response.created on first chunk.
		if !translator.started {
			translator.started = true
			inProgressResp := translator.buildPartialResponse("in_progress", nil, nil)
			writeSSEEvent("response.created", map[string]any{
				"type":     "response.created",
				"response": inProgressResp,
			})
		}

		// Process choice deltas.
		for _, choice := range chunk.Choices {
			// Emit output_item.added and content_part.added on first content.
			if choice.Delta.Content != nil && !translator.outputItemAdded {
				translator.outputItemAdded = true
				translator.contentPartAdded = true

				writeSSEEvent("response.output_item.added", map[string]any{
					"type":         "response.output_item.added",
					"output_index": 0,
					"item": map[string]any{
						"type":    "message",
						"id":      translator.msgID,
						"status":  "in_progress",
						"role":    "assistant",
						"content": []any{},
					},
				})

				writeSSEEvent("response.content_part.added", map[string]any{
					"type":          "response.content_part.added",
					"output_index":  0,
					"content_index": 0,
					"part": map[string]any{
						"type":        "output_text",
						"text":        "",
						"annotations": []any{},
					},
				})
			}

			// Emit content delta.
			if choice.Delta.Content != nil {
				deltaText := *choice.Delta.Content
				translator.currentContent.WriteString(deltaText)

				writeSSEEvent("response.output_text.delta", map[string]any{
					"type":          "response.output_text.delta",
					"output_index":  0,
					"content_index": 0,
					"delta":         deltaText,
				})
			}

			// Emit done events on finish_reason.
			if choice.FinishReason != nil {
				translator.finishReason = choice.FinishReason
				accumulatedText := translator.currentContent.String()

				writeSSEEvent("response.content_part.done", map[string]any{
					"type":          "response.content_part.done",
					"output_index":  0,
					"content_index": 0,
					"part": map[string]any{
						"type":        "output_text",
						"text":        accumulatedText,
						"annotations": []any{},
					},
				})

				writeSSEEvent("response.output_item.done", map[string]any{
					"type":         "response.output_item.done",
					"output_index": 0,
					"item": map[string]any{
						"type":   "message",
						"id":     translator.msgID,
						"status": "completed",
						"role":   "assistant",
						"content": []map[string]any{
							{
								"type":        "output_text",
								"text":        accumulatedText,
								"annotations": []any{},
							},
						},
					},
				})
			}
		}
	}
}

// emitCompleted emits the response.completed event with the full response object.
func (t *responsesEventTranslator) emitCompleted(w http.ResponseWriter, flusher http.Flusher, acc *UsageAccumulator, req ResponsesRequest) {
	accumulatedText := t.currentContent.String()

	var outputItems []ResponseOutputItem
	if t.outputItemAdded {
		outputItems = []ResponseOutputItem{
			{
				Type:   "message",
				ID:     t.msgID,
				Status: "completed",
				Role:   "assistant",
				Content: []ResponseContentPart{
					{
						Type:        "output_text",
						Text:        accumulatedText,
						Annotations: []json.RawMessage{},
					},
				},
			},
		}
	} else {
		outputItems = []ResponseOutputItem{}
	}

	var respUsage *ResponsesUsage
	if acc.HasUsage {
		usage := acc.ToUsageResponse()
		respUsage = chatToResponsesUsage(usage)
	}

	nullJSON := json.RawMessage(`null`)
	emptyTools := json.RawMessage(`[]`)
	truncation := "disabled"

	completedResp := ResponseObject{
		ID:                t.responseID,
		Object:            "response",
		CreatedAt:         t.created,
		Model:             t.aliasID,
		Status:            "completed",
		Output:            outputItems,
		Usage:             respUsage,
		Reasoning:         nullJSON,
		Metadata:          nullJSON,
		MaxOutputTokens:   req.MaxOutputTokens,
		Truncation:        &truncation,
		Tools:             emptyTools,
		IncompleteDetails: nullJSON,
		Error:             nullJSON,
		Temperature:       req.Temperature,
		TopP:              req.TopP,
	}

	dataJSON, err := json.Marshal(map[string]any{
		"type":     "response.completed",
		"response": completedResp,
	})
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", dataJSON)
	flusher.Flush()
}

// buildPartialResponse builds an in-progress ResponseObject for the response.created event.
func (t *responsesEventTranslator) buildPartialResponse(status string, outputItems []ResponseOutputItem, usage *ResponsesUsage) ResponseObject {
	if outputItems == nil {
		outputItems = []ResponseOutputItem{}
	}
	nullJSON := json.RawMessage(`null`)
	emptyTools := json.RawMessage(`[]`)
	truncation := "disabled"

	return ResponseObject{
		ID:                t.responseID,
		Object:            "response",
		CreatedAt:         t.created,
		Model:             t.aliasID,
		Status:            status,
		Output:            outputItems,
		Usage:             usage,
		Reasoning:         nullJSON,
		Metadata:          nullJSON,
		Truncation:        &truncation,
		Tools:             emptyTools,
		IncompleteDetails: nullJSON,
		Error:             nullJSON,
	}
}
