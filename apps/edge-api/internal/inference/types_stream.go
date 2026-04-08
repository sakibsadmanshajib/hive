package inference

import "encoding/json"

// ChatCompletionChunk is a streaming chunk for chat completions.
type ChatCompletionChunk struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	SystemFingerprint *string        `json:"system_fingerprint,omitempty"`
	Choices           []ChunkChoice  `json:"choices"`
	Usage             *UsageResponse `json:"usage,omitempty"`
}

// ChunkChoice is a single choice in a streaming chunk.
type ChunkChoice struct {
	Index        int             `json:"index"`
	Delta        ChunkDelta      `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs,omitempty"`
}

// ChunkDelta is the incremental content in a streaming chunk choice.
type ChunkDelta struct {
	Role         *string         `json:"role,omitempty"`
	Content      *string         `json:"content,omitempty"`
	ToolCalls    json.RawMessage `json:"tool_calls,omitempty"`
	FunctionCall json.RawMessage `json:"function_call,omitempty"`
	Refusal      *string         `json:"refusal,omitempty"`
}
