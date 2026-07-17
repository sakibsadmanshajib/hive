// Package rag implements the /v1/rag/* edge endpoints.
// All endpoints are gated by featuregate.FeatureRAG and scoped to the
// authenticated tenant via RLS.
package rag

import "time"

// DocumentStatus mirrors the rag_documents.status CHECK constraint.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusEmbedded   = "embedded"
	StatusError      = "error"
)

// EmbeddingDimension is 1024 (bge-m3). Must agree with the migration.
const EmbeddingDimension = 1024

// UploadRequest is the JSON body for POST /v1/rag/documents.
type UploadRequest struct {
	Name     string `json:"name"`
	Content  string `json:"content"`   // raw text; future: accept base64 PDF
	MimeType string `json:"mime_type"` // optional, defaults to text/plain
}

// DocumentResponse is the JSON body returned for a single document.
type DocumentResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// SearchRequest is the JSON body for POST /v1/rag/search.
type SearchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"` // defaults to 5 when zero
}

// ChunkResult is one item in a SearchResponse.
type ChunkResult struct {
	ChunkID    string  `json:"chunk_id"`
	DocumentID string  `json:"document_id"`
	Content    string  `json:"content"`
	Score      float32 `json:"score"`
}

// SearchResponse is the JSON body returned by POST /v1/rag/search.
type SearchResponse struct {
	Results []ChunkResult `json:"results"`
}

// ChatMessage is a single OpenAI-style message (role + plain-text content).
// Multi-part content (images, tool calls) is not supported by grounded
// generation; only the last "user" message drives retrieval.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the JSON body for POST /v1/rag/chat.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	TopK     int           `json:"top_k"` // defaults to 5 when zero, capped at maxTopK
	// Stream requests an SSE response (#339): a retrieval-first citations
	// frame followed by the relayed upstream chat.completion.chunk frames and
	// a terminating data: [DONE].
	Stream bool `json:"stream,omitempty"`
}

// ChatChoice is a single choice in a ChatResponse.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason *string     `json:"finish_reason"`
}

// ChatUsage mirrors the OpenAI usage object (token counts only).
type ChatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// ChatResponse is the JSON body returned by POST /v1/rag/chat.
// Citations reuses ChunkResult so callers get the same shape as
// POST /v1/rag/search for the retrieved sources grounding the answer.
type ChatResponse struct {
	ID        string        `json:"id"`
	Object    string        `json:"object"`
	Model     string        `json:"model"`
	Choices   []ChatChoice  `json:"choices"`
	Usage     *ChatUsage    `json:"usage,omitempty"`
	Citations []ChunkResult `json:"citations"`
}
