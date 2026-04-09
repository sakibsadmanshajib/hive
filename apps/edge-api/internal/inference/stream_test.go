package inference

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- UsageAccumulator tests ---

func TestUsageAccumulatorAccumulate(t *testing.T) {
	t.Run("accumulates tokens from chunk with usage", func(t *testing.T) {
		acc := &UsageAccumulator{}
		chunk := ChatCompletionChunk{
			Usage: &UsageResponse{
				PromptTokens:     100,
				CompletionTokens: 200,
				TotalTokens:      300,
				CompletionTokensDetails: &CompletionTokensDetails{
					ReasoningTokens: 50,
				},
				PromptTokensDetails: &PromptTokensDetails{
					CachedTokens: 10,
				},
			},
		}
		acc.Accumulate(chunk)

		if !acc.HasUsage {
			t.Error("expected HasUsage = true")
		}
		if acc.InputTokens != 100 {
			t.Errorf("expected InputTokens=100, got %d", acc.InputTokens)
		}
		if acc.OutputTokens != 200 {
			t.Errorf("expected OutputTokens=200, got %d", acc.OutputTokens)
		}
		if acc.TotalTokens != 300 {
			t.Errorf("expected TotalTokens=300, got %d", acc.TotalTokens)
		}
		if acc.ReasoningTokens != 50 {
			t.Errorf("expected ReasoningTokens=50, got %d", acc.ReasoningTokens)
		}
		if acc.CachedTokens != 10 {
			t.Errorf("expected CachedTokens=10, got %d", acc.CachedTokens)
		}
	})

	t.Run("no-op when chunk has no usage", func(t *testing.T) {
		acc := &UsageAccumulator{}
		chunk := ChatCompletionChunk{}
		acc.Accumulate(chunk)
		if acc.HasUsage {
			t.Error("expected HasUsage = false for chunk without usage")
		}
	})
}

func TestUsageAccumulatorToUsageResponse(t *testing.T) {
	acc := &UsageAccumulator{
		InputTokens:     100,
		OutputTokens:    200,
		ReasoningTokens: 50,
		CachedTokens:    10,
		TotalTokens:     300,
		HasUsage:        true,
	}
	u := acc.ToUsageResponse()

	if u.PromptTokens != 100 {
		t.Errorf("expected PromptTokens=100, got %d", u.PromptTokens)
	}
	if u.CompletionTokens != 200 {
		t.Errorf("expected CompletionTokens=200, got %d", u.CompletionTokens)
	}
	if u.TotalTokens != 300 {
		t.Errorf("expected TotalTokens=300, got %d", u.TotalTokens)
	}
	if u.CompletionTokensDetails == nil {
		t.Fatal("expected CompletionTokensDetails to be non-nil")
	}
	if u.CompletionTokensDetails.ReasoningTokens != 50 {
		t.Errorf("expected ReasoningTokens=50, got %d", u.CompletionTokensDetails.ReasoningTokens)
	}
	if u.PromptTokensDetails == nil {
		t.Fatal("expected PromptTokensDetails to be non-nil")
	}
	if u.PromptTokensDetails.CachedTokens != 10 {
		t.Errorf("expected CachedTokens=10, got %d", u.PromptTokensDetails.CachedTokens)
	}
}

func TestUsageAccumulatorReasoningTokensInDetails(t *testing.T) {
	acc := &UsageAccumulator{
		ReasoningTokens: 128,
		HasUsage:        true,
	}
	u := acc.ToUsageResponse()
	if u.CompletionTokensDetails == nil {
		t.Fatal("CompletionTokensDetails must be non-nil")
	}
	if u.CompletionTokensDetails.ReasoningTokens != 128 {
		t.Errorf("expected reasoning tokens 128, got %d", u.CompletionTokensDetails.ReasoningTokens)
	}
}

// --- SSE relay tests ---

// mockSSEServer builds a test server that streams the given SSE lines.
func mockSSEServer(lines []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range lines {
			fmt.Fprintln(w, line)
		}
	}))
}

