package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type auditEvent struct {
	TenantID     uuid.UUID
	ActorID      uuid.UUID
	Action       string
	Severity     string
	After        any
	RequestID    uuid.UUID
	UserAgent    string
	DeploySHA    string
	Environment  string
	ResourceType string
	ResourceID   string
}

func insertAuditEvent(ctx context.Context, pool *pgxpool.Pool, event auditEvent) error {
	if pool == nil {
		return nil
	}
	after, err := stableJSON(event.After)
	if err != nil {
		return err
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ts := time.Now().UTC()
	monthStart := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	var maxSeq int64
	var prevHash []byte
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), 0),
		       COALESCE(
		         (SELECT row_hash
		            FROM public.audit_log
		           WHERE ts >= $1 AND ts < $2
		           ORDER BY seq DESC
		           LIMIT 1),
		         decode(repeat('00', 32), 'hex')
		       )
		  FROM public.audit_log
		 WHERE ts >= $1 AND ts < $2`,
		monthStart,
		monthEnd,
	).Scan(&maxSeq, &prevHash); err != nil {
		return err
	}

	canon, err := canonicalAudit(event, ts, nil, after)
	if err != nil {
		return err
	}
	sum := sha256.New()
	sum.Write(prevHash)
	sum.Write(canon)
	rowHash := sum.Sum(nil)

	_, err = tx.Exec(ctx, `
		INSERT INTO public.audit_log
		  (tenant_id, actor_id, actor_type, action, resource_type, resource_id,
		   severity, before_json, after_json, request_id, source_ip, user_agent,
		   jwt_claims_digest, deploy_sha, env, ts, seq, prev_hash, row_hash)
		VALUES ($1,$2,'USER',$3,$4,$5,$6,$7,$8,$9,NULL,$10,NULL,$11,$12,$13,$14,$15,$16)`,
		nullableUUID(event.TenantID),
		nullableUUID(event.ActorID),
		event.Action,
		nullableString(event.ResourceType),
		nullableString(event.ResourceID),
		event.Severity,
		nil,
		after,
		nullableUUID(event.RequestID),
		nullableString(event.UserAgent),
		event.DeploySHA,
		event.Environment,
		ts,
		maxSeq+1,
		prevHash,
		rowHash,
	)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type canonicalAuditRow struct {
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

func canonicalAudit(event auditEvent, ts time.Time, before, after json.RawMessage) ([]byte, error) {
	row := canonicalAuditRow{
		ActorType:    "USER",
		Action:       event.Action,
		ResourceType: event.ResourceType,
		ResourceID:   event.ResourceID,
		Severity:     event.Severity,
		BeforeJSON:   before,
		AfterJSON:    after,
		UserAgent:    event.UserAgent,
		DeploySHA:    event.DeploySHA,
		Env:          event.Environment,
		TS:           ts.UTC().Format(time.RFC3339Nano),
	}
	if event.TenantID != uuid.Nil {
		row.TenantID = event.TenantID.String()
	}
	if event.ActorID != uuid.Nil {
		row.ActorID = event.ActorID.String()
	}
	if event.RequestID != uuid.Nil {
		row.RequestID = event.RequestID.String()
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(row); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func stableJSON(value any) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return raw, nil
	}
	return marshalSorted(decoded), nil
}

func marshalSorted(value any) json.RawMessage {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
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
		for index, item := range typed {
			if index > 0 {
				buf.WriteByte(',')
			}
			buf.Write(marshalSorted(item))
		}
		buf.WriteByte(']')
		return buf.Bytes()
	default:
		raw, _ := json.Marshal(value)
		return raw
	}
}
