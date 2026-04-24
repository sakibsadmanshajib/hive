# Phase 8: Payments, FX, and Compliance Checkout - Research

**Researched:** 2026-04-10
**Domain:** Payment rails (Stripe, bKash, SSLCommerz), FX snapshots, Bangladesh VAT/tax compliance, Go payment intent abstraction
**Confidence:** HIGH (core patterns) / MEDIUM (bKash/SSLCommerz specifics — no official Go SDK, hand-rolled HTTP client required)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Payment rail selection**
- Auto-detect available rails by customer's `country_code` from their account profile: BD → bKash + SSLCommerz + Stripe; all others → Stripe only.
- BD customers see all three rails with equal presentation — no preferred/default rail.
- Credit purchase options: predefined tiers (1K, 5K, 10K, 50K, 100K) plus a "Custom amount" field accepting any multiple of 1,000.
- Min/max purchase limits are rail-specific — Claude determines exact thresholds based on each provider's transaction limits.

**FX snapshot lifecycle**
- Exchange rate source: multi-source with fallback. Primary: XE API. Fallback: cached last-known rate if XE is unavailable. Admin override available for emergencies.
- FX quote validity: session-based. Quote is valid for the duration of the checkout session. If the customer leaves and returns, a new quote with a fresh rate is generated.
- FX conversion fee: **5%** (not 3% as in REQUIREMENTS.md BILL-04 — user decision to increase). Baked into the displayed BDT price, not shown as a separate line item.
- NOTE: REQUIREMENTS.md BILL-04 references "3% conversion fee" — this should be updated to 5% to match this decision.

**Checkout flow shape**
- Server-side payment intent + redirect: control-plane creates a payment intent, returns a provider-hosted checkout URL, console redirects customer there, webhook confirms completion.
- Credits posted instantly on Stripe webhook success. BD rails (bKash, SSLCommerz) get a short confirmation delay (minutes, not days) before posting credits due to different webhook reliability characteristics.
- Checkout API lives inside the existing control-plane service as a new `payments` package — same DB, same deployment. Already has direct access to the ledger and profiles services.
- Failed/expired payment intents: auto-cancel with notification in the console. Customer sees an error with a "retry" button. No credits posted. Ledger stays clean.

**Tax and surcharge rules**
- Full tax calculation at checkout — calculate and apply VAT/GST/sales tax based on customer's country and business type at purchase time.
- BD tax treatment: Claude's discretion — research current Bangladesh digital services tax rules and implement correctly.
- Provider fees (bKash ~1.5%, SSLCommerz ~2%, Stripe ~2.9%) absorbed by Hive. Customer sees the same credit price regardless of rail. No rail-specific surcharges.
- First purchase requires complete billing profile (name, country, business type, tax ID if applicable). Subsequent purchases use stored profile. This fulfills the Phase 2 decision to defer billing profile collection to checkout time.

### Claude's Discretion
- FX rate refresh cadence based on XE API limits and rate volatility patterns.
- Exact rail-specific min/max transaction limits based on provider documentation.
- Bangladesh digital services tax rules (VAT rate, business exemptions, BIN validation).
- Exact payment intent schema, webhook signature verification approach, and retry/reconciliation mechanics.
- Exact confirmation delay duration for BD rails before credit posting.
- International tax treatment beyond BD (whether to apply tax for non-BD customers at this stage).

### Deferred Ideas (OUT OF SCOPE)
- Tenant-level fee transparency toggle — Allow accounts to opt in to seeing FX fee as a separate line item.
- Invoice rendering and receipt generation — Phase 9 (BILL-05).
- Spend dashboards and budget alerts — Phase 9 (BILL-06).
- Subscription/recurring billing plans — Explicitly out of scope for v1.
- Update REQUIREMENTS.md BILL-04 — Change "3% conversion fee" to "5% conversion fee" to match user decision.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BILL-03 | Customer can buy credits in increments of 1,000 Hive Credits through Stripe, bKash, and SSLCommerz | Stripe Go SDK v84, bKash Tokenized Checkout API, SSLCommerz API v4 — all three use server-side redirect flow; predefined tiers (1K–100K) + custom multiples of 1K |
| BILL-04 | Hive prices credits at 100,000 Hive Credits per 1 USD and persists the exact FX snapshot plus 5% conversion fee used for every BDT transaction | XE Currency Data API v1 (HTTP Basic Auth, convert_from endpoint); fx_snapshots table with rate + fee baked into BDT amount; session-scoped quote |
| BILL-07 | Hive captures and applies country, business, tax, and payment-method surcharge data needed for compliant checkout and invoicing flows | Bangladesh VAT: 15% standard rate on digital services (5% ITES reduced rate may apply); B2B reverse-charge if buyer has BIN; existing account_billing_profiles table has all required fields |
</phase_requirements>