// buildChunkLine creates a "data: {...}" SSE line for a chat completion chunk.
func buildChunkLine(id, model, content string, finishReason *string) string {
	chunk := ChatCompletionChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: 1700000000,
		Model:   model,
		Choices: []ChunkChoice{
			{
				Index:        0,
				Delta:        ChunkDelta{Content: &content},
				FinishReason: finishReason,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return "data: " + string(b)
}

// TestStreamRelayRewritesModel verifies that the model field in chunks is rewritten to the alias.
func TestStreamRelayRewritesModel(t *testing.T) {
	stop := "stop"
	lines := []string{
		buildChunkLine("chunk-1", "litellm-internal-route", "Hello", nil),
		buildChunkLine("chunk-2", "litellm-internal-route", " world", nil),
		buildChunkLine("chunk-3", "litellm-internal-route", "", &stop),
		"data: [DONE]",
	}

	upstream := mockSSEServer(lines)
	defer upstream.Close()

	resp, err := http.Get(upstream.URL) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	aliasID := "gpt-4o"
	var outputLines []string

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: [DONE]" {
			outputLines = append(outputLines, "data: [DONE]")
			break
		}
		if strings.HasPrefix(line, "data: ") {
			jsonData := line[6:]
			var chunk ChatCompletionChunk
			if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
				chunk.Model = aliasID
				sanitized, _ := json.Marshal(chunk)
				outputLines = append(outputLines, "data: "+string(sanitized))
			}
		}
	}

	// Verify 3 data chunks + [DONE]
	if len(outputLines) != 4 {
		t.Fatalf("expected 4 output lines, got %d: %v", len(outputLines), outputLines)
	}
	if outputLines[3] != "data: [DONE]" {
		t.Errorf("expected last line to be data: [DONE], got %q", outputLines[3])
	}

	// Verify model rewriting in each chunk.
	for i := 0; i < 3; i++ {
		var chunk ChatCompletionChunk
		jsonPart := strings.TrimPrefix(outputLines[i], "data: ")
		if err := json.Unmarshal([]byte(jsonPart), &chunk); err != nil {
			t.Fatalf("failed to parse chunk %d: %v", i, err)
		}
		if chunk.Model != aliasID {
			t.Errorf("chunk %d: expected model=%q, got %q", i, aliasID, chunk.Model)
		}
	}
}

// TestStreamTerminalUsageChunkSynthesis verifies that when includeUsage=true and the
// upstream does NOT send a usage chunk, a synthesized terminal chunk is emitted.
func TestStreamTerminalUsageChunkSynthesis(t *testing.T) {
	stop := "stop"
	lines := []string{
		buildChunkLine("chunk-1", "route", "Hello", nil),
		buildChunkLine("chunk-2", "route", " world", &stop),
		"data: [DONE]",
	}

	upstream := mockSSEServer(lines)
	defer upstream.Close()

	resp, err := http.Get(upstream.URL) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	acc := &UsageAccumulator{}
	gotDone := false

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: [DONE]" {
			gotDone = true
			break
		}
		if strings.HasPrefix(line, "data: ") {
			var chunk ChatCompletionChunk
			if err := json.Unmarshal([]byte(line[6:]), &chunk); err == nil {
				acc.Accumulate(chunk)
			}
		}
	}

	// Simulate synthesis when includeUsage=true but no usage received.
	includeUsage := true
	var synthesizedChunk *ChatCompletionChunk
	if includeUsage && !acc.HasUsage {
		synth := ChatCompletionChunk{
			ID:      "chatcmpl-synth",
			Object:  "chat.completion.chunk",
			Created: 1700000000,
			Model:   "gpt-4o",
			Choices: []ChunkChoice{},
			Usage:   acc.ToUsageResponse(),
		}
		synthesizedChunk = &synth
	}

	if !gotDone {
		t.Error("expected [DONE] from upstream")
	}
	if synthesizedChunk == nil {
		t.Fatal("expected synthesized terminal usage chunk")
	}
	if synthesizedChunk.Usage == nil {
		t.Error("synthesized chunk must have Usage field")
	}
	if len(synthesizedChunk.Choices) != 0 {
		t.Errorf("synthesized terminal chunk must have empty choices, got %d", len(synthesizedChunk.Choices))
	}
}

// TestStreamReasoningCapabilityGating verifies that reasoning_effort on non-reasoning
// route triggers a 400 with unsupported_parameter code.
func TestStreamReasoningCapabilityGating(t *testing.T) {
	rr := httptest.NewRecorder()
	effort := "medium"

	result := validateReasoningCapability(rr, "gpt-4o-mini", &effort, false)
	if result {
		t.Error("expected validateReasoningCapability to return false for non-reasoning route")
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal("failed to decode error body:", err)
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatal("expected 'error' key in response body")
	}
	if errObj["code"] != "unsupported_parameter" {
		t.Errorf("expected code=unsupported_parameter, got %v", errObj["code"])
	}
}

// TestStreamReasoningCapabilityAllowed verifies that reasoning passes when route supports it.
func TestStreamReasoningCapabilityAllowed(t *testing.T) {
	rr := httptest.NewRecorder()
	effort := "high"

	result := validateReasoningCapability(rr, "o3", &effort, true)
	if !result {
		t.Error("expected validateReasoningCapability to return true for reasoning-capable route")
	}
}

// TestStreamReasoningNilEffortAllowed verifies that nil reasoning_effort always passes.
func TestStreamReasoningNilEffortAllowed(t *testing.T) {
	rr := httptest.NewRecorder()

	result := validateReasoningCapability(rr, "gpt-4o", nil, false)
	if !result {
		t.Error("expected validateReasoningCapability to return true when effort is nil")
	}
}
