package auditverifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Verifier struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Verifier {
	return &Verifier{pool: pool}
}

// VerifyPartition walks each tenant's chain independently for the month
// containing t. The hash chain is partitioned by (tenant_id, month) — see
// writer in apps/edge-api/internal/chat/audit.go — so verification MUST
// group rows by tenant_id and walk strict seq order within each group;
// otherwise the recomputed sha256(prev_hash||canon) will not match the
// stored row_hash because adjacent rows in the global seq order may belong
// to different tenant chains.
func (v *Verifier) VerifyPartition(ctx context.Context, t time.Time) (int, error) {
	rows, err := v.pool.Query(ctx, `
		SELECT id, ts,
		       tenant_id::text, actor_id::text, actor_type, action,
		       resource_type, resource_id, severity, before_json, after_json,
		       request_id::text, source_ip::text, user_agent, jwt_claims_digest,
		       deploy_sha, env, seq, prev_hash, row_hash
		  FROM public.audit_log
		 WHERE ts >= date_trunc('month', $1::timestamptz)
		   AND ts <  date_trunc('month', $1::timestamptz) + interval '1 month'
		 ORDER BY tenant_id NULLS FIRST, seq`,
		t,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	// expectedPrev tracks the row_hash of the previous row IN THE SAME
	// tenant chain. Self-consistent rows whose prev_hash does not match
	// the actual previous row_hash indicate a broken link — a missing,
	// reordered, or deleted row — and must be flagged independently of
	// the internal hash check.
	mismatches := 0
	expectedPrev := map[string][]byte{}
	zeroHash := make([]byte, 32)
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return mismatches, err
		}
		tenantKey := ""
		if row.tenantID.Valid {
			tenantKey = row.tenantID.String
		}
		want, seen := expectedPrev[tenantKey]
		if !seen {
			// First row of this tenant's chain in the partition must
			// link to the all-zero sentinel (no previous row).
			want = zeroHash
		}
		if !bytes.Equal(row.prevHash, want) {
			mismatches++
			slog.Warn("audit chain link broken",
				"id", row.id, "seq", row.seq, "tenant_id", tenantKey,
				"reason", "prev_hash does not match previous row in tenant chain")
		}

		canon, err := row.canonical()
		if err != nil {
			return mismatches, err
		}
		sum := sha256.New()
		sum.Write(row.prevHash)
		sum.Write(canon)
		recomputed := sum.Sum(nil)
		if !bytes.Equal(recomputed, row.rowHash) {
			mismatches++
			slog.Warn("audit chain mismatch",
				"id", row.id, "seq", row.seq, "tenant_id", tenantKey,
				"reason", "row_hash does not match sha256(prev_hash||canonical_row)")
		}
		// Even on mismatch, advance the chain using the stored row_hash so
		// subsequent links are evaluated against what the writer actually
		// committed. This surfaces every broken link instead of cascading
		// one fault into all subsequent mismatches.
		expectedPrev[tenantKey] = row.rowHash
	}
	if err := rows.Err(); err != nil {
		return mismatches, err
	}
	return mismatches, nil
}

type scanner interface {
	Scan(dest ...any) error
}

type auditRow struct {
	id              int64
	seq             int64
	ts              time.Time
	tenantID        sql.NullString
	actorID         sql.NullString
	actorType       string
	action          string
	resourceType    sql.NullString
	resourceID      sql.NullString
	severity        string
	beforeJSON      []byte
	afterJSON       []byte
	requestID       sql.NullString
	sourceIP        sql.NullString
	userAgent       sql.NullString
	jwtClaimsDigest sql.NullString
	deploySHA       string
	env             string
	prevHash        []byte
	rowHash         []byte
}

func scanRow(s scanner) (auditRow, error) {
	var row auditRow
	err := s.Scan(
		&row.id, &row.ts, &row.tenantID, &row.actorID, &row.actorType, &row.action,
		&row.resourceType, &row.resourceID, &row.severity, &row.beforeJSON, &row.afterJSON,
		&row.requestID, &row.sourceIP, &row.userAgent, &row.jwtClaimsDigest,
		&row.deploySHA, &row.env, &row.seq, &row.prevHash, &row.rowHash,
	)
	return row, err
}

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

func (r auditRow) canonical() ([]byte, error) {
	before, err := normalizeJSONB(r.beforeJSON)
	if err != nil {
		return nil, err
	}
	after, err := normalizeJSONB(r.afterJSON)
	if err != nil {
		return nil, err
	}

	row := canonicalRow{
		ActorType:  r.actorType,
		Action:     r.action,
		Severity:   r.severity,
		BeforeJSON: before,
		AfterJSON:  after,
		DeploySHA:  r.deploySHA,
		Env:        r.env,
		TS:         r.ts.UTC().Format(time.RFC3339Nano),
	}
	if r.tenantID.Valid {
		row.TenantID = r.tenantID.String
	}
	if r.actorID.Valid {
		row.ActorID = r.actorID.String
	}
	if r.resourceType.Valid {
		row.ResourceType = r.resourceType.String
	}
	if r.resourceID.Valid {
		row.ResourceID = r.resourceID.String
	}
	if r.requestID.Valid {
		row.RequestID = r.requestID.String
	}
	if r.sourceIP.Valid {
		row.SourceIP = r.sourceIP.String
	}
	if r.userAgent.Valid {
		row.UserAgent = r.userAgent.String
	}
	if r.jwtClaimsDigest.Valid {
		row.JWTClaimsDigest = r.jwtClaimsDigest.String
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(row); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func normalizeJSONB(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return marshalSorted(decoded), nil
}

func marshalSorted(v any) json.RawMessage {
	switch typed := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyBytes, _ := json.Marshal(key)
			buf.Write(keyBytes)
			buf.WriteByte(':')
			buf.Write(marshalSorted(typed[key]))
		}
		buf.WriteByte('}')
		return buf.Bytes()
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(marshalSorted(item))
		}
		buf.WriteByte(']')
		return buf.Bytes()
	default:
		raw, _ := json.Marshal(v)
		return raw
	}
}
