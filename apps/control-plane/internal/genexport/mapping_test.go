package genexport_test

import (
	"encoding/json"
	"math/big"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/genexport"
)

func ptr[T any](v T) *T { return &v }

var (
	testStart = time.Date(2026, 8, 29, 14, 2, 0, 0, time.UTC)
	testEnd   = testStart.Add(1200 * time.Millisecond)
)

func completedAttempt() genexport.AttemptRow {
	return genexport.AttemptRow{
		ID:            "11111111-1111-1111-1111-111111111111",
		RequestID:     "req_abc123",
		AttemptNumber: 1,
		Endpoint:      "/v1/chat/completions",
		ModelAlias:    "hive-default",
		UserID:        ptr("22222222-2222-2222-2222-222222222222"),
		TeamID:        ptr("33333333-3333-3333-3333-333333333333"),
		APIKeyID:      ptr("44444444-4444-4444-4444-444444444444"),
		StartedAt:     testStart,
		CompletedAt:   ptr(testEnd),
	}
}

func completedEvent() genexport.UsageEventRow {
	return genexport.UsageEventRow{
		ID:         "55555555-5555-5555-5555-555555555555",
		EventType:  "completed",
		Status:     "completed",
		ModelAlias: "hive-default",
		Endpoint:   "/v1/chat/completions",
		// Spend is recorded as a NEGATIVE delta on the account
		// (accounting/service.go writes `HiveCreditDelta: -actualCredits`),
		// so a 2,783-credit charge lands here as -2783.
		HiveCreditDelta:  -2783,
		InputTokens:      120,
		OutputTokens:     340,
		CacheReadTokens:  56,
		CacheWriteTokens: 78,
		CreatedAt:        testEnd,
	}
}

// TestMapRowCompletedTokensAndIdentity covers the happy path: the four token
// buckets map one-to-one, the trace is the request id, the generation is the
// attempt, and the timestamps come straight off request_attempts.
func TestMapRowCompletedTokensAndIdentity(t *testing.T) {
	g := genexport.MapRow(completedAttempt(), completedEvent())

	assert.Equal(t, "req_abc123", g.TraceID)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", g.ID)
	assert.Equal(t, "hive-default", g.Model)
	assert.Equal(t, testStart, g.StartTime)
	require.NotNil(t, g.EndTime)
	assert.Equal(t, testEnd, *g.EndTime)
	assert.Equal(t, genexport.LevelDefault, g.Level)

	assert.Equal(t, map[string]int64{
		"input":       120,
		"output":      340,
		"cache_read":  56,
		"cache_write": 78,
	}, g.Usage)
}

// TestMapRowCostIsExactRational is the money-path assertion. D-031/D-046: one
// credit is 1/1_000_000_000 USD and the conversion is exact rational
// arithmetic, never float64. Asserted as a rational, not as a float.
func TestMapRowCostIsExactRational(t *testing.T) {
	g := genexport.MapRow(completedAttempt(), completedEvent())

	require.NotNil(t, g.CostUSD)
	want := big.NewRat(2783, 1_000_000_000)
	assert.Zero(t, g.CostUSD.Cmp(want),
		"cost must be exactly 2783/1e9 USD, got %s", g.CostUSD.RatString())
	assert.Equal(t, "2783/1000000000", g.CostUSD.RatString())

	// And the serialized form must carry the same exact value, not a
	// float64 round-trip of it.
	body := generationBody(t, g)
	cost, ok := body["costDetails"].(map[string]any)
	require.True(t, ok, "costDetails must be present on the generation body")
	assert.Equal(t, json.Number("0.000002783"), cost["total"])
}

// TestMapRowReleasedIsWarningWithZeroCost: a released reservation charged
// nothing, so it carries zero cost and is not an error either.
func TestMapRowReleasedIsWarningWithZeroCost(t *testing.T) {
	event := completedEvent()
	event.EventType = "released"
	event.Status = "cancelled"
	event.HiveCreditDelta = 0
	event.InputTokens, event.OutputTokens = 0, 0
	event.CacheReadTokens, event.CacheWriteTokens = 0, 0

	g := genexport.MapRow(completedAttempt(), event)

	assert.Equal(t, genexport.LevelWarning, g.Level)
	require.NotNil(t, g.CostUSD)
	assert.Zero(t, g.CostUSD.Sign(), "a released reservation must carry zero cost")
}

