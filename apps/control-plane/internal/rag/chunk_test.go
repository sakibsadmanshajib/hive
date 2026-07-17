package rag

import (
	"strings"
	"testing"
)

func TestChunkText_Empty(t *testing.T) {
	if got := ChunkText("", 512, 64); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
	if got := ChunkText("   ", 512, 64); got != nil {
		t.Fatalf("expected nil for whitespace input, got %v", got)
	}
}

func TestChunkText_ShortText_SingleChunk(t *testing.T) {
	text := "Hello world."
	chunks := ChunkText(text, 512, 64)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Index != 0 {
		t.Errorf("expected index 0, got %d", chunks[0].Index)
	}
	if chunks[0].Content != "Hello world." {
		t.Errorf("unexpected content: %q", chunks[0].Content)
	}
	if chunks[0].TokenCount < 1 {
		t.Errorf("token count must be >= 1")
	}
}

func TestChunkText_LongText_MultipleChunks(t *testing.T) {
	// Build a text that is definitely longer than one targetTokens window.
	// targetTokens=10 => targetChars=40. Build 200 char text.
	sb := strings.Builder{}
	for i := 0; i < 20; i++ {
		sb.WriteString("This is sentence number one here. ")
	}
	text := sb.String()

	chunks := ChunkText(text, 10, 2)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// Indices must be sequential.
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d has index %d", i, c.Index)
		}
		if strings.TrimSpace(c.Content) == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}
}

func TestChunkText_ZeroTargetUsesDefault(t *testing.T) {
	text := "Short."
	chunks := ChunkText(text, 0, 0)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk with zero target, got %d", len(chunks))
	}
}

func TestChunkText_NegativeOverlapClamped(t *testing.T) {
	text := strings.Repeat("word ", 300)
	// Should not panic.
	chunks := ChunkText(text, 20, -5)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
}

func TestApproxTokens(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 1}, // 3 chars < 4, rounds up to 1
		{"abcd", 1},
		{"abcde", 1},
		{"abcdefgh", 2},
	}
	for _, tc := range cases {
		got := approxTokens(tc.s)
		if got != tc.want {
			t.Errorf("approxTokens(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

func TestEmbeddingDimensionDefault(t *testing.T) {
	// EmbeddingDimension is a config-driven var (set from EMBEDDING_DIM at
	// startup, see main.go), not a hardcoded constant; this only pins the
	// zero-value default so an accidental change to the fallback doesn't
	// silently break the rag_chunks vector(1024) column it must match.
	if EmbeddingDimension != 1024 {
		t.Errorf("EmbeddingDimension default = %d, want 1024", EmbeddingDimension)
	}
}
