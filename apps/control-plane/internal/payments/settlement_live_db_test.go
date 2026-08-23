package payments

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/profiles"
)

// Issue #628, invariant 3. The unit tests above prove the ordering with an
// in-memory ledger, which cannot prove that a CONCURRENT redelivery grants once:
// an application-level "have I granted this already?" check would pass both
// tests and still double-credit under a real race. These run against a live
// Postgres carrying the full migration chain, so the grant is deduplicated by
// the credit_idempotency_keys primary key rather than by application logic.
//
// Gated on HIVE_TEST_DB_URL exactly like the other live-schema suites in this
// repository (see internal/usage/repository_test.go); CI provides it.

func newPaymentsTestPool(t *testing.T) *pgxpool.Pool {
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

// seedPaymentsAccount inserts the auth.users row and the public.accounts row a
// payment intent's account_id must reference, and registers cleanup.
func seedPaymentsAccount(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"payment-settlement-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		t.Skipf("seed auth.users failed (is this a migrated test DB?): %v", err)
	}

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'payment settlement test', 'personal', $2) RETURNING id`,
		"payment-settlement-"+uuid.NewString(), userID,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		// Deliveries survive the intent (FK is ON DELETE SET NULL) so they are
		// removed explicitly before the account cascade.
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM public.payment_webhook_deliveries
			  WHERE payment_intent_id IN (SELECT id FROM public.payment_intents WHERE account_id = $1)`, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth.users WHERE id = $1`, userID)
	})

	return accountID
}

// TestSettlement_ConcurrentDeliveriesGrantExactlyOnce delivers the same provider
// event twice at the same moment against a live database. Exactly one grant must
// land on the append-only ledger, both deliveries must report success, and the
// intent must end completed.
func TestSettlement_ConcurrentDeliveriesGrantExactlyOnce(t *testing.T) {
	pool := newPaymentsTestPool(t)
	ctx := context.Background()
	accountID := seedPaymentsAccount(t, pool)

	repo := NewPgxRepository(pool)
	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))

	const credits int64 = CreditsPerUSD
	providerIntentID := "prov_concurrent_" + uuid.NewString()
	intent := PaymentIntent{
		ID:               uuid.New(),
		AccountID:        accountID,
		Rail:             RailStripe,
		Status:           IntentStatusPendingRedirect,
		Credits:          credits,
		AmountUSD:        100,
		LocalCurrency:    "USD",
		TaxTreatment:     "no_tax",
		TaxRate:          "0.00",
		IdempotencyKey:   "idem-" + providerIntentID,
		ProviderIntentID: providerIntentID,
		Metadata:         map[string]any{},
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := repo.InsertPaymentIntent(ctx, intent); err != nil {
		t.Fatalf("insert intent: %v", err)
	}

	rail := newStubRail(RailStripe)
	rail.eventResult = RailEvent{
		ProviderIntentID: providerIntentID,
		EventType:        "payment.succeeded",
		RawPayload:       []byte(`{"live":true}`),
	}
	svc := buildService(repo, ledgerSvc, &stubProfiles{
		accountProfile: profiles.AccountProfile{CountryCode: "US"},
	}, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: rail})

	// Two deliveries released at the same instant.
	const deliveries = 2
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	errs := make([]error, deliveries)
	for i := 0; i < deliveries; i++ {
		done.Add(1)
		go func(slot int) {
			defer done.Done()
			start.Wait()
			errs[slot] = svc.HandleProviderEvent(ctx, RailStripe, []byte(`{"live":true}`), nil)
		}(i)
	}
	start.Done()
	done.Wait()

	for slot, err := range errs {
		if err != nil {
			t.Fatalf("delivery %d must succeed so the provider stops retrying, got %v", slot, err)
		}
	}

	// The ledger is append-only: exactly one grant entry may exist.
	idempotencyKey := fmt.Sprintf("payment:purchase:%s", intent.ID)
	var grantRows int
	var grantedCredits int64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(credits_delta), 0)
		   FROM public.credit_ledger_entries
		  WHERE account_id = $1 AND entry_type = 'grant' AND idempotency_key = $2`,
		accountID, idempotencyKey,
	).Scan(&grantRows, &grantedCredits); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grantRows != 1 {
		t.Fatalf("expected exactly 1 grant entry for %d concurrent deliveries, got %d (double credit)", deliveries, grantRows)
	}
	if grantedCredits != credits {
		t.Fatalf("expected %d credits granted, got %d", credits, grantedCredits)
	}

	balance, err := ledgerSvc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("get balance: %v", err)
	}
	if balance.PostedCredits != credits {
		t.Fatalf("expected posted balance %d, got %d", credits, balance.PostedCredits)
	}

	settled, err := repo.GetPaymentIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("get intent: %v", err)
	}
	if settled.Status != IntentStatusCompleted {
		t.Fatalf("expected completed after a confirmed grant, got %s", settled.Status)
	}

	// Both inbound deliveries are recorded and neither is left in the dead letter.
	var recorded, unsettled int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE status <> 'processed')
		   FROM public.payment_webhook_deliveries WHERE payment_intent_id = $1`,
		intent.ID,
	).Scan(&recorded, &unsettled); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if recorded != deliveries {
		t.Fatalf("expected %d durable delivery records, got %d", deliveries, recorded)
	}
	if unsettled != 0 {
		t.Fatalf("expected no unsettled deliveries, got %d", unsettled)
	}
}

// TestSettlement_DeadLetterSurvivesUnresolvableIntent proves the dead letter is
// real against the live schema: a delivery that cannot be matched to an intent is
// still persisted, with a NULL intent link, and shows up in the dead-letter view.
func TestSettlement_DeadLetterSurvivesUnresolvableIntent(t *testing.T) {
	pool := newPaymentsTestPool(t)
	ctx := context.Background()

	repo := NewPgxRepository(pool)
	rail := newStubRail(RailStripe)
	rail.eventResult = RailEvent{
		ProviderIntentID: "prov_unmatched_" + uuid.NewString(),
		EventType:        "payment.succeeded",
		RawPayload:       []byte(`{"unmatched":true}`),
	}
	svc := buildService(repo, ledger.NewService(ledger.NewPgxRepository(pool)), &stubProfiles{}, &stubFXProvider{}, map[Rail]PaymentRail{RailStripe: rail})

	marker := "dead-letter-" + uuid.NewString()
	body := fmt.Sprintf(`{"marker":%q}`, marker)
	if err := svc.HandleProviderEvent(ctx, RailStripe, []byte(body), nil); err == nil {
		t.Fatal("expected an error for an unresolvable intent, got nil")
	}

	var id uuid.UUID
	var status, errorDetail, rawBody string
	var intentID *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id, status, error_detail, raw_body, payment_intent_id
		   FROM public.payment_webhook_deliveries
		  WHERE raw_body = $1`, body,
	).Scan(&id, &status, &errorDetail, &rawBody, &intentID); err != nil {
		t.Fatalf("expected a durable dead-letter row for the failed delivery: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.payment_webhook_deliveries WHERE id = $1`, id)
	})

	if status != string(DeliveryStatusFailed) {
		t.Fatalf("expected status %s, got %s", DeliveryStatusFailed, status)
	}
	if intentID != nil {
		t.Fatalf("expected a NULL intent link, got %s", intentID)
	}
	if errorDetail == "" {
		t.Fatal("expected the failure reason persisted")
	}
}
