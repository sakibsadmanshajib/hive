package usage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newAttemptTestPool connects with the privileged role that HIVE_TEST_DB_URL
// carries (matching how control-plane runs in production), so the
// request_attempts unique index is actually exercised. The pool is closed at
// test end.
func newAttemptTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedAttemptAccount inserts an auth.users row and the public.accounts row it
// owns so request_attempts.account_id has a valid FK target, then registers
// cleanup (deleting the account cascades any attempts, then the user).
func seedAttemptAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"attempt-idem-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		t.Skipf("seed auth.users failed (is this a migrated test DB?): %v", err)
	}

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'attempt idem test', 'personal', $2) RETURNING id`,
		"attempt-idem-"+uuid.NewString(), userID,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(c, `DELETE FROM auth.users WHERE id = $1`, userID)
	})
	return accountID
}

// TestCreateAttemptIsIdempotentOnAccountRequestAttempt reproduces the duplicate
// request-attempt insert seen during chat dispatch: the orchestrator starts an
// attempt, then the reservation path starts the same (account, request, attempt)
// again. Before the ON CONFLICT fix the second insert raised
// "duplicate key value violates unique constraint
// idx_request_attempts_account_request_attempt" (SQLSTATE 23505), which aborted
// the reservation transaction. The insert must instead be idempotent: return
// the existing attempt, leave exactly one row, and never double-count.
func TestCreateAttemptIsIdempotentOnAccountRequestAttempt(t *testing.T) {
	pool := newAttemptTestPool(t)
	accountID := seedAttemptAccount(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	requestID := "req-" + uuid.NewString()
	input := StartAttemptInput{
		AccountID:     accountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		Endpoint:      "/v1/chat/completions",
		ModelAlias:    "gpt-test",
		Status:        AttemptStatusDispatching,
		CustomerTags:  map[string]any{},
	}

	first, err := repo.CreateAttempt(ctx, input)
	if err != nil {
		t.Fatalf("first CreateAttempt: %v", err)
	}

	// Second call mirrors the reservation path's internal StartAttempt for the
	// same request. It may carry a different status; the insert must not error.
	input.Status = AttemptStatusAccepted
	second, err := repo.CreateAttempt(ctx, input)
	if err != nil {
		t.Fatalf("second CreateAttempt must be idempotent (no 23505), got: %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("expected the same attempt row on conflict, first=%s second=%s", first.ID, second.ID)
	}
	// First write wins: the durable fact is unchanged, so the status stays
	// dispatching rather than moving backward to accepted.
	if second.Status != AttemptStatusDispatching {
		t.Fatalf("expected preserved status %q, got %q", AttemptStatusDispatching, second.Status)
	}

	// No double reservation: exactly one attempt row exists for the key.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.request_attempts
		 WHERE account_id = $1 AND request_id = $2 AND attempt_number = 1`,
		accountID, requestID,
	).Scan(&count); err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 request_attempts row, got %d", count)
	}
}
