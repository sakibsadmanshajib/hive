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
