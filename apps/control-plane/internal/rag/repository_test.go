package rag

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func mustEncode(t *testing.T, v []float32) string {
	t.Helper()
	s, err := encodeVector(v)
	if err != nil {
		t.Fatalf("encodeVector unexpectedly failed: %v", err)
	}
	return s
}

func TestEncodeVector_Empty(t *testing.T) {
	got, err := encodeVector(nil)
	if err != nil || got != "[]" {
		t.Errorf("expected '[]' nil-err, got %q %v", got, err)
	}
}

func TestEncodeVector_Values(t *testing.T) {
	got := mustEncode(t, []float32{0.1, 0.2, 0.3})
	if !strings.HasPrefix(got, "[") || !strings.HasSuffix(got, "]") {
		t.Errorf("missing brackets: %q", got)
	}
	parts := strings.Split(got[1:len(got)-1], ",")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %q", len(parts), got)
	}
}

func TestEncodeVector_Dimension1024(t *testing.T) {
	v := make([]float32, EmbeddingDimension)
	for i := range v {
		v[i] = float32(i) / float32(EmbeddingDimension)
	}
	got := mustEncode(t, v)
	parts := strings.Split(got[1:len(got)-1], ",")
	if len(parts) != EmbeddingDimension {
		t.Errorf("expected %d parts, got %d", EmbeddingDimension, len(parts))
	}
}

func TestEncodeVector_RejectsNaN(t *testing.T) {
	v := []float32{1.0, float32(math.NaN()), 0.5}
	_, err := encodeVector(v)
	if err == nil {
		t.Error("expected error for NaN element")
	}
}

func TestEncodeVector_RejectsInf(t *testing.T) {
	v := []float32{1.0, float32(math.Inf(1)), 0.5}
	_, err := encodeVector(v)
	if err == nil {
		t.Error("expected error for +Inf element")
	}
	v2 := []float32{1.0, float32(math.Inf(-1)), 0.5}
	_, err = encodeVector(v2)
	if err == nil {
		t.Error("expected error for -Inf element")
	}
}

func TestEncodeVector_FiniteValuesOK(t *testing.T) {
	v := []float32{float32(math.SmallestNonzeroFloat32), float32(math.MaxFloat32 / 2), 0, -1}
	got := mustEncode(t, v)
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(got, bad) {
			t.Errorf("encoded vector contains %q: %s", bad, got)
		}
	}
}

func TestEncodeVector_RoundTrip(t *testing.T) {
	got := mustEncode(t, []float32{0.25, 0.5, 0.75})
	inner := got[1 : len(got)-1]
	for i, part := range strings.Split(inner, ",") {
		var f float64
		if _, err := fmt.Sscanf(part, "%g", &f); err != nil {
			t.Errorf("part %d %q not parseable: %v", i, part, err)
		}
	}
}
