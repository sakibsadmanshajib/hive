package canonical_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

func TestFormatTS_RoundTripStableAt6Digits(t *testing.T) {
	// 9-digit input must be truncated to 6 digits — what Postgres
	// timestamptz preserves.
	in := time.Date(2026, 5, 18, 14, 30, 25, 123456789, time.UTC)
	got := canonical.FormatTS(in)
	want := "2026-05-18T14:30:25.123456Z"
	if got != want {
		t.Fatalf("FormatTS mismatch: got %q want %q", got, want)
	}
	// Re-parsing the formatted string and re-formatting must produce the
	// same bytes — the verifier round-trip property.
	parsed, err := time.Parse("2006-01-02T15:04:05.999999Z07:00", got)
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if canonical.FormatTS(parsed) != got {
		t.Fatalf("not round-trip stable")
	}
}

func TestFormatTS_ZeroFractionalAlwaysPadded(t *testing.T) {
	// 14:00:00 (no microseconds) must still print .000000 — collision
	// with .000123 would break the chain.
	t1 := canonical.FormatTS(time.Date(2026, 5, 18, 14, 0, 0, 0, time.UTC))
	t2 := canonical.FormatTS(time.Date(2026, 5, 18, 14, 0, 0, 123000, time.UTC))
	if t1 == t2 {
		t.Fatalf("zero fractional must differ from .000123: got %q", t1)
	}
	if !strings.HasSuffix(t1, ".000000Z") {
		t.Fatalf("expected .000000Z padding, got %q", t1)
	}
}

func TestRow_MarshalNoHTMLEscape(t *testing.T) {
	r := canonical.Row{
		ActorType:  "USER",
		Action:     "CHAT_REQUEST",
		Severity:   "INFO",
		DeploySHA:  "abc",
		Env:        "prod",
		TS:         "2026-05-18T14:30:25.000000Z",
		AfterJSON:  json.RawMessage(`{"q":"a<b>c"}`),
	}
	got, err := r.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(got, []byte("\\u003c")) {
		t.Fatalf("HTML escape leaked: %s", got)
	}
	if !bytes.Contains(got, []byte("a<b>c")) {
		t.Fatalf("expected literal <>, got: %s", got)
	}
}

func TestRow_MarshalDeterministic(t *testing.T) {
	// Two calls with identical input must produce identical bytes —
	// encoding/json struct ordering relies on declaration order.
	r := canonical.Row{
		ActorType: "USER",
		Action:    "CHAT_REQUEST",
		Severity:  "INFO",
		DeploySHA: "abc",
		Env:       "prod",
		TS:        "2026-05-18T14:30:25.000000Z",
	}
	a, _ := r.Marshal()
	b, _ := r.Marshal()
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic marshal: %s vs %s", a, b)
	}
}

func TestIdentity_NilUUIDsElided(t *testing.T) {
	var r canonical.Row
	(canonical.Identity{TenantID: uuid.Nil}).Apply(&r)
	if r.TenantID != "" {
		t.Fatalf("nil uuid must elide tenant_id, got %q", r.TenantID)
	}
	id := uuid.New()
	(canonical.Identity{TenantID: id}).Apply(&r)
	if r.TenantID != id.String() {
		t.Fatalf("non-nil tenant_id missing")
	}
}

func TestStableJSON_KeysSorted(t *testing.T) {
	in := map[string]any{"b": 1, "a": 2, "c": map[string]any{"z": 1, "y": 2}}
	got, err := canonical.StableJSON(in)
	if err != nil {
		t.Fatalf("StableJSON: %v", err)
	}
	want := `{"a":2,"b":1,"c":{"y":2,"z":1}}`
	if string(got) != want {
		t.Fatalf("not sorted: got %s want %s", got, want)
	}
}

func TestStableJSON_NilReturnsNil(t *testing.T) {
	out, err := canonical.StableJSON(nil)
	if err != nil || out != nil {
		t.Fatalf("nil input must produce nil output: %v %s", err, out)
	}
}

func TestTruncateTS_MicrosecondResolution(t *testing.T) {
	in := time.Date(2026, 5, 18, 14, 30, 25, 999999999, time.UTC)
	out := canonical.TruncateTS(in)
	if out.Nanosecond()%1000 != 0 {
		t.Fatalf("expected ns%%1000 == 0, got %d", out.Nanosecond())
	}
}
