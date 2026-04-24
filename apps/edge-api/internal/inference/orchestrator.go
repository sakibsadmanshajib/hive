package inference

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
)

// Orchestrator coordinates the inference request lifecycle.
type Orchestrator struct {
	authorizer *authz.Authorizer
	routing    *RoutingClient
	accounting *AccountingClient
	litellm    *LiteLLMClient
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(authorizer *authz.Authorizer, routing *RoutingClient, accounting *AccountingClient, litellm *LiteLLMClient) *Orchestrator {
	return &Orchestrator{
		authorizer: authorizer,
		routing:    routing,
		accounting: accounting,
		litellm:    litellm,
	}
}

// dispatchFunc dispatches a request to LiteLLM and returns the raw response.
type dispatchFunc func(ctx context.Context, litellmModel string, body []byte) (*http.Response, error)

// normalizeFunc normalizes a LiteLLM response: strips provider fields, extracts usage.
type normalizeFunc func(respBody []byte, aliasID string) ([]byte, *UsageResponse, error)

// executeSync runs the full non-streaming inference lifecycle:
// authorize -> route -> attempt -> reserve -> dispatch -> normalize -> finalize -> respond.
func (o *Orchestrator) executeSync(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	endpoint string,
	body []byte,
	model string,
	needFlags NeedFlags,
	estimatedCredits int64,
	dispatch dispatchFunc,
	normalize normalizeFunc,
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

	// 3. Start attempt
	requestID := uuid.New().String()
	attempt, err := o.accounting.StartAttempt(ctx, StartAttemptInput{
		AccountID:     snapshot.AccountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		Endpoint:      endpoint,
		ModelAlias:    model,
		Status:        "dispatching",
		APIKeyID:      snapshot.KeyID,
	})
	if err != nil {
		log.Printf("inference: start attempt failed (non-fatal): %v", err)
	}

	// 4. Create reservation
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
			return
		}
		log.Printf("inference: create reservation failed (non-fatal): %v", err)
	}

	// Ensure reservation cleanup if we return without finalizing.
	finalized := false
	defer func() {
		if !finalized && reservation.ID != "" {
			_ = o.accounting.ReleaseReservation(context.Background(), ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "interrupted",
			})
		}
	}()

	// 5. Dispatch to LiteLLM with bounded retry on 429/5xx.
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
		o.recordErrorEvent(ctx, snapshot, attempt, requestID, endpoint, model, resp.StatusCode, string(upstreamBody))
		apierrors.WriteProviderBlindUpstreamError(w, model, resp.StatusCode, string(upstreamBody))
		return
	}

	// 6. Read response body
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		if reservation.ID != "" {
			_ = o.accounting.ReleaseReservation(ctx, ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "read_error",
			})
			finalized = true
		}
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	// 7. Normalize
	normalized, usage, err := normalize(respBody, model)
	if err != nil {
		if reservation.ID != "" {
			_ = o.accounting.ReleaseReservation(ctx, ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        "normalize_error",
			})
			finalized = true
		}
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to process upstream response.", &code)
		return
	}

	// 8. Finalize reservation and record usage
	if reservation.ID != "" {
		actualCredits := estimatedCredits
		if usage != nil {
			actualCredits = usage.TotalTokens
		}
		_ = o.accounting.FinalizeReservation(ctx, FinalizeReservationInput{
			AccountID:              snapshot.AccountID,
			ReservationID:          reservation.ID,
			ActualCredits:          actualCredits,
			TerminalUsageConfirmed: true,
			Status:                 "completed",
		})
		finalized = true
	}

	o.recordCompletedEvent(ctx, snapshot, attempt, requestID, endpoint, model, usage)

	// 9. Write response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(normalized)
}

func (o *Orchestrator) recordErrorEvent(ctx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, requestID, endpoint, model string, statusCode int, errBody string) {
	_ = o.accounting.RecordUsageEvent(ctx, RecordEventInput{
		AccountID:        snapshot.AccountID,
		RequestAttemptID: attempt.ID,
		APIKeyID:         snapshot.KeyID,
		RequestID:        requestID,
		EventType:        "error",
		Endpoint:         endpoint,
		ModelAlias:       model,
		Status:           fmt.Sprintf("upstream_%d", statusCode),
		ErrorCode:        fmt.Sprintf("%d", statusCode),
		ErrorType:        "upstream_error",
	})
}

func (o *Orchestrator) recordCompletedEvent(ctx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, requestID, endpoint, model string, usage *UsageResponse) {
	input := RecordEventInput{
		AccountID:        snapshot.AccountID,
		RequestAttemptID: attempt.ID,
		APIKeyID:         snapshot.KeyID,
		RequestID:        requestID,
		EventType:        "completed",
		Endpoint:         endpoint,
		ModelAlias:       model,
		Status:           "completed",
	}
	if usage != nil {
		input.InputTokens = usage.PromptTokens
		input.OutputTokens = usage.CompletionTokens
		if usage.PromptTokensDetails != nil {
			input.CacheReadTokens = usage.PromptTokensDetails.CachedTokens
		}
		input.HiveCreditDelta = usage.TotalTokens
	}
	_ = o.accounting.RecordUsageEvent(ctx, input)
}
