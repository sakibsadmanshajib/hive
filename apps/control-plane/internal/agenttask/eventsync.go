package agenttask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
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
	events := append([]TaskEvent{statusEvent(t)}, s.pullTaskEvents(ctx, t)...)

	if err := s.repo.AppendEvents(ctx, t, events); err != nil {
		// Retried whole-batch next pass; dedup makes the retry idempotent.
		s.logger.WarnContext(ctx, "agenttask: event append failed, retrying next pass",
			"task_id", t.ID, "error", err)
		return
	}
	s.remember(t)
}

// pullTaskEvents pulls t's sandbox events and workspace listing and maps them
// onto TaskEvent rows, in that order. Both syncTask (t is still active) and
// finishVanished (t just left the active set, this is its last possible
// pass) call this: a short task's real activity, tool calls and file writes,
// often concentrates in the tail of its run, and skipping this pull on the
// final pass is exactly how issue #1206's run went dead for 58 straight
// seconds and never recorded the tool_call/tool_result events a genuine
// terminal command and file write produced. Every failure here logs against
// t alone; the caller still gets whatever partial slice was built.
func (s *EventSyncer) pullTaskEvents(ctx context.Context, t Task) []TaskEvent {
	var events []TaskEvent

	sandboxEvents, err := s.pullSandboxEvents(ctx, t.EngineSessionRef)
	if err != nil {
		s.logger.WarnContext(ctx, "agenttask: event sync pull failed",
			"task_id", t.ID, "error", err)
		return events
	}
	for _, se := range sandboxEvents {
		if ev, ok := mapSandboxEvent(se); ok {
			events = append(events, ev)
		}
	}

	files, ferr := s.src.Files(ctx, t.EngineSessionRef)
	if ferr != nil {
		s.logger.WarnContext(ctx, "agenttask: workspace listing failed",
			"task_id", t.ID, "error", ferr)
		return events
	}
	for _, f := range files {
		// A dot-prefixed entry (.git, .cache, ...) is workspace scaffolding,
		// never something the agent produced for the user; rendering it as a
		// step is exactly the "bare .git listing reads as progress" defect
		// #1206 also named. Filtered here, at the same layer as every other
		// not-an-agent-action exclusion below, rather than left for the
		// frontend to guess at.
		if strings.HasPrefix(f.Name, ".") {
			continue
		}
		events = append(events, fileEvent(f))
	}
	return events
}

// remember records t as still-active-seen.
func (s *EventSyncer) remember(t Task) {
	if s.seen == nil {
		s.seen = make(map[uuid.UUID]Task)
	}
	s.seen[t.ID] = t
}

// finishVanished emits the terminal status event, plus one last sandbox
// events and workspace listing pull, for tasks that left the active set
// since they were last seen, then forgets them. A transient Get failure
// drops the tracking entry anyway: a missed terminal status event is
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
			// This is t's last possible pass: it has already left ListActive,
			// so no future syncTask call will ever run for it again. Pull the
			// tail one last time before recording the terminal status, or
			// whatever tool calls and file writes happened between the last
			// active pass and completion are lost for good, never a retry.
			events := append(s.pullTaskEvents(ctx, final), statusEvent(final))
			if aerr := s.repo.AppendEvents(ctx, t, events); aerr != nil {
				s.logger.WarnContext(ctx, "agenttask: final event append failed",
					"task_id", id, "error", aerr)
			}
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
// drift costs one function, and the fallback for a kind this switch does not
// name NEVER drops silently: it lands as kind `status` carrying the raw
// payload (or a marker when the raw dump was too large to transport), so a
// new upstream event class surfaces in the transcript instead of vanishing
// unexplained.
//
// That is a different thing from the false returns below. Those are named,
// understood kinds this function recognises and deliberately excludes
// because they are bookkeeping, not agent progress: SystemPromptEvent and
// ConversationStateUpdateEvent are bootstrap/state-sync noise confirmed
// against a live run's stored payloads (task a98420c4, issue #1206), where
// three of five rendered lines were exactly this pair falling through the
// unmapped branch with neither a `sandbox_kind` nor a `status` key the
// frontend understood. A user-role MessageEvent is excluded for a narrower,
// concrete reason: agent-engine's only way to put a user message into this
// stream is the task's own initiating prompt (InitialMessage at launch,
// engine.go); there is no running-task follow-up-message surface today, so
// every user-role MessageEvent that will ever reach this function IS that
// same prompt echoed back, not a real interjection.
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
		if e.Source == "user" {
			return TaskEvent{}, false
		}
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
	case "SystemPromptEvent", "ConversationStateUpdateEvent":
		return TaskEvent{}, false
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
