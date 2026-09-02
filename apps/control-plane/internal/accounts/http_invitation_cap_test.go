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
	// The words were never the leak. A wait time derived from the window that
	// refused identifies the dimension on its own: anything at or under five
	// minutes could only be the per-address cooldown, which would report that
	// somebody in another workspace invited that address moments ago.
	inviterRefusal := refuseWith(t, accounts.InvitationLimits{
		Inviter: limit(newMemIncrementer(), 1, time.Hour, "user"),
	})
	if got, want := inviterRefusal.Header().Get("Retry-After"), rr.Header().Get("Retry-After"); got != want {
		t.Errorf("Retry-After is %q for the inviter cap and %q for the recipient cap, so the value names the dimension", got, want)
	}
	var inviterBody map[string]string
	_ = json.Unmarshal(inviterRefusal.Body.Bytes(), &inviterBody)
	if inviterBody["error"] != body["error"] {
		t.Errorf("the message differs by dimension: %q vs %q", inviterBody["error"], body["error"])
	}
}

// refuseWith invites twice against limits that admit one, and returns the
// refused second response.
func refuseWith(t *testing.T, limits accounts.InvitationLimits) *httptest.ResponseRecorder {
	t.Helper()
	repo, accountID, viewer := inviteFixture(t)
	h := accounts.NewHandler(accounts.NewService(repo).WithInvitationLimits(limits))
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/invitations",
			strings.NewReader(`{"email":"invitee@example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hive-Account-ID", accountID.String())
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req.WithContext(auth.WithViewer(context.Background(), viewer)))
		return rr
	}
	if first := send(); first.Code != http.StatusCreated {
		t.Fatalf("first invitation: got %d, want 201 (%s)", first.Code, first.Body.String())
	}
	rr := send()
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429 (%s)", rr.Code, rr.Body.String())
	}
	return rr
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
