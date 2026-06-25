package anthropic_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

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
		t.Errorf("event sequence missing subsequence %v\ngot: %v", want, got)
	}
}

func TestSSETranslator_SimpleTextStream(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-1","model":"route-upstream","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","model":"route-upstream","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","model":"route-upstream","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-1","model":"route-upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	)
	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec, "claude-3-haiku")
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	body := rec.Body.String()
	events := parseSSEEvents(t, body)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	types := make([]string, 0, len(events))
	for _, ev := range events {
		if ty, ok := ev["type"].(string); ok {
			types = append(types, ty)
		}
	}
	assertContainsInOrder(t, types, "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop")

	// Finding 2: model in message_start must be client alias, not upstream route.
	for _, ev := range events {
		if ev["type"] == "message_start" {
			msg, _ := ev["message"].(map[string]interface{})
			if msg == nil {
				t.Error("message_start has no message field")
				continue
			}
			if msg["model"] != "claude-3-haiku" {
				t.Errorf("message_start.model: want claude-3-haiku got %v", msg["model"])
			}
			if m, _ := msg["model"].(string); strings.Contains(m, "route") || strings.Contains(m, "upstream") {
				t.Errorf("upstream route id leaked in message_start.model: %q", m)
			}
		}
	}

	// stop_reason in message_delta.
	for _, ev := range events {
		if ev["type"] == "message_delta" {
			delta, _ := ev["delta"].(map[string]interface{})
			if delta["stop_reason"] != "end_turn" {
				t.Errorf("stop_reason: want end_turn got %v", delta["stop_reason"])
			}
		}
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type: want text/event-stream got %q", rec.Header().Get("Content-Type"))
	}
}

// Finding 1: two concurrent streams must get distinct message IDs.
func TestSSETranslator_ConcurrentStreams_DistinctMessageIDs(t *testing.T) {
	// Stream with empty chunk ID so the translator must generate its own UUID.
	stream := buildOAIStream(
		`{"id":"","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		`{"id":"","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	)

	ids := make([]string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			tr := anthropic.NewSSETranslator(rec, "m")
			if err := tr.Translate(strings.NewReader(stream)); err != nil {
				t.Errorf("stream %d translate error: %v", i, err)
				return
			}
			events := parseSSEEvents(t, rec.Body.String())
			for _, ev := range events {
				if ev["type"] == "message_start" {
					msg, _ := ev["message"].(map[string]interface{})
					if msg != nil {
						ids[i], _ = msg["id"].(string)
					}
					break
				}
			}
		}()
	}
	wg.Wait()

	if ids[0] == "" || ids[1] == "" {
		t.Fatalf("missing message IDs: %v", ids)
	}
	if ids[0] == ids[1] {
		t.Errorf("concurrent streams got same message ID %q", ids[0])
	}
}

func TestSSETranslator_ToolUseStream(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_01","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"Dhaka\"}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-tool","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":15}}`,
	)
	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec, "m")
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
	assertContainsInOrder(t, types, "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop")

	var foundToolBlock, foundInputDelta bool
	for _, ev := range events {
		if ev["type"] == "content_block_start" {
			cb, _ := ev["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				foundToolBlock = true
				if cb["id"] != "call_01" {
					t.Errorf("tool_use id: want call_01 got %v", cb["id"])
				}
				if cb["name"] != "get_weather" {
					t.Errorf("tool_use name: want get_weather got %v", cb["name"])
				}
			}
		}
		if ev["type"] == "content_block_delta" {
			delta, _ := ev["delta"].(map[string]interface{})
			if delta != nil && delta["type"] == "input_json_delta" {
				foundInputDelta = true
			}
		}
		if ev["type"] == "message_delta" {
			delta, _ := ev["delta"].(map[string]interface{})
			if delta != nil && delta["stop_reason"] != "tool_use" {
				t.Errorf("stop_reason: want tool_use got %v", delta["stop_reason"])
			}
		}
	}
	if !foundToolBlock {
		t.Error("no tool_use content_block_start found")
	}
	if !foundInputDelta {
		t.Error("no input_json_delta found")
	}
}

func TestSSETranslator_TextThenToolUse(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{"content":"Checking..."},"finish_reason":null}]}`,
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"fn","arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-m","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec, "m")
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	events := parseSSEEvents(t, rec.Body.String())

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
	idx0, _ := blockStarts[0]["index"].(float64)
	idx1, _ := blockStarts[1]["index"].(float64)
	if idx0 != 0 {
		t.Errorf("block[0] index: want 0 got %v", idx0)
	}
	if idx1 != 1 {
		t.Errorf("block[1] index: want 1 got %v", idx1)
	}
	// content_block_stop must appear between the two starts.
	var stopAfterText, startToolAfterStop bool
	for _, ev := range events {
		ty, _ := ev["type"].(string)
		if ty == "content_block_stop" {
			stopAfterText = true
		}
		if ty == "content_block_start" && stopAfterText {
			cb, _ := ev["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				startToolAfterStop = true
			}
		}
	}
	if !startToolAfterStop {
		t.Error("tool_use content_block_start not preceded by content_block_stop")
	}
}