---

## Summary

Phase 8 builds a three-rail payment abstraction (Stripe, bKash, SSLCommerz) inside the existing control-plane Go service, using a canonical `PaymentIntent` state machine backed by Postgres. The core engineering challenges are: (1) a unified provider interface that hides per-rail flow differences behind a common `InitiateCheckout → webhook/callback → PostCredits` lifecycle; (2) idempotent webhook processing for all three providers using the existing `credit_idempotency_keys` table pattern; and (3) a session-scoped FX snapshot that locks the USD/BDT rate + 5% fee for the duration of checkout.

No official Go SDKs exist for bKash or SSLCommerz — both are HTTP APIs that require a hand-rolled Go client. Stripe has an official Go SDK (`stripe-go v84`, currently at v84.4.1 as of 2026-03-06). The payment intent state machine should be stored in a new `payment_intents` Postgres table, with provider events recorded in a `payment_events` table for reconciliation. BD rails use a confirmation hold before ledger posting (recommended: 2–5 minutes) to account for webhook delivery variability. Bangladesh digital services VAT is 15% standard rate; B2B customers with a BIN are subject to reverse-charge mechanism instead.

**Primary recommendation:** Build a `PaymentRail` interface in `apps/control-plane/internal/payments/` with three concrete implementations (stripe, bkash, sslcommerz), a `FXService` backed by the XE API with Redis cache fallback, and a `PaymentIntentService` that orchestrates the full lifecycle using the existing ledger, profiles, and accounts services.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/stripe/stripe-go/v84` | v84.4.1 | Stripe PaymentIntent creation, Checkout Sessions, webhook event parsing and signature verification | Official Stripe Go SDK; only maintained Go library; v84 is current as of 2026-03-06 |
| `github.com/google/uuid` | v1.6.0 | Payment intent and event IDs | Already in go.mod |
| `github.com/jackc/pgx/v5` | v5.7.2 | Payment intents/events/fx_snapshots Postgres persistence | Already in go.mod |
| `github.com/redis/go-redis/v9` | v9.14.1 | FX rate cache (last-known rate fallback) | Already in go.mod |
| `net/http` (stdlib) | Go 1.24 | bKash and SSLCommerz HTTP clients (no Go SDKs exist) | Standard library; sufficient for REST HTTP APIs |
| `crypto/hmac` + `crypto/sha256` (stdlib) | Go 1.24 | SSLCommerz hash validation | Standard library |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `encoding/json` (stdlib) | Go 1.24 | bKash and SSLCommerz request/response marshalling | Always — no alternative needed |
| `context` (stdlib) | Go 1.24 | Timeout propagation to external payment APIs | Always — XE/bKash/SSLCommerz calls need 30s timeout |
| `time` (stdlib) | Go 1.24 | FX snapshot timestamps, intent expiry | Always |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Stripe PaymentIntent + redirect | Stripe Checkout Session | Both valid; PaymentIntent gives more control over the flow and fits server-side intent model; Checkout Session is simpler but less flexible for multi-rail consistency |
| XE Currency Data API | Open Exchange Rates, ExchangeRate-API, Fixer | XE is the user-specified primary source; alternatives are viable fallbacks but user locked XE as primary |
| Redis for FX cache | Postgres for FX cache | Redis already in stack; appropriate for short-lived rate cache; Postgres used only for durable FX snapshots baked into payment records |

**Installation:**
```bash
go get github.com/stripe/stripe-go/v84@v84.4.1
```
bKash and SSLCommerz: no Go packages — implement HTTP clients in `internal/payments/bkash/` and `internal/payments/sslcommerz/`.

**Version verification:** Confirmed `github.com/stripe/stripe-go/v84@v84.4.1` via `proxy.golang.org` on 2026-04-10. Published 2026-03-06.

---

## Architecture Patterns

### Recommended Project Structure
```
apps/control-plane/internal/payments/
├── types.go          # PaymentIntent, PaymentEvent, FXSnapshot, Rail enum, intent states
├── service.go        # PaymentIntentService — orchestrates initiate/confirm/cancel/post
├── repository.go     # Repository interface + pgx implementation for payment_intents, payment_events, fx_snapshots
├── http.go           # Handler: POST /api/v1/checkout/initiate, webhook endpoints (unauthenticated + sig-verified)
├── http_test.go      # Handler tests
├── service_test.go   # Service tests with stub repo + stub rail
├── fx.go             # FXService: XE API call, Redis cache fallback, admin override logic
├── fx_test.go        # FX service tests
├── rail.go           # PaymentRail interface definition
├── stripe/
│   └── rail.go       # Stripe PaymentIntent creation + ConstructEvent webhook handler
├── bkash/
│   └── rail.go       # bKash Tokenized Checkout: grant token → create → execute flow
└── sslcommerz/
    └── rail.go       # SSLCommerz: initiate → IPN callback → validate transaction flow