// TestMapRowRefundedNetsAgainstTheCharge: a refund returns credits, which is a
// positive delta, and must therefore read as negative spend so a sum over a
// trace nets to what the customer actually paid.
func TestMapRowRefundedNetsAgainstTheCharge(t *testing.T) {
	event := completedEvent()
	event.EventType = "refunded"
	event.HiveCreditDelta = 2783

	g := genexport.MapRow(completedAttempt(), event)

	assert.Equal(t, genexport.LevelWarning, g.Level)
	require.NotNil(t, g.CostUSD)
	assert.Equal(t, "-2783/1000000000", g.CostUSD.RatString())
}

// TestMapRowErrorCarriesClassification: issue #1453's shape. A terminal error
// becomes a first-class ERROR generation carrying its error code and type.
func TestMapRowErrorCarriesClassification(t *testing.T) {
	event := completedEvent()
	event.EventType = "error"
	event.Status = "failed"
	event.HiveCreditDelta = 0
	event.ErrorCode = ptr("insufficient_credits")
	event.ErrorType = ptr("billing_error")

	g := genexport.MapRow(completedAttempt(), event)

	assert.Equal(t, genexport.LevelError, g.Level)
	assert.Contains(t, g.StatusMsg, "insufficient_credits")
	assert.Contains(t, g.StatusMsg, "billing_error")
}

// TestMapRowReconciledIsDefault: a reconciled row is a corrected settlement,
// not a failure.
func TestMapRowReconciledIsDefault(t *testing.T) {
	event := completedEvent()
	event.EventType = "reconciled"

	g := genexport.MapRow(completedAttempt(), event)
	assert.Equal(t, genexport.LevelDefault, g.Level)
}

