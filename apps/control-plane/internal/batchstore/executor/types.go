// Package executor implements Hive's local batch executor: it processes a
// JSONL batch input file line-by-line by dispatching each row to the LiteLLM
// chat-completions endpoint with bounded concurrency, composes
// OpenAI-shape output.jsonl + errors.jsonl, and settles per-line credits via
// the existing reservation primitive. It exists so that the v1.0 batch
// success-path can ship without a LiteLLM-supported batch upstream provider
// (OpenAI/Azure/Vertex/Anthropic). See
// .planning/phases/10-routing-storage-critical-fixes/KNOWN-ISSUE-batch-upstream.md
// for the original constraint and
// .planning/phases/15-batch-local-executor/DECISIONS.md for the resolved
// open questions.
package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ConcurrencyDefault is the default number of in-flight per-line dispatches.
// ConcurrencyCeiling is the hard server-side cap (env values above are clamped).
const (
	ConcurrencyDefault    = 8
	ConcurrencyCeiling    = 32
	MaxRetriesDefault     = 3
	MaxRetriesCeiling     = 5
	LineTimeoutDefault    = 60 * time.Second
	LineTimeoutFloor      = 5 * time.Second
	LineTimeoutCeiling    = 5 * time.Minute
	ScannerBufferMaxBytes = 4 * 1024 * 1024 // 4MB headroom over OpenAI's ~1MB per-line limit
)

// ExecutorKind enumerates the dispatch strategy the submitter selects per batch.
type ExecutorKind string

const (
	// KindAuto reads from route_capabilities.executor_kind (Task 3 wiring).
	KindAuto ExecutorKind = "auto"
	// KindLocal forces the local executor regardless of route capability.
	KindLocal ExecutorKind = "local"
	// KindUpstream forces the existing LiteLLM upstream batch-file path.
	KindUpstream ExecutorKind = "upstream"
)

// Config holds env-driven knobs for the local executor. Validate clamps to
// safe ranges and applies defaults for zero values.
type Config struct {
	Concurrency int
	MaxRetries  int
	LineTimeout time.Duration
	Kind        ExecutorKind
}

// Validate fills zero values with defaults and clamps out-of-range values to
// safe bounds. It returns an error only on structurally invalid input that
// cannot be repaired (e.g., unknown Kind value).
func (c *Config) Validate() error {
	if c.Concurrency <= 0 {
		c.Concurrency = ConcurrencyDefault
	}
	if c.Concurrency > ConcurrencyCeiling {
		c.Concurrency = ConcurrencyCeiling
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = MaxRetriesDefault
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = MaxRetriesDefault
	}
	if c.MaxRetries > MaxRetriesCeiling {
		c.MaxRetries = MaxRetriesCeiling
	}
	if c.LineTimeout <= 0 {
		c.LineTimeout = LineTimeoutDefault
	}
	if c.LineTimeout < LineTimeoutFloor {
		c.LineTimeout = LineTimeoutFloor
	}
	if c.LineTimeout > LineTimeoutCeiling {
		c.LineTimeout = LineTimeoutCeiling
	}
	switch c.Kind {
	case "":
		c.Kind = KindAuto
	case KindAuto, KindLocal, KindUpstream:
		// known kinds
	default:
		return fmt.Errorf("executor: unknown kind %q", c.Kind)
	}
	return nil
}

// InputLine is a single decoded entry from the customer-supplied input.jsonl.
// Body is preserved as raw bytes so the chat-completions dispatcher can pass
// it through with only the model field rewritten to the LiteLLM model name.
//
// Alias is injected by the executor before dispatch and carries the batch's
// model_alias resolved at submission time. Routing uses Alias, not body.model
// — per-line body.model is treated as opaque customer payload (the inference
// port rewrites it to the routed LiteLLM model name). Alias is not present
// in the on-disk JSONL.
type InputLine struct {
	CustomID string          `json:"custom_id"`
	Method   string          `json:"method"`
	URL      string          `json:"url"`
	Body     json.RawMessage `json:"body"`
	Alias    string          `json:"-"`
}

// OutputLine is the OpenAI-shape success entry written to output.jsonl.
type OutputLine struct {
	ID       string          `json:"id"`
	CustomID string          `json:"custom_id"`
	Response *OutputResponse `json:"response"`
	Error    *ErrorObj       `json:"error"`
}

// OutputResponse mirrors the OpenAI batch output shape's "response" object.
type OutputResponse struct {
	StatusCode int             `json:"status_code"`
	RequestID  string          `json:"request_id"`
	Body       json.RawMessage `json:"body"`
}

// ErrorLine is the OpenAI-shape failure entry written to errors.jsonl.
type ErrorLine struct {
	ID       string    `json:"id"`
	CustomID string    `json:"custom_id"`
	Response *struct{} `json:"response"`
	Error    *ErrorObj `json:"error"`
}

// ErrorObj is the OpenAI error envelope. Message is sanitized — it must not
// contain provider names like "openrouter", "groq", or "litellm".
type ErrorObj struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DispatchResult is the per-line outcome the dispatcher returns to the
// executor. Exactly one of Output or Error is non-nil.
type DispatchResult struct {
	CustomID         string
	Output           *OutputLine
	Error            *ErrorLine
	Attempts         int
	ConsumedCredits  int64
	UsedPromptTokens int64
	UsedCompTokens   int64
}

// ScanResult is yielded from the JSONL scanner. RawLine is set whenever the
// scanner could not decode a line into InputLine — the caller writes it to
// errors.jsonl with code=invalid_json rather than crashing the scan.
type ScanResult struct {
	Line    *InputLine
	Err     error
	RawLine []byte
}

// errInvalidJSON sentinels a per-line JSON decode failure for the scanner.
var errInvalidJSON = errors.New("invalid json line")

// IsInvalidJSON reports whether err originated from a malformed JSONL line.
func IsInvalidJSON(err error) bool { return errors.Is(err, errInvalidJSON) }
