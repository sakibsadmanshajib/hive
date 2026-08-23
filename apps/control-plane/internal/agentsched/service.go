package agentsched

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Service validates schedule CRUD at the boundary and owns cadence math.
// Handler never talks to Repository directly, mirroring agenttask.Service.
type Service struct {
	repo Repository
	now  func() time.Time
}

// NewService constructs a Service. A nil now defaults to time.Now; tests
// inject a fake clock.
func NewService(repo Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, now: now}
}

// CreateInput is the validated-shape input for Service.Create. The wire
// handler builds it from the request body; every field is re-validated here,
// because the handler is not the only caller the scheduler will ever have.
type CreateInput struct {
	Name         string
	Instructions string
	Schedule     string
}

// UpdateInput is the full replacement body for Service.Update (PUT semantics:
// absent fields are an explicit empty, not a keep).
type UpdateInput struct {
	Name         string
	Instructions string
	Schedule     string
	Enabled      bool
}

// sanitizeInstructions strips control characters except newline and tab.
// Instructions become prompts handed to a model and eventually to a sandbox,
// so anything outside printable text (e.g. \r, NUL, terminal escapes) is
// dropped at the boundary rather than stored. Mirrors the input-parsing rule
// in CLAUDE.md's working notes for this feature.
func sanitizeInstructions(in string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, in)
}

// validSchedule reports whether s matches the restricted first-slice grammar
// the migration's CHECK constraint also enforces: daily, weekly, or
// interval:N with 1 <= N <= 168.
func validSchedule(s string) bool {
	switch s {
	case "daily", "weekly":
		return true
	}
	if !strings.HasPrefix(s, "interval:") {
		return false
	}
	n, ok := parseIntervalHours(s)
	return ok && n >= 1 && n <= maxIntervalHours
}

const maxIntervalHours = 168

// parseIntervalHours extracts N from "interval:N". Returns ok=false unless N
// is one or more ASCII digits with no sign, spaces, or overflow.
func parseIntervalHours(s string) (int, bool) {
	rest, ok := strings.CutPrefix(s, "interval:")
	if !ok || rest == "" || len(rest) > 3 {
		return 0, false
	}
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// cadence returns the fixed duration of one schedule period. The claim
// function advances next_run_at with the same arithmetic in SQL; keep the two
// in sync (see 20260823_01_agent_task_schedules.sql).
func cadence(schedule string) (time.Duration, error) {
	switch schedule {
	case "daily":
		return 24 * time.Hour, nil
	case "weekly":
		return 7 * 24 * time.Hour, nil
	}
	if n, ok := parseIntervalHours(schedule); ok {
		return time.Duration(n) * time.Hour, nil
	}
	return 0, ErrInvalidSchedule
}

// validate applies boundary rules shared by Create and Update: name length,
// instruction length after control-char stripping, schedule grammar. Returns
// the cleaned values.
func validate(name, instructions, schedule string) (string, string, error) {
	name = strings.TrimSpace(name)
	instructions = sanitizeInstructions(strings.TrimSpace(instructions))

	if name == "" || len(name) > maxNameLength {
		return "", "", ErrInvalidName
	}
	if instructions == "" || len(instructions) > maxInstructionsLength {
		return "", "", ErrInvalidInstructions
	}
	if !validSchedule(schedule) {
		return "", "", ErrInvalidSchedule
	}
	return name, instructions, nil
}

// Create persists a new enabled schedule whose first run is one full cadence
// out from now — never immediately, so creating a routine does not fire a
// surprise task before the user has reviewed it.
func (s *Service) Create(ctx context.Context, tenantID, userID uuid.UUID, in CreateInput) (Schedule, error) {
	name, instructions, err := validate(in.Name, in.Instructions, in.Schedule)
	if err != nil {
		return Schedule{}, err
	}
	cad, err := cadence(in.Schedule)
	if err != nil {
		return Schedule{}, err
	}
	next := s.now().Add(cad)
	out, err := s.repo.Create(ctx, Schedule{
		TenantID:     tenantID,
		UserID:       userID,
		Name:         name,
		Instructions: instructions,
		Schedule:     in.Schedule,
		Enabled:      true,
		NextRunAt:    &next,
	})
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched: create: %w", err)
	}
	return out, nil
}

// Get returns one schedule scoped to (tenantID, userID): another user's
// schedule inside the same tenant reads as not found, same as agenttask.
func (s *Service) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Schedule, error) {
	return s.repo.Get(ctx, tenantID, userID, id)
}

// List returns every schedule userID owns within tenantID, newest first.
func (s *Service) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Schedule, error) {
	return s.repo.List(ctx, tenantID, userID)
}

// Update replaces the mutable fields of one schedule. Changing the cadence
// recomputes next_run_at from now so the row never carries a stale due date;
// leaving it unchanged keeps the existing schedule position.
func (s *Service) Update(ctx context.Context, tenantID, userID, id uuid.UUID, in UpdateInput) (Schedule, error) {
	current, err := s.repo.Get(ctx, tenantID, userID, id)
	if err != nil {
		return Schedule{}, err
	}
	name, instructions, err := validate(in.Name, in.Instructions, in.Schedule)
	if err != nil {
		return Schedule{}, err
	}
	next := current.NextRunAt
	if in.Schedule != current.Schedule {
		cad, err := cadence(in.Schedule)
		if err != nil {
			return Schedule{}, err
		}
		t := s.now().Add(cad)
		next = &t
	}
	out, err := s.repo.Update(ctx, Schedule{
		ID:           current.ID,
		TenantID:     current.TenantID,
		UserID:       current.UserID,
		Name:         name,
		Instructions: instructions,
		Schedule:     in.Schedule,
		Enabled:      in.Enabled,
		NextRunAt:    next,
	})
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched: update: %w", err)
	}
	return out, nil
}

// SetEnabled flips the enabled flag without touching any other field.
// Enabling resets next_run_at to one full cadence from now: while disabled,
// next_run_at freezes, so a row disabled past its due date would otherwise
// fire once, immediately, the moment it was re-enabled.
func (s *Service) SetEnabled(ctx context.Context, tenantID, userID, id uuid.UUID, enabled bool) (Schedule, error) {
	var next *time.Time
	if enabled {
		current, err := s.repo.Get(ctx, tenantID, userID, id)
		if err != nil {
			return Schedule{}, err
		}
		cad, err := cadence(current.Schedule)
		if err != nil {
			return Schedule{}, err
		}
		t := s.now().Add(cad)
		next = &t
	}
	out, err := s.repo.SetEnabled(ctx, tenantID, userID, id, enabled, next)
	if err != nil {
		return Schedule{}, fmt.Errorf("agentsched: set enabled: %w", err)
	}
	return out, nil
}

// Delete removes one schedule. Deleting a schedule never touches tasks its
// earlier runs created (last_task_id is SET NULL on the DB side).
func (s *Service) Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	return s.repo.Delete(ctx, tenantID, userID, id)
}