```

New Supabase migrations:
```
supabase/migrations/
├── 20260410_01_payment_intents.sql      # payment_intents, payment_events tables
└── 20260410_02_fx_snapshots.sql         # fx_snapshots table
```

### Pattern 1: PaymentRail Interface

Each provider implements a single interface. The `PaymentIntentService` calls `Initiate` to get a redirect URL, and `ProcessEvent` to handle provider callbacks/webhooks.

```go
// Source: project pattern from internal/accounting/service.go (interface per dependency)
type PaymentRail interface {
    RailName() string
    Initiate(ctx context.Context, input InitiateInput) (InitiateResult, error)
    ProcessEvent(ctx context.Context, rawBody []byte, headers map[string]string) (RailEvent, error)
}

type InitiateInput struct {
    PaymentIntentID uuid.UUID
    AccountID       uuid.UUID
    Credits         int64
    AmountUSD       decimal  // for Stripe
    AmountBDT       int64    // for BD rails (paise/tk * 100)
    Currency        string
    CallbackBaseURL string
    CustomerName    string
    CustomerEmail   string
}

type InitiateResult struct {
    ProviderIntentID string
    RedirectURL      string
    ExpiresAt        time.Time
}

type RailEvent struct {
    ProviderIntentID string
    EventType        string // "payment.succeeded", "payment.failed", "payment.expired"
    RawPayload       []byte
}
```

### Pattern 2: Payment Intent State Machine

States stored in `payment_intents.status` column. Transitions are enforced at the service layer.

```
created → pending_redirect → provider_processing → confirming (BD only) → completed
                                                 ↘ failed
                                                 ↘ expired
                                                 ↘ cancelled
```

- `created`: Intent row inserted, credits not reserved, no provider call made
- `pending_redirect`: Initiate called, provider URL returned to console, awaiting redirect
- `provider_processing`: Customer arrived at provider page (inferred from webhook arrival)
- `confirming`: BD rails only — webhook received but brief hold before ledger post
- `completed`: Webhook success confirmed + ledger grant posted (idempotent)
- `failed` / `expired` / `cancelled`: Terminal states, no credits posted

```go
// Source: project pattern (immutable state transitions, no silent mutations)
func (s *Service) transitionStatus(ctx context.Context, intentID uuid.UUID, from, to IntentStatus) error {
    updated, err := s.repo.CompareAndSetStatus(ctx, intentID, from, to)
    if err != nil {
        return fmt.Errorf("payments: transition %s→%s: %w", from, to, err)
    }
    if !updated {
        return ErrInvalidTransition
    }
    return nil
}
```

### Pattern 3: Idempotent Webhook Processing

Webhook handlers must be unauthenticated endpoints (provider callbacks) but signature-verified. Credit posting re-uses the existing `credit_idempotency_keys` mechanism.

```go
// Source: pattern from internal/ledger/service.go (idempotency_key enforcement)
// Webhook idempotency key: "payment:{provider}:{provider_intent_id}:{event_type}"
func (s *Service) PostPurchaseGrant(ctx context.Context, intentID uuid.UUID) error {
    intent, err := s.repo.GetPaymentIntent(ctx, intentID)
    // ...
    idempotencyKey := fmt.Sprintf("payment:purchase:%s", intentID)
    _, err = s.ledgerSvc.GrantCredits(ctx, intent.AccountID, idempotencyKey, intent.Credits, map[string]any{
        "payment_intent_id": intentID,
        "rail":              intent.Rail,
        "fx_snapshot_id":    intent.FXSnapshotID,
    })
    // ledger already has unique constraint on (account_id, entry_type, idempotency_key)
    // so duplicate webhook delivery produces no double-credit
    return err
}
```

### Pattern 4: Session-Scoped FX Quote

FX snapshot is created at `Initiate` time and stored durably. The checkout session carries the `fx_snapshot_id`. On payment completion, the snapshot is referenced from the ledger entry metadata.

```go
type FXSnapshot struct {
    ID            uuid.UUID `db:"id"`
    AccountID     uuid.UUID `db:"account_id"`
    BaseCurrency  string    `db:"base_currency"`  // "USD"
    QuoteCurrency string    `db:"quote_currency"` // "BDT"
    MidRate       string    `db:"mid_rate"`       // raw rate from XE, e.g. "110.25"
    FeeRate       string    `db:"fee_rate"`       // "0.05" (5%)
    EffectiveRate string    `db:"effective_rate"` // mid_rate * (1 + fee_rate)
    SourceAPI     string    `db:"source_api"`     // "xe" | "cache" | "admin_override"
    FetchedAt     time.Time `db:"fetched_at"`
    CreatedAt     time.Time `db:"created_at"`
}
```

XE API call:
```go
// Source: XE Currency Data API v1 Specifications (xecdapi.xe.com/docs/v1/)
// GET https://xecdapi.xe.com/v1/convert_from.json/?from=USD&to=BDT&amount=1
// Auth: HTTP Basic — account_id:api_key
req.SetBasicAuth(xeAccountID, xeAPIKey)
```

### Pattern 5: Stripe Integration

```go
// Source: github.com/stripe/stripe-go v84 (pkg.go.dev/github.com/stripe/stripe-go/v84)
import (
    stripe "github.com/stripe/stripe-go/v84"
    "github.com/stripe/stripe-go/v84/paymentintent"
    "github.com/stripe/stripe-go/v84/webhook"
)

