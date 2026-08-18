package anthropic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// SSETranslator is a stateful re-emitter that converts an OpenAI SSE stream
// (chat.completion.chunk) into the Anthropic streaming event sequence:
//
//	message_start
//	content_block_start  (index 0, text)
//	content_block_delta* (text_delta)
//	-- on first tool_call delta:
//	content_block_stop
//	content_block_start  (index N, tool_use, id+name)
//	content_block_delta* (input_json_delta)
//	-- end of stream:
//	content_block_stop
//	message_delta        (stop_reason, usage)
//	message_stop
//
// The translator never leaks provider names or upstream identifiers.
// clientAlias is the model name the client sent; it is echoed in message_start
// so upstream route identifiers never reach the client.
type SSETranslator struct {
	w       http.ResponseWriter
	flusher http.Flusher

	// clientAlias is the model alias echoed back to the client.
	clientAlias string

	// state
	messageID    string
	inputTokens  int
	outputTokens int
	stopReason   string

	openBlockIndex int  // index of the currently open content block
	hasOpenBlock   bool // whether a content_block_start has been emitted without stop

	// started records whether message_start has been emitted, and done whether
	// the terminal sequence has. Both live on the translator (rather than in a
	// single read loop) so bytes can be fed in incrementally as the delegated
	// handler writes them.
	started bool
	done    bool

	// ended records that the upstream [DONE] sentinel arrived. It is separate
	// from done because a caller that feeds lines one at a time may ignore the
	// FeedLine return value, and the promise in FeedLine's contract is that
	// lines after the sentinel are dropped rather than translated.
	ended bool

	// tool call accumulation: keyed by upstream tool_calls[].index
	toolBlocks map[int]toolBlockState

	// writeErr captures the first write error so Translate can surface it.
	writeErr error
}

type toolBlockState struct {
	blockIndex int    // Anthropic block index
	id         string // tool_use id
	name       string // function name
}

// NewSSETranslator creates a translator bound to the given ResponseWriter.
// clientAlias is echoed in message_start instead of any upstream model field.
// It sets the Anthropic SSE headers on the writer immediately.
func NewSSETranslator(w http.ResponseWriter, clientAlias string) *SSETranslator {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	return &SSETranslator{
		w:           w,
		flusher:     flusher,
		clientAlias: clientAlias,
		toolBlocks:  make(map[int]toolBlockState),
	}
}

// Translate reads an upstream OpenAI SSE body and emits the Anthropic event
// sequence to the bound ResponseWriter. It returns after the upstream [DONE]
// signal or on a read/write error.
func (t *SSETranslator) Translate(body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		if t.writeErr != nil {
			break
		}
		if streamEnded := t.FeedLine(scanner.Bytes()); streamEnded {
			break
		}
	}

	if t.writeErr != nil {
		return t.writeErr
	}

	t.Finish()

	if t.writeErr != nil {
		return t.writeErr
	}
	return scanner.Err()
}

// FeedLine consumes one raw line of an upstream OpenAI SSE body and emits any
// Anthropic events it implies. It reports whether the upstream stream has ended
// (the [DONE] sentinel), after which further lines are ignored.
//
// Feeding line by line, rather than only reading from an io.Reader, is what lets
// a translator sit in front of an in-process handler: the delegated handler
// writes bytes to a ResponseWriter, so there is no reader to scan, and buffering
// the whole response to create one would destroy time-to-first-token.
func (t *SSETranslator) FeedLine(line []byte) bool {
	if t.ended || t.done || t.writeErr != nil {
		return t.ended || t.done
	}
	if len(line) == 0 || !bytes.HasPrefix(line, []byte("data: ")) {
		return false
	}
	payload := bytes.TrimPrefix(line, []byte("data: "))
	if bytes.Equal(payload, []byte("[DONE]")) {
		t.ended = true
		return true
	}

	var chunk OAIChunk
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return false
	}

	if !t.started {
		t.started = true
		// Use the upstream chunk ID when present, otherwise generate a
		// unique ID per stream so concurrent streams never collide.
		t.messageID = chunk.ID
		if t.messageID == "" {
			t.messageID = "msg_" + uuid.New().String()
		} else if !strings.HasPrefix(t.messageID, "msg_") {
			t.messageID = "msg_" + t.messageID
		}
		t.emitMessageStart()
	}

	if chunk.Usage != nil {
		t.inputTokens = chunk.Usage.PromptTokens
		t.outputTokens = chunk.Usage.CompletionTokens
	}

	if len(chunk.Choices) == 0 {
		return false
	}
	choice := chunk.Choices[0]

	if choice.FinishReason != "" {
		t.stopReason = mapFinishReason(choice.FinishReason)
	}

	delta := choice.Delta

	if delta.Content != "" {
		if !t.hasOpenBlock {
			t.openTextBlock()
		}
		t.emitTextDelta(delta.Content)
	}

	for _, tc := range delta.ToolCalls {
		t.handleToolCallDelta(tc)
	}

	return false
}

