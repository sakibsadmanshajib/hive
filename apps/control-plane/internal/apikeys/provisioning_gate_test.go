package apikeys

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The gate under test: an account with no public.tenant_billing_accounts row
// cannot mint a secret. Issue #1330 -- the console happily issued a key for
// such an account, rendered it in the copy-it-now panel, and edge-api then
// refused every request made with it (403 account_not_provisioned) with a
// message naming no cause and offering no remedy.

func TestCreateKeyRefusesAccountWithNoBillingTenant(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	repo.unmappedAccounts[accountID] = true

	_, err := svc.CreateKey(context.Background(), accountID, uuid.New(), CreateKeyInput{
		Nickname: "dead-on-arrival",
	})
	if !errors.Is(err, ErrAccountNotProvisioned) {
		t.Fatalf("expected ErrAccountNotProvisioned, got %v", err)
	}
	if len(repo.keys) != 0 {
		t.Fatalf("expected no key row to be written, got %d", len(repo.keys))
	}
	if len(repo.events) != 0 {
		t.Fatalf("expected no key event to be written, got %d", len(repo.events))
	}
}

func TestCreateKeyAllowsProvisionedAccount(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	repo.tenantByAccount[accountID] = uuid.New()

	result, err := svc.CreateKey(context.Background(), accountID, uuid.New(), CreateKeyInput{
		Nickname: "usable",
	})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if result.Secret == "" {
		t.Fatal("expected a secret for a provisioned account")
	}
}

// Rotation mints a fresh secret too, so it carries the same trap: a customer
// whose key is being refused clicks Rotate and receives a second key that is
// refused identically.
func TestRotateKeyRefusesAccountWithNoBillingTenant(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	accountID := uuid.New()
	actorID := uuid.New()
	created, err := svc.CreateKey(context.Background(), accountID, actorID, CreateKeyInput{Nickname: "first"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	// In production this is a key that predates the gate, sitting on an
	// account that was never mapped at all.
	repo.unmappedAccounts[accountID] = true

	_, err = svc.RotateKey(context.Background(), accountID, actorID, created.Key.ID, "second", nil)
	if !errors.Is(err, ErrAccountNotProvisioned) {
		t.Fatalf("expected ErrAccountNotProvisioned, got %v", err)
	}
	if got := repo.keys[created.Key.ID].Status; got != KeyStatusActive {
		t.Fatalf("expected the source key untouched by a refused rotation, got status %q", got)
	}
	if len(repo.keys) != 1 {
		t.Fatalf("expected no replacement key row, got %d keys", len(repo.keys))
	}
}

// The refusal has to reach the customer as something they can act on. An
// opaque 500 here would be the same failure the gateway already produced,
// just moved earlier.
func TestCreateKeyHTTPRefusalIsActionable(t *testing.T) {
	vc := ownerVC()
	h, repo := newTestHandler(vc)
	repo.unmappedAccounts[vc.CurrentAccount.ID] = true

	rr := doRequest(t, h, http.MethodPost, "/api/v1/accounts/current/api-keys", map[string]string{
		"nickname": "dead-on-arrival",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}

	body := decodeBody(t, rr)
	msg, _ := body["error"].(string)
	// Names what is missing, and echoes the gateway's own error code so a
	// customer's report and an operator's log line join up.
	for _, want := range []string{"billing", "account_not_provisioned"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("expected refusal message to mention %q, got %q", want, msg)
		}
	}
	// The console proxy never forwards upstream error text to the browser, so
	// the code is the only part of this response that can reach a customer as
	// anything more specific than "Conflict".
	if code, _ := body["code"].(string); code != "account_not_provisioned" {
		t.Fatalf("expected code account_not_provisioned, got %q", code)
	}
	if _, leaked := body["secret"]; leaked {
		t.Fatal("refused creation must not return a secret")
	}
}

func TestRotateKeyHTTPRefusalIsActionable(t *testing.T) {
	vc := ownerVC()
	h, repo := newTestHandler(vc)

	created, err := h.svc.CreateKey(context.Background(), vc.CurrentAccount.ID, vc.User.ID, CreateKeyInput{Nickname: "first"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	repo.unmappedAccounts[vc.CurrentAccount.ID] = true

	rr := doRequest(t, h, http.MethodPost,
		"/api/v1/accounts/current/api-keys/"+created.Key.ID.String()+"/rotate",
		map[string]string{"nickname": "second"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "billing") {
		t.Fatalf("expected refusal message to mention billing, got %q", msg)
	}
}
