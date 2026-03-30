package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
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
		if filter.RequestID == "" || event.RequestID == filter.RequestID {
			events = append(events, event)
		}
	}

	if filter.Limit > 0 && len(events) > filter.Limit {
		return append([]UsageEvent(nil), events[:filter.Limit]...), nil
	}

	return append([]UsageEvent(nil), events...), nil
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
