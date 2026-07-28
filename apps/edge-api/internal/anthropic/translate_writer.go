package anthropic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// maxTranslatedBodyBytes bounds how much of a buffered response the translator
// holds in memory, matching the ceiling the OpenAI sync path already applies
// when reading an upstream body.
const maxTranslatedBodyBytes = 10 << 20

type translateMode int

const (
	modeUndecided translateMode = iota
	// modeForwardError forwards a non-2xx response unchanged. The chat and
	// inference paths own the upstream boundary and already sanitize what they
	// emit there (errors.WriteProviderBlindUpstreamError strips provider names,
	// route slugs and upstream exception classes), and their refusals use the
	// same OpenAI error envelope this package uses for its own validation
	// failures. Re-wrapping them here would add a second, weaker sanitizer.
	modeForwardError
	// modeStream translates the response event by event as it arrives.
	modeStream
	// modeCollect buffers the whole response and translates it once the
	// delegated handler returns: either a single OpenAI completion body, or a
	// stream folded back into one Anthropic message for a caller that did not
	// ask to stream.
	modeCollect
)

// translatingWriter is the http.ResponseWriter handed to the OpenAI
// chat-completions handler chain when it is invoked in-process on behalf of
// POST /v1/messages. It converts that chain's OpenAI-shaped response into the
// Anthropic Messages wire format and writes the result to the real client
// writer.
//
// The delegated chain decides everything about the response headers and status;
// this type only decides how to re-shape the body, so a refusal, a stream and a
// single completion each reach the caller in the form an Anthropic client
// expects.
type translatingWriter struct {
	dst         http.ResponseWriter
	clientAlias string
	wantStream  bool

	header http.Header
	status int
	mode   translateMode

	sse      *SSETranslator
	pending  bytes.Buffer // trailing partial SSE line, stream mode only
	body     bytes.Buffer // whole response body, collect and error modes
	overflow bool
}

func newTranslatingWriter(dst http.ResponseWriter, clientAlias string, wantStream bool) *translatingWriter {
	return &translatingWriter{
		dst:         dst,
		clientAlias: clientAlias,
		wantStream:  wantStream,
		header:      make(http.Header),
	}
}

// Header returns a private header map. The delegated handler's headers describe
// an OpenAI response, so they are read when choosing a translation mode but
// never copied to the client verbatim.
func (t *translatingWriter) Header() http.Header { return t.header }

func (t *translatingWriter) WriteHeader(status int) {
	if t.mode != modeUndecided {
		return
	}
	t.status = status
	t.decide()
}

func (t *translatingWriter) Write(p []byte) (int, error) {
	if t.mode == modeUndecided {
		t.status = http.StatusOK
		t.decide()
	}

	if t.mode == modeStream {
		t.feed(p)
	} else {
		t.collect(p)
	}

	// Always report a full write: buffering for translation is not a short
	// write, and the delegated handler must not treat it as one.
	return len(p), nil
}

