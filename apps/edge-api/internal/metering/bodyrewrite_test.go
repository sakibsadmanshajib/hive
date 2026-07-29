package metering

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRewriteBody_ForcesIncludeUsageWhenAbsent(t *testing.T) {
	raw := []byte(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	got, err := RewriteBody(raw)
	if err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}
	assertIncludeUsageTrue(t, got)
	assertFieldPreserved(t, got, "model", "hive-fast")
}

func TestRewriteBody_ForcesIncludeUsageWhenExplicitlyFalse(t *testing.T) {
	raw := []byte(`{"model":"hive-fast","stream":true,"stream_options":{"include_usage":false}}`)
	got, err := RewriteBody(raw)
	if err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}
	assertIncludeUsageTrue(t, got)
}

func TestRewriteBody_PreservesEveryOtherField(t *testing.T) {
	raw := []byte(`{"model":"hive-fast","max_tokens":512,"temperature":0.4,"tools":[{"type":"function"}]}`)
	got, err := RewriteBody(raw)
	if err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}
	var gotFields map[string]json.RawMessage
	if err := json.Unmarshal(got, &gotFields); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	for _, key := range []string{"model", "max_tokens", "temperature", "tools"} {
		if _, ok := gotFields[key]; !ok {
			t.Errorf("rewritten body dropped field %q", key)
		}
	}
}

func TestRewriteBody_PreservesOtherStreamOptionsSubfields(t *testing.T) {
	raw := []byte(`{"model":"hive-fast","stream_options":{"include_usage":false,"custom_field":"keep-me"}}`)
	got, err := RewriteBody(raw)
	if err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}
	var fields struct {
		StreamOptions struct {
			IncludeUsage bool   `json:"include_usage"`
			CustomField  string `json:"custom_field"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !fields.StreamOptions.IncludeUsage {
		t.Errorf("include_usage = false, want true")
	}
	if fields.StreamOptions.CustomField != "keep-me" {
		t.Errorf("custom_field = %q, want preserved", fields.StreamOptions.CustomField)
	}
}

// TestRewriteBody_ByteIdenticalContentWhenAlreadyTrue is the diff-the-wire
// regression named in the design brief (section 3.4 point 2): a client that
// already sends stream_options.include_usage:true must come back out
// content-equivalent -- same fields, same values, field ordering aside --
// proving include_usage is the ONE thing this function is allowed to touch.
func TestRewriteBody_ByteIdenticalContentWhenAlreadyTrue(t *testing.T) {
	raw := []byte(`{"model":"hive-fast","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	got, err := RewriteBody(raw)
	if err != nil {
		t.Fatalf("RewriteBody: %v", err)
	}

	var want, gotDecoded any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("decode original: %v", err)
	}
	if err := json.Unmarshal(got, &gotDecoded); err != nil {
		t.Fatalf("decode rewritten: %v", err)
	}
	if !reflect.DeepEqual(want, gotDecoded) {
		t.Errorf("rewritten body content changed:\n got  = %s\n want (content-equivalent to) = %s", got, raw)
	}
}

func TestRewriteBody_RejectsInvalidJSON(t *testing.T) {
	_, err := RewriteBody([]byte(`not json`))
	if err == nil {
		t.Fatal("expected an error for invalid JSON input, got nil")
	}
}

func assertIncludeUsageTrue(t *testing.T, body []byte) {
	t.Helper()
	var fields struct {
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !fields.StreamOptions.IncludeUsage {
		t.Errorf("stream_options.include_usage = false, want true; body = %s", body)
	}
}

func assertFieldPreserved(t *testing.T, body []byte, key, want string) {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got, _ := fields[key].(string)
	if got != want {
		t.Errorf("field %q = %q, want %q", key, got, want)
	}
}
