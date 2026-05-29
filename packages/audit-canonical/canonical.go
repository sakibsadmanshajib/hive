// Package canonical provides the single source of truth for the
// deterministic JSON shape (and timestamp format) that the audit row
// hash chain is computed over.
//
// Both the control-plane (SyncWriter) and the edge-api (chat audit hot
// path) MUST produce identical bytes for any given event, and the
// verifier MUST be able to reconstruct those same bytes from the row
// it reads back out of Postgres. Before this package existed each
// writer carried its own private struct + Format(time.RFC3339Nano)
// call. Two failure modes followed:
//
//  1. The two writer structs drifted independently. A field reorder or
//     a renamed json tag in one writer would silently break chain
//     verification for rows produced by the other writer.
//  2. time.RFC3339Nano emits up to 9 fractional-second digits while
//     Postgres timestamptz stores 6. A write at e.g. 14:00:00.123456789
//     would canonicalise with 9 digits but read back with 6, so the
//     verifier's re-canonicalisation produced a different byte string
//     and every row mismatched.
//
// To close both holes:
//
//   - The Row struct here is the only canonical row definition. Both
//     writers Marshal through it, the verifier Unmarshal+Marshal
//     through the same struct on read-back.
//   - FormatTS truncates the timestamp to microseconds (which is the
//     resolution Postgres timestamptz keeps) and emits a fixed-width
//     6-digit fractional-second representation. Round-trip stable.
//
// Adding a new field to the canonical hash is an explicit, audited
// schema change — bump the chain epoch and document the migration.
package canonical

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Row is the deterministic JSON shape over which row_hash is taken.
// Fields are ordered to match the historical chain shape; encoding/json
// emits struct fields in declaration order so reordering this struct
// would invalidate every existing row_hash. Treat the field order as
// part of the on-disk contract.
//
// Hash-chain columns (seq, prev_hash, row_hash) are intentionally
// excluded — the hash covers payload + identity + timestamp only, so
// the chain link itself is not self-referential.
type Row struct {
	TenantID        string          `json:"tenant_id,omitempty"`
	ActorID         string          `json:"actor_id,omitempty"`
	ActorType       string          `json:"actor_type"`
	Action          string          `json:"action"`
	ResourceType    string          `json:"resource_type,omitempty"`
	ResourceID      string          `json:"resource_id,omitempty"`
	Severity        string          `json:"severity"`
	BeforeJSON      json.RawMessage `json:"before_json,omitempty"`
	AfterJSON       json.RawMessage `json:"after_json,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
	SourceIP        string          `json:"source_ip,omitempty"`
	UserAgent       string          `json:"user_agent,omitempty"`
	JWTClaimsDigest string          `json:"jwt_claims_digest,omitempty"`
	DeploySHA       string          `json:"deploy_sha"`
	Env             string          `json:"env"`
	TS              string          `json:"ts"`
}

// tsLayout emits Y-m-dTH:M:S.UUUUUUZ where UUUUUU is exactly six
// digits of microseconds. We pad with zeros rather than using Go's
// "999999" which strips trailing zeros — that variability would make
// 14:00:00.000000 collide with 14:00:00 on the wire, breaking the
// re-canonicalisation contract.
const tsLayout = "2006-01-02T15:04:05.000000Z07:00"

// FormatTS returns the canonical UTC microsecond-resolution string for
// the timestamp. Both writers MUST format ts through this helper and
// MUST also pass the same truncated time.Time into INSERT so Postgres
// stores exactly what the hash committed to.
func FormatTS(ts time.Time) string {
	return ts.UTC().Truncate(time.Microsecond).Format(tsLayout)
}

// TruncateTS returns the timestamp at microsecond resolution in UTC.
// Use this when persisting the timestamp to Postgres so the value
// stored exactly matches the string covered by the canonical hash.
func TruncateTS(ts time.Time) time.Time {
	return ts.UTC().Truncate(time.Microsecond)
}

// Marshal serialises the row to its canonical JSON byte string. We use
// json.Encoder with SetEscapeHTML(false) so '<', '>', '&' are emitted
// literally instead of `<` etc — identical to Postgres's jsonb
// output for the same content. The trailing newline that Encoder
// writes is trimmed so the hash matches what verifier.scanRow produces.
func (r Row) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Identity carries the foreign-key-ish fields for a Row. Callers
// build a Row by combining application fields with an Identity so the
// uuid.Nil → "" conversion logic lives in one place.
type Identity struct {
	TenantID  uuid.UUID
	ActorID   uuid.UUID
	RequestID uuid.UUID
}

// Apply copies non-nil identity fields onto the Row. uuid.Nil is
// elided so the omitempty tags fire and the canonical bytes do not
// include zero-valued keys.
func (i Identity) Apply(r *Row) {
	if i.TenantID != uuid.Nil {
		r.TenantID = i.TenantID.String()
	}
	if i.ActorID != uuid.Nil {
		r.ActorID = i.ActorID.String()
	}
	if i.RequestID != uuid.Nil {
		r.RequestID = i.RequestID.String()
	}
}

// StableJSON returns a JSON representation of v with map keys sorted
// at every level. This is the only safe form for inclusion in
// BeforeJSON/AfterJSON: Go's encoding/json sorts map keys for
// `map[string]…` values but jsonb in Postgres has its own canonical
// key order. By pre-sorting on write we ensure the bytes we hash are
// the bytes the verifier will recompute after reading the jsonb back.
func StableJSON(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	// Decode with UseNumber so JSON numbers are preserved as their exact
	// literal text (json.Number) rather than being coerced to float64.
	// float64 coercion loses precision on large integers (e.g. token
	// counts, cost micro-amounts) and rewrites the representation
	// (1e+09, dropped trailing zeros), which would make the canonical
	// bytes we hash diverge from the value the verifier re-reads — a
	// false tamper alert. json.Marshal emits a json.Number as a raw
	// numeric literal, so marshalSorted's default branch round-trips it
	// unchanged.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		// Already a JSON primitive (string, number, bool) — return as-is.
		return raw, nil
	}
	out, err := marshalSorted(decoded)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// marshalSorted recurses through decoded JSON values and re-emits them
// with object keys sorted lexicographically. Arrays preserve order.
func marshalSorted(v any) (json.RawMessage, error) {
	switch typed := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for k := range typed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			sub, err := marshalSorted(typed[k])
			if err != nil {
				return nil, err
			}
			buf.Write(sub)
		}
		buf.WriteByte('}')
		return buf.Bytes(), nil
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range typed {
			if i > 0 {
				buf.WriteByte(',')
			}
			sub, err := marshalSorted(e)
			if err != nil {
				return nil, err
			}
			buf.Write(sub)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
}
