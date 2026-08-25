package budgets_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
)

// Notifier behavior: log fallbacks never fail, the composite dispatcher
// delivers a webhook carrying an HMAC-SHA256 signature over the raw body
// bytes, and a failing webhook is REPORTED as an error so the cron leaves the
// alert unstamped and retries on the next pass.
//
// Invariant: webhook failure must be reported, not swallowed; the cron keys
// its stamping decision off this error return (fail-closed dispatch).

func spendAlertFixture(url string) budgets.SpendAlert {
	email := "ops@example.com"
	secret := "s3cret"
	u := url
	return budgets.SpendAlert{
		ID:            uuid.New(),
		WorkspaceID:   uuid.New(),
		ThresholdPct:  80,
		Email:         &email,
		WebhookURL:    &u,
		WebhookSecret: &secret,
	}
}

func TestLogNotifiersNeverFail(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := spendAlertFixture("unused")
	if err := budgets.NewLogNotifier(logger).SendBudgetAlert(context.Background(), uuid.New(), budgets.BudgetThreshold{ThresholdCredits: 100}, 50); err != nil {
		t.Fatalf("log email fallback errored: %v", err)
	}
	if err := budgets.NewLogAlertNotifier(nil).NotifySpendAlert(context.Background(), a, a.WorkspaceID, big.NewInt(500), big.NewInt(1000)); err != nil {
		t.Fatalf("log alert fallback errored: %v", err)
	}
}

func TestCompositeNotifierWebhook(t *testing.T) {
	t.Run("success carries verifiable HMAC signature", func(t *testing.T) {
		var gotSig string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			mac := hmac.New(sha256.New, []byte("s3cret"))
			mac.Write(body)
			gotSig = r.Header.Get("X-Hive-Signature")
			if gotSig != hex.EncodeToString(mac.Sum(nil)) {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(200)
		}))
		defer server.Close()

		a := spendAlertFixture(server.URL)
		cn := budgets.NewCompositeNotifier(nil, nil)
		if err := cn.NotifySpendAlert(context.Background(), a, a.WorkspaceID, big.NewInt(500), big.NewInt(1000)); err != nil {
			t.Fatalf("webhook delivery failed: %v", err)
		}
	})

	t.Run("non-2xx is retried then reported as error", func(t *testing.T) {
		var hits int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.WriteHeader(500)
		}))
		defer server.Close()

		a := spendAlertFixture(server.URL)
		a.Email = nil // isolate the webhook channel
		cn := budgets.NewCompositeNotifier(nil, nil)

		err := cn.NotifySpendAlert(context.Background(), a, a.WorkspaceID, big.NewInt(500), big.NewInt(1000))
		if err == nil {
			t.Fatal("persistent 500s must surface as an error so the cron does not stamp")
		}
		if hits < 2 {
			t.Fatalf("expected retries before giving up, server hit %d times", hits)
		}
	})
}
