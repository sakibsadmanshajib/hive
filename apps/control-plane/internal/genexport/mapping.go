// Package genexport exports settled generations to a Langfuse-compatible
// ingestion endpoint.
//
// It reads the two tables that already hold the truth, public.request_attempts
// joined to public.usage_events, and posts them to Langfuse's public ingestion
// API. Nothing here runs on a request path: the exporter is a poller over rows
// that are already committed, in control-plane, so a slow or absent Langfuse
// can neither slow a request nor fail one.
//
// Three properties are load-bearing and are enforced by mapping_test.go rather
// than by convention:
//
//   - Content cannot leak. Neither source table stores prompt or completion
//     text, so there is no field to forward and no runtime flag that could turn
//     one on. Turning content capture on would be a code change plus a schema
//     change plus a review, which is the correct amount of friction.
//   - Provider names cannot leak. Neither source table has a provider column.
//     `model_alias` is the customer-facing identifier and is what ships.
//   - Cost is ours. It is derived from usage_events.hive_credit_delta in exact
//     rational arithmetic (D-031, D-046), never from a provider, never from
//     LiteLLM, and never through a float64.
package genexport

import (
	"encoding/json"
	"math/big"
	"strings"
	"time"
)

// creditsPerUSD is the credit unit fixed by D-046: 1 USD is 1,000,000,000
// Hive credits.
const creditsPerUSD = 1_000_000_000

// Langfuse observation levels.
const (
	LevelDefault = "DEFAULT"
	LevelWarning = "WARNING"
	LevelError   = "ERROR"
)

// TerminalEventTypes are the only usage_events the exporter reads. D-034: a
// reservation reaches a terminal state exactly once, and an in-flight charge is
// never published as if it were final. `accepted`, `reservation_created` and
// `stream_update` are deliberately absent.
var TerminalEventTypes = []string{"completed", "released", "refunded", "error", "reconciled"}

// AttemptRow is the public.request_attempts projection the exporter reads.
// Every field here is an identifier, a dimension or a timestamp. There is no
// content-bearing and no provider-bearing column to add, which is the point.
type AttemptRow struct {
	ID            string // request_attempts.id, the generation identity
	RequestID     string // request_attempts.request_id, the trace identity
	AttemptNumber int
	Endpoint      string
	ModelAlias    string
	UserID        *string
	TeamID        *string
	APIKeyID      *string
	StartedAt     time.Time
	CompletedAt   *time.Time
}

// UsageEventRow is the public.usage_events projection the exporter reads.
// usage_events.provider_request_id is deliberately not projected: it is the
// one column on this table whose name touches the provider boundary, it
// answers no observability question we have, and leaving it out keeps the
// no-provider-name property structural.
type UsageEventRow struct {
	ID               string
	EventType        string
	Status           string
	Endpoint         string
	ModelAlias       string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	// HiveCreditDelta is the account delta in credits. Spend is recorded as a
	// NEGATIVE delta (accounting/service.go writes `-actualCredits`), so a
	// charge arrives here below zero and a refund arrives above it.
	HiveCreditDelta int64
	ErrorCode       *string
	ErrorType       *string
	CreatedAt       time.Time
}

// Generation is one settled attempt, in Langfuse's vocabulary.
type Generation struct {
	TraceID   string
	ID        string
	Name      string
	Model     string
	StartTime time.Time
	EndTime   *time.Time
	Usage     map[string]int64
	CostUSD   *big.Rat
	UserID    *string
	Tags      []string
	Level     string
	StatusMsg string
	// AttemptNumber and EventType ride along as metadata dimensions. The
	// attempt number is what makes a retry legible; the event type says which
	// terminal state this settlement reached.
	AttemptNumber int
	EventType     string
}

// MapRow turns one settled usage event and its attempt into a Generation.
func MapRow(attempt AttemptRow, event UsageEventRow) Generation {
	model := attempt.ModelAlias
	if model == "" {
		model = event.ModelAlias
	}
	name := attempt.Endpoint
	if name == "" {
		name = event.Endpoint
	}

	return Generation{
		TraceID:   attempt.RequestID,
		ID:        attempt.ID,
		Name:      name,
		Model:     model,
		StartTime: attempt.StartedAt,
		EndTime:   attempt.CompletedAt,
		Usage: map[string]int64{
			"input":       event.InputTokens,
			"output":      event.OutputTokens,
			"cache_read":  event.CacheReadTokens,
			"cache_write": event.CacheWriteTokens,
		},
		CostUSD:       costUSD(event.HiveCreditDelta),
		UserID:        attempt.UserID,
		Tags:          tagsFor(attempt, event),
		Level:         levelFor(event.EventType),
		StatusMsg:     statusMessage(event),
		AttemptNumber: attempt.AttemptNumber,
		EventType:     event.EventType,
	}
}

