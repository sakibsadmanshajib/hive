// Package anthropic provides a translation layer between the Anthropic Messages
// wire format and the internal OpenAI-shaped dispatch core. It never calls real
// Anthropic, and it never dispatches upstream itself: requests are lowered to the
// internal chat shape, handed to the wired POST /v1/chat/completions chain (which
// owns alias resolution, entitlement, metering and the upstream boundary), and
// responses are lifted back to Anthropic shape.
package anthropic

import "encoding/json"

// --------------------------------------------------------------------------
// Inbound request types (Anthropic -> internal)
// --------------------------------------------------------------------------

// MessagesRequest is the Anthropic POST /v1/messages request body.
type MessagesRequest struct {
	Model         string           `json:"model"`
	Messages      []Message        `json:"messages"`
	System        SystemField      `json:"system,omitempty"`
	MaxTokens     int              `json:"max_tokens"`
	Tools         []Tool           `json:"tools,omitempty"`
	ToolChoice    *ToolChoice      `json:"tool_choice,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	TopP          *float64         `json:"top_p,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	Metadata      *RequestMetadata `json:"metadata,omitempty"`
	// CacheControl, at request root, is a valid alternative to a per-block
	// breakpoint: it applies a single ephemeral cache marker to the whole
	// request instead of the caller placing one on each block by hand. Kept
	// as a pointer so an absent key stays absent (see CacheControl doc).
	CacheControl *CacheControl `json:"cache_control,omitempty"`
	// SessionID is not part of Anthropic's own /v1/messages schema; it exists
	// here purely as passthrough for a client or proxy that injects it to ask
	// OpenRouter for sticky provider routing, which is what keeps a warm
	// cache addressed at the same upstream instance across turns. Preserved
	// verbatim rather than dropped, capped at the 256 chars OpenRouter
	// documents; a longer value is truncated rather than rejected, since this
	// is a routing hint, not a correctness-bearing field.
	SessionID string `json:"session_id,omitempty"`
}

// RequestMetadata carries optional caller metadata (unused in routing).
type RequestMetadata struct {
	UserID string `json:"user_id,omitempty"`
}

// CacheControl marks an Anthropic prompt-caching breakpoint. It is valid on a
// content block, a system prompt block, a tool definition, and the request
// root. It is always a pointer field so an absent cache_control stays absent
// on the wire: an empty {} object is a materially different request from no
// key at all (Anthropic's own API rejects certain shapes with an empty
// cache_control differently from a request that never mentions it), so a
// plain (non-pointer) struct with omitempty would be wrong here -- Go's
// encoding/json does not treat a zero-value struct as empty for omitempty
// purposes, meaning a bare CacheControl{} field would always serialize.
type CacheControl struct {
	Type string `json:"type"` // "ephemeral" is the only type Anthropic defines today.
	// TTL is "5m" (the default when omitted) or "1h". Omitted rather than
	// forced to "5m" so a caller that never set it gets back exactly the
	// request it sent, and so the 1h/5m write-price split (2x vs 1.25x) can
	// be recovered downstream from what the caller actually asked for.
	TTL string `json:"ttl,omitempty"`
}

// SystemField can be a plain string or an array of TextBlocks.
// We use a custom unmarshaller so neither form needs an any field.
type SystemField struct {
	Text string
	// Blocks preserves the original array-form blocks (nil for a plain-string
	// system field). Text alone collapses every block to one concatenated
	// string, which loses each block's cache_control breakpoint; Blocks is
	// what ToOAIRequest consults to keep that data when it is present.
	Blocks []ContentBlock
}

func (s *SystemField) UnmarshalJSON(b []byte) error {
	// Try string first.
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		s.Text = str
		return nil
	}
	// Fall back to array of content blocks; concatenate text.
	var blocks []ContentBlock
	if err := json.Unmarshal(b, &blocks); err != nil {
		return err
	}
	for _, bl := range blocks {
		if bl.Type == "text" {
			s.Text += bl.Text
		}
	}
	s.Blocks = blocks
	return nil
}

// Message is a single conversation turn.
type Message struct {
	Role    string         `json:"role"` // "user" | "assistant"
	Content MessageContent `json:"content"`
}

// MessageContent can be a plain string or a []ContentBlock. Custom unmarshaller.
type MessageContent struct {
	Text   string
	Blocks []ContentBlock
}

func (mc *MessageContent) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		mc.Text = str
		return nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(b, &blocks); err != nil {
		return err
	}
	mc.Blocks = blocks
	return nil
}

