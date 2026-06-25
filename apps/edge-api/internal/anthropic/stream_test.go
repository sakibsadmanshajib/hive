package anthropic_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

// parseSSEEvents reads the raw SSE body and returns a list of parsed events
// (data lines only), preserving order.
func parseSSEEvents(t *testing.T, body string) []map[string]interface{} {
	t.Helper()
	var events []map[string]interface{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("parse event %q: %v", payload, err)
		}
		events = append(events, ev)
	}
	return events
}

// buildOAIStream builds a minimal OpenAI SSE stream body from the given chunk
// JSON strings, terminated with [DONE].
func buildOAIStream(chunks ...string) string {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString("data: ")
		sb.WriteString(c)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

func TestSSETranslator_SimpleTextStream(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-1","model":"claude-3-haiku","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","model":"claude-3-haiku","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","model":"claude-3-haiku","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","model":"claude-3-haiku","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	)

	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec)
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}

	body := rec.Body.String()
	events := parseSSEEvents(t, body)

	// Must start with message_start.
	if len(events) == 0 {
		t.Fatal("no events")
	}
	if events[0]["type"] != "message_start" {
		t.Errorf("first event type: want message_start got %v", events[0]["type"])
	}

	// Verify event sequence contains the required types in order.
	types := make([]string, 0, len(events))
	for _, ev := range events {
		if ty, ok := ev["type"].(string); ok {
			types = append(types, ty)
		}
	}

	assertContainsInOrder(t, types,
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	)

	// Verify stop_reason in message_delta.
	for _, ev := range events {
		if ev["type"] == "message_delta" {
			delta, _ := ev["delta"].(map[string]interface{})
			if delta == nil {
				t.Error("message_delta has no delta field")
				continue
			}
			if delta["stop_reason"] != "end_turn" {
				t.Errorf("stop_reason: want end_turn got %v", delta["stop_reason"])
			}
		}
	}

	// Content-Type header must be text/event-stream.
	ct := rec.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type: want text/event-stream got %q", ct)
	}
}

func TestSSETranslator_ToolUseStream(t *testing.T) {
	// Simulates an OpenAI stream that emits a tool_calls delta.
	stream := buildOAIStream(
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_01","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Dhaka\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":15}}`,
	)

	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec)
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}

	body := rec.Body.String()
	events := parseSSEEvents(t, body)

	types := make([]string, 0, len(events))
	for _, ev := range events {
		if ty, ok := ev["type"].(string); ok {
			types = append(types, ty)
		}
	}

	// Must have message_start and a tool_use content_block_start.
	assertContainsInOrder(t, types,
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	)

	// The content_block_start for the tool should have type=tool_use.
	var foundToolBlock bool
	for _, ev := range events {
		if ev["type"] == "content_block_start" {
			cb, _ := ev["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				foundToolBlock = true
				if cb["id"] != "call_01" {
					t.Errorf("tool_use block id: want call_01 got %v", cb["id"])
				}
				if cb["name"] != "get_weather" {
					t.Errorf("tool_use block name: want get_weather got %v", cb["name"])
				}
			}
		}
	}
	if !foundToolBlock {
		t.Error("no tool_use content_block_start found in stream")
	}

	// The input_json_delta events should carry partial_json.
	var foundInputDelta bool
	for _, ev := range events {
		if ev["type"] == "content_block_delta" {
			delta, _ := ev["delta"].(map[string]interface{})
			if delta != nil && delta["type"] == "input_json_delta" {
				foundInputDelta = true
			}
		}
	}
	if !foundInputDelta {
		t.Error("no input_json_delta content_block_delta found in stream")
	}

	// stop_reason in message_delta must be tool_use.
	for _, ev := range events {
		if ev["type"] == "message_delta" {
			delta, _ := ev["delta"].(map[string]interface{})
			if delta != nil && delta["stop_reason"] != "tool_use" {
				t.Errorf("stop_reason: want tool_use got %v", delta["stop_reason"])
			}
		}
	}
}

func TestSSETranslator_TextThenToolUse(t *testing.T) {
	// Text delta followed by a tool_call delta: must close text block before
	// opening tool_use block, and use correct block indices.
	stream := buildOAIStream(
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{"content":"Checking..."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"fn","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)

	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec)
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}

	body := rec.Body.String()
	events := parseSSEEvents(t, body)

	// Count content_block_start events: must be 2 (text at 0, tool_use at 1).
	var blockStarts []map[string]interface{}
	for _, ev := range events {
		if ev["type"] == "content_block_start" {
			blockStarts = append(blockStarts, ev)
		}
	}
	if len(blockStarts) != 2 {
		t.Fatalf("content_block_start count: want 2 got %d", len(blockStarts))
	}
	cb0, _ := blockStarts[0]["content_block"].(map[string]interface{})
	if cb0 == nil || cb0["type"] != "text" {
		t.Errorf("block[0] type: want text got %v", cb0)
	}
	cb1, _ := blockStarts[1]["content_block"].(map[string]interface{})
	if cb1 == nil || cb1["type"] != "tool_use" {
		t.Errorf("block[1] type: want tool_use got %v", cb1)
	}

	// Block indices must be 0 and 1.
	idx0, _ := blockStarts[0]["index"].(float64)
	idx1, _ := blockStarts[1]["index"].(float64)
	if idx0 != 0 {
		t.Errorf("block[0] index: want 0 got %v", idx0)
	}
	if idx1 != 1 {
		t.Errorf("block[1] index: want 1 got %v", idx1)
	}

	// There must be a content_block_stop between text and tool blocks.
	var stopAfterText, startTool bool
	for _, ev := range events {
		ty, _ := ev["type"].(string)
		if ty == "content_block_stop" {
			stopAfterText = true
		}
		if ty == "content_block_start" && stopAfterText {
			cb, _ := ev["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				startTool = true
			}
		}
	}
	if !startTool {
		t.Error("tool_use content_block_start was not preceded by a content_block_stop")
	}
}

func TestSSETranslator_EmptyStream(t *testing.T) {
	stream := "data: [DONE]\n\n"

	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec)
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}

	// Even an empty stream must emit the terminal sequence.
	body := rec.Body.String()
	events := parseSSEEvents(t, body)

	hasMessageDelta := false
	hasMessageStop := false
	for _, ev := range events {
		switch ev["type"] {
		case "message_delta":
			hasMessageDelta = true
		case "message_stop":
			hasMessageStop = true
		}
	}
	if !hasMessageDelta {
		t.Error("missing message_delta on empty stream")
	}
	if !hasMessageStop {
		t.Error("missing message_stop on empty stream")
	}
}

func TestSSETranslator_UsageInMessageDelta(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-u","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-u","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
	)

	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec)
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}

	body := rec.Body.String()
	events := parseSSEEvents(t, body)

	for _, ev := range events {
		if ev["type"] == "message_delta" {
			usage, _ := ev["usage"].(map[string]interface{})
			if usage == nil {
				t.Error("message_delta has no usage field")
				continue
			}
			outTokens, _ := usage["output_tokens"].(float64)
			if outTokens != 3 {
				t.Errorf("output_tokens: want 3 got %v", outTokens)
			}
		}
	}
}

// assertContainsInOrder checks that want is a subsequence of got.
func assertContainsInOrder(t *testing.T, got []string, want ...string) {
	t.Helper()
	wi := 0
	for _, g := range got {
		if wi >= len(want) {
			break
		}
		if g == want[wi] {
			wi++
		}
	}
	if wi < len(want) {
		t.Errorf("event sequence does not contain %v in order\ngot: %v", want, got)
	}
}
