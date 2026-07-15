package licensing

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Recorder persists the latest validated Entitlement snapshot so tier is a
// plain queryable SQL attribute (owner decision, issue #304 comment,
// 2026-07-07) -- future tier-eligibility work reads this table directly
// rather than re-parsing the license file. Recording is best-effort: a DB
// write failure never blocks a Source.Current read from returning the
// freshly validated value (see Handler).
type Recorder interface {
	Record(ctx context.Context, e Entitlement) error
}

// PgxRecorder upserts into the public.license_state singleton row. Hive
// Enterprise is single-tenant per install (one license per deployment,
// matching the NVIDIA DLS site-license pattern), so there is exactly one
// row -- enforced at the schema level, see supabase/migrations.
type PgxRecorder struct {
	Pool *pgxpool.Pool
}

// Record upserts e into the singleton public.license_state row.
func (r PgxRecorder) Record(ctx context.Context, e Entitlement) error {
	_, err := r.Pool.Exec(ctx, `
		INSERT INTO public.license_state
			(singleton, tier, seats, issued_at, expires_at, validated_at, valid, reason, updated_at)
		VALUES (TRUE, $1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (singleton) DO UPDATE SET
			tier         = EXCLUDED.tier,
			seats        = EXCLUDED.seats,
			issued_at    = EXCLUDED.issued_at,
			expires_at   = EXCLUDED.expires_at,
			validated_at = EXCLUDED.validated_at,
			valid        = EXCLUDED.valid,
			reason       = EXCLUDED.reason,
			updated_at   = now()`,
		e.Tier, e.Seats, e.IssuedAt, e.ExpiresAt, e.ValidatedAt, e.Valid, e.Reason)
	return err
}
