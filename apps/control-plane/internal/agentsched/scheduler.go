package agentsched

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// TaskCreator is the narrow surface the Scheduler needs from
// agenttask.Service. The real Service satisfies it directly; tests inject a
// fake. Going through the SAME service path a manual creation uses is the
// whole point (brief for this slice): metering, quota and engine gating then
// apply to scheduled runs identically to manual ones, with no second code
// path that could drift.
type TaskCreator interface {
	CreateTask(ctx context.Context, tenantID, userID uuid.UUID, pack agenttask.Pack, instructions, bearerJWT string) (agenttask.Task, error)
}

// scheduledPack is the pack every scheduled run launches as in this first
// slice. Schedules carry no pack column yet; coding-pack is the default
// because it needs no user bearer JWT (a schedule has none: JWTs expire long
// before a weekly recurrence) and knowledge-work-pack's artifact publishing
// is skipped without one anyway.
const scheduledPack = agenttask.PackCoding

// runFailureMessage is the provider-blind text persisted into last_error when
// CreateTask fails. No engine or infra detail reaches a customer-readable
// column, same posture as agenttask.Service's launch-failure messages.
const runFailureMessage = "scheduled task could not be created; it will retry on the next cadence"

// SchedulerConfig controls Scheduler behaviour.
type SchedulerConfig struct {
	// Interval between ticks. Zero defaults to one minute.
	Interval time.Duration
	// Batch caps how many schedules one tick claims. Zero defaults to 100.
	Batch int
	// Logger; nil defaults to slog.Default().
	Logger *slog.Logger
}

// Scheduler turns due agent_task_schedules rows into real agent tasks.
//
// One tick = ClaimDue(now, batch) + one CreateTask per claimed row. All
// concurrency safety lives inside ClaimDue: its SECURITY DEFINER statement
// locks candidate rows FOR UPDATE SKIP LOCKED and advances next_run_at in the
// same statement that returns them, so two control-plane instances (or a
// retried tick) can never fire the same schedule twice — a claimed row stops
// matching "due" at claim time, not after its task is created. A CreateTask
// failure therefore cannot hot-loop either: next_run_at already sits one full
// cadence out, and the failure only records last_error.
type Scheduler struct {
	repo     Repository
	tasks    TaskCreator
	interval time.Duration
	batch    int
	logger   *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	started bool
}

// NewScheduler builds a Scheduler. repo and tasks must be non-nil.
func NewScheduler(repo Repository, tasks TaskCreator, cfg SchedulerConfig) *Scheduler {
	if repo == nil {
		panic("agentsched: nil repository")
	}
	if tasks == nil {
		panic("agentsched: nil task creator")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	batch := cfg.Batch
	if batch <= 0 {
		batch = 100
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{repo: repo, tasks: tasks, interval: interval, batch: batch, logger: logger}
}

// RunOnce performs exactly one tick at the given clock reading. now is a
// parameter rather than an internal clock so tests drive due/not-due without
// sleeps; production passes time.Now via Start.
func (s *Scheduler) RunOnce(ctx context.Context, now time.Time) (fired int) {
	due, err := s.repo.ClaimDue(ctx, now, s.batch)
	if err != nil {
		s.logger.WarnContext(ctx, "agentsched: claim due failed, skipping tick",
			"error", err)
		return 0
	}
	for _, sched := range due {
		task, err := s.tasks.CreateTask(ctx, sched.TenantID, sched.UserID, scheduledPack, sched.Instructions, "")
		if err != nil {
			// Provider-blind persistence; the real detail stays in the log.
			s.logger.WarnContext(ctx, "agentsched: scheduled create failed, backing off one cadence",
				"schedule_id", sched.ID, "tenant_id", sched.TenantID, "error", err)
			if recErr := s.repo.RecordRunFailure(ctx, sched.TenantID, sched.ID, runFailureMessage); recErr != nil && !errors.Is(recErr, ErrNotFound) {
				s.logger.WarnContext(ctx, "agentsched: could not record run failure",
					"schedule_id", sched.ID, "error", recErr)
			}
			continue
		}
		if err := s.repo.RecordRunSuccess(ctx, sched.TenantID, sched.ID, task.ID); err != nil &&
			!errors.Is(err, ErrNotFound) {
			// The task exists and was launched by Service.CreateTask's own
			// background path regardless; losing the pointer write is a
			// bookkeeping loss for the list view, not a reason to fail the run.
			s.logger.WarnContext(ctx, "agentsched: could not record run success",
				"schedule_id", sched.ID, "task_id", task.ID, "error", err)
		}
		fired++
	}
	return fired
}

// Start launches the tick loop on a background goroutine. Subsequent Start
// calls are no-ops until Stop is called. Mirrors agenttask.Poller's
// Start/Stop shape.
func (s *Scheduler) Start(parent context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	doneCh := make(chan struct{})
	s.cancel = cancel
	s.doneCh = doneCh
	s.started = true

	go s.loop(ctx, doneCh)
}

// Stop signals the loop to exit and waits for the in-flight tick to finish.
// Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	cancel := s.cancel
	doneCh := s.doneCh
	s.mu.Unlock()

	cancel()
	<-doneCh

	s.mu.Lock()
	if s.doneCh == doneCh {
		s.started = false
		s.cancel = nil
		s.doneCh = nil
	}
	s.mu.Unlock()
}

func (s *Scheduler) loop(ctx context.Context, doneCh chan<- struct{}) {
	defer close(doneCh)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx, time.Now())
		}
	}
}

// compile-time proof the concrete service path satisfies the seam.
var _ TaskCreator = (*agenttask.Service)(nil)
