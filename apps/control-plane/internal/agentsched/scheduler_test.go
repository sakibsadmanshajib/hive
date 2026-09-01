package agentsched

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// fakeTasks records CreateTask calls.
type fakeTasks struct {
	mu    sync.Mutex
	calls int
	err   error
	last  struct {
		tenantID     uuid.UUID
		userID       uuid.UUID
		pack         agenttask.Pack
		instructions string
		bearerJWT    string
	}
}

func (f *fakeTasks) CreateTask(ctx context.Context, tenantID, userID uuid.UUID, pack agenttask.Pack, instructions string, projectID uuid.UUID, bearerJWT string) (agenttask.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last.tenantID = tenantID
	f.last.userID = userID
	f.last.pack = pack
	f.last.instructions = instructions
	f.last.bearerJWT = bearerJWT
	if f.err != nil {
		return agenttask.Task{}, f.err
	}
	return agenttask.Task{ID: uuid.New(), TenantID: tenantID, UserID: userID}, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newScheduler(repo Repository, tasks TaskCreator) *Scheduler {
	return newSchedulerWithSolvency(repo, tasks, solvent())
}

func newSchedulerWithSolvency(repo Repository, tasks TaskCreator, sol Solvency) *Scheduler {
	return NewScheduler(repo, tasks, sol, SchedulerConfig{Logger: quietLogger()})
}

func TestScheduler_FiresDueScheduleOncePerTick(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo(Schedule{
		ID: idA, TenantID: tenantA, UserID: userA,
		Name: "n", Instructions: "do the thing", Schedule: "daily",
		Enabled:   true,
		NextRunAt: ptrTime(now.Add(-time.Minute)),
	})
	tasks := &fakeTasks{}
	s := newScheduler(repo, tasks)
	ctx := context.Background()

	fired := s.RunOnce(ctx, now)
	if fired != 1 || tasks.calls != 1 {
		t.Fatalf("fired=%d calls=%d, want 1/1", fired, tasks.calls)
	}
	got, _ := repo.Get(ctx, tenantA, userA, idA)
	if got.LastTaskID == nil {
		t.Fatal("last_task_id must be recorded")
	}
	if got.LastError != "" {
		t.Fatalf("last_error = %q, want empty on success", got.LastError)
	}

	fired = s.RunOnce(ctx, now)
	if fired != 0 {
		t.Fatalf("second tick at same clock reading fired=%d, want 0", fired)
	}
	tasks.mu.Lock()
	calls := tasks.calls
	tasks.mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls=%d after two ticks, want 1", calls)
	}
}

func TestScheduler_SkipsNotDueAndDisabled(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	repo := newFakeRepo(
		Schedule{
			ID: idA, TenantID: tenantA, UserID: userA,
			Name: "future", Instructions: "i", Schedule: "daily",
			Enabled:   true,
			NextRunAt: &future,
		},
		Schedule{
			ID: uuid.New(), TenantID: tenantA, UserID: userA,
			Name: "disabled", Instructions: "i", Schedule: "daily",
			Enabled:   false,
			NextRunAt: &past,
		},
	)
	tasks := &fakeTasks{}
	s := newScheduler(repo, tasks)

	if fired := s.RunOnce(context.Background(), now); fired != 0 {
		t.Fatalf("fired=%d, want 0 for not-due and disabled schedules", fired)
	}
	tasks.mu.Lock()
	calls := tasks.calls
	tasks.mu.Unlock()
	if calls != 0 {
		t.Fatalf("calls=%d, want 0", calls)
	}
}

func TestScheduler_FailureRecordsErrorAndBacksOffOneCadence(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo(Schedule{
		ID: idA, TenantID: tenantA, UserID: userA,
		Name: "n", Instructions: "i", Schedule: "daily",
		Enabled:   true,
		NextRunAt: ptrTime(now.Add(-time.Minute)),
	})
	tasks := &fakeTasks{err: errors.New("engine unavailable")}
	s := newScheduler(repo, tasks)
	ctx := context.Background()

	fired := s.RunOnce(ctx, now)
	if fired != 0 {
		t.Fatalf("fired=%d, want 0 on failure", fired)
	}
	got, _ := repo.Get(ctx, tenantA, userA, idA)
	if got.LastError == "" {
		t.Fatal("last_error recorded on failure")
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(now) {
		t.Fatal("failure must leave next_run_at one cadence out, not due again")
	}
	if fired := s.RunOnce(ctx, now); fired != 0 {
		t.Fatal("retry tick must not hot-loop the failed schedule")
	}
}