// Flush is required, not optional: the streaming orchestrator refuses to stream
// at all unless its ResponseWriter is an http.Flusher.
func (t *translatingWriter) Flush() {
	if t.mode != modeStream {
		return
	}
	if flusher, ok := t.dst.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (t *translatingWriter) decide() {
	switch {
	case t.status < 200 || t.status > 299:
		t.mode = modeForwardError
	case t.wantStream && strings.HasPrefix(t.header.Get("Content-Type"), "text/event-stream"):
		t.mode = modeStream
		// Commits the Anthropic SSE headers and a 200 to the client.
		t.sse = NewSSETranslator(t.dst, t.clientAlias)
	default:
		t.mode = modeCollect
	}
}

// feed splits raw downstream bytes into SSE lines and hands each complete line
// to the translator. A write can end mid-line, so the remainder is held until
// the rest arrives. Both producers bound their own line length (the streaming
// relay scans with a 512 KiB limit, the chat dispatcher with 4 MiB), so the
// pending buffer cannot grow without bound.
func (t *translatingWriter) feed(p []byte) {
	t.pending.Write(p)
	for {
		idx := bytes.IndexByte(t.pending.Bytes(), '\n')
		if idx < 0 {
			return
		}
		line := make([]byte, idx)
		copy(line, t.pending.Bytes()[:idx])
		t.pending.Next(idx + 1)
		t.sse.FeedLine(bytes.TrimSuffix(line, []byte("\r")))
	}
}

func (t *translatingWriter) collect(p []byte) {
	if t.overflow {
		return
	}
	if t.body.Len()+len(p) > maxTranslatedBodyBytes {
		t.overflow = true
		t.body.Reset()
		return
	}
	t.body.Write(p)
}

// finish completes the translation. It must be called once the delegated
// handler has returned, and reports any error hit while writing to the client.
func (t *translatingWriter) finish() error {
	switch t.mode {
	case modeStream:
		if t.pending.Len() > 0 {
			t.sse.FeedLine(bytes.TrimSuffix(t.pending.Bytes(), []byte("\r")))
			t.pending.Reset()
		}
		t.sse.Finish()
		return t.sse.WriteErr()

	case modeForwardError:
		t.forwardError()
		return nil

	case modeCollect:
		return t.writeTranslated()

	default:
		// The delegated handler returned without writing a response at all.
		t.writeUpstreamError("the request could not be completed")
		return errors.New("anthropic: delegated handler wrote no response")
	}
}

func (t *translatingWriter) forwardError() {
	if t.overflow || t.body.Len() == 0 {
		apierr.WriteProviderBlindUpstreamError(t.dst, t.clientAlias, t.status, "")
		return
	}
	contentType := t.header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	t.dst.Header().Set("Content-Type", contentType)
	t.dst.WriteHeader(t.status)
	_, _ = t.dst.Write(t.body.Bytes())
}

func (t *translatingWriter) writeTranslated() error {
	if t.overflow {
		t.writeUpstreamError("the response was too large to return")
		return errors.New("anthropic: delegated response exceeded the translation buffer")
	}

	resp, err := parseOAIResult(t.body.Bytes())
	if err != nil {
		t.writeUpstreamError("the response could not be returned")
		return err
	}

	if t.wantStream {
		// Defensive: the caller asked to stream but the delegated handler
		// answered with a single body. Re-emit it as a one-message Anthropic
		// stream rather than breaking the caller's expected content type.
		translator := NewSSETranslator(t.dst, t.clientAlias)
		if line, marshalErr := json.Marshal(chunkFromOAIResponse(resp)); marshalErr == nil {
			translator.FeedLine(append([]byte("data: "), line...))
		}
		translator.Finish()
		return translator.WriteErr()
	}

	t.dst.Header().Set("Content-Type", "application/json")
	t.dst.WriteHeader(http.StatusOK)
	return json.NewEncoder(t.dst).Encode(FromOAIResponse(resp, t.clientAlias))
}

func (t *translatingWriter) writeUpstreamError(message string) {
	code := "upstream_error"
	apierr.WriteError(t.dst, http.StatusBadGateway, "api_error", message, &code)
}

// parseOAIResult reads a completed OpenAI-shaped response, accepting either a
// single completion body or a full SSE stream.
func parseOAIResult(raw []byte) (OAIResponse, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return OAIResponse{}, errors.New("anthropic: empty response body")
	}
	if bytes.HasPrefix(trimmed, []byte("data:")) || bytes.HasPrefix(trimmed, []byte("event:")) {
		return foldOAIStream(trimmed)
	}

	var resp OAIResponse
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return OAIResponse{}, err
	}
	return resp, nil
}

// foldOAIStream folds an OpenAI SSE stream into the single non-streaming
// response shape, so a caller that did not ask to stream can still be served
// from one. This is the normal path for a chat session, whose dispatcher streams
// unconditionally.
func foldOAIStream(raw []byte) (OAIResponse, error) {
	var (
		out          OAIResponse
		content      strings.Builder
		finishReason string
		toolOrder    []int
		toolCalls    = map[int]*OAIToolCall{}
		toolArgs     = map[int]*strings.Builder{}
		sawChunk     bool
	)

	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}

		var chunk OAIChunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			continue
		}
		sawChunk = true

		if out.ID == "" {
			out.ID = chunk.ID
		}
		if chunk.Usage != nil {
			out.Usage = *chunk.Usage
		}

		for _, choice := range chunk.Choices {
			content.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			for _, delta := range choice.Delta.ToolCalls {
				call, seen := toolCalls[delta.Index]
				if !seen {
					call = &OAIToolCall{Type: "function"}
					toolCalls[delta.Index] = call
					toolArgs[delta.Index] = &strings.Builder{}
					toolOrder = append(toolOrder, delta.Index)
				}
				if delta.ID != "" {
					call.ID = delta.ID
				}
				if delta.Function.Name != "" {
					call.Function.Name = delta.Function.Name
				}
				toolArgs[delta.Index].WriteString(delta.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return OAIResponse{}, err
	}
	if !sawChunk {
		return OAIResponse{}, errors.New("anthropic: response carried no completion data")
	}

	message := OAIMsg{Role: "assistant", Content: content.String()}
	for _, index := range toolOrder {
		call := *toolCalls[index]
		call.Function.Arguments = toolArgs[index].String()
		message.ToolCalls = append(message.ToolCalls, call)
	}

	out.Object = "chat.completion"
	out.Choices = []OAIChoice{{Index: 0, Message: message, FinishReason: finishReason}}
	return out, nil
}

// chunkFromOAIResponse lowers a single completion body back into one streaming
// chunk, so the SSE translator can serve it without a second code path.
func chunkFromOAIResponse(resp OAIResponse) OAIChunk {
	chunk := OAIChunk{ID: resp.ID, Object: "chat.completion.chunk", Usage: &resp.Usage}
	if len(resp.Choices) == 0 {
		return chunk
	}

	choice := resp.Choices[0]
	delta := OAIDelta{Role: choice.Message.Role, Content: choice.Message.Content}
	for index, call := range choice.Message.ToolCalls {
		delta.ToolCalls = append(delta.ToolCalls, OAIToolCallDelta{
			Index: index,
			ID:    call.ID,
			Type:  call.Type,
			Function: OAIFunctionCallDelta{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	chunk.Choices = []OAIChunkChoice{{Index: 0, Delta: delta, FinishReason: choice.FinishReason}}
	return chunk
}