// Finding 7: two sequential (parallel) tool_use blocks must get indices 0,1,2
// and each content_block_stop must precede the next content_block_start.
func TestSSETranslator_TwoParallelToolUseBlocks(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-p","model":"m","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		// First tool call opens (index 0 in upstream deltas).
		`{"id":"chatcmpl-p","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"tool_a","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-p","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
		// Second tool call opens (index 1 in upstream deltas).
		`{"id":"chatcmpl-p","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"tool_b","arguments":""}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-p","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{}"}}]},"finish_reason":null}]}`,
		`{"id":"chatcmpl-p","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec, "m")
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	events := parseSSEEvents(t, rec.Body.String())

	// Collect content_block_start events.
	var starts []map[string]interface{}
	for _, ev := range events {
		if ev["type"] == "content_block_start" {
			starts = append(starts, ev)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("content_block_start count: want 2 got %d", len(starts))
	}
	idx0, _ := starts[0]["index"].(float64)
	idx1, _ := starts[1]["index"].(float64)
	if idx0 != 0 || idx1 != 1 {
		t.Errorf("block indices: want 0,1 got %v,%v", idx0, idx1)
	}

	// Both blocks must be tool_use.
	for i, s := range starts {
		cb, _ := s["content_block"].(map[string]interface{})
		if cb == nil || cb["type"] != "tool_use" {
			t.Errorf("block[%d] not tool_use: %v", i, cb)
		}
	}

	// Each content_block_stop must precede the next content_block_start.
	// i.e. the sequence must contain: start stop start stop.
	var seq []string
	for _, ev := range events {
		ty, _ := ev["type"].(string)
		if ty == "content_block_start" || ty == "content_block_stop" {
			seq = append(seq, ty)
		}
	}
	// Expected: start, stop, start, stop (at minimum).
	if len(seq) < 4 {
		t.Fatalf("start/stop sequence too short: %v", seq)
	}
	assertContainsInOrder(t, seq, "content_block_start", "content_block_stop", "content_block_start", "content_block_stop")
}

func TestSSETranslator_EmptyStream(t *testing.T) {
	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec, "m")
	if err := tr.Translate(strings.NewReader("data: [DONE]\n\n")); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	events := parseSSEEvents(t, rec.Body.String())
	var hasDelta, hasStop bool
	for _, ev := range events {
		switch ev["type"] {
		case "message_delta":
			hasDelta = true
		case "message_stop":
			hasStop = true
		}
	}
	if !hasDelta {
		t.Error("missing message_delta on empty stream")
	}
	if !hasStop {
		t.Error("missing message_stop on empty stream")
	}
}

func TestSSETranslator_UsageInMessageDelta(t *testing.T) {
	stream := buildOAIStream(
		`{"id":"chatcmpl-u","model":"m","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		`{"id":"chatcmpl-u","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
	)
	rec := httptest.NewRecorder()
	tr := anthropic.NewSSETranslator(rec, "m")
	if err := tr.Translate(strings.NewReader(stream)); err != nil {
		t.Fatalf("translate error: %v", err)
	}
	events := parseSSEEvents(t, rec.Body.String())
	for _, ev := range events {
		if ev["type"] == "message_delta" {
			usage, _ := ev["usage"].(map[string]interface{})
			if usage == nil {
				t.Error("message_delta has no usage field")
				continue
			}
			if usage["output_tokens"].(float64) != 3 {
				t.Errorf("output_tokens: want 3 got %v", usage["output_tokens"])
			}
		}
	}
}
