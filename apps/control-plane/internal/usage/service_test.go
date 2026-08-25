package usage

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
)

type stubRepo struct {
	accountsMap       map[uuid.UUID]*accounts.Account
	memberships       []accounts.Membership
	invitations       map[string]*accounts.Invitation
	attempts          map[uuid.UUID][]RequestAttempt
	events            map[uuid.UUID][]UsageEvent
	lastAttemptInput  StartAttemptInput
	lastRecordedEvent RecordEventInput
	lastAttemptsLimit int
	lastEventsFilter  ListEventsFilter

	lastAnalyticsFilter    AnalyticsFilter
	usageSummaryRows       []UsageSummaryRow
	spendSummaryRows       []SpendSummaryRow
	errorSummaryRows       []ErrorSummaryRow
	lastUpdateAttemptID    uuid.UUID
	lastUpdateStatus       string
	lastUpdateCompletedAt  *time.Time
	updateAttemptStatusErr error
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		accountsMap: make(map[uuid.UUID]*accounts.Account),
		invitations: make(map[string]*accounts.Invitation),
		attempts:    make(map[uuid.UUID][]RequestAttempt),
		events:      make(map[uuid.UUID][]UsageEvent),
	}
}

func (s *stubRepo) CreateAttempt(_ context.Context, input StartAttemptInput) (RequestAttempt, error) {
	s.lastAttemptInput = input

	attempt := RequestAttempt{
		ID:               uuid.New(),
		AccountID:        input.AccountID,
		RequestID:        input.RequestID,
		AttemptNumber:    input.AttemptNumber,
		Endpoint:         input.Endpoint,
		ModelAlias:       input.ModelAlias,
		Status:           input.Status,
		UserID:           input.UserID,
		TeamID:           input.TeamID,
		ServiceAccountID: input.ServiceAccountID,
		APIKeyID:         input.APIKeyID,
		CustomerTags:     input.CustomerTags,
		StartedAt:        time.Now().UTC(),
	}

	s.attempts[input.AccountID] = append(s.attempts[input.AccountID], attempt)
	return attempt, nil
}

func (s *stubRepo) UpdateAttemptStatus(_ context.Context, attemptID uuid.UUID, status string, completedAt *time.Time) error {
	s.lastUpdateAttemptID = attemptID
	s.lastUpdateStatus = status
	s.lastUpdateCompletedAt = completedAt
	if s.updateAttemptStatusErr != nil {
		return s.updateAttemptStatusErr
	}
	for accountID, attempts := range s.attempts {
		for idx, attempt := range attempts {
			if attempt.ID == attemptID {
				attempt.Status = AttemptStatus(status)
				attempt.CompletedAt = completedAt
				s.attempts[accountID][idx] = attempt
				return nil
			}
		}
	}

	return errors.New("attempt not found")
}

func (s *stubRepo) RecordEvent(_ context.Context, input RecordEventInput) (UsageEvent, error) {
	s.lastRecordedEvent = input

	event := UsageEvent{
		ID:                uuid.New(),
		AccountID:         input.AccountID,
		RequestAttemptID:  input.RequestAttemptID,
		RequestID:         input.RequestID,
		EventType:         input.EventType,
		Endpoint:          input.Endpoint,
		ModelAlias:        input.ModelAlias,
		Status:            input.Status,
		InputTokens:       input.InputTokens,
		OutputTokens:      input.OutputTokens,
		CacheReadTokens:   input.CacheReadTokens,
		CacheWriteTokens:  input.CacheWriteTokens,
		HiveCreditDelta:   input.HiveCreditDelta,
		ProviderRequestID: input.ProviderRequestID,
		InternalMetadata:  input.InternalMetadata,
		CustomerTags:      input.CustomerTags,
		ErrorCode:         input.ErrorCode,
		ErrorType:         input.ErrorType,
		CreatedAt:         time.Now().UTC(),
	}

	s.events[input.AccountID] = append(s.events[input.AccountID], event)
	return event, nil
}