// Finish emits the terminal Anthropic event sequence. It is idempotent, so a
// caller that has already seen [DONE] can still call it unconditionally.
func (t *SSETranslator) Finish() {
	if t.done {
		return
	}
	t.done = true

	if t.hasOpenBlock {
		t.emitContentBlockStop(t.openBlockIndex)
		t.hasOpenBlock = false
	}
	t.emitMessageDelta()
	t.emitMessageStop()
}

// WriteErr returns the first write error the translator hit, if any.
func (t *SSETranslator) WriteErr() error { return t.writeErr }

// --- emitters ---

func (t *SSETranslator) emitMessageStart() {
	ev := StreamEvent{
		Type: "message_start",
		Message: &StreamMessage{
			ID:           t.messageID,
			Type:         "message",
			Role:         "assistant",
			Model:        t.clientAlias,
			Content:      []string{},
			StopReason:   nil,
			StopSequence: nil,
			Usage:        StreamUsage{InputTokens: t.inputTokens},
		},
	}
	t.writeEvent("message_start", ev)
	t.writePing()
}

func (t *SSETranslator) openTextBlock() {
	t.openBlockIndex = 0
	t.hasOpenBlock = true
	t.writeEvent("content_block_start", StreamEvent{
		Type:  "content_block_start",
		Index: indexPtr(0),
		ContentBlock: &StreamContentBlock{
			Type: "text",
			Text: "",
		},
	})
}

func (t *SSETranslator) emitTextDelta(text string) {
	t.writeEvent("content_block_delta", StreamEvent{
		Type:  "content_block_delta",
		Index: indexPtr(t.openBlockIndex),
		Delta: &StreamDelta{
			Type: "text_delta",
			Text: text,
		},
	})
}

func (t *SSETranslator) handleToolCallDelta(tc OAIToolCallDelta) {
	bs, exists := t.toolBlocks[tc.Index]
	if !exists {
		hadOpenBlock := t.hasOpenBlock
		if t.hasOpenBlock {
			t.emitContentBlockStop(t.openBlockIndex)
			t.hasOpenBlock = false
		}
		var nextIndex int
		if len(t.toolBlocks) == 0 {
			if hadOpenBlock {
				nextIndex = 1
			} else {
				nextIndex = 0
			}
		} else {
			maxIdx := 0
			for _, existing := range t.toolBlocks {
				if existing.blockIndex > maxIdx {
					maxIdx = existing.blockIndex
				}
			}
			nextIndex = maxIdx + 1
		}

		bs = toolBlockState{
			blockIndex: nextIndex,
			id:         tc.ID,
			name:       tc.Function.Name,
		}
		t.toolBlocks[tc.Index] = bs

		t.openBlockIndex = nextIndex
		t.hasOpenBlock = true
		t.writeEvent("content_block_start", StreamEvent{
			Type:  "content_block_start",
			Index: indexPtr(nextIndex),
			ContentBlock: &StreamContentBlock{
				Type: "tool_use",
				ID:   tc.ID,
				Name: tc.Function.Name,
			},
		})
	}

	if tc.Function.Arguments != "" {
		t.writeEvent("content_block_delta", StreamEvent{
			Type:  "content_block_delta",
			Index: indexPtr(bs.blockIndex),
			Delta: &StreamDelta{
				Type:        "input_json_delta",
				PartialJSON: tc.Function.Arguments,
			},
		})
	}
}

func (t *SSETranslator) emitContentBlockStop(index int) {
	t.writeEvent("content_block_stop", StreamEvent{
		Type:  "content_block_stop",
		Index: indexPtr(index),
	})
}

// indexPtr exists only so StreamEvent.Index (a pointer, so message_start and
// message_delta events which never set it serialize with no "index" key at
// all) can still be assigned the address of a literal int at each
// content_block_* call site.
func indexPtr(i int) *int { return &i }

func (t *SSETranslator) emitMessageDelta() {
	if t.stopReason == "" {
		t.stopReason = "end_turn"
	}
	t.writeEvent("message_delta", StreamEvent{
		Type: "message_delta",
		Delta: &StreamDelta{
			Type:       "message_delta",
			StopReason: t.stopReason,
		},
		Usage: &StreamUsage{OutputTokens: t.outputTokens},
	})
}

func (t *SSETranslator) emitMessageStop() {
	t.writeEvent("message_stop", map[string]string{"type": "message_stop"})
}

func (t *SSETranslator) writePing() {
	t.writeRaw("event: ping\ndata: {\"type\":\"ping\"}\n\n")
}

func (t *SSETranslator) writeEvent(eventType string, data interface{}) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	t.writeRaw("event: " + eventType + "\ndata: " + string(b) + "\n\n")
}

func (t *SSETranslator) writeRaw(s string) {
	if t.writeErr != nil {
		return
	}
	_, err := fmt.Fprint(t.w, s)
	if err != nil {
		t.writeErr = err
		return
	}
	if t.flusher != nil {
		t.flusher.Flush()
	}
}
