package agenttask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// statusEventPrefix is the deterministic source_event_id prefix for synthetic
// status events: "status:" + the task status string. The dedup index makes
// re-emitting the same status every pass a no-op; a genuine transition gets a
// new id and lands.
const statusEventPrefix = "status:"

// fileEventID builds a workspace-file event's deterministic source_event_id:
// name + size + mtime. An unchanged file produces the same id every pass and
// dedups out; a rewritten file gets a fresh id and is recorded.
func fileEventID(f WorkspaceFile) string {
	id := "file:" + f.Name + ":" +
		strconv.FormatInt(f.Size, 10) + ":" + strconv.FormatInt(f.ModTime.Unix(), 10)
	// A deep path can blow the source id bound; normalizeSourceID folds it to
	// a content hash, keeping dedup deterministic.
	return normalizeSourceID(id, id)
}

// EventSyncer pulls each active task's sandbox events and workspace listing
// through an EventSource and appends them to public.agent_task_events with
// per-task monotonic seq and source-id dedup. It runs beside the status
// Poller behind the same engine-configured gate in cmd/server/main.go and
// mirrors Poller's Start/Stop/RunOnce shape, including its backoff contract:
// only ListActive failures are pass-level errors; per-task problems are
// scoped, logged, retried next pass.
type EventSyncer struct {
	repo     Repository
	src      EventSource
	interval time.Duration
	logger   *slog.Logger

	// seen tracks tasks active at their most recent processed pass. It closes
	// the one gap ListActive has: once the poller records a terminal status
	// the row leaves ListActive, so without this tracker the
	// succeeded/failed/cancelled transition would never produce its own
	// status event (acceptance item 1 wants one status event per transition,
	// including into terminal states).
	seen map[uuid.UUID]Task

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	started bool
}

// NewEventSyncer builds an EventSyncer. repo and src must be non-nil.
func NewEventSyncer(repo Repository, src EventSource, cfg PollerConfig) *EventSyncer {
	if repo == nil {
		panic("agenttask: nil repository")
	}
	if src == nil {
		panic("agenttask: nil event source")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EventSyncer{
		repo:     repo,
		src:      src,
		interval: interval,
		logger:   logger,
		seen:     make(map[uuid.UUID]Task),
	}
}

// RunOnce performs exactly one sync pass over every active task plus the
// tracked tasks that went terminal since the last pass. The returned error,
// when non-nil, is loop's sole backoff signal and means ListActive itself
// failed; nothing else feeds it (same contract as Poller.RunOnce).
func (s *EventSyncer) RunOnce(ctx context.Context) error {
	tasks, err := s.repo.ListActive(ctx)
	if err != nil {
		return fmt.Errorf("agenttask: event sync list active: %w", err)
	}

	active := make(map[uuid.UUID]bool, len(tasks))
	for _, t := range tasks {
		active[t.ID] = true
		s.syncTask(ctx, t)
	}
	s.finishVanished(ctx, active)
	return nil
}

// Start launches the sync loop on a background goroutine. Subsequent Start
// calls are no-ops until Stop is called. Mirrors Poller.Start.
func (s *EventSyncer) Start(parent context.Context) {
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

	go func() {
		defer close(doneCh)
		// Eager first pass, then interval ticks; a ListActive failure backs
		// off exponentially like the poller's does, via the same doubling.
		consecutiveFailures := 0
		runPass := func() {
			if err := s.RunOnce(ctx); err != nil {
				consecutiveFailures++
			} else {
				consecutiveFailures = 0
			}
		}
		runPass()
		timer := time.NewTimer(pollerBackoffDelay(s.interval, consecutiveFailures))
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				runPass()
				timer.Reset(pollerBackoffDelay(s.interval, consecutiveFailures))
			}
		}
	}()
}