// Create intent
params := &stripe.PaymentIntentParams{
    Amount:   stripe.Int64(amountCents),
    Currency: stripe.String("usd"),
    Metadata: map[string]string{
        "hive_payment_intent_id": intentID.String(),
    },
}
pi, err := paymentintent.New(params)

// Webhook signature verification (raw body required)
event, err := webhook.ConstructEvent(payload, sigHeader, webhookSecret)
// DefaultTolerance = 300 seconds; rejects stale signatures
```

Relevant Stripe webhook events:
- `payment_intent.succeeded` → transition to `completed`, post ledger grant
- `payment_intent.payment_failed` → transition to `failed`
- `payment_intent.canceled` → transition to `cancelled`

### Pattern 6: bKash Tokenized Checkout

bKash uses a three-step flow with token refresh. No Go SDK — plain `net/http`.

```go
// Source: bKash Tokenized Checkout API (developer.bka.sh)
// Step 1: Grant Token (POST /tokenized/checkout/token/grant)
// Headers: username, password; Body: app_key, app_secret
// Response: id_token (short-lived), token_type, expires_in

// Step 2: Create Payment (POST /tokenized/checkout/create)
// Headers: Authorization: id_token, X-App-Key: app_key
// Body: mode="0011", payerReference, callbackURL, amount, currency="BDT",
//       intent="sale", merchantInvoiceNumber
// Response: paymentID, bkashURL (redirect here)

// Step 3: Execute Payment (POST /tokenized/checkout/execute)
// Called server-side after customer redirected back with ?paymentID=&status=success
// Headers: Authorization: id_token, X-App-Key: app_key
// Body: paymentID
// Response: trxID, transactionStatus="Completed"
```

bKash min/max limits (sandbox documentation, MEDIUM confidence):
- Minimum: BDT 1 (effectively Hive's 1K credit tier is the floor)
- Maximum: BDT 30,000 per transaction (approx $270 USD)
- Daily limit: BDT 200,000 per user wallet

Idempotency for bKash: `merchantInvoiceNumber` maps to `payment_intent_id` — same invoice number on retry returns the same payment, preventing duplicates.

Callback security: Do NOT trust `?status=success` in callback URL alone. Always call Execute server-side to verify actual payment status.

Confirmation delay for BD rails: recommend **3 minutes** before posting ledger grant. Store `confirming_at` timestamp; a background job or webhook handler checks elapsed time before calling `PostPurchaseGrant`.

### Pattern 7: SSLCommerz Integration

```
// Source: SSLCommerz API v4 (developer.sslcommerz.com/doc/v4/)
// Initiate: POST https://securepay.sslcommerz.com/gwprocess/v4/api.php
//   (sandbox: https://sandbox.sslcommerz.com/gwprocess/v4/api.php)
// Required params (form-encoded): store_id, store_passwd, total_amount, currency="BDT",
//   tran_id (= payment_intent_id), success_url, fail_url, cancel_url,
//   cus_name, cus_email, cus_add1, cus_city, cus_country, ipn_url
// Response JSON: status, sessionkey, GatewayPageURL → redirect here

// IPN (POST to ipn_url): val_id, amount, card_type, store_amount, tran_id, status
// Validation: GET https://securepay.sslcommerz.com/validator/api/validationserverAPI.php
//   ?val_id=VAL_ID&store_id=STORE_ID&store_passwd=STORE_PASSWD&format=json
// Validates IPN authenticity server-side

