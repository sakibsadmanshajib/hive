package usermemories

import (
	"context"
	"log/slog"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// Service implements the four memory verbs over Repository, enforcing the
// slice's bounds at the boundary: content is sanitized (control characters
// stripped so every stored fact is single-line) and capped at
// MaxContentLen, and the per-user row cap evicts oldest-first on overflow.
// Memory content later crosses into chat prompts, which is why the
// sanitization happens here and not at any softer layer.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create stores one memory after sanitization and enforces the per-user cap.
func (s *Service) Create(ctx context.Context, tenantID, userID uuid.UUID, raw string, sourceChatID *string) (Memory, error) {
	content, err := SanitizeContent(raw)
	if err != nil {
		return Memory{}, err
	}
	m, err := s.repo.Create(ctx, tenantID, userID, content, sourceChatID)
	if err != nil {
		return Memory{}, err
	}
	// Cap enforcement runs after a successful insert; eviction removes only
	// rows beyond MaxMemoriesPerUser for this exact user. A failed eviction
	// must not fail the create (the insert already succeeded and the cap is
	// re-checked on the next create), but it is never silent.
	if _, err := s.repo.EvictOldest(ctx, tenantID, userID, MaxMemoriesPerUser); err != nil {
		slog.Warn("usermemories: per-user cap eviction failed", "err", err, "user_id", userID.String())
	}
	return m, nil
}

// List returns the user's memories, newest first.
func (s *Service) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Memory, error) {
	return s.repo.List(ctx, tenantID, userID)
}

// Update rewrites one memory's content after sanitization.
func (s *Service) Update(ctx context.Context, tenantID, userID, id uuid.UUID, raw string) (Memory, error) {
	content, err := SanitizeContent(raw)
	if err != nil {
		return Memory{}, err
	}
	return s.repo.Update(ctx, tenantID, userID, id, content)
}

// Delete removes one memory. Missing ids inside the caller's own scope and
// foreign ids both surface as ErrNotFound (404 outside).
func (s *Service) Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	return s.repo.Delete(ctx, tenantID, userID, id)
}

// Get returns one memory within scope.
func (s *Service) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Memory, error) {
	return s.repo.Get(ctx, tenantID, userID, id)
}

// SanitizeContent folds every whitespace/control character to a plain
// space, drops all other control characters (so each stored fact renders as
// a single prompt line and cannot smuggle newline framing into the recall
// block), collapses runs of whitespace to one space, and caps length at
// MaxContentLen characters (runes). Empty results fail: an empty memory
// would be pure noise in recall.
func SanitizeContent(raw string) (string, error) {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r) || r == unicode.ReplacementChar:
			return -1
		default:
			return r
		}
	}, raw)

	fields := strings.Fields(clean)
	if len(fields) == 0 {
		return "", ErrEmptyContent
	}
	content := strings.Join(fields, " ")
	if len([]rune(content)) > MaxContentLen {
		runes := []rune(content)
		content = string(runes[:MaxContentLen])
	}
	return content, nil
}