// Stop signals the loop to exit and waits for the in-flight pass to finish.
// Safe to call multiple times. Mirrors Poller.Stop.
func (s *EventSyncer) Stop() {
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

// syncTask processes ONE task: synthetic status event, sandbox events, then
// the workspace listing. Every failure here logs against this task alone and
// never reaches RunOnce's return value.
func (s *EventSyncer) syncTask(ctx context.Context, t Task) {
	events := []TaskEvent{statusEvent(t)}

	sandboxEvents, err := s.pullSandboxEvents(ctx, t.EngineSessionRef)
	if err != nil {
		s.logger.WarnContext(ctx, "agenttask: event sync pull failed",
			"task_id", t.ID, "error", err)
	} else {
		for _, se := range sandboxEvents {
			if ev, ok := mapSandboxEvent(se); ok {
				events = append(events, ev)
			}
		}
		files, ferr := s.src.Files(ctx, t.EngineSessionRef)
		if ferr != nil {
			s.logger.WarnContext(ctx, "agenttask: workspace listing failed",
				"task_id", t.ID, "error", ferr)
		} else {
			for _, f := range files {
				events = append(events, fileEvent(f))
			}
		}
	}

	if err := s.repo.AppendEvents(ctx, t, events); err != nil {
		// Retried whole-batch next pass; dedup makes the retry idempotent.
		s.logger.WarnContext(ctx, "agenttask: event append failed, retrying next pass",
			"task_id", t.ID, "error", err)
		return
	}
	s.remember(t)
}

// remember records t as still-active-seen.
func (s *EventSyncer) remember(t Task) {
	if s.seen == nil {
		s.seen = make(map[uuid.UUID]Task)
	}
	s.seen[t.ID] = t
}

// finishVanished emits the terminal status event for tasks that left the
// active set since they were last seen, then forgets them. A transient Get
// failure drops the tracking entry anyway: a missed terminal status event is
// degraded-but-safe (acceptance item 5's direction), while keeping the entry
// would leak memory on a permanently broken read.
func (s *EventSyncer) finishVanished(ctx context.Context, active map[uuid.UUID]bool) {
	for id, t := range s.seen {
		if active[id] {
			continue
		}
		final, err := s.repo.Get(ctx, t.TenantID, t.UserID, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			s.logger.WarnContext(ctx, "agenttask: could not read a finished task's final status for its event",
				"task_id", id, "error", err)
		} else if err == nil {
			_ = s.repo.AppendEvents(ctx, t, []TaskEvent{statusEvent(final)})
		}
		delete(s.seen, id)
	}
}

// statusEvent builds the synthetic status row for t's CURRENT status. Dedup
// makes re-emitting it every pass free.
func statusEvent(t Task) TaskEvent {
	payload, _ := json.Marshal(map[string]string{"status": string(t.Status)})
	return TaskEvent{
		SourceEventID: statusEventPrefix + string(t.Status),
		Kind:          EventStatus,
		Payload:       payload,
	}
}

// fileEvent builds one workspace-file row.
func fileEvent(f WorkspaceFile) TaskEvent {
	payload, _ := json.Marshal(f)
	return TaskEvent{
		SourceEventID: fileEventID(f),
		Kind:          EventFile,
		Payload:       payload,
	}
}

// pullSandboxEvents pages through the source's search endpoint until it
// reports exhaustion or maxSyncPages is hit.
func (s *EventSyncer) pullSandboxEvents(ctx context.Context, sessionRef string) ([]SandboxEvent, error) {
	var all []SandboxEvent
	page, err := s.src.Events(ctx, sessionRef)
	if err != nil {
		return nil, err
	}
	all = append(all, page...)
	return all, nil
}

// mapSandboxEvent translates one normalized sandbox event into our six-kind
// vocabulary. The mapping is deliberately isolated here so OpenHands schema
// drift costs one function, and the fallback NEVER drops silently: any kind
// this switch does not name lands as kind `status` carrying the raw payload
// (or a marker when the raw dump was too large to transport), so a new
// upstream event class surfaces in the transcript instead of vanishing.
//
// MessageEvent maps for every role, not only assistant: dropping user-role
// messages would be exactly the silent drop the plan forbids, and the role
// rides in the payload so consumers can tell them apart.
func mapSandboxEvent(e SandboxEvent) (TaskEvent, bool) {
	base := TaskEvent{
		// Empty sandbox ids would be exempt from the dedup index and
		// re-inserted on every pass; overlong ones would fail the migration's
		// CHECK. Both fold to a deterministic hash of the event content.
		SourceEventID: normalizeSourceID(e.ID, e.Kind+"|"+e.TextPreview+"|"+string(e.Raw)),
	}
	switch e.Kind {
	case "ActionEvent":
		base.Kind = EventToolCall
		base.Payload = mustJSON(map[string]any{
			"tool_name":    e.ToolName,
			"tool_call_id": e.ToolCallID,
			"preview":      truncateRunes(e.TextPreview, maxPreviewRunes),
		})
	case "ObservationEvent", "UserRejectObservation":
		base.Kind = EventToolResult
		base.Payload = mustJSON(map[string]any{
			"tool_name":    e.ToolName,
			"tool_call_id": e.ToolCallID,
			"preview":      truncateRunes(e.TextPreview, maxPreviewRunes),
		})
	case "MessageEvent":
		base.Kind = EventMessage
		base.Payload = mustJSON(map[string]any{
			"role":    e.Source,
			"preview": truncateRunes(e.TextPreview, maxPreviewRunes),
		})
	case "AgentErrorEvent":
		base.Kind = EventError
		base.Payload = mustJSON(map[string]any{
			"preview": truncateRunes(e.TextPreview, maxPreviewRunes),
		})
	default:
		base.Kind = EventStatus
		if raw := capEventPayload(e.Raw); raw != nil {
			base.Payload = raw
		} else {
			base.Payload = mustJSON(map[string]any{
				"sandbox_kind":  e.Kind,
				"unmapped_note": "stored from the unknown-kind fallback",
			})
		}
	}
	capped := capEventPayload(base.Payload)
	base.Payload = capped
	return base, true
}

// mustJSON marshals v, falling back to a fixed marker on failure. All inputs
// are plain maps of JSON-safe values, so the error branch is unreachable in
// practice; it exists so callers never handle a partial payload.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil || len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return b
}