func (s *stubRepo) ListAttempts(_ context.Context, accountID uuid.UUID, requestID string, limit int) ([]RequestAttempt, error) {
	s.lastAttemptsLimit = limit

	var attempts []RequestAttempt
	for _, attempt := range s.attempts[accountID] {
		if requestID == "" || attempt.RequestID == requestID {
			attempts = append(attempts, attempt)
		}
	}

	if limit > 0 && len(attempts) > limit {
		return append([]RequestAttempt(nil), attempts[:limit]...), nil
	}

	return append([]RequestAttempt(nil), attempts...), nil
}

func (s *stubRepo) ListEvents(_ context.Context, filter ListEventsFilter) ([]UsageEvent, error) {
	s.lastEventsFilter = filter

	var events []UsageEvent
	for _, event := range s.events[filter.AccountID] {
		if filter.RequestID != "" && event.RequestID != filter.RequestID {
			continue
		}
		if filter.ModelAlias != "" && event.ModelAlias != filter.ModelAlias {
			continue
		}
		if filter.APIKeyID != nil && (event.APIKeyID == nil || *event.APIKeyID != *filter.APIKeyID) {
			continue
		}
		if filter.Status != "" && event.Status != filter.Status {
			continue
		}
		if filter.ErrorsOnly && event.ErrorCode == "" {
			continue
		}
		if !filter.From.IsZero() && event.CreatedAt.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && !event.CreatedAt.Before(filter.To) {
			continue
		}
		events = append(events, event)
	}

	// Same order the pgx query reads in: newest first, id as the tiebreaker.
	sort.Slice(events, func(i, j int) bool {
		if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].CreatedAt.After(events[j].CreatedAt)
		}
		return events[i].ID.String() > events[j].ID.String()
	})

	// Continue after the keyset cursor row.
	if filter.CursorID != nil && *filter.CursorID != uuid.Nil {
		for idx, event := range events {
			if event.ID == *filter.CursorID {
				events = events[idx+1:]
				break
			}
		}
	}

	if filter.Limit > 0 && len(events) > filter.Limit {
		return append([]UsageEvent(nil), events[:filter.Limit]...), nil
	}

	return append([]UsageEvent(nil), events...), nil
}

func (s *stubRepo) GetUsageSummary(_ context.Context, filter AnalyticsFilter) ([]UsageSummaryRow, error) {
	s.lastAnalyticsFilter = filter
	return s.usageSummaryRows, nil
}

func (s *stubRepo) GetSpendSummary(_ context.Context, filter AnalyticsFilter) ([]SpendSummaryRow, error) {
	s.lastAnalyticsFilter = filter
	return s.spendSummaryRows, nil
}

func (s *stubRepo) GetErrorSummary(_ context.Context, filter AnalyticsFilter) ([]ErrorSummaryRow, error) {
	s.lastAnalyticsFilter = filter
	return s.errorSummaryRows, nil
}

func (s *stubRepo) ListMembershipsByUserID(_ context.Context, userID uuid.UUID) ([]accounts.Membership, error) {
	var memberships []accounts.Membership
	for _, membership := range s.memberships {
		if membership.UserID == userID {
			memberships = append(memberships, membership)
		}
	}

	return memberships, nil
}

func (s *stubRepo) ActiveTenantID(_ context.Context, _ uuid.UUID) (uuid.UUID, bool, error) {
	return uuid.Nil, false, nil
}

func (s *stubRepo) CreateAccount(_ context.Context, acct accounts.Account) error {
	s.accountsMap[acct.ID] = &acct
	return nil
}

func (s *stubRepo) CreateMembership(_ context.Context, membership accounts.Membership) error {
	s.memberships = append(s.memberships, membership)
	return nil
}

func (s *stubRepo) CreateProfile(_ context.Context, _ accounts.AccountProfile) error {
	return nil
}

