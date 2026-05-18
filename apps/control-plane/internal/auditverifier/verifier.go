package auditverifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

type Verifier struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Verifier {
	return &Verifier{pool: pool}
}

// VerifyPartition walks each tenant's chain independently for the
// month containing t. The hash chain is per-tenant (prev_hash links
// the previous row in the same tenant's chain, regardless of month);
// the partition table layout exists for storage and retention, not for
// chain semantics. Verification therefore:
//
//  1. Loads the prior-month link for every tenant present in this
//     partition (cross-partition stitching). Without this, the first
//     row of each new month was previously verified against the
//     all-zero sentinel — a tampered or missing month-boundary row
//     would be silently accepted or falsely flagged.
//  2. Groups rows by tenant_id, walks strict seq order within each
//     group, and recomputes sha256(prev_hash||canonical) for each row.
//
// The byte string that gets hashed is produced by the shared
// packages/audit-canonical package, which both writers also use, so
// every row produced anywhere in the system is re-verifiable here.
func (v *Verifier) VerifyPartition(ctx context.Context, t time.Time) (int, error) {
	partitionStart := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)

	// Cross-partition stitching: pull the most-recent row_hash per
	// tenant from any earlier partition. We seed expectedPrev with
	// those so the first row of this partition's chain links to the
	// previous month's last row, not the all-zero sentinel.
	expectedPrev, err := v.bootstrapExpectedPrev(ctx, partitionStart)
	if err != nil {
		return 0, err
	}
	zeroHash := make([]byte, 32)

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

	mismatches := 0
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
			// No prior-partition row for this tenant either — first
			// row in this tenant's chain ever.
			want = zeroHash
		}
		if !bytes.Equal(row.prevHash, want) {
			mismatches++
			slog.Warn("audit chain link broken",
				"id", row.id, "seq", row.seq, "tenant_id", tenantKey,
				"reason", "prev_hash does not match previous row in tenant chain")
		}

		// Canonical bytes per current 6-digit microsecond format.
		canon, err := row.canonical()
		if err != nil {
			return mismatches, err
		}
		sum := sha256.New()
		sum.Write(row.prevHash)
		sum.Write(canon)
		recomputed := sum.Sum(nil)
		if !bytes.Equal(recomputed, row.rowHash) {
			// Legacy fallback: rows written before the canonical
			// epoch (commit 40e8484 + this commit) used
			// time.RFC3339Nano for the canonical `ts` field. A
			// pure-fmt change cannot retroactively rehash those
			// rows, so try the legacy format before flagging a
			// mismatch. New rows from this commit forward only ever
			// hash with the fixed microsecond format above.
			legacyCanon, legacyErr := row.canonicalLegacy()
			if legacyErr == nil {
				legacySum := sha256.New()
				legacySum.Write(row.prevHash)
				legacySum.Write(legacyCanon)
				if bytes.Equal(legacySum.Sum(nil), row.rowHash) {
					expectedPrev[tenantKey] = row.rowHash
					continue
				}
			}
			mismatches++
			slog.Warn("audit chain mismatch",
				"id", row.id, "seq", row.seq, "tenant_id", tenantKey,
				"reason", "row_hash does not match sha256(prev_hash||canonical_row)")
		}
		// Always advance using the stored row_hash so subsequent links
		// are evaluated against what the writer actually committed —
		// surfaces every broken link instead of cascading.
		expectedPrev[tenantKey] = row.rowHash
	}
	if err := rows.Err(); err != nil {
		return mismatches, err
	}
	return mismatches, nil
}

