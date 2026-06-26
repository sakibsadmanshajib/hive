package rag

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestEncodeVector verifies the pgvector text format serialiser.
func TestEncodeVector_Empty(t *testing.T) {
	got := encodeVector(nil)
	if got != "[]" {
		t.Errorf("expected '[]', got %q", got)
	}
}

func TestEncodeVector_Values(t *testing.T) {
	v := []float32{0.1, 0.2, 0.3}
	got := encodeVector(v)
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Errorf("missing brackets: %q", got)
	}
	// Must contain all three values separated by commas.
	inner := got[1 : len(got)-1]
	parts := strings.Split(inner, ",")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %q", len(parts), got)
	}
}

func TestEncodeVector_Dimension1024(t *testing.T) {
	v := make([]float32, EmbeddingDimension)
	for i := range v {
		v[i] = float32(i) / float32(EmbeddingDimension)
	}
	got := encodeVector(v)
	inner := got[1 : len(got)-1]
	parts := strings.Split(inner, ",")
	if len(parts) != EmbeddingDimension {
		t.Errorf("expected %d parts, got %d", EmbeddingDimension, len(parts))
	}
}

func TestEncodeVector_NoStringInterpolation(t *testing.T) {
	// Verify NaN/Inf are never produced (would break pgvector cast).
	v := make([]float32, 4)
	v[0] = 1.0
	v[1] = -1.0
	v[2] = 0.0
	v[3] = 0.5
	got := encodeVector(v)
	if strings.Contains(got, "NaN") || strings.Contains(got, "Inf") {
		t.Errorf("unexpected NaN/Inf in encoded vector: %q", got)
	}
}

func TestEncodeVector_SpecialFloat(t *testing.T) {
	// Confirm %g never emits NaN or ±Inf for normal finite values.
	v := []float32{float32(math.SmallestNonzeroFloat32), float32(math.MaxFloat32 / 2)}
	got := encodeVector(v)
	for _, bad := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		if strings.Contains(got, bad) {
			t.Errorf("encoded vector contains %q: %s", bad, got)
		}
	}
}

func TestEncodeVector_RoundTrip(t *testing.T) {
	// The output must be parseable as a bracket-enclosed comma list of floats.
	v := []float32{0.25, 0.5, 0.75}
	got := encodeVector(v)
	inner := got[1 : len(got)-1]
	for i, part := range strings.Split(inner, ",") {
		var f float64
		if _, err := fmt.Sscanf(part, "%g", &f); err != nil {
			t.Errorf("part %d %q not parseable: %v", i, part, err)
		}
	}
}
