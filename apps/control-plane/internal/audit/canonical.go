package audit

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// canonicalRow is the deterministic JSON shape over which row_hash is taken.
// It excludes hash-related columns (seq, prev_hash, row_hash) so the hash
// covers payload plus identity, not the chain link itself.
type canonicalRow struct {
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

func canonicalize(e Event, deploySHA, env string, ts time.Time, before, after json.RawMessage) ([]byte, error) {
	row := canonicalRow{
		ActorType:       string(e.Actor.Type),
		Action:          e.Action,
		ResourceType:    e.ResourceType,
		ResourceID:      e.ResourceID,
		Severity:        string(e.Severity),
		BeforeJSON:      before,
		AfterJSON:       after,
		SourceIP:        e.SourceIP,
		UserAgent:       e.UserAgent,
		JWTClaimsDigest: e.JWTClaimsDigest,
		DeploySHA:       deploySHA,
		Env:             env,
		TS:              ts.UTC().Format(time.RFC3339Nano),
	}
	if e.TenantID != uuid.Nil {
		row.TenantID = e.TenantID.String()
	}
	if e.Actor.ID != uuid.Nil {
		row.ActorID = e.Actor.ID.String()
	}
	if e.RequestID != uuid.Nil {
		row.RequestID = e.RequestID.String()
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(row); err != nil {
		return nil, err
	}
	out := bytes.TrimRight(buf.Bytes(), "\n")
	return out, nil
}

// stableJSON marshals an arbitrary value with sorted map keys for hash stability.
func stableJSON(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw, nil
	}
	return marshalSorted(m), nil
}

func marshalSorted(v any) json.RawMessage {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			buf.Write(marshalSorted(x[k]))
		}
		buf.WriteByte('}')
		return buf.Bytes()
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(marshalSorted(e))
		}
		buf.WriteByte(']')
		return buf.Bytes()
	default:
		raw, _ := json.Marshal(v)
		return raw
	}
}
