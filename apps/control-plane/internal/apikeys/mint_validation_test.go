package apikeys

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

const keysBase = "/api/v1/accounts/current/api-keys"

// Issue #1400: the nickname was unbounded end to end, so one 5000-character
// value pushed every other column of the console key table out of reach for
// every key in the workspace, and no product surface could shorten it
// afterwards. The bound belongs on the server because the form is not the
// only caller.
func TestCreateKeyRejectsNicknameOverTheBound(t *testing.T) {
	h, _ := newTestHandler(ownerVC())

	rr := doRequest(t, h, http.MethodPost, keysBase, map[string]string{
		"nickname": strings.Repeat("A", MaxNicknameLen+1),
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an over-long nickname, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "nickname") {
		t.Fatalf("error must name the field it rejected, got %q", msg)
	}
}

func TestCreateKeyAcceptsNicknameExactlyAtTheBound(t *testing.T) {
	h, _ := newTestHandler(ownerVC())

	rr := doRequest(t, h, http.MethodPost, keysBase, map[string]string{
		"nickname": strings.Repeat("A", MaxNicknameLen),
	})

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 at the bound, got %d: %s", rr.Code, rr.Body.String())
	}
}

// The bound counts characters, not bytes, so a name in Bangla or any other
// non-Latin script gets the same allowance as an English one.
func TestCreateKeyCountsNicknameInRunesNotBytes(t *testing.T) {
	h, _ := newTestHandler(ownerVC())

	rr := doRequest(t, h, http.MethodPost, keysBase, map[string]string{
		"nickname": strings.Repeat("চ", MaxNicknameLen),
	})

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for %d multi-byte runes, got %d: %s", MaxNicknameLen, rr.Code, rr.Body.String())
	}
}

func TestCreateKeyRejectsWhitespaceOnlyNickname(t *testing.T) {
	h, _ := newTestHandler(ownerVC())

	rr := doRequest(t, h, http.MethodPost, keysBase, map[string]string{"nickname": "   "})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a blank nickname, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRotateKeyRejectsNicknameOverTheBound(t *testing.T) {
	h, _ := newTestHandler(ownerVC())

	created := doRequest(t, h, http.MethodPost, keysBase, map[string]string{"nickname": "rotate-me"})
	if created.Code != http.StatusCreated {
		t.Fatalf("setup create: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	id, ok := decodeBody(t, created)["id"].(string)
	if !ok || id == "" {
		t.Fatalf("setup create: response carried no id: %s", created.Body.String())
	}

	rr := doRequest(t, h, http.MethodPost, keysBase+"/"+id+"/rotate", map[string]string{
		"nickname": strings.Repeat("A", MaxNicknameLen+1),
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an over-long rotate nickname, got %d: %s", rr.Code, rr.Body.String())
	}
}

// A key whose expiry is already past is dead the moment it is minted, and the
// console lists it as Expired immediately. Refuse it the same way a negative
// credit limit is refused (issue #1400).
func TestCreateKeyRejectsExpiryInThePast(t *testing.T) {
	h, _ := newTestHandler(ownerVC())

	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	rr := doRequest(t, h, http.MethodPost, keysBase, map[string]string{
		"nickname":   "dead-on-arrival",
		"expires_at": past,
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a past expiry, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "expires_at") {
		t.Fatalf("error must name the field it rejected, got %q", msg)
	}
}

func TestCreateKeyAcceptsExpiryInTheFuture(t *testing.T) {
	h, _ := newTestHandler(ownerVC())

	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	rr := doRequest(t, h, http.MethodPost, keysBase, map[string]string{
		"nickname":   "still-alive",
		"expires_at": future,
	})

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for a future expiry, got %d: %s", rr.Code, rr.Body.String())
	}
}