func (s *stubRepo) ProvisionDefaultWorkspace(_ context.Context, acct accounts.Account, membership accounts.Membership, _ accounts.AccountProfile) (uuid.UUID, bool, error) {
	s.accountsMap[acct.ID] = &acct
	s.memberships = append(s.memberships, membership)
	return acct.ID, false, nil
}

func (s *stubRepo) GetAccountByID(_ context.Context, id uuid.UUID) (*accounts.Account, error) {
	acct, ok := s.accountsMap[id]
	if !ok {
		return nil, accounts.ErrNotFound
	}

	return acct, nil
}

func (s *stubRepo) CreateInvitation(_ context.Context, invitation accounts.Invitation) error {
	s.invitations[invitation.TokenHash] = &invitation
	return nil
}

func (s *stubRepo) FindInvitationByTokenHash(_ context.Context, tokenHash string) (*accounts.Invitation, error) {
	invitation, ok := s.invitations[tokenHash]
	if !ok {
		return nil, accounts.ErrNotFound
	}

	return invitation, nil
}

func (s *stubRepo) AcceptInvitation(_ context.Context, invitationID uuid.UUID, acceptedAt time.Time) error {
	for _, invitation := range s.invitations {
		if invitation.ID == invitationID {
			invitation.AcceptedAt = &acceptedAt
			return nil
		}
	}

	return accounts.ErrNotFound
}

func (s *stubRepo) ListMembersByAccountID(_ context.Context, accountID uuid.UUID) ([]accounts.Member, error) {
	var members []accounts.Member
	for _, membership := range s.memberships {
		if membership.AccountID == accountID {
			members = append(members, accounts.Member{
				UserID: membership.UserID,
				Role:   membership.Role,
				Status: membership.Status,
			})
		}
	}

	return members, nil
}

// ActivateMembership exists to satisfy accounts.Repository. No test in this
// package walks the invitation flow.
func (s *stubRepo) ActivateMembership(_ context.Context, _, _ uuid.UUID, _ string) error {
	return accounts.ErrNotFound
}

func (s *stubRepo) UpdateMembershipRole(_ context.Context, accountID, userID uuid.UUID, role string) error {
	for i := range s.memberships {
		if s.memberships[i].AccountID == accountID && s.memberships[i].UserID == userID {
			updated := s.memberships[i]
			updated.Role = role
			s.memberships[i] = updated
			return nil
		}
	}
	return accounts.ErrNotFound
}

func TestRecordEventRedactsPromptFields(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	accountID := uuid.New()
	attemptID := uuid.New()

	_, err := svc.RecordEvent(context.Background(), RecordEventInput{
		AccountID:        accountID,
		RequestAttemptID: attemptID,
		RequestID:        "req_123",
		EventType:        UsageEventAccepted,
		Endpoint:         "/v1/responses",
		ModelAlias:       "hive-fast",
		Status:           "accepted",
		InternalMetadata: map[string]any{
			"prompt": "never-store-this",
			"safe":   "keep-me",
			"nested": map[string]any{
				"messages": []any{
					map[string]any{"role": "user", "content": "secret"},
				},
				"keep": "still-here",
			},
		},
	})
	if err != nil {
		t.Fatalf("RecordEvent returned error: %v", err)
	}

	if _, ok := repo.lastRecordedEvent.InternalMetadata["prompt"]; ok {
		t.Fatal("expected prompt field to be removed before persistence")
	}

	nested, ok := repo.lastRecordedEvent.InternalMetadata["nested"].(map[string]any)
	if !ok {
		t.Fatal("expected nested metadata to remain a map")
	}
	if _, ok := nested["messages"]; ok {
		t.Fatal("expected nested messages field to be removed before persistence")
	}
	if nested["keep"] != "still-here" {
		t.Fatalf("expected nested keep field to survive, got %#v", nested["keep"])
	}
}

