package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Issue #1063. The chat user's balance is a tenant balance reached through
// tenant_billing_accounts; the signed-in chat principal only ever identifies
// itself by the email its Open WebUI session carries, and the browser never
// learns the tenant->account link.

func TestGetChatBalanceResolvesTenantBillingAccount(t *testing.T) {
	repo := newStubRepo()
	email := "member@test.local"

	accountID := uuid.New()
	repo.emailAccounts[email] = accountID
	repo.entries[accountID] = []LedgerEntry{
		{EntryType: EntryTypeGrant, CreditsDelta: 500000},
		{EntryType: EntryTypeUsageCharge, CreditsDelta: -120000},
	}
	repo.usageSince = 42000

	svc := NewService(repo)
	balance, found, err := svc.GetChatBalance(context.Background(), " Member@Test.Local ")
	if err != nil {
		t.Fatalf("GetChatBalance: %v", err)
	}
	if !found {
		t.Fatal("expected membership to resolve")
	}

	want := ChatBalance{
		PostedCredits:    380000,
		AvailableCredits: 380000,
		UsageToday:       42000,
	}
	if balance != want { //nolint:gocritic // comparable struct, field-by-field noise otherwise
		t.Fatalf("balance mismatch:\n got %+v\nwant %+v", balance, want)
	}
}

func TestGetChatBalanceUnknownEmailIsNotFoundNotError(t *testing.T) {
	repo := newStubRepo()
	repo.emailAccounts["known@test.local"] = uuid.New()

	svc := NewService(repo)
	_, found, err := svc.GetChatBalance(context.Background(), "stranger@test.local")
	if err != nil {
		t.Fatalf("unknown email must not error: %v", err)
	}
	if found {
		t.Fatal("unmapped email must report not-found")
	}
}

func TestChatBalanceHandler(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.emailAccounts["member@test.local"] = accountID
	repo.entries[accountID] = []LedgerEntry{
		{EntryType: EntryTypeGrant, CreditsDelta: 1000000},
		{EntryType: EntryTypeUsageCharge, CreditsDelta: -250000},
	}

	svc := NewService(repo)
	mux := http.NewServeMux()
	RegisterChatBalanceRoute(mux, svc, func(h http.Handler) http.Handler { return h })

	do := func(method string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/internal/chat/credits/balance", strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	t.Run("post with known email returns trimmed balance", func(t *testing.T) {
		rec := do(http.MethodPost, `{"email":"Member@Test.Local"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, key := range []string{"posted_credits", "reserved_credits", "available_credits", "usage_today_credits"} {
			if _, ok := got[key]; !ok {
				t.Fatalf("missing %s in %s", key, rec.Body.String())
			}
		}
		if got["available_credits"].(float64) != 750000 {
			t.Fatalf("available_credits = %v", got["available_credits"])
		}
	})

	t.Run("non-post method rejected", func(t *testing.T) {
		if rec := do(http.MethodGet, ""); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status %d", rec.Code)
		}
	})

	t.Run("empty email rejected", func(t *testing.T) {
		for _, body := range []string{`{"email":""}`, `{}`, `not json`} {
			if rec := do(http.MethodPost, body); rec.Code != http.StatusBadRequest {
				t.Fatalf("body %q -> status %d, want 400", body, rec.Code)
			}
		}
	})

	// Issue #1599. A principal whose tenant has no billing account holds no
	// credit, which is a zero balance, not a missing resource. The 404 this
	// used to answer rendered no banner at all, so a workspace that could not
	// chat looked exactly like one that merely had nothing to show, and the
	// only surface naming the problem was edge-api's refusal on the next
	// message.
	t.Run("unbilled email reads as a zero balance without leaking identity state", func(t *testing.T) {
		rec := do(http.MethodPost, `{"email":"stranger@test.local"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, key := range []string{"posted_credits", "reserved_credits", "available_credits", "usage_today_credits"} {
			value, ok := got[key]
			if !ok {
				t.Fatalf("missing %s in %s", key, rec.Body.String())
			}
			if value.(float64) != 0 {
				t.Fatalf("%s = %v, want 0", key, value)
			}
		}
		if strings.Contains(strings.ToLower(rec.Body.String()), "stranger") {
			t.Fatalf("response echoes the email: %s", rec.Body.String())
		}
	})

	// The answer for an unbilled principal must be byte-identical to a real
	// zero balance: anything that distinguishes the two turns this route into
	// an oracle for whether an email is billed at all.
	t.Run("unbilled and genuinely-zero answers are indistinguishable", func(t *testing.T) {
		zeroAccount := uuid.New()
		repo.emailAccounts["broke@test.local"] = zeroAccount
		repo.entries[zeroAccount] = []LedgerEntry{
			{EntryType: EntryTypeGrant, CreditsDelta: 1000},
			{EntryType: EntryTypeUsageCharge, CreditsDelta: -1000},
		}

		unbilled := do(http.MethodPost, `{"email":"stranger@test.local"}`)
		zero := do(http.MethodPost, `{"email":"broke@test.local"}`)
		if unbilled.Code != zero.Code || unbilled.Body.String() != zero.Body.String() {
			t.Fatalf("unbilled %d %s differs from zero %d %s",
				unbilled.Code, unbilled.Body.String(), zero.Code, zero.Body.String())
		}
	})

	t.Run("oversized body rejected", func(t *testing.T) {
		rec := do(http.MethodPost, `{"email":"`+strings.Repeat("a", 4096)+`@test.local"}`)
		if rec.Code == http.StatusOK {
			t.Fatalf("oversized body accepted: %d", rec.Code)
		}
	})
}

func TestChatBalanceRouteIsWrappedByGate(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	repo.emailAccounts["member@test.local"] = accountID

	wrapped := false
	mux := http.NewServeMux()
	RegisterChatBalanceRoute(mux, NewService(repo), func(h http.Handler) http.Handler {
		wrapped = true
		return h
	})

	req := httptest.NewRequest(http.MethodPost, "/internal/chat/credits/balance",
		bytes.NewReader([]byte(`{"email":"member@test.local"}`)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if !wrapped {
		t.Fatal("gate middleware never invoked")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}
