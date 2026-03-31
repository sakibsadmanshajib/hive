package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

func viewerCtx(viewer auth.Viewer) context.Context {
	return auth.WithViewer(context.Background(), viewer)
}

func newHTTPHandler(repo *stubRepo) http.Handler {
	usageSvc := NewService(repo)
	accountsSvc := accounts.NewService(repo)
	return NewHandler(usageSvc, accountsSvc)
}

func TestListUsageEventsUsesCurrentAccount(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountOneID := uuid.New()
	accountTwoID := uuid.New()

	repo.accountsMap[accountOneID] = &accounts.Account{
		ID:          accountOneID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.accountsMap[accountTwoID] = &accounts.Account{
		ID:          accountTwoID,
		Slug:        "workspace-two",
		DisplayName: "Workspace Two",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountOneID, UserID: userID, Role: "owner", Status: "active"},
		{ID: uuid.New(), AccountID: accountTwoID, UserID: userID, Role: "owner", Status: "active"},
	}
	repo.events[accountOneID] = []UsageEvent{{
		ID:                uuid.New(),
		AccountID:         accountOneID,
		RequestAttemptID:  uuid.New(),
		RequestID:         "req_one",
		EventType:         UsageEventAccepted,
		Endpoint:          "/v1/responses",
		ModelAlias:        "hive-fast",
		Status:            "accepted",
		HiveCreditDelta:   10,
		ProviderRequestID: "provider-one",
		InternalMetadata:  map[string]any{"safe": "value"},
	}}
	repo.events[accountTwoID] = []UsageEvent{{
		ID:                uuid.New(),
		AccountID:         accountTwoID,
		RequestAttemptID:  uuid.New(),
		RequestID:         "req_two",
		EventType:         UsageEventCompleted,
		Endpoint:          "/v1/chat/completions",
		ModelAlias:        "hive-pro",
		Status:            "completed",
		OutputTokens:      42,
		HiveCreditDelta:   -35,
		ProviderRequestID: "provider-two",
		InternalMetadata:  map[string]any{"debug": "secret"},
	}}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events", nil)
	req.Header.Set("X-Hive-Account-ID", accountTwoID.String())
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastEventsFilter.AccountID != accountTwoID {
		t.Fatalf("expected account filter %s, got %s", accountTwoID, repo.lastEventsFilter.AccountID)
	}

	var response map[string][]map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	events := response["events"]
	if len(events) != 1 {
		t.Fatalf("expected 1 usage event, got %d", len(events))
	}
	if events[0]["request_id"] != "req_two" {
		t.Fatalf("expected current-account event req_two, got %#v", events[0]["request_id"])
	}
	if _, ok := events[0]["provider_request_id"]; ok {
		t.Fatal("expected provider_request_id to be omitted from the response")
	}
	if _, ok := events[0]["internal_metadata"]; ok {
		t.Fatal("expected internal_metadata to be omitted from the response")
	}
}

func TestListRequestAttemptsDefaultsLimit(t *testing.T) {
	repo := newStubRepo()
	userID := uuid.New()
	accountID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	for i := 0; i < 25; i++ {
		repo.attempts[accountID] = append(repo.attempts[accountID], RequestAttempt{
			ID:            uuid.New(),
			AccountID:     accountID,
			RequestID:     uuid.NewString(),
			AttemptNumber: i + 1,
			Endpoint:      "/v1/responses",
			ModelAlias:    "hive-fast",
			Status:        AttemptStatusAccepted,
		})
	}

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/request-attempts", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastAttemptsLimit != 20 {
		t.Fatalf("expected default attempt limit 20, got %d", repo.lastAttemptsLimit)
	}

	var response struct {
		Attempts []RequestAttempt `json:"attempts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if len(response.Attempts) != 20 {
		t.Fatalf("expected 20 request attempts, got %d", len(response.Attempts))
	}
}

func TestListEventsOmitsCacheFieldsWhenZero(t *testing.T) {
	repo := newStubRepo()
	viewer, accountID := seedUsageHTTPAccount(repo)
	repo.events[accountID] = []UsageEvent{{
		ID:               uuid.New(),
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		RequestID:        "req_zero_cache",
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		Status:           "completed",
		InputTokens:      12,
		OutputTokens:     7,
		HiveCreditDelta:  -4,
		CustomerTags:     map[string]any{"tenant": "acme"},
		CreatedAt:        time.Now().UTC(),
	}}

	handler := newHTTPHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string][]map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if len(response["events"]) != 1 {
		t.Fatalf("expected 1 usage event, got %d", len(response["events"]))
	}

	event := response["events"][0]
	if _, ok := event["cache_read_tokens"]; ok {
		t.Fatal("expected cache_read_tokens to be omitted when zero")
	}
	if _, ok := event["cache_write_tokens"]; ok {
		t.Fatal("expected cache_write_tokens to be omitted when zero")
	}
	if event["request_id"] != "req_zero_cache" {
		t.Fatalf("expected request_id req_zero_cache, got %#v", event["request_id"])
	}
}

func TestListEventsIncludesCacheFieldsWhenPresent(t *testing.T) {
	repo := newStubRepo()
	viewer, accountID := seedUsageHTTPAccount(repo)
	repo.events[accountID] = []UsageEvent{{
		ID:               uuid.New(),
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		RequestID:        "req_cached",
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		Status:           "completed",
		InputTokens:      42,
		OutputTokens:     19,
		CacheReadTokens:  11,
		CacheWriteTokens: 23,
		HiveCreditDelta:  -8,
		CustomerTags:     map[string]any{"tenant": "acme"},
		CreatedAt:        time.Now().UTC(),
	}}

	handler := newHTTPHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var response map[string][]map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if len(response["events"]) != 1 {
		t.Fatalf("expected 1 usage event, got %d", len(response["events"]))
	}

	event := response["events"][0]
	if got := event["cache_read_tokens"]; got != float64(11) {
		t.Fatalf("expected cache_read_tokens 11, got %#v", got)
	}
	if got := event["cache_write_tokens"]; got != float64(23) {
		t.Fatalf("expected cache_write_tokens 23, got %#v", got)
	}
	if event["model_alias"] != "hive-fast" {
		t.Fatalf("expected model_alias hive-fast, got %#v", event["model_alias"])
	}
}

func seedUsageHTTPAccount(repo *stubRepo) (auth.Viewer, uuid.UUID) {
	userID := uuid.New()
	accountID := uuid.New()

	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "workspace-one",
		DisplayName: "Workspace One",
		AccountType: "business",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	return auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}, accountID
}