func TestRedactMetadataStripsNestedMessageContent(t *testing.T) {
	redacted := RedactMetadata(map[string]any{
		"request": map[string]any{
			"meta": map[string]any{
				"content": "remove-this",
				"label":   "keep-this",
			},
			"messages": []any{
				map[string]any{"role": "user", "content": "hidden"},
			},
		},
	})

	request, ok := redacted["request"].(map[string]any)
	if !ok {
		t.Fatal("expected request object to survive redaction")
	}
	if _, ok := request["messages"]; ok {
		t.Fatal("expected messages key to be removed recursively")
	}

	meta, ok := request["meta"].(map[string]any)
	if !ok {
		t.Fatal("expected nested meta object to remain")
	}
	if _, ok := meta["content"]; ok {
		t.Fatal("expected nested content key to be removed recursively")
	}
	if meta["label"] != "keep-this" {
		t.Fatalf("expected label to survive redaction, got %#v", meta["label"])
	}
}

func TestStartAttemptRejectsBlankRequestID(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	_, err := svc.StartAttempt(context.Background(), StartAttemptInput{
		AccountID:     uuid.New(),
		RequestID:     "   ",
		AttemptNumber: 1,
		Endpoint:      "/v1/responses",
		ModelAlias:    "hive-fast",
		Status:        AttemptStatusAccepted,
	})
	if err == nil {
		t.Fatal("expected StartAttempt to reject a blank request ID")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T", err)
	}
}

// -----------------------------------------------------------------------------
// Analytics filter validation + pass-through (GetUsageSummary / GetSpendSummary
// / GetErrorSummary). These three were entirely uncovered: validateAnalyticsFilter
// gates every one of them but had zero direct tests, so a broken group_by
// allow-list or a reversed from/to check could ship silently.
// -----------------------------------------------------------------------------

func TestGetUsageSummary_RejectsInvalidGroupBy(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	_, err := svc.GetUsageSummary(context.Background(), AnalyticsFilter{
		AccountID: uuid.New(),
		GroupBy:   "not_a_real_dimension",
	})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError for bad group_by, got %v", err)
	}
	if validationErr.Field != "group_by" {
		t.Fatalf("expected field=group_by, got %q", validationErr.Field)
	}
}

func TestGetUsageSummary_RejectsFromNotBeforeTo(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)

	now := time.Now().UTC()
	_, err := svc.GetUsageSummary(context.Background(), AnalyticsFilter{
		AccountID: uuid.New(),
		GroupBy:   "model",
		From:      now,
		To:        now.Add(-1 * time.Hour), // to before from
	})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError when from is not before to, got %v", err)
	}
}

