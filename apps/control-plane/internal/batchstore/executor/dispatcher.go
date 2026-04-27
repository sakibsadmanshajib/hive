package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// InferencePort is a small interface that the dispatcher depends on.
// In production the implementation is liteLLMInferenceClient (calls LiteLLM
// /v1/chat/completions); in tests it is a fake. See DECISIONS.md Q1 for why
// the dispatcher does not import edge-api/internal/inference.
type InferencePort interface {
	// ChatCompletion executes a single chat-completions request. The body is
	// the raw JSON the customer supplied for that batch line; the model field
	// inside is rewritten to the LiteLLM model name by the implementation.
	// Returns the OpenAI response body, the OpenAI usage object (for
	// per-line credit attribution), the upstream HTTP status code, and an
	// error. A non-nil error with a non-2xx status indicates a customer- or
	// upstream-attributable failure; a nil error means success.
	ChatCompletion(ctx context.Context, model string, body json.RawMessage) (respBody json.RawMessage, usage *Usage, statusCode int, err error)
}

// Usage carries the per-line token counts the dispatcher attributes credits with.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

// CreditPolicy converts per-line usage to credits. Default is the same model
// edge-api uses: 1 credit per 1k prompt tokens + 1 credit per 1k completion
// tokens, rounded up. Tests can inject simpler policies.
type CreditPolicy interface {
	Credits(usage *Usage) int64
}

// DefaultCreditPolicy applies the per-line credit formula. Numbers chosen to
// match the existing creditsPerRequest fallback in batchstore/worker.go
// (1000 credits per chat-completions call) when usage is unavailable.
type DefaultCreditPolicy struct{}

// Credits returns prompt + completion tokens (1 credit per token) — matches
// the granularity of the existing accounting service. Returns 1000 (the
// fallback per-call cost in batchstore/worker.go) if usage is nil.
func (DefaultCreditPolicy) Credits(usage *Usage) int64 {
	if usage == nil {
		return 1000
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.PromptTokens + usage.CompletionTokens
}

// Dispatcher is a bounded worker pool that fans out per-line dispatches to
// the inference port. It owns retry policy, per-line timeout, and provider-
// name sanitization. Construct with NewDispatcher.
type Dispatcher struct {
	cfg       Config
	inference InferencePort
	credits   CreditPolicy
	now       func() time.Time

	// inflight is bumped before each in-flight call and decremented after; a
	// test-visible peak counter records the maximum observed.
	inflight   atomic.Int64
	peakInFlt  atomic.Int64
	peakUpdate sync.Mutex
}

// NewDispatcher constructs a Dispatcher. cfg.Validate is called.
func NewDispatcher(cfg Config, inference InferencePort, credits CreditPolicy) (*Dispatcher, error) {
	if inference == nil {
		return nil, errors.New("dispatcher: inference port is required")
	}
	if credits == nil {
		credits = DefaultCreditPolicy{}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Dispatcher{cfg: cfg, inference: inference, credits: credits, now: time.Now}, nil
}

// PeakInFlight returns the maximum number of in-flight dispatches observed
// during the lifetime of this Dispatcher. Used by tests to assert the
// concurrency cap is honored.
func (d *Dispatcher) PeakInFlight() int64 { return d.peakInFlt.Load() }

// Dispatch executes a single line and returns the result. The caller writes
// the result into the appropriate output/error JSONL writer. Method/URL on
// the input line are validated; non-POST or non-/v1/chat/completions lines
// short-circuit to an error.
func (d *Dispatcher) Dispatch(ctx context.Context, line InputLine) DispatchResult {
	if !strings.EqualFold(line.Method, "POST") || line.URL != "/v1/chat/completions" {
		return d.errResult(line.CustomID, "invalid_request",
			"method must be POST and url must be /v1/chat/completions", 1)
	}
	if _, err := extractModel(line.Body); err != nil {
		return d.errResult(line.CustomID, "invalid_request",
			"request body missing or invalid model field", 1)
	}
	alias := strings.TrimSpace(line.Alias)
	if alias == "" {
		return d.errResult(line.CustomID, "invalid_request",
			"missing batch model alias", 1)
	}

	backoff := []time.Duration{100 * time.Millisecond, 400 * time.Millisecond, 1600 * time.Millisecond}
	var lastErr error
	var lastStatus int
	for attempt := 1; attempt <= d.cfg.MaxRetries; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, d.cfg.LineTimeout)
		d.bumpInFlight(+1)
		body, usage, status, callErr := d.inference.ChatCompletion(callCtx, alias, line.Body)
		d.bumpInFlight(-1)
		cancel()

		if callErr == nil && status >= 200 && status < 300 {
			out := &OutputLine{
				ID:       "batch_req_" + uuid.New().String(),
				CustomID: line.CustomID,
				Response: &OutputResponse{StatusCode: status, RequestID: uuid.New().String(), Body: body},
				Error:    nil,
			}
			res := DispatchResult{
				CustomID:        line.CustomID,
				Output:          out,
				Attempts:        attempt,
				ConsumedCredits: d.credits.Credits(usage),
			}
			if usage != nil {
				res.UsedPromptTokens = usage.PromptTokens
				res.UsedCompTokens = usage.CompletionTokens
			}
			return res
		}

		lastErr = callErr
		lastStatus = status

		// 4xx (except 408/429) — terminal, no retry.
		if status >= 400 && status < 500 && status != 408 && status != 429 {
			return d.errResult(line.CustomID, codeForStatus(status), errMessage(callErr, "request rejected"), attempt)
		}
		if errors.Is(callErr, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			// timeout — retry up to MaxRetries
		}

		// Backoff before next attempt unless we are past last.
		if attempt < d.cfg.MaxRetries {
			delay := backoff[min(attempt-1, len(backoff)-1)]
			select {
			case <-ctx.Done():
				return d.errResult(line.CustomID, "cancelled", "execution cancelled", attempt)
			case <-time.After(delay):
			}
		}
	}
	if lastStatus == 0 {
		return d.errResult(line.CustomID, "upstream_error", errMessage(lastErr, "upstream request failed after retries"), d.cfg.MaxRetries)
	}
	return d.errResult(line.CustomID, codeForStatus(lastStatus), errMessage(lastErr, "upstream request failed after retries"), d.cfg.MaxRetries)
}

