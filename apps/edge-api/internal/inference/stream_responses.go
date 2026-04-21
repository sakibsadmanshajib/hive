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
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
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
		status := http.StatusUnauthorized
		if authErr.Error.Type == "insufficient_quota" {
			status = http.StatusTooManyRequests
		} else if authErr.Error.Code != nil && *authErr.Error.Code == "model_not_found" {
			status = http.StatusNotFound
		}
		if authErr.Error.Code != nil && *authErr.Error.Code == "rate_limit_exceeded" {
			apierrors.WriteRateLimitError(w, authErr.Error.Message, authErr.Error.Code, headers)
			return
		}
		apierrors.WriteError(w, status, authErr.Error.Type, authErr.Error.Message, authErr.Error.Code)
		return
	}

	// 2. Select route
	route, err := o.routing.SelectRoute(ctx, SelectRouteInput{
		AliasID:             model,
		NeedResponses:       needFlags.NeedResponses,
		NeedChatCompletions: needFlags.NeedChatCompletions,
		NeedStreaming:        needFlags.NeedStreaming,
		NeedReasoning:        needFlags.NeedReasoning,
	})
	if err != nil {
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
		AccountID:        snapshot.AccountID,
		RequestID:        requestID,
		AttemptNumber:    1,
		APIKeyID:         snapshot.KeyID,
		Endpoint:         EndpointResponses,
		ModelAlias:       model,
		EstimatedCredits: estimatedCredits,
		PolicyMode:       "strict",
	})
	if err != nil {
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "budget") || strings.Contains(err.Error(), "insufficient") {
			code := "insufficient_quota"
			apierrors.WriteError(w, http.StatusTooManyRequests, "insufficient_quota",
				"You exceeded your current quota, please check your plan and billing details.", &code)
			return
		}
		log.Printf("inference: create reservation failed (non-fatal): %v", err)
	}

	// Cleanup defer for client disconnects.
	finalized := false
	acc := &UsageAccumulator{}
	defer func() {
		if !finalized && reservation.ID != "" {
			_ = o.accounting.ReleaseReservation(context.Background(), ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "client_disconnect",
			})
			o.recordInterruptedEvent(context.Background(), snapshot, attempt, requestID, EndpointResponses, model, acc)
		}
	}()

	// 6. Dispatch to LiteLLM (always with stream_options for usage)
	resp, err := o.litellm.ChatCompletion(ctx, route.LiteLLMModelName, body)
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
		return
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
		o.recordErrorEvent(ctx, snapshot, attempt, requestID, EndpointResponses, model, resp.StatusCode, string(upstreamBody))
		apierrors.WriteProviderBlindUpstreamError(w, model, resp.StatusCode, string(upstreamBody))
		return
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
		return
	}

	// 8. Set SSE headers and commit response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 9. Build state machine
	responseID := "resp_" + uuid.New().String()
	msgID := "msg_" + uuid.New().String()
	translator := &responsesEventTranslator{
		responseID: responseID,
		aliasID:    model,
		created:    time.Now().Unix(),
		msgID:      msgID,
	}

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

		// Accumulate usage if present.
		acc.Accumulate(chunk)

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

	// 11. Finalize reservation
	if reservation.ID != "" {
		usage := acc.ToUsageResponse()
		actualCredits := estimatedCredits
		if acc.HasUsage {
			actualCredits = usage.TotalTokens
		}
		_ = o.accounting.FinalizeReservation(ctx, FinalizeReservationInput{
			AccountID:              snapshot.AccountID,
			ReservationID:          reservation.ID,
			ActualCredits:          actualCredits,
			TerminalUsageConfirmed: acc.HasUsage,
			Status:                 "completed",
		})
		finalized = true
	}

	o.recordCompletedEvent(ctx, snapshot, attempt, requestID, EndpointResponses, model, acc.ToUsageResponse())
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
