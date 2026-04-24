package inference

import "encoding/json"

// Endpoint type constants.
const (
	EndpointChatCompletions = "chat_completions"
	EndpointCompletions     = "completions"
	EndpointResponses       = "responses"
	EndpointEmbeddings      = "embeddings"
)

// NeedFlags describes the capabilities required for route selection.
type NeedFlags struct {
	NeedChatCompletions bool
	NeedCompletions     bool
	NeedResponses       bool
	NeedEmbeddings      bool
	NeedStreaming        bool
	NeedReasoning       bool
	// NOTE: NeedToolCalling is intentionally omitted in Phase 6.
	// Tool-calling capability enforcement is delegated to LiteLLM's
	// provider-error response path. A future phase may add this field
	// for Hive-layer pre-dispatch gating.
}

// StreamOptions controls streaming behavior.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// --- Chat Completions ---

// ChatCompletionRequest is the OpenAI-compatible chat completion request.
type ChatCompletionRequest struct {
	Model               string          `json:"model"`
	Messages            json.RawMessage `json:"messages"`
	Stream              bool            `json:"stream,omitempty"`
	StreamOptions       *StreamOptions  `json:"stream_options,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	N                   *int            `json:"n,omitempty"`
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Stop                json.RawMessage `json:"stop,omitempty"`
	PresencePenalty     *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64        `json:"frequency_penalty,omitempty"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFormat      json.RawMessage `json:"response_format,omitempty"`
	ReasoningEffort     *string         `json:"reasoning_effort,omitempty"`
	Logprobs            *bool           `json:"logprobs,omitempty"`
	TopLogprobs         *int            `json:"top_logprobs,omitempty"`
	Seed                *int            `json:"seed,omitempty"`
	User                *string         `json:"user,omitempty"`
	Functions           json.RawMessage `json:"functions,omitempty"`
	FunctionCall        json.RawMessage `json:"function_call,omitempty"`
}

// ChatCompletionResponse is the OpenAI-compatible chat completion response.
type ChatCompletionResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	SystemFingerprint *string                `json:"system_fingerprint,omitempty"`
	Choices           []ChatCompletionChoice `json:"choices"`
	Usage             *UsageResponse         `json:"usage,omitempty"`
}

// ChatCompletionChoice is a single choice in a chat completion response.
type ChatCompletionChoice struct {
	Index        int                   `json:"index"`
	Message      ChatCompletionMessage `json:"message"`
	FinishReason *string               `json:"finish_reason"`
	Logprobs     json.RawMessage       `json:"logprobs,omitempty"`
}

// ChatCompletionMessage is a message in a chat completion choice.
type ChatCompletionMessage struct {
	Role         string          `json:"role"`
	Content      *string         `json:"content"`
	ToolCalls    json.RawMessage `json:"tool_calls,omitempty"`
	FunctionCall json.RawMessage `json:"function_call,omitempty"`
	Refusal      *string         `json:"refusal,omitempty"`
}

// --- Legacy Completions ---

// CompletionRequest is the OpenAI-compatible legacy completion request.
type CompletionRequest struct {
	Model         string          `json:"model"`
	Prompt        json.RawMessage `json:"prompt"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions *StreamOptions  `json:"stream_options,omitempty"`
	MaxTokens     *int            `json:"max_tokens,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	N             *int            `json:"n,omitempty"`
	Stop          json.RawMessage `json:"stop,omitempty"`
	Suffix        *string         `json:"suffix,omitempty"`
	Echo          *bool           `json:"echo,omitempty"`
	Logprobs      *int            `json:"logprobs,omitempty"`
	Seed          *int            `json:"seed,omitempty"`
	User          *string         `json:"user,omitempty"`
}

// CompletionResponse is the OpenAI-compatible legacy completion response.
type CompletionResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []CompletionChoice `json:"choices"`
	Usage   *UsageResponse     `json:"usage,omitempty"`
}

// CompletionChoice is a single choice in a legacy completion response.
type CompletionChoice struct {
	Text         string          `json:"text"`
	Index        int             `json:"index"`
	Logprobs     json.RawMessage `json:"logprobs,omitempty"`
	FinishReason *string         `json:"finish_reason"`
}

// --- Shared Usage ---

