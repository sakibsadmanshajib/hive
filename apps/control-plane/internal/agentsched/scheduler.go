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
// fake. Going through the same service path a manual creation uses means the
// launcher's own engine gating and sandbox quota apply to a scheduled run
// exactly as they do to a manual one, with no second code path that could
// drift.
//
// It does NOT mean a scheduled run inherits the checks a manual one passes
// before reaching this seam. This comment used to claim it did, and the claim
// was false in the direction that mattered: agenttask.Service.CreateTask
// meters nothing and asks nothing about the tenant's balance.
//
// Nor does the manual route carry a solvency check that this path could be
// said to skip. It has none today either; one is being added a layer above
// CreateTask, in edge-api's own handler, by the separate change for issue
// #669, and edge-api is a hop the scheduler never traverses in any case. So
// when this comment was written neither half existed, and a tenant at zero
// credits could create a routine and have it launch sandboxes on a cadence
// forever (issue #1490). What closes that here is the explicit s.solvency
// check in RunOnce below, not this seam and not anything upstream of it.
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

// insufficientCreditsMessage is the provider-blind text persisted into
// last_error when the solvency gate refuses a launch (#1490). Kept separate
// from runFailureMessage because the two are not the same event and the
// tenant's response to them differs: this one is fixed by topping up, the
// other one clears on its own.
const insufficientCreditsMessage = "scheduled task not started: the account does not have enough credits"

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
	solvency Solvency
	interval time.Duration
	batch    int
	logger   *slog.Logger

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	started bool
}

// NewScheduler builds a Scheduler. repo, tasks and solvency must be non-nil.
//
// solvency is a constructor argument rather than a SchedulerConfig field
// because a config field left at its zero value defaults silently, and the
// silent default for a money gate is "admit everyone" (issue #1490).
func NewScheduler(repo Repository, tasks TaskCreator, solvency Solvency, cfg SchedulerConfig) *Scheduler {
	if repo == nil {
		panic("agentsched: nil repository")
	}
	if tasks == nil {
		panic("agentsched: nil task creator")
	}
	if solvency == nil {
		panic("agentsched: nil solvency")
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
	return &Scheduler{repo: repo, tasks: tasks, solvency: solvency, interval: interval, batch: batch, logger: logger}
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
		// Solvency gate (#1490), re-asked at every launch rather than trusted
		// from creation time: a routine is unbounded in time, so a tenant
		// solvent when it was created can be at zero by its tenth run, and
		// nothing between the two moments would notice.
		//
		// A lookup failure refuses too. It records the ordinary retry message
		// rather than the credit one, because the tenant's balance is unknown
		// and telling them to top up an account that may be fully funded is
		// worse than saying nothing. Either way the row has already had
		// next_run_at advanced by ClaimDue, so this backs off a full cadence
		// instead of hot-looping against a database that is having a bad
		// minute.
		if err := s.solvency.Check(ctx, sched.TenantID, launchFloor); err != nil {
			message := runFailureMessage
			if errors.Is(err, ErrInsufficientCredits) {
				message = insufficientCreditsMessage
			}
			s.logger.WarnContext(ctx, "agentsched: solvency gate refused a scheduled launch, backing off one cadence",
				"schedule_id", sched.ID, "tenant_id", sched.TenantID, "error", err)
			s.recordFailure(ctx, sched, message)
			continue
		}

		task, err := s.tasks.CreateTask(ctx, sched.TenantID, sched.UserID, scheduledPack, sched.Instructions, "")
		if err != nil {
			// Provider-blind persistence; the real detail stays in the log.
			s.logger.WarnContext(ctx, "agentsched: scheduled create failed, backing off one cadence",
				"schedule_id", sched.ID, "tenant_id", sched.TenantID, "error", err)
			s.recordFailure(ctx, sched, runFailureMessage)
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

// recordFailure persists a provider-blind message against one claimed row.
// ErrNotFound is expected and ignored: a schedule deleted between the claim
// and this write is a normal race, not a failure worth logging.
func (s *Scheduler) recordFailure(ctx context.Context, sched Schedule, message string) {
	if err := s.repo.RecordRunFailure(ctx, sched.TenantID, sched.ID, message); err != nil && !errors.Is(err, ErrNotFound) {
		s.logger.WarnContext(ctx, "agentsched: could not record run failure",
			"schedule_id", sched.ID, "error", err)
	}
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
