package usage

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func RedactMetadata(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}

	redacted := make(map[string]any, len(input))
	for key, value := range input {
		switch key {
		case "prompt", "prompts", "input", "inputs", "messages", "response", "responses", "completion", "completions", "content", "output_text":
			continue
		default:
			redacted[key] = redactValue(value)
		}
	}

	return redacted
}

func (s *Service) StartAttempt(ctx context.Context, input StartAttemptInput) (RequestAttempt, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.ModelAlias = strings.TrimSpace(input.ModelAlias)

	if input.RequestID == "" {
		return RequestAttempt{}, &ValidationError{Field: "request_id", Message: "request_id is required"}
	}
	if input.AttemptNumber <= 0 {
		return RequestAttempt{}, &ValidationError{Field: "attempt_number", Message: "attempt_number must be greater than zero"}
	}
	if input.Endpoint == "" {
		return RequestAttempt{}, &ValidationError{Field: "endpoint", Message: "endpoint is required"}
	}
	if input.ModelAlias == "" {
		return RequestAttempt{}, &ValidationError{Field: "model_alias", Message: "model_alias is required"}
	}
	if input.Status == "" {
		input.Status = AttemptStatusAccepted
	}
	input.CustomerTags = normalizeJSONMap(input.CustomerTags)

	attempt, err := s.repo.CreateAttempt(ctx, input)
	if err != nil {
		return RequestAttempt{}, fmt.Errorf("usage: start attempt: %w", err)
	}

	return attempt, nil
}

func (s *Service) RecordEvent(ctx context.Context, input RecordEventInput) (UsageEvent, error) {
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.ModelAlias = strings.TrimSpace(input.ModelAlias)
	input.Status = strings.TrimSpace(input.Status)

	if input.RequestAttemptID == uuid.Nil {
		return UsageEvent{}, &ValidationError{Field: "request_attempt_id", Message: "request_attempt_id is required"}
	}
	if input.RequestID == "" {
		return UsageEvent{}, &ValidationError{Field: "request_id", Message: "request_id is required"}
	}
	if input.EventType == "" {
		return UsageEvent{}, &ValidationError{Field: "event_type", Message: "event_type is required"}
	}
	if input.Endpoint == "" {
		return UsageEvent{}, &ValidationError{Field: "endpoint", Message: "endpoint is required"}
	}
	if input.ModelAlias == "" {
		return UsageEvent{}, &ValidationError{Field: "model_alias", Message: "model_alias is required"}
	}
	if input.Status == "" {
		return UsageEvent{}, &ValidationError{Field: "status", Message: "status is required"}
	}

	input.InternalMetadata = RedactMetadata(input.InternalMetadata)
	input.CustomerTags = normalizeJSONMap(input.CustomerTags)

	event, err := s.repo.RecordEvent(ctx, input)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("usage: record event: %w", err)
	}

	return event, nil
}

func (s *Service) UpdateAttemptStatus(ctx context.Context, attemptID uuid.UUID, status AttemptStatus, completedAt *time.Time) error {
	if attemptID == uuid.Nil {
		return &ValidationError{Field: "request_attempt_id", Message: "request_attempt_id is required"}
	}
	if !isValidAttemptStatus(status) {
		return &ValidationError{Field: "status", Message: "status is invalid"}
	}

	if err := s.repo.UpdateAttemptStatus(ctx, attemptID, string(status), completedAt); err != nil {
		return fmt.Errorf("usage: update attempt status: %w", err)
	}

	return nil
}

func (s *Service) ListAttempts(ctx context.Context, accountID uuid.UUID, requestID string, limit int) ([]RequestAttempt, error) {
	if limit <= 0 {
		limit = 20
	}

	attempts, err := s.repo.ListAttempts(ctx, accountID, strings.TrimSpace(requestID), limit)
	if err != nil {
		return nil, fmt.Errorf("usage: list attempts: %w", err)
	}

	return attempts, nil
}

func (s *Service) ListEvents(ctx context.Context, filter ListEventsFilter) ([]UsageEvent, error) {
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	filter.RequestID = strings.TrimSpace(filter.RequestID)

	events, err := s.repo.ListEvents(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("usage: list events: %w", err)
	}

	return events, nil
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return RedactMetadata(typed)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, redactValue(item))
		}
		return result
	default:
		return value
	}
}

func isValidAttemptStatus(status AttemptStatus) bool {
	switch status {
	case AttemptStatusAccepted, AttemptStatusDispatching, AttemptStatusStreaming, AttemptStatusCompleted, AttemptStatusFailed, AttemptStatusCancelled, AttemptStatusInterrupted:
		return true
	default:
		return false
	}
}
