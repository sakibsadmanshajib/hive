package signup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
)

func TestWebhook_HappyPath_InsertsMembershipAndAudits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantID := mustInsertTenant(t, ctx, pool, "office", "ENTERPRISE_EDGE")
	userID := mustInsertAuthUser(t, ctx, pool, "ada@office.example")

	addedUser := ""
	addedGroup := ""
	groupAdder := func(ctx context.Context, group, email string) error {
		addedUser = email
		addedGroup = group
		return nil
	}
	groupEnsurer := func(ctx context.Context, name string) (string, error) {
		return "grp-" + name, nil
	}

	resolver := signup.NewResolver(signup.ResolverDeps{
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) {
			if domain == "office.example" {
				return tenantID, nil
			}
			return uuid.Nil, signup.ErrNoMatch
		},
	})

	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  &noopWAL{},
	})

	h := signup.NewWebhook(signup.WebhookDeps{
		Pool:         pool,
		Resolver:     resolver,
		EnsureGroup:  groupEnsurer,
		AddUser:      groupAdder,
		Audit:        logger,
		SharedSecret: "shh",
	})

	body, _ := json.Marshal(map[string]any{
		"user_id": userID,
		"email":   "ada@office.example",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/auth/user-created", bytes.NewReader(body))
	req.Header.Set("X-Hive-Signup-Secret", "shh")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "ada@office.example", addedUser)
	require.Equal(t, "grp-tenant_"+tenantID.String(), addedGroup)

	var role string
	err := pool.QueryRow(ctx,
		`SELECT role FROM public.tenant_users WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID).Scan(&role)
	require.NoError(t, err)
	require.Equal(t, "MEMBER", role)

	var actions []string
	rows, err := pool.Query(ctx,
		`SELECT action FROM public.audit_log WHERE tenant_id=$1 ORDER BY seq`, tenantID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	require.NoError(t, rows.Err())
	require.Contains(t, actions, "AUTH_SIGNUP_SUCCESS")
	require.Contains(t, actions, "TENANT_USER_ADD")
	require.Contains(t, actions, "OWUI_GROUP_ADD_SUCCESS")
}

// TestWebhook_HappyPath_GrantsNoCredits is a regression guard for a live bug
// found and fixed 2026-07-26 (PR #453): the signup page implied a free
// credit grant that does not exist anywhere in code. Credits are
// owner-discretionary only (no auto-trial, no signup bonus, no
// auto-referral reward). This asserts a brand-new signup's credit ledger is
// genuinely empty — zero rows, zero balance — immediately after the
// tenant_users membership is created, so a future change that wires an
// automatic credit grant into the signup path fails this test instead of
// silently reintroducing the same bug.
func TestWebhook_HappyPath_GrantsNoCredits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := newPool(t, ctx)
	t.Cleanup(func() { pool.Close() })

	tenantID := mustInsertTenant(t, ctx, pool, "credit-check", "ENTERPRISE_EDGE")
	// tenant_id and account_id share the same UUID by convention — the
	// credit ledger (supabase/migrations/20260330_01_credits_ledger.sql)
	// still keys off the pre-Phase-19 public.accounts table, and
	// apps/control-plane/internal/accounts/repository.go's CreateAccount
	// takes an explicit id rather than generating one, so real tenant
	// provisioning creates both rows under one shared id.
	mustInsertAccount(t, ctx, pool, tenantID, "credit-check-acct")
	userID := mustInsertAuthUser(t, ctx, pool, "grace@credit-check.example")

	resolver := signup.NewResolver(signup.ResolverDeps{
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) {
			if domain == "credit-check.example" {
				return tenantID, nil
			}
			return uuid.Nil, signup.ErrNoMatch
		},
	})
	logger := audit.NewLogger(audit.LoggerDeps{
		Sync: audit.NewSyncWriter(pool, audit.WriterConfig{DeploySHA: "s", Env: "test"}),
		WAL:  &noopWAL{},
	})
	// EnsureGroup/AddUser intentionally left nil: OWUI provisioning is
	// unrelated to credits and is already covered by the happy-path test
	// above; the webhook logs and continues when they are unset.
	h := signup.NewWebhook(signup.WebhookDeps{
		Pool:         pool,
		Resolver:     resolver,
		Audit:        logger,
		SharedSecret: "shh",
	})

	body, _ := json.Marshal(map[string]any{
		"user_id": userID,
		"email":   "grace@credit-check.example",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/auth/user-created", bytes.NewReader(body))
	req.Header.Set("X-Hive-Signup-Secret", "shh")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	var membershipCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.tenant_users WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, userID).Scan(&membershipCount))
	require.Equal(t, 1, membershipCount, "signup must still create the tenant membership")

	var ledgerEntries int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM public.credit_ledger_entries WHERE account_id=$1`,
		tenantID).Scan(&ledgerEntries))
	require.Equal(t, 0, ledgerEntries,
		"signup must not create any credit ledger entries — credits are owner-discretionary only, granted by a separate admin action")

	var balance int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(credits_delta), 0) FROM public.credit_ledger_entries WHERE account_id=$1`,
		tenantID).Scan(&balance))
	require.Equal(t, int64(0), balance, "a brand-new signup must start at exactly zero credits")
}

func TestWebhook_BadSecret_401(t *testing.T) {
	h := signup.NewWebhook(signup.WebhookDeps{SharedSecret: "expected"})
	req := httptest.NewRequest(http.MethodPost, "/internal/auth/user-created", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Hive-Signup-Secret", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestWebhook_DisposableBackstop_NoProvision verifies the webhook honours the
// optional disposable-domain backstop: when DisposableCheck reports a throwaway
// address it stops before any tenant provisioning (so no DB pool is required)
// and returns 204 so Supabase does not retry. This guards the path where a
// scripted signup bypasses the web-console precheck and writes auth.users
// directly via the Supabase API.
func TestWebhook_DisposableBackstop_NoProvision(t *testing.T) {
	var checkedEmail string
	called := false
	provisionEnsure := func(context.Context, string) (string, error) {
		called = true
		return "", nil
	}
	h := signup.NewWebhook(signup.WebhookDeps{
		// Pool/Resolver/Audit intentionally non-nil only where the blocked path
		// touches them. The blocked path must not reach provisioning, so the
		// group-ensure closure should never run.
		Resolver: signup.NewResolver(signup.ResolverDeps{}),
		// Warning-severity, non-security audit routes to the (noop) WAL tier, so
		// the SyncWriter's nil pool is never dereferenced in this DB-free test.
		Audit:        audit.NewLogger(audit.LoggerDeps{Sync: audit.NewSyncWriter(nil, audit.WriterConfig{}), WAL: &noopWAL{}}),
		EnsureGroup:  provisionEnsure,
		SharedSecret: "shh",
		DisposableCheck: func(email string) (bool, error) {
			checkedEmail = email
			return true, nil
		},
	})

	body, _ := json.Marshal(map[string]any{
		"user_id": uuid.New(),
		"email":   "abuser@mailinator.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/auth/user-created", bytes.NewReader(body))
	req.Header.Set("X-Hive-Signup-Secret", "shh")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "abuser@mailinator.com", checkedEmail)
	require.False(t, called, "provisioning must not run for a disposable signup")
}

type noopWAL struct{}

func (noopWAL) Write(ctx context.Context, e audit.Event) error { return nil }

func mustInsertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, deployment string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO public.tenants(slug, name, deployment) VALUES ($1, $1, $2) RETURNING id`,
		slug, deployment).Scan(&id)
	require.NoError(t, err)
	return id
}

func mustInsertAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, slug string) {
	t.Helper()
	ownerID := mustInsertAuthUser(t, ctx, pool, slug+"-owner@example.com")
	_, err := pool.Exec(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES ($1, $2, $2, 'business', $3)`,
		id, slug, ownerID)
	require.NoError(t, err)
}

func mustInsertAuthUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&id)
	require.NoError(t, err)
	return id
}

func newPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool
}