// TestMapRowRetryIsASiblingGeneration: attempt 2 of the same request is a
// distinct generation under the same trace, which is the only surface in the
// product that makes a retry visible at all.
func TestMapRowRetryIsASiblingGeneration(t *testing.T) {
	first := genexport.MapRow(completedAttempt(), completedEvent())

	retryAttempt := completedAttempt()
	retryAttempt.ID = "99999999-9999-9999-9999-999999999999"
	retryAttempt.AttemptNumber = 2
	second := genexport.MapRow(retryAttempt, completedEvent())

	assert.Equal(t, first.TraceID, second.TraceID, "a retry stays under the same trace")
	assert.NotEqual(t, first.ID, second.ID, "a retry is a distinct generation")

	body := generationBody(t, second)
	metadata, ok := body["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 2, metadata["attempt_number"])
}

// TestIngestionBatchShape asserts the wire shape Langfuse's /api/public/ingestion
// expects: a list of typed, individually-identified events.
func TestIngestionBatchShape(t *testing.T) {
	batch := genexport.MapRow(completedAttempt(), completedEvent()).IngestionBatch()
	require.Len(t, batch, 2)

	types := []string{}
	for _, item := range batch {
		types = append(types, item["type"].(string))
		assert.NotEmpty(t, item["id"], "every batch event needs its own id for deduplication")
		assert.NotEmpty(t, item["timestamp"])
		assert.NotNil(t, item["body"])
	}
	assert.Equal(t, []string{"trace-create", "generation-create"}, types)

	// The batch event ids are deterministic, so a redelivery after a crash
	// between the POST and the cursor write upserts rather than duplicates.
	again := genexport.MapRow(completedAttempt(), completedEvent()).IngestionBatch()
	assert.Equal(t, batch[0]["id"], again[0]["id"])
	assert.Equal(t, batch[1]["id"], again[1]["id"])
}

// forbiddenKeys are the key names through which prompt text, completion text
// or a provider name could reach Langfuse. `input` and `output` are Langfuse's
// own content fields, and they are exactly how the deleted audit sink leaked
// content (sinks/langfuse.go set generationBody["input"] = after["prompt"]).
var forbiddenKeys = []string{
	"input", "output", "prompt", "completion", "completions",
	"messages", "content", "text", "provider", "provider_name",
	"providerRequestId", "provider_request_id", "choices",
}

// TestBatchCarriesNoContentOrProviderName is the privacy regression guard, and
// it is the reason this whole design is safe to run. Three independent legs:
// no forbidden key anywhere in the serialized batch, an exact key set on each
// body so adding a field forces this test to be updated, and a check that the
// source row types have no content-bearing or provider-bearing field to begin
// with.
func TestBatchCarriesNoContentOrProviderName(t *testing.T) {
	// Leg 1: no forbidden key name anywhere in the serialized batch.
	raw, err := json.Marshal(map[string]any{
		"batch": genexport.MapRow(completedAttempt(), completedEvent()).IngestionBatch(),
	})
	require.NoError(t, err)

	var decoded any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	for _, key := range collectKeys(decoded, "") {
		leaf := key
		if i := strings.LastIndex(key, "."); i >= 0 {
			leaf = key[i+1:]
		}
		for _, forbidden := range forbiddenKeys {
			// usageDetails legitimately counts tokens under "input" and
			// "output"; those are integers, never text. Everything else
			// named this way is a content field.
			if strings.EqualFold(leaf, forbidden) && !strings.Contains(key, "usageDetails") {
				t.Errorf("batch carries forbidden key %q: prompt, completion and provider values must never leave Hive", key)
			}
		}
	}

	// Leg 2: exact key sets. A future field addition fails here rather than
	// silently shipping to a second datastore.
	batch := genexport.MapRow(completedAttempt(), completedEvent()).IngestionBatch()
	traceBody, ok := batch[0]["body"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t,
		[]string{"id", "name", "timestamp", "userId", "tags"},
		keysOf(traceBody),
		"trace body key set changed; confirm no new key can carry content or a provider name")

	genBody := generationBody(t, genexport.MapRow(completedAttempt(), completedEvent()))
	assert.ElementsMatch(t,
		[]string{"id", "traceId", "name", "model", "startTime", "endTime",
			"usageDetails", "costDetails", "level", "statusMessage", "metadata"},
		keysOf(genBody),
		"generation body key set changed; confirm no new key can carry content or a provider name")

	// Leg 3: the source projections themselves have no content-bearing or
	// provider-bearing field, which is why the property is structural rather
	// than a flag someone can flip.
	leaky := regexp.MustCompile(`(?i)prompt|completion|content|message|provider|body|text|payload|choice`)
	for _, typ := range []reflect.Type{
		reflect.TypeOf(genexport.AttemptRow{}),
		reflect.TypeOf(genexport.UsageEventRow{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			assert.False(t, leaky.MatchString(name),
				"%s.%s looks content-bearing or provider-bearing; the exporter must not read such a column", typ.Name(), name)
		}
	}
}

// TestTerminalEventTypesAreTerminalOnly guards D-034: the exporter never
// publishes an in-flight charge as if it were final.
func TestTerminalEventTypesAreTerminalOnly(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{"completed", "released", "refunded", "error", "reconciled"},
		genexport.TerminalEventTypes,
		"only terminal usage_events may be exported (D-034)")

	for _, inFlight := range []string{"accepted", "reservation_created", "stream_update"} {
		assert.NotContains(t, genexport.TerminalEventTypes, inFlight)
	}
}

func generationBody(t *testing.T, g genexport.Generation) map[string]any {
	t.Helper()
	batch := g.IngestionBatch()
	require.Len(t, batch, 2)
	body, ok := batch[1]["body"].(map[string]any)
	require.True(t, ok, "generation body must be a map")
	return body
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// collectKeys walks decoded JSON and returns every key as a dotted path.
func collectKeys(v any, prefix string) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out = append(out, path)
			out = append(out, collectKeys(val, path)...)
		}
	case []any:
		for _, val := range t {
			out = append(out, collectKeys(val, prefix)...)
		}
	}
	return out
}