// UsageResponse is the OpenAI-compatible usage object.
type UsageResponse struct {
	PromptTokens            int64                    `json:"prompt_tokens"`
	CompletionTokens        int64                    `json:"completion_tokens"`
	TotalTokens             int64                    `json:"total_tokens"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
}

// CompletionTokensDetails is the breakdown of completion tokens.
type CompletionTokensDetails struct {
	ReasoningTokens          int64 `json:"reasoning_tokens"`
	AcceptedPredictionTokens int64 `json:"accepted_prediction_tokens"`
	RejectedPredictionTokens int64 `json:"rejected_prediction_tokens"`
}

// PromptTokensDetails is the breakdown of prompt tokens.
type PromptTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}

// --- Embeddings ---

// EmbeddingsRequest is the OpenAI-compatible embeddings request.
type EmbeddingsRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat *string         `json:"encoding_format,omitempty"`
	Dimensions     *int            `json:"dimensions,omitempty"`
	User           *string         `json:"user,omitempty"`
}

// EmbeddingsResponse is the OpenAI-compatible embeddings response.
type EmbeddingsResponse struct {
	Object string           `json:"object"`
	Data   []EmbeddingObject `json:"data"`
	Model  string           `json:"model"`
	Usage  *EmbeddingsUsage `json:"usage"`
}

// EmbeddingObject is a single embedding in the response.
type EmbeddingObject struct {
	Object    string          `json:"object"`
	Embedding json.RawMessage `json:"embedding"`
	Index     int             `json:"index"`
}

// EmbeddingsUsage is the usage object for embeddings responses.
type EmbeddingsUsage struct {
	PromptTokens int64 `json:"prompt_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

// --- Responses API ---

// ResponsesRequest is the OpenAI Responses API request.
type ResponsesRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Stream             bool            `json:"stream,omitempty"`
	Instructions       *string         `json:"instructions,omitempty"`
	Text               json.RawMessage `json:"text,omitempty"`
	Tools              json.RawMessage `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	Temperature        *float64        `json:"temperature,omitempty"`
	TopP               *float64        `json:"top_p,omitempty"`
	MaxOutputTokens    *int            `json:"max_output_tokens,omitempty"`
	Reasoning          json.RawMessage `json:"reasoning,omitempty"`
	Store              *bool           `json:"store,omitempty"`
	User               *string         `json:"user,omitempty"`
	PreviousResponseID *string         `json:"previous_response_id,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	Truncation         *string         `json:"truncation,omitempty"`
}

// ResponseObject is the OpenAI Responses API response object.
type ResponseObject struct {
	ID                string              `json:"id"`
	Object            string              `json:"object"`
	CreatedAt         int64               `json:"created_at"`
	Model             string              `json:"model"`
	Status            string              `json:"status"`
	Output            []ResponseOutputItem `json:"output"`
	Usage             *ResponsesUsage     `json:"usage,omitempty"`
	Text              json.RawMessage     `json:"text,omitempty"`
	Reasoning         json.RawMessage     `json:"reasoning"`
	Metadata          json.RawMessage     `json:"metadata"`
	Temperature       *float64            `json:"temperature,omitempty"`
	TopP              *float64            `json:"top_p,omitempty"`
	MaxOutputTokens   *int                `json:"max_output_tokens"`
	Truncation        *string             `json:"truncation,omitempty"`
	ToolChoice        json.RawMessage     `json:"tool_choice,omitempty"`
	Tools             json.RawMessage     `json:"tools"`
	IncompleteDetails json.RawMessage     `json:"incomplete_details"`
	Error             json.RawMessage     `json:"error"`
}

// ResponseOutputItem is a single output item in a Responses API response.
type ResponseOutputItem struct {
	Type    string                `json:"type"`
	ID      string                `json:"id"`
	Status  string                `json:"status"`
	Role    string                `json:"role"`
	Content []ResponseContentPart `json:"content"`
}

// ResponseContentPart is a content part in a Responses API output item.
type ResponseContentPart struct {
	Type        string            `json:"type"`
	Text        string            `json:"text"`
	Annotations []json.RawMessage `json:"annotations"`
}

// ResponsesUsage is the usage object for the Responses API (field names differ from chat/completions).
type ResponsesUsage struct {
	InputTokens         int64                `json:"input_tokens"`
	OutputTokens        int64                `json:"output_tokens"`
	TotalTokens         int64                `json:"total_tokens"`
	OutputTokensDetails *OutputTokensDetails `json:"output_tokens_details,omitempty"`
	InputTokensDetails  *InputTokensDetails  `json:"input_tokens_details,omitempty"`
}

// OutputTokensDetails is the breakdown of output tokens for the Responses API.
type OutputTokensDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

// InputTokensDetails is the breakdown of input tokens for the Responses API.
type InputTokensDetails struct {
	CachedTokens int64 `json:"cached_tokens"`
}