// Pool spawns d.cfg.Concurrency worker goroutines that read from in and write
// per-line results to out. Pool returns when in is closed and all workers
// have drained.
func (d *Dispatcher) Pool(ctx context.Context, in <-chan InputLine, out chan<- DispatchResult) {
	var wg sync.WaitGroup
	for i := 0; i < d.cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for line := range in {
				select {
				case <-ctx.Done():
					return
				default:
				}
				out <- d.Dispatch(ctx, line)
			}
		}()
	}
	wg.Wait()
}

func (d *Dispatcher) bumpInFlight(delta int64) {
	cur := d.inflight.Add(delta)
	if delta > 0 {
		// Update peak if current exceeds it.
		for {
			peak := d.peakInFlt.Load()
			if cur <= peak {
				break
			}
			if d.peakInFlt.CompareAndSwap(peak, cur) {
				break
			}
		}
	}
}

func (d *Dispatcher) errResult(customID, code, message string, attempts int) DispatchResult {
	return DispatchResult{
		CustomID: customID,
		Error: &ErrorLine{
			ID:       "batch_req_" + uuid.New().String(),
			CustomID: customID,
			Response: nil,
			Error:    &ErrorObj{Code: code, Message: SanitizeMessage(message)},
		},
		Attempts:        attempts,
		ConsumedCredits: 0,
	}
}

// providerNameRe matches provider tokens that must never appear in
// customer-facing error messages. Case-insensitive.
var providerNameRe = regexp.MustCompile(`(?i)\b(openrouter|groq|litellm)\b`)

// SanitizeMessage strips provider tokens and any attached path/qualifier.
// Replacement collapses adjacent whitespace.
func SanitizeMessage(msg string) string {
	if msg == "" {
		return "upstream error"
	}
	cleaned := providerNameRe.ReplaceAllString(msg, "upstream")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return cleaned
}

func errMessage(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	return err.Error()
}

func codeForStatus(status int) string {
	switch status {
	case 0:
		return "upstream_error"
	case 400:
		return "invalid_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 408:
		return "timeout"
	case 422:
		return "invalid_request"
	case 429:
		return "rate_limited"
	default:
		if status >= 500 {
			return "upstream_error"
		}
		return "request_rejected"
	}
}

// extractModel reads the "model" field from the request body without fully
// decoding it. Used so the body can be passed through unchanged to the
// inference port (which itself rewrites the model field for LiteLLM).
func extractModel(body json.RawMessage) (string, error) {
	if len(body) == 0 {
		return "", errors.New("empty body")
	}
	var head struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return "", fmt.Errorf("decode body: %w", err)
	}
	if strings.TrimSpace(head.Model) == "" {
		return "", errors.New("missing model")
	}
	return head.Model, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