// Hash verification (additional): md5(store_passwd + val_id + amount + currency) 
// against verify_sign field in IPN payload
```

SSLCommerz min/max limits: Minimum BDT 10; Maximum BDT 500,000 per transaction.

### Anti-Patterns to Avoid
- **Trusting redirect callbacks without server-side verification:** bKash and SSLCommerz callbacks carry query params that can be tampered. Always execute/validate server-side before marking an intent complete.
- **Storing raw body after webhook parse:** Read raw body once into `[]byte` for signature verification, then pass to json.Unmarshal separately. Stripe's `ConstructEvent` requires the raw body — do not pre-parse.
- **Mutable FX snapshots:** Once a snapshot is created for a session, it must not be updated. Create a new snapshot row for each new checkout session.
- **Posting credits synchronously in webhook handler:** Webhook handlers should be fast — record the event, queue the grant, return 200 quickly. For BD rails specifically, the grant is delayed; even for Stripe, state transition + ledger write should complete within the handler but the HTTP response must be returned promptly.
- **Trusting Redis FX cache as financial truth:** Redis holds the rate for performance only. The durable `fx_snapshots` Postgres table is the authoritative record for every BDT transaction.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Stripe webhook signature verification | Custom HMAC-SHA256 validator | `webhook.ConstructEvent` from `stripe-go/v84` | Handles timestamp tolerance, v1/v0 signature variants, replay protection |
| Payment intent deduplication at Stripe | Custom dedup logic | Stripe's built-in idempotency keys via `stripe.IdempotencyKey` on params | Stripe natively supports idempotency keys on all mutating API calls |
| Credit double-posting on webhook replay | Custom webhook dedup table | Existing `credit_idempotency_keys` table + `ledger.GrantCredits` | Already ACID-enforced via unique constraint `(account_id, entry_type, idempotency_key)` |
| FX rate math with floating point | `float64` arithmetic | `int64` micro-units or string-based decimal comparison | Floating point precision loss corrupts financial records |
| Currency amount formatting | Manual BDT formatting | Represent amounts as `int64` paisa (BDT * 100) internally; format only at display boundary | Avoids rounding errors in multi-step fee/tax math |

**Key insight:** The ledger's idempotency guarantee is the payment system's primary safety net. Webhook deduplication for credit posting is free — use it everywhere.

---

## Common Pitfalls

### Pitfall 1: Webhook Raw Body Consumption
**What goes wrong:** Go's `http.Request.Body` is a stream — if any middleware reads it (e.g., `json.NewDecoder(r.Body).Decode()`), the body is consumed. Stripe's `ConstructEvent` receives an empty byte slice and fails signature verification.
**Why it happens:** Standard middleware patterns read and discard the body before the handler runs.
**How to avoid:** Read `r.Body` into a `[]byte` with `io.ReadAll` at the start of the webhook handler, before any JSON parsing. Pass the raw bytes to `ConstructEvent`, then unmarshal separately.
**Warning signs:** `stripe.ErrInvalidSignature` in logs despite correct webhook secret.

### Pitfall 2: BDT Amount Precision
**What goes wrong:** Stripe amounts are in smallest currency unit (cents/paise). BDT doesn't use subdivision in everyday use but the API expects integer paisa (1 BDT = 100 paisa for Stripe). SSLCommerz and bKash accept decimal BDT amounts (e.g., "1500.00") as strings.
**Why it happens:** Each provider has a different amount format.
**How to avoid:** Store all amounts internally as `int64` micro-units (BDT * 100). Convert to provider-specific format at the adapter boundary. Document clearly in the `InitiateInput` struct.
**Warning signs:** Payment amounts off by 100x, or providers rejecting amounts with unexpected decimal places.

### Pitfall 3: bKash Token Expiry Mid-Session
**What goes wrong:** bKash `id_token` expires after a short TTL (typically 3600 seconds). If the grant token call and the create payment call are separated by enough time (or the token is cached across sessions), payment creation fails with an auth error.
**Why it happens:** bKash requires fresh tokens per checkout session.
**How to avoid:** Grant a new token per checkout initiation. Do not cache bKash tokens across different payment intent sessions. Cache only within the same request context.
**Warning signs:** bKash returning 401 on Create Payment despite valid credentials.

### Pitfall 4: SSLCommerz IPN vs. Redirect Race
**What goes wrong:** SSLCommerz sends both a browser redirect (success_url) and a server IPN notification. If the IPN arrives after the redirect but before the server-side validation completes, a naively concurrent handler may double-post credits.
**Why it happens:** IPN and redirect arrive nearly simultaneously; both trigger the same completion handler.
**How to avoid:** Use `CompareAndSetStatus` on the payment intent (Postgres-level compare-and-swap via `UPDATE ... WHERE status = 'pending' RETURNING id`). Only the first writer wins; the second finds no matching row and is a no-op. The existing `credit_idempotency_keys` constraint provides the secondary guard.
**Warning signs:** Duplicate `grant` entries in `credit_ledger_entries` with the same idempotency key (would be caught by unique constraint, but the error should be handled gracefully rather than propagated as a 500).

### Pitfall 5: XE API Rate Limits with Session-Scoped Quotes
**What goes wrong:** If every checkout page load generates a fresh XE API call, a traffic spike exhausts the XE API rate limit. Stale rates then fall back to cached values, but if the cache TTL is too long, customers see outdated prices.
**Why it happens:** Session-based quotes require a fresh rate per new checkout session.
**How to avoid:** Cache the XE rate in Redis with a short TTL (recommended: 5 minutes). A new checkout session within the TTL window reuses the cached rate. A new session after TTL expiry refreshes from XE. The `fx_snapshots` row records `source_api: "cache"` vs `"xe"` for auditability. Admin override bypasses both.
**Warning signs:** XE API 429 responses, falling back to very old cached rates.

### Pitfall 6: Bangladesh VAT Application
**What goes wrong:** Applying VAT incorrectly — either charging 15% to B2B customers who should use reverse-charge, or applying 5% ITES rate when the service qualifies for standard 15%.
**Why it happens:** Bangladesh VAT has both a standard rate (15%) and a reduced ITES rate (5%), plus B2B reverse-charge exemption.
**How to avoid:** At checkout time: (1) Check `country_code == "BD"`. (2) Check `legal_entity_type` — if `individual` or `sole_proprietor`, apply 15% VAT to the displayed price (included). (3) If `private_company` or `public_company` with a valid `vat_number`/BIN, apply reverse-charge: zero-rate on invoice, note on receipt that buyer is liable. (4) Non-BD customers: no VAT applied at this stage (deferred decision per CONTEXT.md). Store the applied tax treatment in the payment event metadata for Phase 9 invoicing.
**Warning signs:** Missing `vat_number` field being treated as a hard block — it should be optional; absence means no reverse-charge, full VAT applies.

---

## Code Examples

### Payment Intent Repository — Compare-and-Set Status
```go
// Source: project pattern from internal/accounting (immutable state + idempotency)
// Postgres: UPDATE payment_intents SET status=$1, updated_at=now()
//   WHERE id=$2 AND status=$3 RETURNING id
func (r *repository) CompareAndSetStatus(ctx context.Context, id uuid.UUID, from, to IntentStatus) (bool, error) {
    var returnedID uuid.UUID
    err := r.db.QueryRow(ctx,
        `UPDATE payment_intents SET status=$1, updated_at=now()
         WHERE id=$2 AND status=$3 RETURNING id`,
        string(to), id, string(from),
    ).Scan(&returnedID)
    if errors.Is(err, pgx.ErrNoRows) {
        return false, nil // already transitioned or not found
    }
    if err != nil {
        return false, fmt.Errorf("payments: compare-and-set status: %w", err)
    }
    return true, nil
}
```

### Stripe Webhook Handler
```go
// Source: stripe-go v84 webhook package (pkg.go.dev/github.com/stripe/stripe-go/v84/webhook)
func (h *Handler) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
    payload, err := io.ReadAll(r.Body)
    if err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable body"})
        return
    }
    event, err := webhook.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), h.stripeWebhookSecret)
    if err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid signature"})
        return
    }
    // process event.Type: "payment_intent.succeeded", etc.
    w.WriteHeader(http.StatusOK)
}
```

### FX Snapshot Creation
```go
// Source: XE Currency Data API v1 (xecdapi.xe.com/docs/v1/)
// GET https://xecdapi.xe.com/v1/convert_from.json/?from=USD&to=BDT&amount=1
func (f *FXService) FetchUSDToBDT(ctx context.Context) (midRate string, source string, err error) {
    ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()

    req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
        "https://xecdapi.xe.com/v1/convert_from.json/?from=USD&to=BDT&amount=1", nil)
    req.SetBasicAuth(f.accountID, f.apiKey)

    resp, err := f.httpClient.Do(req)
    if err != nil {
        // fallback to Redis cache
        return f.fetchCachedRate(ctx)
    }
    // parse response: to[0].mid field
    // cache result in Redis with 5-minute TTL
    // return rate, "xe", nil
}
```

### Tax Calculation at Checkout
```go
// Source: research findings — Bangladesh VAT rules 2025
type TaxResult struct {
    TaxRate        string // "0.15", "0.05", or "0.00"
    TaxTreatment   string // "bd_vat_15", "bd_vat_5_ites", "bd_reverse_charge", "no_tax"
    TaxIncluded    bool   // true = VAT already baked into displayed price
    ReverseCharge  bool   // true = buyer is liable (B2B with BIN)
}

