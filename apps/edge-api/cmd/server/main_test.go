package main

import "testing"

func TestResolveSpecPathDefaultsToGeneratedHiveContract(t *testing.T) {
	t.Setenv("OPENAPI_SPEC_PATH", "")

	got := resolveSpecPath()

	want := "/app/packages/openai-contract/generated/hive-openapi.yaml"
	if got != want {
		t.Fatalf("resolveSpecPath() = %q, want %q", got, want)
	}
}

func TestResolveSpecPathHonorsOverride(t *testing.T) {
	t.Setenv("OPENAPI_SPEC_PATH", "/tmp/override.yaml")

	got := resolveSpecPath()

	if got != "/tmp/override.yaml" {
		t.Fatalf("resolveSpecPath() = %q, want override path", got)
	}
}
