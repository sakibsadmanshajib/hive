package accounts_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

func TestInvitationHandler_CapAnswers429WithRetryAfterAndNoDimension(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	backend := newMemIncrementer()
	h := accounts.NewHandler(accounts.NewService(repo).
		WithInvitationLimits(accounts.InvitationLimits{
			// Named "recipient" on purpose: the response must not disclose
			// which dimension refused, because a per-address refusal would
			// otherwise tell the caller that somebody recently invited it.
			RecipientBurst: limit(backend, 1, 5*time.Minute, "recipient"),
		}))
	ctx := auth.WithViewer(context.Background(), viewer)

	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/invitations",
			strings.NewReader(`{"email":"invitee@example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hive-Account-ID", accountID.String())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req.WithContext(ctx))
		return rr
	}

	if rr := send(); rr.Code != http.StatusCreated {
		t.Fatalf("first invitation: got %d, want 201 (%s)", rr.Code, rr.Body.String())
	}

	rr := send()
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second invitation: got %d, want 429 (%s)", rr.Code, rr.Body.String())
	}
	retry, err := strconv.Atoi(rr.Header().Get("Retry-After"))
	if err != nil || retry <= 0 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", rr.Header().Get("Retry-After"))
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "invitation_rate_limited" {
		t.Errorf("code = %q, want invitation_rate_limited", body["code"])
	}
	if body["error"] == "" {
		t.Error("the refusal carries no message, so the console has nothing honest to show")
	}
	for _, leak := range []string{"recipient", "inviter", "tenant", "invitee@example.com"} {
		if strings.Contains(strings.ToLower(body["error"]+body["code"]), leak) {
			t.Errorf("the refusal leaks %q: %s", leak, rr.Body.String())
		}
	}
}

func TestInvitationHandler_CounterOutageAnswers503(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	backend := newMemIncrementer()
	backend.err = errors.New("redis: connection refused")
	h := accounts.NewHandler(accounts.NewService(repo).
		WithInvitationLimits(accounts.InvitationLimits{
			Inviter: limit(backend, 30, time.Hour, "user"),
		}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/invitations",
		strings.NewReader(`{"email":"invitee@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hive-Account-ID", accountID.String())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(auth.WithViewer(context.Background(), viewer)))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 (%s)", rr.Code, rr.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["code"] != "invitation_unavailable" {
		t.Errorf("code = %q, want invitation_unavailable", body["code"])
	}
}
