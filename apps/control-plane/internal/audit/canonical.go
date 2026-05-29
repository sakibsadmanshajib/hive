package audit

import (
	"encoding/json"
	"time"

	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

// canonicalize produces the deterministic byte string the row_hash is
// computed over. It delegates row shape, timestamp format, and JSON
// encoding rules to packages/audit-canonical so the edge-api hot path
// (chat/audit.go) and the verifier remain byte-identical for the same
// logical event. See packages/audit-canonical/canonical.go for the
// shape and round-trip rules.
func canonicalize(e Event, deploySHA, env string, ts time.Time, before, after json.RawMessage) ([]byte, error) {
	row := canonical.Row{
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
		TS:              canonical.FormatTS(ts),
	}
	(canonical.Identity{
		TenantID:  e.TenantID,
		ActorID:   e.Actor.ID,
		RequestID: e.RequestID,
	}).Apply(&row)
	return row.Marshal()
}

// stableJSON wraps the shared StableJSON helper so callers do not need
// to import the audit-canonical package directly. The implementation
// is identical — keys are sorted lexicographically at every level so
// the verifier reproduces the same bytes after a jsonb round-trip.
func stableJSON(v any) (json.RawMessage, error) {
	return canonical.StableJSON(v)
}
