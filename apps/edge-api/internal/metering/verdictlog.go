package metering

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VerdictRecord mirrors public.metering_shadow_verdicts column-for-column
// (see the migration in this same PR,
// 20260728_04_metering_shadow_verdicts.sql). Nothing here is read by any
// enforcement path in Step 2; it exists purely to grade precedence-order
// correctness and the per-model pricing formula against real traffic before
// Step 4 turns either into a refusal or a debit.
type VerdictRecord struct {
	RequestID                string
	TenantID                 uuid.UUID // uuid.Nil for an API-key principal; stored as NULL
	AccountID                uuid.UUID // uuid.Nil when unresolved; stored as NULL
	PrincipalType            string    // "api_key" | "session"
	Deployment               string    // "HIVE_CLOUD" | "ENTERPRISE_EDGE" | "" (stored as NULL)
	Endpoint                 string
	ModelAlias               string
	PrecedenceRule           string
	Verdict                  string
	WouldRefuseCode          string // "" stored as NULL
	PromptTokens             int64
	CompletionTokens         int64
	TerminalUsageConfirmed   bool
	EstimatedCreditsLegacy   int64
	EstimatedCreditsPerModel int64
	Disconnected             bool
	DeliveredTokens          *int64 // nil unless Disconnected
}

// VerdictLogger is the seam Gate writes through. Tests use an in-memory
// fake (see gate_test.go's recordingLogger); production wiring (a later
// Wave 3 PR) supplies PGVerdictLogger.
type VerdictLogger interface {
	LogVerdict(ctx context.Context, record VerdictRecord) error
}

// PGVerdictLogger writes directly to public.metering_shadow_verdicts on the
// pool edge-api already holds open (e.g. chat.Deps.Pool today) -- no new
// network hop, matching InsertTrace/insertAuditEvent's existing pattern
// (chat/trace.go, chat/audit.go), including their nil-pool no-op shape so
// tests that construct a Handler/Orchestrator without a real pool are
// unaffected.
type PGVerdictLogger struct {
	Pool *pgxpool.Pool
}

// LogVerdict implements VerdictLogger.
func (l *PGVerdictLogger) LogVerdict(ctx context.Context, r VerdictRecord) error {
	if l == nil || l.Pool == nil {
		return nil
	}
	_, err := l.Pool.Exec(ctx, `
		INSERT INTO public.metering_shadow_verdicts (
			request_id, tenant_id, account_id, principal_type, deployment,
			endpoint, model_alias, precedence_rule, verdict, would_refuse_code,
			prompt_tokens, completion_tokens, terminal_usage_confirmed,
			estimated_credits_legacy, estimated_credits_per_model,
			disconnected, delivered_tokens
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		r.RequestID,
		nullableUUID(r.TenantID),
		nullableUUID(r.AccountID),
		r.PrincipalType,
		nullableString(r.Deployment),
		r.Endpoint,
		r.ModelAlias,
		r.PrecedenceRule,
		r.Verdict,
		nullableString(r.WouldRefuseCode),
		r.PromptTokens,
		r.CompletionTokens,
		r.TerminalUsageConfirmed,
		r.EstimatedCreditsLegacy,
		r.EstimatedCreditsPerModel,
		r.Disconnected,
		r.DeliveredTokens,
	)
	return err
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