// costUSD converts a credit delta to a USD spend figure in exact rational
// arithmetic. The sign is flipped because the column records spend as a
// negative account delta: a 2,783-credit charge is stored as -2783 and is
// 0.000002783 USD of spend, while a refund is stored positive and reads as
// negative spend so a sum over a trace nets to what the customer actually paid.
//
// Negation happens on the big.Rat rather than on the int64 so that the extreme
// value (math.MinInt64) cannot overflow.
func costUSD(creditDelta int64) *big.Rat {
	cost := new(big.Rat).SetFrac64(creditDelta, creditsPerUSD)
	return cost.Neg(cost)
}

func levelFor(eventType string) string {
	switch eventType {
	case "error":
		return LevelError
	case "released", "refunded":
		return LevelWarning
	default:
		// "completed" and "reconciled". A reconciled row is a corrected
		// settlement, not a failure.
		return LevelDefault
	}
}

// statusMessage carries the terminal classification. For a failure that is the
// error type and code, which is what makes issue #1453's silent refusals
// visible; otherwise it is the settlement status.
func statusMessage(event UsageEventRow) string {
	parts := make([]string, 0, 2)
	if event.ErrorType != nil && *event.ErrorType != "" {
		parts = append(parts, *event.ErrorType)
	}
	if event.ErrorCode != nil && *event.ErrorCode != "" {
		parts = append(parts, *event.ErrorCode)
	}
	if len(parts) > 0 {
		return strings.Join(parts, ": ")
	}
	return event.Status
}

func tagsFor(attempt AttemptRow, event UsageEventRow) []string {
	tags := []string{
		"endpoint:" + attempt.Endpoint,
		"model:" + attempt.ModelAlias,
		"event:" + event.EventType,
	}
	if attempt.APIKeyID != nil && *attempt.APIKeyID != "" {
		tags = append(tags, "api_key:"+*attempt.APIKeyID)
	}
	if attempt.TeamID != nil && *attempt.TeamID != "" {
		tags = append(tags, "team:"+*attempt.TeamID)
	}
	return tags
}

// IngestionBatch renders the Generation as Langfuse /api/public/ingestion batch
// events: one trace upsert and one generation upsert.
//
// The event ids are derived from the attempt id rather than generated, so a
// redelivery (a crash between a successful POST and the cursor write) upserts
// the same two rows instead of duplicating them.
//
// The key sets below are asserted exactly by mapping_test.go. Adding a key here
// fails that test, which is deliberate: it forces anyone widening this payload
// to state that the new key cannot carry prompt text, completion text or a
// provider name.
func (g Generation) IngestionBatch() []map[string]any {
	timestamp := g.StartTime.UTC().Format(time.RFC3339Nano)

	var endTime any
	if g.EndTime != nil {
		endTime = g.EndTime.UTC().Format(time.RFC3339Nano)
	}

	var cost any
	if g.CostUSD != nil {
		// json.Number keeps the exact decimal on the wire. A float64 here
		// would be a rounding step on the money path, which D-031 forbids.
		// Nine places is exact for a denominator of 1e9.
		cost = json.Number(g.CostUSD.FloatString(9))
	}

	usage := make(map[string]any, len(g.Usage))
	for key, value := range g.Usage {
		usage[key] = value
	}

	return []map[string]any{
		{
			"id":        "trace-" + g.ID,
			"type":      "trace-create",
			"timestamp": timestamp,
			"body": map[string]any{
				"id":        g.TraceID,
				"name":      g.Name,
				"timestamp": timestamp,
				"userId":    stringOrNil(g.UserID),
				"tags":      g.Tags,
			},
		},
		{
			"id":        "gen-" + g.ID,
			"type":      "generation-create",
			"timestamp": timestamp,
			"body": map[string]any{
				"id":            g.ID,
				"traceId":       g.TraceID,
				"name":          g.Name,
				"model":         g.Model,
				"startTime":     timestamp,
				"endTime":       endTime,
				"usageDetails":  usage,
				"costDetails":   map[string]any{"total": cost},
				"level":         g.Level,
				"statusMessage": g.StatusMsg,
				"metadata":      g.metadata(),
			},
		},
	}
}

// metadata carries the two structural facts that have nowhere else to go. Both
// are dimensions, never text.
func (g Generation) metadata() map[string]any {
	return map[string]any{
		"attempt_number": g.AttemptNumber,
		"event_type":     g.EventType,
	}
}

func stringOrNil(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}
