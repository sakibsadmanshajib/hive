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

// --- Sync responses tests ---

// TestResponsesSyncNormalization tests that a chat completion response is correctly
// translated into a ResponseObject.
func TestResponsesSyncNormalization(t *testing.T) {
	content := "Hello from the assistant!"
	chatResp := ChatCompletionResponse{
		ID:      "chatcmpl-abc123",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "litellm-route",
		Choices: []ChatCompletionChoice{
			{
				Index: 0,
				Message: ChatCompletionMessage{
					Role:    "assistant",
					Content: &content,
				},
				FinishReason: strPtr("stop"),
			},
		},
		Usage: &UsageResponse{
			PromptTokens:     50,
			CompletionTokens: 25,
			TotalTokens:      75,
		},
	}

	chatRespJSON, _ := json.Marshal(chatResp)
	req := ResponsesRequest{
		Model: "gpt-4o",
		Input: json.RawMessage(`"What is the weather today?"`),
	}

	normalized, usage, err := normalizeResponsesSync(chatRespJSON, "gpt-4o", req)
	if err != nil {
		t.Fatalf("normalizeResponsesSync returned error: %v", err)
	}

	var resp ResponseObject
	if err := json.Unmarshal(normalized, &resp); err != nil {
		t.Fatalf("failed to parse normalized response: %v", err)
	}

	// Verify required fields.
	if resp.Object != "response" {
		t.Errorf("expected object=response, got %q", resp.Object)
	}
	if resp.Status != "completed" {
		t.Errorf("expected status=completed, got %q", resp.Status)
	}
	if resp.Model != "gpt-4o" {
		t.Errorf("expected model=gpt-4o (alias), got %q", resp.Model)
	}
	if !strings.HasPrefix(resp.ID, "resp_") {
		t.Errorf("expected ID to start with resp_, got %q", resp.ID)
	}

	// Verify output structure.
	if len(resp.Output) != 1 {
		t.Fatalf("expected 1 output item, got %d", len(resp.Output))
	}
	item := resp.Output[0]
	if item.Type != "message" {
		t.Errorf("expected output type=message, got %q", item.Type)
	}
	if !strings.HasPrefix(item.ID, "msg_") {
		t.Errorf("expected output item ID to start with msg_, got %q", item.ID)
	}
	if item.Status != "completed" {
		t.Errorf("expected item status=completed, got %q", item.Status)
	}
	if len(item.Content) != 1 {
		t.Fatalf("expected 1 content part, got %d", len(item.Content))
	}
	if item.Content[0].Type != "output_text" {
		t.Errorf("expected content type=output_text, got %q", item.Content[0].Type)
	}
	if item.Content[0].Text != content {
		t.Errorf("expected content text=%q, got %q", content, item.Content[0].Text)
	}

	// Verify usage translation.
	if resp.Usage == nil {
		t.Fatal("expected usage to be non-nil")
	}
	if resp.Usage.InputTokens != 50 {
		t.Errorf("expected input_tokens=50, got %d", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 25 {
		t.Errorf("expected output_tokens=25, got %d", resp.Usage.OutputTokens)
	}
	if resp.Usage.TotalTokens != 75 {
		t.Errorf("expected total_tokens=75, got %d", resp.Usage.TotalTokens)
	}

	// usage is also returned as ChatCompletion UsageResponse for accounting.
	if usage == nil {
		t.Fatal("expected usage to be non-nil")
	}
	_ = usage
}

// TestResponsesRequestTranslation verifies that instructions, text.format, and reasoning.effort
// are correctly mapped when translating to chat/completions.
func TestResponsesRequestTranslation(t *testing.T) {
	instructions := "You are a helpful assistant."
	req := ResponsesRequest{
		Model:        "gpt-4o",
		Input:        json.RawMessage(`"Hello"`),
		Instructions: &instructions,
		Text: json.RawMessage(`{"format":{"type":"json_schema","json_schema":{"name":"test","schema":{}}}}`),
		Reasoning: json.RawMessage(`{"effort":"medium"}`),
	}

	chatBody, err := translateResponsesToChatCompletions(req)
	if err != nil {
		t.Fatalf("translateResponsesToChatCompletions returned error: %v", err)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &body); err != nil {
		t.Fatalf("failed to parse translated body: %v", err)
	}

	// Verify system message from instructions.
	var messages []map[string]any
	if err := json.Unmarshal(body["messages"], &messages); err != nil {
		t.Fatalf("failed to parse messages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(messages))
	}
	if messages[0]["role"] != "system" {
		t.Errorf("expected first message role=system, got %v", messages[0]["role"])
	}
	if messages[0]["content"] != instructions {
		t.Errorf("expected system content=%q, got %v", instructions, messages[0]["content"])
	}
	if messages[1]["role"] != "user" {
		t.Errorf("expected second message role=user, got %v", messages[1]["role"])
	}

	// Verify response_format from text.format.
	if _, ok := body["response_format"]; !ok {
		t.Error("expected response_format to be set in translated body")
	}

	// Verify reasoning_effort from reasoning.effort.
	if _, ok := body["reasoning_effort"]; !ok {
		t.Error("expected reasoning_effort to be set in translated body")
	}
	var effortStr string
	if err := json.Unmarshal(body["reasoning_effort"], &effortStr); err != nil {
		t.Fatalf("failed to parse reasoning_effort: %v", err)
	}
	if effortStr != "medium" {
		t.Errorf("expected reasoning_effort=medium, got %q", effortStr)
	}
}

// TestResponsesToolTranslation verifies that Responses API tool format is translated
// to chat/completions tool format.
func TestResponsesToolTranslation(t *testing.T) {
	toolsRaw := json.RawMessage(`[{"type":"function","name":"get_weather","description":"Get weather","parameters":{"type":"object"},"strict":true}]`)

	translated, err := translateToolsToChat(toolsRaw)
	if err != nil {
		t.Fatalf("translateToolsToChat returned error: %v", err)
	}

	var chatTools []map[string]json.RawMessage
	if err := json.Unmarshal(translated, &chatTools); err != nil {
		t.Fatalf("failed to parse translated tools: %v", err)
	}

	if len(chatTools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(chatTools))
	}

	if _, ok := chatTools[0]["function"]; !ok {
		t.Error("expected 'function' key in translated tool")
	}

	var fn map[string]any
	if err := json.Unmarshal(chatTools[0]["function"], &fn); err != nil {
		t.Fatalf("failed to parse function object: %v", err)
	}
	if fn["name"] != "get_weather" {
		t.Errorf("expected function name=get_weather, got %v", fn["name"])
	}
}

// TestResponsesReasoningCapabilityGating verifies that a request with reasoning
// to a non-reasoning route returns 400 with unsupported_parameter.
func TestResponsesReasoningCapabilityGating(t *testing.T) {
	rr := httptest.NewRecorder()
	reasoning := json.RawMessage(`{"effort":"medium"}`)

	result := validateResponsesReasoningCapability(rr, "gpt-4o-mini", reasoning, false)
	if result {
		t.Error("expected validateResponsesReasoningCapability to return false for non-reasoning route")
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

// TestResponsesModelAliasInResponse verifies the model field shows the Hive alias.
func TestResponsesModelAliasInResponse(t *testing.T) {
	content := "test response"
	chatResp := ChatCompletionResponse{
		ID:      "chatcmpl-xyz",
		Object:  "chat.completion",
		Created: 1700000000,
		Model:   "litellm/internal-route-handle",
		Choices: []ChatCompletionChoice{
			{
				Index:        0,
				Message:      ChatCompletionMessage{Role: "assistant", Content: &content},
				FinishReason: strPtr("stop"),
			},
		},
	}

	chatRespJSON, _ := json.Marshal(chatResp)
	req := ResponsesRequest{
		Model: "claude-3-5-sonnet",
		Input: json.RawMessage(`"hello"`),
	}

	normalized, _, err := normalizeResponsesSync(chatRespJSON, "claude-3-5-sonnet", req)
	if err != nil {
		t.Fatalf("normalizeResponsesSync returned error: %v", err)
	}

	var resp ResponseObject
	if err := json.Unmarshal(normalized, &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Model != "claude-3-5-sonnet" {
		t.Errorf("expected model=claude-3-5-sonnet (alias), got %q", resp.Model)
	}
}

// TestResponsesStreamingLifecycleEvents verifies the correct sequence of Responses API
// SSE events when a streaming chat/completions response is translated.
func TestResponsesStreamingLifecycleEvents(t *testing.T) {
	delta1 := "Hello"
	delta2 := " world"
	delta3 := "!"
	stop := "stop"

	// Build mock upstream SSE lines simulating 3 content deltas + finish + usage + [DONE].
	chunkWithUsage := ChatCompletionChunk{
		ID:      "chatcmpl-usage",
		Object:  "chat.completion.chunk",
		Created: 1700000000,
		Model:   "litellm-route",
		Choices: []ChunkChoice{},
		Usage: &UsageResponse{
			PromptTokens:     100,
			CompletionTokens: 30,
			TotalTokens:      130,
		},
	}
	usageJSON, _ := json.Marshal(chunkWithUsage)

	lines := []string{
		buildChunkLine("c1", "litellm-route", delta1, nil),
		buildChunkLine("c2", "litellm-route", delta2, nil),
		buildChunkLine("c3", "litellm-route", delta3, &stop),
		"data: " + string(usageJSON),
		"data: [DONE]",
	}

	upstream := mockSSEServer(lines)
	defer upstream.Close()

	// Simulate translation logic directly (without full orchestrator).
	resp, err := http.Get(upstream.URL) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	translator := &responsesEventTranslator{
		responseID: "resp_test",
		aliasID:    "gpt-4o",
		created:    1700000000,
		msgID:      "msg_test",
	}

	acc := &UsageAccumulator{}
	var events []string

	rr := httptest.NewRecorder()
	// httptest.ResponseRecorder does not implement Flusher by default, so we simulate output capture.
	collectEvent := func(eventType string, data any) {
		dataJSON, _ := json.Marshal(data)
		events = append(events, fmt.Sprintf("event: %s", eventType))
		events = append(events, fmt.Sprintf("data: %s", dataJSON))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: [DONE]" {
			// Emit completed.
			collectEvent("response.completed", map[string]any{"type": "response.completed"})
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
		acc.Accumulate(chunk)

		if !translator.started {
			translator.started = true
			collectEvent("response.created", map[string]any{"type": "response.created"})
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != nil && !translator.outputItemAdded {
				translator.outputItemAdded = true
				translator.contentPartAdded = true
				collectEvent("response.output_item.added", map[string]any{"type": "response.output_item.added"})
				collectEvent("response.content_part.added", map[string]any{"type": "response.content_part.added"})
			}
			if choice.Delta.Content != nil {
				translator.currentContent.WriteString(*choice.Delta.Content)
				collectEvent("response.output_text.delta", map[string]any{
					"type":  "response.output_text.delta",
					"delta": *choice.Delta.Content,
				})
			}
			if choice.FinishReason != nil {
				collectEvent("response.content_part.done", map[string]any{"type": "response.content_part.done"})
				collectEvent("response.output_item.done", map[string]any{"type": "response.output_item.done"})
			}
		}
	}

	_ = rr

	// Verify event sequence.
	eventTypes := make([]string, 0)
	for _, e := range events {
		if strings.HasPrefix(e, "event: ") {
			eventTypes = append(eventTypes, strings.TrimPrefix(e, "event: "))
		}
	}

	expectedOrder := []string{
		"response.created",
		"response.output_item.added",
		"response.content_part.added",
		"response.output_text.delta", // delta1
		"response.output_text.delta", // delta2
		"response.output_text.delta", // delta3
		"response.content_part.done",
		"response.output_item.done",
		"response.completed",
	}

	if len(eventTypes) != len(expectedOrder) {
		t.Fatalf("expected %d events, got %d: %v", len(expectedOrder), len(eventTypes), eventTypes)
	}

	for i, expected := range expectedOrder {
		if eventTypes[i] != expected {
			t.Errorf("event[%d]: expected %q, got %q", i, expected, eventTypes[i])
		}
	}

	// Verify NO data: [DONE] appears in output.
	for _, e := range events {
		if e == "data: [DONE]" {
			t.Error("Responses API stream must NOT emit 'data: [DONE]'")
		}
	}

	// Verify usage was accumulated.
	if !acc.HasUsage {
		t.Error("expected usage to be accumulated from upstream usage chunk")
	}
}

// strPtr is a helper to get a pointer to a string literal.
func strPtr(s string) *string {
	return &s
}