func TestGetUsageSummary_ValidFilterReachesRepository(t *testing.T) {
	repo := newStubRepo()
	want := []UsageSummaryRow{{GroupKey: "gpt-test", TotalInputTokens: 10, TotalOutputTokens: 20, TotalCreditsSpent: 5, RequestCount: 1}}
	repo.usageSummaryRows = want
	svc := NewService(repo)

	accountID := uuid.New()
	got, err := svc.GetUsageSummary(context.Background(), AnalyticsFilter{AccountID: accountID, GroupBy: "endpoint"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].GroupKey != "gpt-test" {
		t.Fatalf("expected the repository's rows to pass through unchanged, got %#v", got)
	}
	if repo.lastAnalyticsFilter.AccountID != accountID || repo.lastAnalyticsFilter.GroupBy != "endpoint" {
		t.Fatalf("expected the validated filter to reach the repository unchanged, got %#v", repo.lastAnalyticsFilter)
	}
}

func TestGetSpendSummary_RejectsInvalidGroupBy(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	_, err := svc.GetSpendSummary(context.Background(), AnalyticsFilter{AccountID: uuid.New(), GroupBy: "bogus"})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestGetSpendSummary_ValidFilterReachesRepository(t *testing.T) {
	repo := newStubRepo()
	want := []SpendSummaryRow{{GroupKey: "api-key-1", TotalCredits: 100, EntryCount: 3}}
	repo.spendSummaryRows = want
	svc := NewService(repo)

	got, err := svc.GetSpendSummary(context.Background(), AnalyticsFilter{AccountID: uuid.New(), GroupBy: "api_key"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].TotalCredits != 100 {
		t.Fatalf("expected repository rows to pass through, got %#v", got)
	}
}

func TestGetErrorSummary_RejectsInvalidGroupBy(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	_, err := svc.GetErrorSummary(context.Background(), AnalyticsFilter{AccountID: uuid.New(), GroupBy: "bogus"})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
}

func TestGetErrorSummary_ValidFilterReachesRepository(t *testing.T) {
	repo := newStubRepo()
	want := []ErrorSummaryRow{{GroupKey: "model-x", ErrorCount: 2, TotalRequests: 10, ErrorRate: 0.2}}
	repo.errorSummaryRows = want
	svc := NewService(repo)

	got, err := svc.GetErrorSummary(context.Background(), AnalyticsFilter{AccountID: uuid.New(), GroupBy: "model"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ErrorRate != 0.2 {
		t.Fatalf("expected repository rows to pass through, got %#v", got)
	}
}

func TestGetUsageSummary_EmptyGroupByIsAllowed(t *testing.T) {
	// Empty string defaults to "model" at the query level (repository.go's
	// switch default case); the service validator must let it through rather
	// than rejecting it as an invalid dimension.
	repo := newStubRepo()
	svc := NewService(repo)
	if _, err := svc.GetUsageSummary(context.Background(), AnalyticsFilter{AccountID: uuid.New(), GroupBy: ""}); err != nil {
		t.Fatalf("expected empty group_by to be accepted, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// UpdateAttemptStatus validation
// -----------------------------------------------------------------------------

func TestUpdateAttemptStatus_RejectsNilAttemptID(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	err := svc.UpdateAttemptStatus(context.Background(), uuid.Nil, AttemptStatusCompleted, nil)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError for nil attempt id, got %v", err)
	}
}

func TestUpdateAttemptStatus_RejectsInvalidStatus(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	err := svc.UpdateAttemptStatus(context.Background(), uuid.New(), AttemptStatus("not_a_status"), nil)
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError for invalid status, got %v", err)
	}
	if repo.lastUpdateAttemptID != uuid.Nil {
		t.Fatal("repository must not be called when status validation fails")
	}
}

func TestUpdateAttemptStatus_ValidStatusReachesRepository(t *testing.T) {
	repo := newStubRepo()
	svc := NewService(repo)
	accountID := uuid.New()
	attemptID := uuid.New()
	repo.attempts[accountID] = []RequestAttempt{{ID: attemptID, AccountID: accountID}}
	completed := time.Now().UTC()

	for _, status := range []AttemptStatus{
		AttemptStatusAccepted, AttemptStatusDispatching, AttemptStatusStreaming,
		AttemptStatusCompleted, AttemptStatusFailed, AttemptStatusCancelled, AttemptStatusInterrupted,
	} {
		if err := svc.UpdateAttemptStatus(context.Background(), attemptID, status, &completed); err != nil {
			t.Fatalf("status %q: unexpected error: %v", status, err)
		}
		if repo.lastUpdateStatus != string(status) {
			t.Fatalf("expected repository to receive status %q, got %q", status, repo.lastUpdateStatus)
		}
	}
}

func TestUpdateAttemptStatus_PropagatesRepositoryError(t *testing.T) {
	repo := newStubRepo()
	repo.updateAttemptStatusErr = errors.New("boom")
	svc := NewService(repo)
	err := svc.UpdateAttemptStatus(context.Background(), uuid.New(), AttemptStatusCompleted, nil)
	if err == nil {
		t.Fatal("expected repository error to propagate")
	}
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		t.Fatal("a repository failure must not be reported as a ValidationError")
	}
}