func CalculateTax(profile profiles.BillingProfile, credits int64) TaxResult {
    if profile.CountryCode != "BD" {
        return TaxResult{TaxRate: "0.00", TaxTreatment: "no_tax"}
    }
    isBusinessWithBIN := profile.VATNumber != "" && 
        (profile.LegalEntityType == "private_company" || profile.LegalEntityType == "public_company")
    if isBusinessWithBIN {
        return TaxResult{TaxRate: "0.00", TaxTreatment: "bd_reverse_charge", ReverseCharge: true}
    }
    // Standard 15% for individuals and unregistered businesses
    return TaxResult{TaxRate: "0.15", TaxTreatment: "bd_vat_15", TaxIncluded: true}
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Stripe.js client-side PaymentIntent confirmation | Server-side PaymentIntent + provider-hosted redirect (Checkout Session or payment link) | 2022+ | Reduces PCI scope; no card data touches Hive servers |
| Floating-point currency math | Integer micro-unit arithmetic | Industry standard | Eliminates rounding errors in FX and tax computation |
| Per-event webhook tables | Idempotency-key dedup in existing financial tables | Project pattern from Phase 3 | Leverages existing ACID-enforced ledger for dedup |
| Polling for payment status | Webhook-first + fallback poll | Industry standard | Lower latency confirmation; polling as safety net only |
| Global webhook secret | Per-endpoint webhook signing secrets (Stripe) | Stripe best practice | One compromised endpoint doesn't expose all webhooks |

**Deprecated/outdated:**
- `stripe.ChargeParams` (legacy Charges API): Replaced by PaymentIntents. Do not use.
- SSLCommerz v3 API: v4 is current. v3 endpoints differ in URL structure.
- bKash Checkout v1.1: v1.2.0-beta is the current tokenized checkout version used in production.

---

## Open Questions

1. **XE API tier and rate limits**
   - What we know: XE offers multiple tiers; all use HTTP Basic auth to `xecdapi.xe.com`; `convert_from` endpoint returns mid-rate
   - What's unclear: Exact requests/month and update frequency for the tier Hive will subscribe to; whether the free/starter tier has sufficient limits for production checkout volume
   - Recommendation: Default to 5-minute Redis TTL for the FX cache; this keeps XE calls bounded at ~288/day regardless of checkout volume. If XE is unavailable, serve the cached rate and log the fallback event. Admin override path handles emergencies.

2. **bKash production credential onboarding timeline**
   - What we know: bKash requires merchant onboarding to get `app_key`, `app_secret`, `username`, `password`; sandbox is freely available at `merchantserver.sandbox.bka.sh`
   - What's unclear: Whether Hive has or can quickly obtain production bKash credentials
   - Recommendation: Build against the sandbox environment; use feature flags or config to switch between sandbox and production URLs.

3. **BD rail confirmation delay — exact duration**
   - What we know: User specified "minutes, not days"; both bKash and SSLCommerz can deliver IPN within seconds under normal conditions but with lower SLA reliability than Stripe
   - What's unclear: Whether a 3-minute delay is the right balance between UX friction and reliability
   - Recommendation: Implement 3-minute confirmation delay as a configurable value (env var `BD_RAIL_CONFIRM_DELAY_SECONDS=180`). Run a background goroutine (or Asynq task, already in go.mod) that sweeps `confirming` intents past the delay threshold and posts the grant.

4. **Non-BD international tax treatment**
   - What we know: User deferred this decision; context says "whether to apply tax for non-BD customers at this stage"
   - What's unclear: Whether to silently apply zero tax to all non-BD customers, or to implement a stub that makes extension easy
   - Recommendation: For v1, apply zero tax to all non-BD customers and store `tax_treatment: "no_tax"` in payment event metadata. The tax calculation function should accept a country code parameter so Phase 9 (invoicing) can extend it.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` + table-driven tests |
| Config file | none — `go test ./...` from `apps/control-plane/` |
| Quick run command | `go test ./internal/payments/... -race -count=1` |
| Full suite command | `go test ./... -race -count=1` from `apps/control-plane/` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| BILL-03 | Rail detection: BD country → 3 rails; non-BD → Stripe only | unit | `go test ./internal/payments/... -run TestRailDetection -race` | ❌ Wave 0 |
| BILL-03 | Credit tier validation: multiples of 1K, min/max per rail | unit | `go test ./internal/payments/... -run TestCreditTierValidation -race` | ❌ Wave 0 |
| BILL-03 | Stripe initiate returns redirect URL; Stripe webhook posts grant | unit | `go test ./internal/payments/... -run TestStripeRail -race` | ❌ Wave 0 |
| BILL-03 | bKash initiate returns bkashURL; execute posts grant after delay | unit | `go test ./internal/payments/... -run TestBKashRail -race` | ❌ Wave 0 |
| BILL-03 | SSLCommerz initiate returns GatewayPageURL; IPN posts grant | unit | `go test ./internal/payments/... -run TestSSLCommerzRail -race` | ❌ Wave 0 |
| BILL-03 | Duplicate webhook does not double-post credits | unit | `go test ./internal/payments/... -run TestWebhookIdempotency -race` | ❌ Wave 0 |
| BILL-04 | FX snapshot created with mid_rate, 5% fee, effective_rate | unit | `go test ./internal/payments/... -run TestFXSnapshot -race` | ❌ Wave 0 |
| BILL-04 | XE API unavailable → Redis cache fallback; cache miss → error | unit | `go test ./internal/payments/... -run TestFXFallback -race` | ❌ Wave 0 |
| BILL-04 | BDT amount = USD × effective_rate (integer math, no float) | unit | `go test ./internal/payments/... -run TestBDTAmountCalculation -race` | ❌ Wave 0 |
| BILL-07 | BD individual → 15% VAT applied | unit | `go test ./internal/payments/... -run TestTaxCalculation -race` | ❌ Wave 0 |
| BILL-07 | BD company with BIN → reverse-charge, zero VAT | unit | `go test ./internal/payments/... -run TestTaxCalculation -race` | ❌ Wave 0 |
| BILL-07 | Non-BD → no tax | unit | `go test ./internal/payments/... -run TestTaxCalculation -race` | ❌ Wave 0 |
| BILL-07 | First purchase gates on complete billing profile | unit | `go test ./internal/payments/... -run TestBillingProfileGate -race` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/payments/... -race -count=1`
- **Per wave merge:** `go test ./... -race -count=1` from `apps/control-plane/`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `apps/control-plane/internal/payments/service_test.go` — service unit tests with stub repo + stub rail
- [ ] `apps/control-plane/internal/payments/fx_test.go` — FX service tests with stub HTTP client
- [ ] `apps/control-plane/internal/payments/http_test.go` — handler tests for checkout initiation + webhook endpoints
- [ ] `apps/control-plane/internal/payments/stripe/rail_test.go` — Stripe rail tests with mock Stripe client
- [ ] `apps/control-plane/internal/payments/bkash/rail_test.go` — bKash rail tests with stub HTTP transport
- [ ] `apps/control-plane/internal/payments/sslcommerz/rail_test.go` — SSLCommerz rail tests with stub HTTP transport
- [ ] `supabase/migrations/20260410_01_payment_intents.sql` — payment_intents + payment_events schema
- [ ] `supabase/migrations/20260410_02_fx_snapshots.sql` — fx_snapshots schema

---

## Sources

### Primary (HIGH confidence)
- `proxy.golang.org/github.com/stripe/stripe-go/v84/@latest` — confirmed v84.4.1, published 2026-03-06
- `pkg.go.dev/github.com/stripe/stripe-go/v84` — PaymentIntent API, webhook.ConstructEvent signature
- `xecdapi.xe.com/docs/v1/` — XE Currency Data API authentication and convert_from endpoint
- `xecdapi.xe.com/docs/v1/authentication` — HTTP Basic auth with account_id:api_key
- Project codebase: `internal/ledger/`, `internal/profiles/`, `internal/accounting/` — established Go patterns

### Secondary (MEDIUM confidence)
- `developer.sslcommerz.com/doc/v4/` — SSLCommerz API v4 endpoint structure, required params, IPN format (verified via web search; no Go SDK)
- `developer.bka.sh` (referenced, not directly fetched) — bKash Tokenized Checkout v1.2 flow (grant token → create → execute → callback); corroborated by multiple implementation repositories
- `anrok.com/vat-software-digital-services/bangladesh` — Bangladesh 15% standard VAT on digital services; 5% ITES reduced rate
- `vatcalc.com/bangladesh/bangladesh-vat-on-foreign-digital-services/` — B2B reverse-charge mechanism, foreign company obligations since 2018
- `taxsummaries.pwc.com/bangladesh/corporate/other-taxes` — PwC Bangladesh VAT summary

### Tertiary (LOW confidence — flag for validation)
- bKash transaction limits (BDT 30,000/transaction, BDT 200,000/day) — sourced from community implementations; verify with official bKash merchant documentation during implementation
- SSLCommerz max BDT 500,000/transaction — sourced from community documentation; verify with SSLCommerz merchant agreement
- bKash 5% ITES rate applicability — research suggests standard 15% is safer assumption for digital services from a foreign company; consult legal counsel before going live

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — stripe-go v84.4.1 verified via Go module proxy; stdlib HTTP for BD rails
- Architecture: HIGH — follows established project patterns (ledger idempotency, accounting service interfaces, types/service/repository/http layout)
- Stripe integration: HIGH — official SDK with ConstructEvent; well-documented
- bKash/SSLCommerz integration: MEDIUM — no Go SDK; flow documented via community sources corroborating official API structure
- Bangladesh VAT rules: MEDIUM — multiple sources agree on 15% standard rate + B2B reverse-charge; ITES 5% applicability LOW
- FX math: HIGH — integer micro-unit pattern is industry standard for financial systems
- Rail limits: LOW — community sources only; must verify with provider documentation

**Research date:** 2026-04-10
**Valid until:** 2026-05-10 (stable stack; bKash/SSLCommerz API versions change infrequently; Bangladesh VAT rules valid through June 2025 ITES exemption transition)
