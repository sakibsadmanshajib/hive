// Command seed-demo populates a fresh Hive database with synthetic demo data
// so that dashboards, billing views, and analytics show representative content
// rather than empty states.
//
// All data is wholly synthetic. No real customer data is used or referenced.
// The script is fully idempotent: running it multiple times on the same DB
// produces the same final state.
//
// Usage:
//
//	DATABASE_URL=postgres://... go run ./scripts/seed-demo/
//
// Required env var:
//
//	DATABASE_URL — Postgres connection string (same format as SUPABASE_DB_URL)
//
// Optional env var:
//
//	DEMO_SEED_VERBOSE=1 — print each SQL statement label executed
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Fixed demo identity UUIDs — stable across runs (idempotency).
// These are deterministic demo UUIDs, not derived from real customer data.
// ---------------------------------------------------------------------------

const (
	demoUserID    = "00000000-de00-0001-0000-000000000001"
	demoAccountID = "00000000-de00-0002-0000-000000000002"
	demoAPIKeyID1 = "00000000-de00-0003-0000-000000000003"
	demoAPIKeyID2 = "00000000-de00-0004-0000-000000000004"
)

// syntheticTokenHash computes a deterministic hash string from a label.
// The resulting value is stored as a token_hash in api_keys — it is NOT a
// real API key secret and cannot be used to authenticate.
func syntheticTokenHash(label string) string {
	sum := sha256.Sum256([]byte("hive-demo-seed-token-hash:" + label))
	return hex.EncodeToString(sum[:])
}

// demoModels is the set of synthetic model aliases used in usage events.
var demoModels = []string{
	"gpt-4o",
	"gpt-4o-mini",
	"claude-sonnet-4",
	"llama-3.3-70b",
}

// demoEndpoints are the API endpoints referenced in usage events.
var demoEndpoints = []string{
	"/v1/chat/completions",
	"/v1/embeddings",
}