// ContentBlock is a typed content unit within a message.
type ContentBlock struct {
	// Shared
	Type string `json:"type"` // "text"|"image"|"tool_use"|"tool_result"

	// type=text
	Text string `json:"text,omitempty"`

	// type=image
	Source *ImageSource `json:"source,omitempty"`

	// type=tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// type=tool_result
	ToolUseID string             `json:"tool_use_id,omitempty"`
	Content   *ToolResultContent `json:"content,omitempty"`

	// CacheControl is valid on any block type above: text, image, tool_use,
	// and tool_result all support a cache breakpoint.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ImageSource describes an image payload (base64 or URL).
type ImageSource struct {
	Type      string `json:"type"`       // "base64" | "url"
	MediaType string `json:"media_type"` // e.g. "image/png"
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// ToolResultContent can be a string or []ContentBlock. Custom unmarshaller.
type ToolResultContent struct {
	Text   string
	Blocks []ContentBlock
}

func (t *ToolResultContent) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		t.Text = str
		return nil
	}
	var blocks []ContentBlock
	if err := json.Unmarshal(b, &blocks); err != nil {
		return err
	}
	t.Blocks = blocks
	return nil
}

// Tool describes an Anthropic function tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
	// CacheControl on any one tool caches the whole tools array up to and
	// including that entry -- Anthropic has no separate "cache the tools as
	// a group" field, this per-tool placement is the group mechanism.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ToolChoice controls how the model selects tools.
type ToolChoice struct {
	Type string `json:"type"` // "auto" | "any" | "tool"
	Name string `json:"name,omitempty"`
}

// --------------------------------------------------------------------------
// Outbound response types (internal -> Anthropic)
// --------------------------------------------------------------------------

// MessagesResponse is the Anthropic /v1/messages non-streaming response.
type MessagesResponse struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"` // always "message"
	Role         string          `json:"role"` // always "assistant"
	Model        string          `json:"model"`
	Content      []ResponseBlock `json:"content"`
	StopReason   string          `json:"stop_reason"`
	StopSequence *string         `json:"stop_sequence,omitempty"`
	Usage        ResponseUsage   `json:"usage"`
}

// ResponseBlock is a content block in a response (text or tool_use).
type ResponseBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ResponseUsage carries token counts in the Anthropic response envelope.
// This is the EXCLUSIVE shape real Anthropic clients (Claude Code included)
// read cache savings from: InputTokens is the fresh/uncached remainder only,
// never the total prompt size, and CacheCreationInputTokens /
// CacheReadInputTokens are reported alongside it rather than folded in. See
// freshInputTokens in translate_response.go for the arithmetic that gets an
// inclusive upstream number (OpenRouter's prompt_tokens, which already
// contains the cached and written tokens) into this exclusive shape.
type ResponseUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// CountTokensResponse is the /v1/messages/count_tokens response.
type CountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// --------------------------------------------------------------------------
// SSE streaming event types
// --------------------------------------------------------------------------

// StreamEvent is a typed SSE event emitted in an Anthropic streaming response.
type StreamEvent struct {
	Type string `json:"type"`
	// Index is a pointer because 0 is a valid, common index (the first and
	// often only content block) that must still serialize, while message_start
	// and message_delta (which reuse this same struct and never set Index) must
	// carry no "index" key at all -- the real Anthropic protocol has no such
	// field on those two event types. A plain int with `omitempty` dropped the
	// key for index 0 as readily as for "unset", which is exactly the bug this
	// type used to have (see TestSSETranslator_FirstBlockIndexIsPresentAndZero).
	Index *int `json:"index,omitempty"`

	// message_start
	Message *StreamMessage `json:"message,omitempty"`

	// content_block_start
	ContentBlock *StreamContentBlock `json:"content_block,omitempty"`

	// content_block_delta
	Delta *StreamDelta `json:"delta,omitempty"`

	// message_delta
	Usage *StreamUsage `json:"usage,omitempty"`
}

// StreamMessage is the partial message envelope in message_start.
// StopReason and StopSequence are *string so nil serialises as JSON null
// (absent), matching the Anthropic protocol (stop_reason is null at start).
type StreamMessage struct {
	ID           string      `json:"id"`
	Type         string      `json:"type"`
	Role         string      `json:"role"`
	Model        string      `json:"model"`
	Content      []string    `json:"content"` // always empty at message_start
	StopReason   *string     `json:"stop_reason"`
	StopSequence *string     `json:"stop_sequence"`
	Usage        StreamUsage `json:"usage"`
}

