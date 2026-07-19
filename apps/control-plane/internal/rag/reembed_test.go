package rag

import (
	"context"
	"testing"
)

// orderedEmbedClient returns one vector per input whose first element encodes
// a running counter, so batching order can be asserted end to end.
type orderedEmbedClient struct {
	calls   int
	next    float32
	failOn  int // 1-based call index to fail on; 0 never fails
	batches []int
}

func (o *orderedEmbedClient) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	o.calls++
	o.batches = append(o.batches, len(inputs))
	if o.failOn > 0 && o.calls >= o.failOn {
		return nil, context.DeadlineExceeded
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		v := make([]float32, EmbeddingDimension)
		v[0] = o.next
		o.next++
		out[i] = v
	}
	return out, nil
}

func TestEmbedInBatches_SplitsAndPreservesOrder(t *testing.T) {
	e := &orderedEmbedClient{}
	texts := []string{"a", "b", "c", "d", "e"}

	vecs, err := embedInBatches(context.Background(), e, texts, 2)
	if err != nil {
		t.Fatalf("embedInBatches error: %v", err)
	}
	if len(vecs) != 5 {
		t.Fatalf("got %d vectors, want 5", len(vecs))
	}
	// batchSize=2 over 5 items -> batches of 2,2,1.
	wantBatches := []int{2, 2, 1}
	if len(e.batches) != len(wantBatches) {
		t.Fatalf("batches = %v, want %v", e.batches, wantBatches)
	}
	for i, b := range wantBatches {
		if e.batches[i] != b {
			t.Fatalf("batch %d size = %d, want %d (all: %v)", i, e.batches[i], b, e.batches)
		}
	}
	// Order preserved: first element counts 0..4 across the flattened result.
	for i, v := range vecs {
		if v[0] != float32(i) {
			t.Errorf("vector %d marker = %v, want %d (order not preserved)", i, v[0], i)
		}
	}
}

func TestEmbedInBatches_DefaultBatchSize(t *testing.T) {
	e := &orderedEmbedClient{}
	texts := make([]string, 40) // > default 32 -> exactly 2 batches
	for i := range texts {
		texts[i] = "x"
	}
	if _, err := embedInBatches(context.Background(), e, texts, 0); err != nil {
		t.Fatalf("embedInBatches error: %v", err)
	}
	if e.calls != 2 {
		t.Fatalf("calls = %d, want 2 (default batch size 32 over 40 items)", e.calls)
	}
}

func TestEmbedInBatches_FailClosed(t *testing.T) {
	e := &orderedEmbedClient{failOn: 2} // second batch errors
	texts := []string{"a", "b", "c", "d"}
	vecs, err := embedInBatches(context.Background(), e, texts, 2)
	if err == nil {
		t.Fatal("expected error on batch failure, got nil (must fail closed, not persist partial)")
	}
	if vecs != nil {
		t.Errorf("expected nil vectors on failure, got %d (no partial result)", len(vecs))
	}
}

func TestEmbedInBatches_Empty(t *testing.T) {
	e := &orderedEmbedClient{}
	vecs, err := embedInBatches(context.Background(), e, nil, 8)
	if err != nil {
		t.Fatalf("embedInBatches(nil) error: %v", err)
	}
	if len(vecs) != 0 {
		t.Errorf("got %d vectors for empty input, want 0", len(vecs))
	}
	if e.calls != 0 {
		t.Errorf("calls = %d, want 0 for empty input", e.calls)
	}
}
