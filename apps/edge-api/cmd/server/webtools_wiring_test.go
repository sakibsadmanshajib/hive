package main

import (
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/webtools"
)

// Zero means no bound at all, which is the unbounded embedding burst issue
// #1609 exists to end. It is refused rather than obeyed, and refusing it
// means the default is used, not that the gateway refuses to start.
func TestWebToolsEmbedConcurrencyRefusesNonPositiveValues(t *testing.T) {
	for _, raw := range []string{"0", "-1", "-4", "banana", "1.5", " "} {
		t.Setenv("HIVE_WEBTOOLS_EMBED_CONCURRENCY", raw)
		if got := resolveWebToolsEmbedConcurrency(); got != webtools.DefaultEmbedConcurrency {
			t.Fatalf("%q resolved to %d, want the default %d", raw, got, webtools.DefaultEmbedConcurrency)
		}
	}
}

func TestWebToolsEmbedConcurrencyHonoursAPositiveValue(t *testing.T) {
	t.Setenv("HIVE_WEBTOOLS_EMBED_CONCURRENCY", "2")
	if got := resolveWebToolsEmbedConcurrency(); got != 2 {
		t.Fatalf("resolved to %d, want 2", got)
	}
}

func TestWebToolsEmbedConcurrencyDefaultsWhenUnset(t *testing.T) {
	t.Setenv("HIVE_WEBTOOLS_EMBED_CONCURRENCY", "")
	if got := resolveWebToolsEmbedConcurrency(); got != webtools.DefaultEmbedConcurrency {
		t.Fatalf("resolved to %d, want the default %d", got, webtools.DefaultEmbedConcurrency)
	}
}

// A deployment with no embedding backend still gets a working pipeline: small
// pages need no embedding at all, and a large one fails loudly rather than
// being quietly truncated. What must not happen is the nil-pointer-in-an-
// interface shape, where "no embedder" reads as "an embedder is wired" and
// panics on the first large page.
func TestWebFetchPipelineBuildsWithoutAnEmbedder(t *testing.T) {
	if got := buildWebFetchPipeline(nil); got == nil {
		t.Fatal("the pipeline was not built without an embedding backend")
	}
}