// StreamContentBlock is the block header in content_block_start.
type StreamContentBlock struct {
	Type string `json:"type"` // "text" | "tool_use"
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Text string `json:"text,omitempty"`
}

// StreamDelta is the delta payload in content_block_delta or message_delta.
type StreamDelta struct {
	Type         string  `json:"type"` // "text_delta" | "input_json_delta" | "message_delta"
	Text         string  `json:"text,omitempty"`
	PartialJSON  string  `json:"partial_json,omitempty"`
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// StreamUsage carries token counts in streaming events. Same exclusive shape
// as ResponseUsage (see its doc comment): InputTokens excludes both cache
// fields below, it is never the raw upstream prompt_tokens total.
type StreamUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// --------------------------------------------------------------------------
// Internal OpenAI-shaped types used by the translator.
// These mirror the fields the chat.Handler and LiteLLM understand.
// --------------------------------------------------------------------------

// OAIToolChoice is a typed union for the OpenAI tool_choice field.
// Either a string sentinel ("auto", "required", "none") or a named function
// selector. This replaces the bare interface{} to keep the type-safe promise.
type OAIToolChoice struct {
	// Sentinel is set when tool_choice is "auto", "required", or "none".
	Sentinel string
	// Named is set when tool_choice selects a specific function.
	Named *OAINamedToolChoice
}

// OAINamedToolChoice is the structured form of a named function selector.
type OAINamedToolChoice struct {
	Type     string                     `json:"type"` // always "function"
	Function OAINamedToolChoiceFunction `json:"function"`
}

// OAINamedToolChoiceFunction holds the function name for a named tool choice.
type OAINamedToolChoiceFunction struct {
	Name string `json:"name"`
}

// MarshalJSON serialises OAIToolChoice to either a JSON string or an object.
func (tc OAIToolChoice) MarshalJSON() ([]byte, error) {
	if tc.Named != nil {
		return json.Marshal(tc.Named)
	}
	return json.Marshal(tc.Sentinel)
}

// OAIRequest is the OpenAI chat completions request shape.
type OAIRequest struct {
	Model         string            `json:"model"`
	Messages      []OAIMessage      `json:"messages"`
	Tools         []OAITool         `json:"tools,omitempty"`
	ToolChoice    *OAIToolChoice    `json:"tool_choice,omitempty"`
	MaxTokens     *int              `json:"max_tokens,omitempty"`
	Stop          []string          `json:"stop,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StreamOptions *OAIStreamOptions `json:"stream_options,omitempty"`
	// CacheControl carries a request-root cache breakpoint straight through.
	// This body is forwarded to LiteLLM (and from there to OpenRouter)
	// essentially byte-for-byte -- see litellm_client.go's rewriteModel,
	// which only ever touches the "model" key -- so this field surviving the
	// marshal here is what makes it survive the whole hop.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
	// SessionID: see MessagesRequest.SessionID.
	SessionID string `json:"session_id,omitempty"`
}

// OAIStreamOptions mirrors OpenAI's stream_options object. IncludeUsage asks
// upstream for a terminal usage frame, which is what lets the metered dispatch
// path settle a credit reservation at real token counts instead of the flat
// pre-dispatch estimate.
type OAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// OAIMessageContent is a typed union for an OAI message's content field.
// Either a plain string or a multipart content-parts array.
type OAIMessageContent struct {
	// Text is set when content is a plain string.
	Text string
	// Parts is set when content is a multipart array (vision, etc.).
	Parts []OAIContentPart
}

// MarshalJSON serialises OAIMessageContent to either a JSON string or array.
func (c OAIMessageContent) MarshalJSON() ([]byte, error) {
	if len(c.Parts) > 0 {
		return json.Marshal(c.Parts)
	}
	return json.Marshal(c.Text)
}

// OAIMessage is a single OpenAI chat message.
type OAIMessage struct {
	Role       string            `json:"role"`
	Content    OAIMessageContent `json:"content,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	ToolCalls  []OAIToolCall     `json:"tool_calls,omitempty"`
	// CacheControl carries a breakpoint that collapsed onto this whole
	// message rather than one of its content parts -- the tool_result case,
	// where Content is always a single flattened string with no parts array
	// to hang a per-part cache_control on.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// OAIToolCall is an assistant tool invocation.
type OAIToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // "function"
	Function OAIFunctionCall `json:"function"`
	// CacheControl carries a breakpoint from an Anthropic tool_use content
	// block onto its lowered tool-call form.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// OAIFunctionCall holds the function name and JSON-stringified arguments.
type OAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OAITool is an OpenAI function tool definition.
type OAITool struct {
	Type     string      `json:"type"` // "function"
	Function OAIFunction `json:"function"`
	// CacheControl sits at the top level, sibling of Type and Function,
	// mirroring where Anthropic places it on its own Tool object.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// OAIFunction holds a function definition.
type OAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// OAIImageURL is used inside content part arrays for vision.
type OAIImageURL struct {
	URL string `json:"url"`
}

// OAIContentPart is a typed part inside a multipart content array.
type OAIContentPart struct {
	Type     string       `json:"type"` // "text" | "image_url"
	Text     string       `json:"text,omitempty"`
	ImageURL *OAIImageURL `json:"image_url,omitempty"`
	// CacheControl carries an Anthropic content-block breakpoint through
	// unchanged: same placement (sibling of "type"), same shape.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// OAIResponse is the OpenAI chat completions response shape.
type OAIResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Model   string      `json:"model"`
	Choices []OAIChoice `json:"choices"`
	Usage   OAIUsage    `json:"usage"`
}

// OAIChoice is a single completion choice.
type OAIChoice struct {
	Index        int    `json:"index"`
	Message      OAIMsg `json:"message"`
	FinishReason string `json:"finish_reason"`
}

// OAIMsg is the message body in a choice.
type OAIMsg struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	ToolCalls []OAIToolCall `json:"tool_calls,omitempty"`
}

// OAIUsage holds token counts from the OpenAI response. PromptTokens here is
// INCLUSIVE, the OpenAI/OpenRouter convention: it already contains whatever
// PromptTokensDetails.CachedTokens and CacheWriteTokens report, unlike the
// Anthropic-native EXCLUSIVE convention ResponseUsage/StreamUsage use for the
// client-facing wire. freshInputTokens in translate_response.go does that
// conversion; nothing here does it, this type only has to capture the
// upstream number.
//
// This struct is populated by unmarshaling the bytes the inference package's
// own chat/completions handler already wrote (it does its own typed
// round-trip through inference.UsageResponse before this package ever sees a
// byte), so PromptTokensDetails only carries data end-to-end once that struct
// also declares matching fields with these same JSON names -- see the PR body
// merge-coordination note.
type OAIUsage struct {
	PromptTokens        int                     `json:"prompt_tokens"`
	CompletionTokens    int                     `json:"completion_tokens"`
	TotalTokens         int                     `json:"total_tokens"`
	PromptTokensDetails *OAIPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// OAIPromptTokensDetails is the cache breakdown nested under usage in the
// OpenAI-compatible shape OpenRouter reports for a cached Anthropic request.
// This struct is only ever unmarshaled here, never marshaled out to a real
// OpenAI-compatible client (that outbound contract belongs to the
// inference package's own response types, not this one) -- it exists purely
// so this package can read the numbers back out of them for the Anthropic
// echo in translate_response.go and stream.go.
type OAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	// CacheWriteTokens has no OpenAI-native counterpart (OpenAI itself never
	// bills a cache write); OpenRouter adds it as an extension when the
	// upstream model does.
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// OAIDelta is a streaming delta chunk.
type OAIDelta struct {
	Role      string             `json:"role,omitempty"`
	Content   string             `json:"content,omitempty"`
	ToolCalls []OAIToolCallDelta `json:"tool_calls,omitempty"`
}

// OAIToolCallDelta is a partial tool call in a streaming chunk.
type OAIToolCallDelta struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function OAIFunctionCallDelta `json:"function,omitempty"`
}

// OAIFunctionCallDelta is a partial function call in a streaming delta.
type OAIFunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// OAIChunkChoice is a single choice in a streaming chunk.
type OAIChunkChoice struct {
	Index        int      `json:"index"`
	Delta        OAIDelta `json:"delta"`
	FinishReason string   `json:"finish_reason,omitempty"`
}

// OAIChunk is a single SSE data frame in an OpenAI streaming response.
type OAIChunk struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Model   string           `json:"model"`
	Choices []OAIChunkChoice `json:"choices"`
	Usage   *OAIUsage        `json:"usage,omitempty"`
}
