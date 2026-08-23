package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// stubRoleStore is a minimal platform.RoleStore backing a real
// *platform.RoleService for tests, keyed by userID -> is_platform_admin.
type stubRoleStore struct {
	adminUsers map[uuid.UUID]bool
}

func (s *stubRoleStore) GetMembershipRole(_ context.Context, _, _ uuid.UUID) (platform.MembershipRole, error) {
	return "", nil
}

func (s *stubRoleStore) IsPlatformAdmin(_ context.Context, userID uuid.UUID) (bool, error) {
	return s.adminUsers[userID], nil
}

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

	var response struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	events := response.Events
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

	var response struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("expected 1 usage event, got %d", len(response.Events))
	}

	event := response.Events[0]
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

	var response struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("expected 1 usage event, got %d", len(response.Events))
	}

	event := response.Events[0]
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

func TestListEventsIncludesAPIKeyIDWhenPresent(t *testing.T) {
	repo := newStubRepo()
	viewer, accountID := seedUsageHTTPAccount(repo)
	apiKeyID := uuid.New()
	repo.events[accountID] = []UsageEvent{{
		ID:               uuid.New(),
		AccountID:        accountID,
		RequestAttemptID: uuid.New(),
		APIKeyID:         &apiKeyID,
		RequestID:        "req_attributed",
		EventType:        UsageEventCompleted,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		Status:           "completed",
		HiveCreditDelta:  -3,
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

	var response struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	got := response.Events[0]["api_key_id"]
	if got != apiKeyID.String() {
		t.Fatalf("expected api_key_id %s, got %#v", apiKeyID, got)
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

func TestAnalyticsUsageRejectsUnverifiedViewer(t *testing.T) {
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

	handler := newHTTPHandler(repo)
	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "owner@example.com",
		EmailVerified: false,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/analytics/usage", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandler_UsageAuthzMatrix verifies the Phase 18 permission matrix for
// analytics/usage endpoints: analytics.view requires verified owner or verified member.
func TestHandler_UsageAuthzMatrix(t *testing.T) {
	cases := []struct {
		name       string
		role       string
		verified   bool
		wantStatus int
	}{
		{"owner verified", "owner", true, http.StatusOK},
		{"owner unverified", "owner", false, http.StatusForbidden},
		{"member verified", "member", true, http.StatusOK},
		{"member unverified", "member", false, http.StatusForbidden},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			userID := uuid.New()
			accountID := uuid.New()

			repo.accountsMap[accountID] = &accounts.Account{
				ID:          accountID,
				Slug:        "ws",
				DisplayName: "WS",
				AccountType: "personal",
				OwnerUserID: userID,
			}
			repo.memberships = []accounts.Membership{
				{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: tc.role, Status: "active"},
			}

			handler := newHTTPHandler(repo)
			viewer := auth.Viewer{UserID: userID, Email: "u@example.com", EmailVerified: tc.verified}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events", nil)
			req = req.WithContext(viewerCtx(viewer))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("want %d got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestListUsageEvents_PlatformAdminOverlayGrantsUnverifiedAccess is a
// regression guard for issue #424: resolveCurrentAccountID hardcoded
// isAdmin=false when building the Actor, so a real platform admin who is not
// account-verified was silently denied usage analytics access even though the
// admin overlay should grant it. A hardcoded-false version returns 403 here;
// the fix must return 200.
func TestListUsageEvents_PlatformAdminOverlayGrantsUnverifiedAccess(t *testing.T) {
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
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "member", Status: "active"},
	}

	roleSvc := platform.NewRoleService(&stubRoleStore{adminUsers: map[uuid.UUID]bool{userID: true}})
	usageSvc := NewService(repo)
	accountsSvc := accounts.NewService(repo)
	handler := NewHandler(usageSvc, accountsSvc).WithRoleService(roleSvc)

	viewer := auth.Viewer{UserID: userID, Email: "admin@example.com", EmailVerified: false}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for platform admin overlay, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUsageEventsRequireAuthenticatedViewer(t *testing.T) {
	repo := newStubRepo()

	handler := newHTTPHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a viewer, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUsageEventsRejectMalformedFilters(t *testing.T) {
	cases := []struct {
		name   string
		query  string
		expect string
	}{
		{name: "bad api_key_id", query: "api_key_id=not-a-uuid", expect: "api_key_id must be a valid UUID"},
		{name: "bad errors", query: "errors=yes", expect: "errors must be true or false"},
		{name: "bad window", query: "window=2h", expect: "window must be one of: 1h, 24h, 7d, 30d"},
		{name: "bad from", query: "from=yesterday", expect: "from must be ISO8601 (RFC3339)"},
		{name: "bad to", query: "to=123", expect: "to must be ISO8601 (RFC3339)"},
		{name: "bad cursor", query: "cursor=nope", expect: "cursor must be a valid UUID"},
		{name: "window plus explicit bound", query: "window=24h&from=2026-08-01T00%3A00%3A00Z", expect: "window cannot be combined with from/to"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			viewer, _ := seedUsageHTTPAccount(repo)

			handler := newHTTPHandler(repo)
			req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events?"+tc.query, nil)
			req = req.WithContext(viewerCtx(viewer))
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid response JSON: %v", err)
			}
			if body["error"] != tc.expect {
				t.Fatalf("expected error %q, got %q", tc.expect, body["error"])
			}
		})
	}
}

// seedEvents gives the account n completed events with strictly increasing
// created_at stamps so the stub's newest-first ordering is deterministic.
func seedEvents(repo *stubRepo, accountID uuid.UUID, n int) {
	base := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		repo.events[accountID] = append(repo.events[accountID], UsageEvent{
			ID:               uuid.New(),
			AccountID:        accountID,
			RequestAttemptID: uuid.New(),
			RequestID:        "req_" + strconv.Itoa(i),
			EventType:        UsageEventCompleted,
			Endpoint:         "/v1/chat/completions",
			ModelAlias:       "hive-fast",
			Status:           "completed",
			OutputTokens:     int64(i + 1),
			CreatedAt:        base.Add(time.Duration(i) * time.Minute),
		})
	}
}

func TestUsageEventsCursorPagination(t *testing.T) {
	repo := newStubRepo()
	viewer, accountID := seedUsageHTTPAccount(repo)
	seedEvents(repo, accountID, 25)

	handler := newHTTPHandler(repo)

	type page struct {
		requestIDs []string
		nextCursor string
	}
	fetchPage := func(t *testing.T, query string) page {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events"+query, nil)
		req = req.WithContext(viewerCtx(viewer))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for %q, got %d: %s", query, rr.Code, rr.Body.String())
		}
		var body struct {
			Events     []map[string]any `json:"events"`
			NextCursor string           `json:"next_cursor"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("invalid response JSON: %v", err)
		}
		p := page{nextCursor: body.NextCursor}
		for _, event := range body.Events {
			if id, ok := event["request_id"].(string); ok {
				p.requestIDs = append(p.requestIDs, id)
			}
		}
		return p
	}

	first := fetchPage(t, "?limit=10")
	if len(first.requestIDs) != 10 {
		t.Fatalf("expected 10 events on page one, got %d", len(first.requestIDs))
	}
	if first.nextCursor == "" {
		t.Fatal("expected a next cursor on a full first page")
	}

	second := fetchPage(t, "?limit=10&cursor="+first.nextCursor)
	if len(second.requestIDs) != 10 {
		t.Fatalf("expected 10 events on page two, got %d", len(second.requestIDs))
	}
	for _, id := range first.requestIDs {
		for _, seen := range second.requestIDs {
			if id == seen {
				t.Fatalf("page two repeats page-one event %s", id)
			}
		}
	}
	if second.nextCursor == "" {
		t.Fatal("expected a next cursor on a full second page")
	}

	third := fetchPage(t, "?limit=10&cursor="+second.nextCursor)
	if len(third.requestIDs) != 5 {
		t.Fatalf("expected the 5 remaining events on page three, got %d", len(third.requestIDs))
	}
	if third.nextCursor != "" {
		t.Fatalf("expected no next cursor on a short final page, got %q", third.nextCursor)
	}
}

// TestUsageEventsFiltersReachRepository pins each filter param onto the
// ListEventsFilter the repository receives: model_alias and status verbatim,
// api_key_id parsed, errors=true as ErrorsOnly, window as a From bound.
func TestUsageEventsFiltersReachRepository(t *testing.T) {
	repo := newStubRepo()
	viewer, _ := seedUsageHTTPAccount(repo)
	keyID := uuid.New()

	handler := newHTTPHandler(repo)
	query := "model_alias=hive-fast&status=completed&api_key_id=" + keyID.String() + "&errors=true&window=24h"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events?"+query, nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	filter := repo.lastEventsFilter
	if filter.ModelAlias != "hive-fast" {
		t.Fatalf("expected model_alias hive-fast, got %q", filter.ModelAlias)
	}
	if filter.Status != "completed" {
		t.Fatalf("expected status completed, got %q", filter.Status)
	}
	if filter.APIKeyID == nil || *filter.APIKeyID != keyID {
		t.Fatalf("expected api_key_id %s, got %v", keyID, filter.APIKeyID)
	}
	if !filter.ErrorsOnly {
		t.Fatal("expected errors=true to set ErrorsOnly")
	}
	if filter.From.IsZero() || filter.To.IsZero() {
		t.Fatal("expected window=24h to populate from/to")
	}
	if !filter.From.Before(filter.To) {
		t.Fatalf("expected from before to, got from=%v to=%v", filter.From, filter.To)
	}
}

func TestUsageEventsLimitCappedAt100(t *testing.T) {
	repo := newStubRepo()
	viewer, _ := seedUsageHTTPAccount(repo)

	handler := newHTTPHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events?limit=5000", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.lastEventsFilter.Limit != 100 {
		t.Fatalf("expected limit clamped to 100, got %d", repo.lastEventsFilter.Limit)
	}
}

func TestUsageEventsModelAliasFilterNarrowsResults(t *testing.T) {
	repo := newStubRepo()
	viewer, accountID := seedUsageHTTPAccount(repo)
	seedEvents(repo, accountID, 3)
	repo.events[accountID][0].ModelAlias = "hive-pro"

	handler := newHTTPHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/usage-events?model_alias=hive-pro", nil)
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}
	if len(body.Events) != 1 {
		t.Fatalf("expected only the hive-pro event, got %d events", len(body.Events))
	}
	if body.Events[0]["model_alias"] != "hive-pro" {
		t.Fatalf("expected the hive-pro event, got %#v", body.Events[0]["model_alias"])
	}
}
