package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TraceRow struct {
	TenantID       uuid.UUID
	UserID         uuid.UUID
	RequestID      uuid.UUID
	Model          string
	Provider       string
	InTokens       int
	OutTokens      int
	LatencyMs      int
	CostCredits    int64
	FinishReason   string
	PromptHash     string
	CompletionHash string
}

func InsertTrace(ctx context.Context, pool *pgxpool.Pool, trace TraceRow) error {
	if pool == nil {
		return nil
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO public.llm_traces (
			tenant_id, user_id, request_id, model, provider,
			in_tokens, out_tokens, latency_ms, cost_credits,
			finish_reason, prompt_hash, completion_hash
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		trace.TenantID,
		nullableUUID(trace.UserID),
		trace.RequestID,
		trace.Model,
		trace.Provider,
		trace.InTokens,
		trace.OutTokens,
		trace.LatencyMs,
		trace.CostCredits,
		nullableString(trace.FinishReason),
		trace.PromptHash,
		trace.CompletionHash,
	)
	return err
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func nullableUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
