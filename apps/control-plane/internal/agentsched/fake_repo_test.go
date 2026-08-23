package agentsched

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// fakeRepo is an in-memory agentsched.Repository implementing the claim
// semantics of public.agent_task_schedules_claim_due in miniature: filters by
// enabled and next_run_at <= now, advances next_run_at at claim time, so the
// scheduler's due/not-due/disabled/concurrent-tick-once guarantees are tested
// against the same shape as the SQL.
type fakeRepo struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]*Schedule
	created   []Schedule
	failures  []string // recorded failure messages
	successes int

	claimErr error
}

func newFakeRepo(rows ...Schedule) *fakeRepo {
	f := &fakeRepo{rows: make(map[uuid.UUID]*Schedule)}
	for i := range rows {
		s := rows[i]
		f.rows[s.ID] = &s
	}
	return f
}

func (f *fakeRepo) Create(ctx context.Context, s Schedule) (Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	out := s
	f.rows[s.ID] = &out
	f.created = append(f.created, out)
	return out, nil
}

func (f *fakeRepo) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[id]
	if row == nil || row.TenantID != tenantID || row.UserID != userID {
		return Schedule{}, ErrNotFound
	}
	return *row, nil
}

func (f *fakeRepo) List(ctx context.Context, tenantID, userID uuid.UUID) ([]Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Schedule
	for _, row := range f.rows {
		if row.TenantID == tenantID && row.UserID == userID {
			out = append(out, *row)
		}
	}
	return out, nil
}

func (f *fakeRepo) Update(ctx context.Context, s Schedule) (Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[s.ID]
	if row == nil || row.TenantID != s.TenantID || row.UserID != s.UserID {
		return Schedule{}, ErrNotFound
	}
	out := *row
	out.Name = s.Name
	out.Instructions = s.Instructions
	out.Schedule = s.Schedule
	out.Enabled = s.Enabled
	out.NextRunAt = s.NextRunAt
	out.UpdatedAt = time.Now()
	f.rows[s.ID] = &out
	return out, nil
}

func (f *fakeRepo) SetEnabled(ctx context.Context, tenantID, userID, id uuid.UUID, enabled bool, nextRunAt *time.Time) (Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[id]
	if row == nil || row.TenantID != tenantID || row.UserID != userID {
		return Schedule{}, ErrNotFound
	}
	out := *row
	out.Enabled = enabled
	if nextRunAt != nil && enabled {
		out.NextRunAt = nextRunAt
	}
	f.rows[id] = &out
	return out, nil
}

func (f *fakeRepo) Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[id]
	if row == nil || row.TenantID != tenantID || row.UserID != userID {
		return ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

func (f *fakeRepo) RecordRunSuccess(ctx context.Context, tenantID, id, taskID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[id]
	if row == nil {
		return ErrNotFound
	}
	t := taskID
	row.LastTaskID = &t
	f.successes++
	return nil
}

func (f *fakeRepo) RecordRunFailure(ctx context.Context, tenantID, id uuid.UUID, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.rows[id]
	if row == nil {
		return ErrNotFound
	}
	row.LastError = message
	f.failures = append(f.failures, message)
	return nil
}

// ClaimDue mirrors agent_task_schedules_claim_due: due = enabled AND
// next_run_at <= now; claims advance next_run_at by one cadence at claim
// time.
func (f *fakeRepo) ClaimDue(ctx context.Context, now time.Time, batch int) ([]Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	var out []Schedule
	for _, row := range f.rows {
		if !row.Enabled || row.NextRunAt == nil || row.NextRunAt.After(now) {
			continue
		}
		cad, err := cadence(row.Schedule)
		if err != nil {
			return nil, err
		}
		next := now.Add(cad)
		row.NextRunAt = &next
		out = append(out, *row)
	}
	if batch > 0 && len(out) > batch {
		out = out[:batch]
	}
	return out, nil
}