// bootstrapExpectedPrev returns, for every tenant_id with at least one
// audit row strictly before partitionStart, the row_hash of that
// tenant's most recent row before the partition. The NULL tenant key
// is the empty string to match the in-walk encoding.
//
// Ordering is `ts DESC, seq DESC, id DESC` — NOT raw seq — because
// seq counters restart per partition. A previous-partition June
// seq=100 is older than a current-partition July seq=1 but raw seq
// ordering would prefer June=100, breaking the verifier's chain
// bootstrap. The (tenant_id, ts DESC) index covers this efficiently
// for the prior-partition window.
//
// We do not pre-filter to "tenants present in this partition" because
// that would require two scans and Postgres handles the broader
// query cheaply via the (tenant_id, ts DESC) index. Tenants absent
// from the current partition just contribute unused map entries.
func (v *Verifier) bootstrapExpectedPrev(ctx context.Context, partitionStart time.Time) (map[string][]byte, error) {
	rows, err := v.pool.Query(ctx, `
		SELECT DISTINCT ON (tenant_id)
		       tenant_id::text, row_hash
		  FROM public.audit_log
		 WHERE ts < $1
		 ORDER BY tenant_id NULLS FIRST, ts DESC, seq DESC, id DESC`,
		partitionStart,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]byte)
	for rows.Next() {
		var (
			tenant sql.NullString
			hash   []byte
		)
		if err := rows.Scan(&tenant, &hash); err != nil {
			return nil, err
		}
		key := ""
		if tenant.Valid {
			key = tenant.String
		}
		out[key] = hash
	}
	return out, rows.Err()
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

// canonical produces the deterministic byte string that row_hash was
// computed over. Field order, json tags, and timestamp format match
// the writers via the shared packages/audit-canonical package.
func (r auditRow) canonical() ([]byte, error) {
	before, err := normalizeJSONB(r.beforeJSON)
	if err != nil {
		return nil, err
	}
	after, err := normalizeJSONB(r.afterJSON)
	if err != nil {
		return nil, err
	}

	row := canonical.Row{
		ActorType:  r.actorType,
		Action:     r.action,
		Severity:   r.severity,
		BeforeJSON: before,
		AfterJSON:  after,
		DeploySHA:  r.deploySHA,
		Env:        r.env,
		// Re-format from the timestamptz Postgres handed us. Even if a
		// writer regression formatted with RFC3339Nano, the verifier
		// normalises to the canonical 6-digit microsecond form so the
		// hash check uses the same string the row was actually
		// committed with.
		TS: canonical.FormatTS(r.ts),
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

	return row.Marshal()
}

// normalizeJSONB decodes jsonb bytes from Postgres and re-serialises
// them through the canonical sorted-keys path so the round-trip is
// stable. Postgres jsonb has its own internal key ordering and we
// cannot depend on it matching the writer's stable form. NOTE: if
// the audit_log schema ever changes these columns from `jsonb` to
// `json` (text), Postgres would preserve insertion order rather than
// applying its own — and the writer's sort would already match —
// making the re-sort here cause false mismatches for legacy rows.
// The current schema uses `jsonb` (see 20260516_04_phase19_audit_log.sql);
// any migration away from that needs verifier coordination.
func normalizeJSONB(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return canonical.StableJSON(decoded)
}

// canonicalLegacy reproduces the pre-epoch canonical bytes a writer
// would have produced when the codebase still used
// time.RFC3339Nano. Returns the byte string suitable for re-hashing
// historical rows during the verifier's chain walk.
//
// This function exists ONLY to grandfather rows written before the
// canonical 6-digit microsecond format landed. Do not extend it for
// new fields; every future shape change must bump a chain epoch
// stored alongside the row, not silently widen the fallback path.
func (r auditRow) canonicalLegacy() ([]byte, error) {
	before, err := normalizeJSONB(r.beforeJSON)
	if err != nil {
		return nil, err
	}
	after, err := normalizeJSONB(r.afterJSON)
	if err != nil {
		return nil, err
	}
	row := canonical.Row{
		ActorType:  r.actorType,
		Action:     r.action,
		Severity:   r.severity,
		BeforeJSON: before,
		AfterJSON:  after,
		DeploySHA:  r.deploySHA,
		Env:        r.env,
		// Legacy writers used time.RFC3339Nano which strips trailing
		// zeros from the fractional component. After Postgres rounds
		// to microseconds the readback may have fewer than 6 digits.
		TS: r.ts.UTC().Format(time.RFC3339Nano),
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
	return row.Marshal()
}
