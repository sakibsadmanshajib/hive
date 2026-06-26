// Package rag provides document ingestion: chunking, embedding, and storage.
// All operations are tenant-scoped; no cross-tenant access is possible.
package rag

import (
	"strings"
	"unicode"
)

const (
	// EmbeddingDimension is the vector size for bge-m3. Must agree with the
	// rag_chunks.embedding column definition in the migration.
	EmbeddingDimension = 1024

	// DefaultChunkTokens is the target chunk size in approximate tokens.
	// ~512 tokens at ~4 chars/token = ~2048 chars.
	DefaultChunkTokens = 512

	// DefaultOverlapTokens is the overlap between consecutive chunks.
	DefaultOverlapTokens = 64

	charsPerToken = 4
)

// Chunk is a contiguous text segment extracted from a document.
type Chunk struct {
	Index   int
	Content string
	// TokenCount is an approximation: len(Content)/charsPerToken.
	TokenCount int
}

// ChunkText splits text into overlapping chunks of approximately targetTokens
// tokens with overlapTokens of overlap. It preserves sentence boundaries
// where possible (splitting on newlines then sentences).
//
// Empty text returns an empty slice. A single chunk is returned when the
// text fits within one window.
func ChunkText(text string, targetTokens, overlapTokens int) []Chunk {
	if targetTokens <= 0 {
		targetTokens = DefaultChunkTokens
	}
	if overlapTokens < 0 {
		overlapTokens = 0
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	targetChars := targetTokens * charsPerToken
	overlapChars := overlapTokens * charsPerToken

	if len(text) <= targetChars {
		return []Chunk{{
			Index:      0,
			Content:    text,
			TokenCount: approxTokens(text),
		}}
	}

	// Split into sentences (period/newline boundaries) for cleaner cuts.
	sentences := splitSentences(text)

	var chunks []Chunk
	var buf strings.Builder
	idx := 0

	flush := func() {
		content := strings.TrimSpace(buf.String())
		if content == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Index:      idx,
			Content:    content,
			TokenCount: approxTokens(content),
		})
		idx++
	}

	for _, sent := range sentences {
		if buf.Len()+len(sent) > targetChars && buf.Len() > 0 {
			flush()
			// Overlap: keep the tail of the previous chunk.
			prev := buf.String()
			buf.Reset()
			if overlapChars > 0 && len(prev) > overlapChars {
				buf.WriteString(prev[len(prev)-overlapChars:])
			}
		}
		buf.WriteString(sent)
		buf.WriteByte(' ')
	}
	flush()

	return chunks
}

// approxTokens estimates token count as len(s)/charsPerToken.
func approxTokens(s string) int {
	n := len(s) / charsPerToken
	if n == 0 && len(s) > 0 {
		return 1
	}
	return n
}

// splitSentences splits text on sentence-ending punctuation and newlines.
// It keeps the delimiter attached to the preceding segment.
func splitSentences(text string) []string {
	var out []string
	var buf strings.Builder

	runes := []rune(text)
	for i, r := range runes {
		buf.WriteRune(r)
		end := r == '\n' || r == '\r'
		if !end && (r == '.' || r == '!' || r == '?') {
			// Only split when followed by whitespace or end of string.
			next := i + 1
			if next >= len(runes) || unicode.IsSpace(runes[next]) {
				end = true
			}
		}
		if end {
			s := strings.TrimSpace(buf.String())
			if s != "" {
				out = append(out, s)
			}
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