func main() {
	ctx := context.Background()
	verbose := os.Getenv("DEMO_SEED_VERBOSE") == "1"

	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dbURL == "" {
		log.Fatal("seed-demo: DATABASE_URL env var is required")
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("seed-demo: connect pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("seed-demo: ping db: %v", err)
	}
	log.Println("seed-demo: connected to database")

	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Fatalf("seed-demo: begin tx: %v", err)
	}

	var txErr error
	defer func() {
		if txErr != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	sd := &seeder{tx: tx, verbose: verbose, rng: rand.New(rand.NewSource(42))}
	if txErr = sd.seedAll(ctx); txErr != nil {
		log.Fatalf("seed-demo: %v", txErr)
	}

	if txErr = tx.Commit(ctx); txErr != nil {
		log.Fatalf("seed-demo: commit: %v", txErr)
	}
	log.Println("seed-demo: done — demo data seeded successfully")
}

type seeder struct {
	tx      pgx.Tx
	verbose bool
	rng     *rand.Rand
}

func (s *seeder) exec(ctx context.Context, label, sql string, args ...any) error {
	if s.verbose {
		log.Printf("SQL [%s]", label)
	}
	_, err := s.tx.Exec(ctx, sql, args...)
	return err
}

func (s *seeder) seedAll(ctx context.Context) error {
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"auth identity stub", s.seedAuthUser},
		{"demo account", s.seedAccount},
		{"account profile", s.seedAccountProfile},
		{"billing profile", s.seedBillingProfile},
		{"account membership", s.seedMembership},
		{"credit ledger entries", s.seedLedger},
		{"API keys", s.seedAPIKeys},
		{"historical usage", s.seedUsage},
	}
	for _, step := range steps {
		log.Printf("seed-demo: seeding %s", step.name)
		if err := step.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// auth.users stub
// Required because public.accounts.owner_user_id FKs to auth.users(id).
// We insert a minimal stub row only; the password hash is not real and
// cannot be used to log in.
// ---------------------------------------------------------------------------

func (s *seeder) seedAuthUser(ctx context.Context) error {
	// Produce a placeholder hash that is not a real bcrypt value.
	placeholder := "$2a$10$seed-demo-not-a-real-password-hash-placeholder-value-x"
	return s.exec(ctx, "auth.users upsert", `
		INSERT INTO auth.users (
			id, email, encrypted_password, email_confirmed_at,
			created_at, updated_at, aud, role
		) VALUES (
			$1::uuid, $2, $3, now(), now(), now(), 'authenticated', 'authenticated'
		)
		ON CONFLICT (id) DO NOTHING
	`, demoUserID, "demo-owner@hive-demo.example", placeholder)
}

// ---------------------------------------------------------------------------
// Account
// ---------------------------------------------------------------------------

func (s *seeder) seedAccount(ctx context.Context) error {
	return s.exec(ctx, "accounts upsert", `
		INSERT INTO public.accounts (
			id, slug, display_name, account_type, owner_user_id,
			created_at, updated_at
		) VALUES (
			$1::uuid, 'hive-demo-org', 'Hive Demo Organisation',
			'business', $2::uuid,
			now() - interval '30 days', now()
		)
		ON CONFLICT (id) DO NOTHING
	`, demoAccountID, demoUserID)
}

// ---------------------------------------------------------------------------
// Account profile
// ---------------------------------------------------------------------------

func (s *seeder) seedAccountProfile(ctx context.Context) error {
	return s.exec(ctx, "account_profiles upsert", `
		INSERT INTO public.account_profiles (
			account_id, owner_name, login_email,
			country_code, state_region, profile_setup_complete
		) VALUES (
			$1::uuid,
			'Demo Owner',
			'demo-owner@hive-demo.example',
			'BD',
			'Dhaka',
			true
		)
		ON CONFLICT (account_id) DO NOTHING
	`, demoAccountID)
}

// ---------------------------------------------------------------------------
// Billing profile
// ---------------------------------------------------------------------------

func (s *seeder) seedBillingProfile(ctx context.Context) error {
	return s.exec(ctx, "account_billing_profiles upsert", `
		INSERT INTO public.account_billing_profiles (
			account_id,
			billing_contact_name,
			billing_contact_email,
			legal_entity_name,
			legal_entity_type,
			country_code,
			created_at,
			updated_at
		) VALUES (
			$1::uuid,
			'Demo Billing Contact',
			'billing@hive-demo.example',
			'Hive Demo Ltd',
			'private_company',
			'BD',
			now() - interval '30 days',
			now()
		)
		ON CONFLICT (account_id) DO NOTHING
	`, demoAccountID)
}

// ---------------------------------------------------------------------------
// Membership
// ---------------------------------------------------------------------------

func (s *seeder) seedMembership(ctx context.Context) error {
	return s.exec(ctx, "account_memberships upsert", `
		INSERT INTO public.account_memberships (
			account_id, user_id, role, status, created_at
		) VALUES (
			$1::uuid, $2::uuid, 'owner', 'active', now() - interval '30 days'
		)
		ON CONFLICT (account_id, user_id) DO NOTHING
	`, demoAccountID, demoUserID)
}

// ---------------------------------------------------------------------------
// Credit ledger — append-only, immutable.
// Pre-funded with two simulated BDT top-ups and synthetic usage charges.
// All deltas are integer credits; no float64.
// Net balance: 500000 + 250000 - 45000 - 32500 - 28000 - 18750 = 625750 credits.
// ---------------------------------------------------------------------------

func (s *seeder) seedLedger(ctx context.Context) error {
	type entry struct {
		idempKey    string
		entryType   string
		creditsDelta int64
		daysAgo     int
		metadata    string
	}

	entries := []entry{
		{
			idempKey:     "seed:grant:initial-topup-30d",
			entryType:    "grant",
			creditsDelta: 500_000,
			daysAgo:      30,
			metadata:     `{"source":"demo_seed","rail":"sslcommerz","note":"initial demo top-up"}`,
		},
		{
			idempKey:     "seed:grant:second-topup-15d",
			entryType:    "grant",
			creditsDelta: 250_000,
			daysAgo:      15,
			metadata:     `{"source":"demo_seed","rail":"bkash","note":"second demo top-up"}`,
		},
		{
			idempKey:     "seed:charge:usage-batch-1",
			entryType:    "usage_charge",
			creditsDelta: -45_000,
			daysAgo:      28,
			metadata:     `{"source":"demo_seed","note":"synthetic usage batch 1"}`,
		},
		{
			idempKey:     "seed:charge:usage-batch-2",
			entryType:    "usage_charge",
			creditsDelta: -32_500,
			daysAgo:      20,
			metadata:     `{"source":"demo_seed","note":"synthetic usage batch 2"}`,
		},
		{
			idempKey:     "seed:charge:usage-batch-3",
			entryType:    "usage_charge",
			creditsDelta: -28_000,
			daysAgo:      10,
			metadata:     `{"source":"demo_seed","note":"synthetic usage batch 3"}`,
		},
		{
			idempKey:     "seed:charge:usage-batch-4",
			entryType:    "usage_charge",
			creditsDelta: -18_750,
			daysAgo:      5,
			metadata:     `{"source":"demo_seed","note":"synthetic usage batch 4"}`,
		},
	}

	for _, e := range entries {
		ts := time.Now().Add(-time.Duration(e.daysAgo) * 24 * time.Hour)
		err := s.exec(ctx, "credit_ledger_entries upsert: "+e.idempKey, `
			INSERT INTO public.credit_ledger_entries (
				account_id, entry_type, credits_delta,
				idempotency_key, metadata, created_at
			) VALUES (
				$1::uuid, $2, $3, $4, $5::jsonb, $6
			)
			ON CONFLICT (account_id, entry_type, idempotency_key) DO NOTHING
		`, demoAccountID, e.entryType, e.creditsDelta, e.idempKey, e.metadata, ts)
		if err != nil {
			return fmt.Errorf("entry %q: %w", e.idempKey, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// API keys — two synthetic active keys.
// Raw secrets are NEVER stored; only token_hash and redacted_suffix.
// The hash values are deterministic and cannot be reversed to a usable key.
// ---------------------------------------------------------------------------

func (s *seeder) seedAPIKeys(ctx context.Context) error {
	keys := []struct {
		id           string
		nickname     string
		hashLabel    string
		redactedSufx string
	}{
		{
			id:           demoAPIKeyID1,
			nickname:     "Demo Production Key",
			hashLabel:    "prod-key-1",
			redactedSufx: "d001",
		},
		{
			id:           demoAPIKeyID2,
			nickname:     "Demo Development Key",
			hashLabel:    "dev-key-2",
			redactedSufx: "d002",
		},
	}

	for _, k := range keys {
		ts := time.Now().Add(-25 * 24 * time.Hour)
		lastUsed := time.Now().Add(-2 * time.Hour)
		tokenHash := syntheticTokenHash(k.hashLabel)

		err := s.exec(ctx, "api_keys upsert: "+k.nickname, `
			INSERT INTO public.api_keys (
				id, account_id, nickname, token_hash, redacted_suffix,
				status, created_by_user_id, last_used_at, created_at, updated_at
			) VALUES (
				$1::uuid, $2::uuid, $3, $4, $5,
				'active', $6::uuid, $7, $8, $8
			)
			ON CONFLICT (id) DO NOTHING
		`, k.id, demoAccountID, k.nickname, tokenHash, k.redactedSufx,
			demoUserID, lastUsed, ts)
		if err != nil {
			return fmt.Errorf("api key %q: %w", k.nickname, err)
		}

		err = s.exec(ctx, "api_key_events created: "+k.nickname, `
			INSERT INTO public.api_key_events (
				api_key_id, account_id, event_type, actor_user_id,
				metadata, created_at
			) VALUES (
				$1::uuid, $2::uuid, 'created', $3::uuid,
				'{"source":"demo_seed"}'::jsonb, $4
			)
			ON CONFLICT DO NOTHING
		`, k.id, demoAccountID, demoUserID, ts)
		if err != nil {
			return fmt.Errorf("api key event %q: %w", k.nickname, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Usage history — synthetic request_attempts + usage_events spread over
// the past 30 days to populate analytics and billing views.
// ---------------------------------------------------------------------------

func (s *seeder) seedUsage(ctx context.Context) error {
	const numRequests = 60

	apiKeyIDs := []string{demoAPIKeyID1, demoAPIKeyID2}

	for i := 0; i < numRequests; i++ {
		hoursAgo := s.rng.Intn(30*24) + 1
		startedAt := time.Now().Add(-time.Duration(hoursAgo) * time.Hour)
		completedAt := startedAt.Add(time.Duration(500+s.rng.Intn(4500)) * time.Millisecond)

		model := demoModels[s.rng.Intn(len(demoModels))]
		endpoint := demoEndpoints[s.rng.Intn(len(demoEndpoints))]
		apiKeyID := apiKeyIDs[s.rng.Intn(len(apiKeyIDs))]

		// Synthetic token counts — realistic distribution.
		inputTokens := int64(200 + s.rng.Intn(3800))
		outputTokens := int64(100 + s.rng.Intn(900))
		// Simplified: 1 credit per token.
		creditDelta := -(inputTokens + outputTokens)

		requestID := fmt.Sprintf("seed-req-%04d-%d", i, startedAt.UnixNano())
		attemptID := deterministicUUID("attempt:" + requestID)
		eventID := deterministicUUID("event:" + requestID)

		err := s.exec(ctx, fmt.Sprintf("request_attempts[%d]", i), `
			INSERT INTO public.request_attempts (
				id, account_id, request_id, attempt_number,
				endpoint, model_alias, status,
				user_id, api_key_id,
				started_at, completed_at
			) VALUES (
				$1::uuid, $2::uuid, $3, 1, $4, $5, 'completed',
				$6::uuid, $7::uuid, $8, $9
			)
			ON CONFLICT (account_id, request_id, attempt_number) DO NOTHING
		`, attemptID, demoAccountID, requestID, endpoint, model,
			demoUserID, apiKeyID, startedAt, completedAt)
		if err != nil {
			return fmt.Errorf("request_attempt[%d]: %w", i, err)
		}

		err = s.exec(ctx, fmt.Sprintf("usage_events[%d]", i), `
			INSERT INTO public.usage_events (
				id, account_id, request_attempt_id, request_id,
				event_type, endpoint, model_alias, status,
				input_tokens, output_tokens,
				cache_read_tokens, cache_write_tokens,
				hive_credit_delta,
				created_at
			) VALUES (
				$1::uuid, $2::uuid, $3::uuid, $4,
				'completed', $5, $6, 'completed',
				$7, $8, 0, 0, $9, $10
			)
			ON CONFLICT DO NOTHING
		`, eventID, demoAccountID, attemptID, requestID,
			endpoint, model, inputTokens, outputTokens, creditDelta, completedAt)
		if err != nil {
			return fmt.Errorf("usage_event[%d]: %w", i, err)
		}
	}
	return nil
}

// deterministicUUID derives a stable UUID from a seed string via SHA-256.
// Used for seed data only; not cryptographically significant.
func deterministicUUID(seed string) string {
	sum := sha256.Sum256([]byte("hive-demo-uuid:" + seed))
	h := hex.EncodeToString(sum[:16])
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}
