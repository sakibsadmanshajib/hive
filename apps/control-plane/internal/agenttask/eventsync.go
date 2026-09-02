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

	// offsets tracks how many of a task's sandbox events have already been
	// appended, so a pass writes only what is new. The source hands back the
	// conversation's whole event log every time, and AppendEvents issues one
	// INSERT per event inside a transaction holding a per-task advisory lock,
	// so re-appending the history every pass and letting the dedup index
	// throw it away costs work that grows with the square of the run's
	// length. At a cadence slow enough to be useless (the old 15s) that was
	// merely wasteful; at one fast enough for a step to appear while it is
	// happening it is the difference between affordable and not.
	//
	// An optimisation over a source this process does not control, so it is
	// never the only thing standing between an event and the database:
	// FlushTask reads the run from the beginning, and dedup makes the overlap
	// free.
	offsets map[uuid.UUID]int

	// syncedFiles is the same idea for the workspace listing, which has no
	// offset because it is a listing rather than a log: it is the set of file
	// event ids already appended for a task, and an entry whose name, size and
	// mtime are unchanged since the last pass is not written again. Without
	// it every pass re-writes one row per workspace file, which is the same
	// square-of-the-run cost the offset above removes, arriving by the other
	// door. Bounded by the workspace's top level and dropped with the task.
	syncedFiles map[uuid.UUID]map[string]bool

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
		interval = DefaultEventSyncInterval
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
		offsets:  make(map[uuid.UUID]int),

		syncedFiles: make(map[uuid.UUID]map[string]bool),
	}
}

// DefaultEventSyncInterval is how often an active task's sandbox is asked what
// it has done since the last pass.
//
// It is not the status poller's interval and must not be folded back into it.
// The two answer different questions on different clocks: a status is a fact
// about a run that is either over or not, while a step is something a person
// is waiting to watch happen, and a step that surfaces fifteen seconds after
// the agent took it is indistinguishable from a hang (issues #1622, #1504).
// This is deliberately close to the chat transcript's own 3s follow poll
// (COWORK_POLL_INTERVAL_MS in Chat.svelte), because the two are in series:
// the worst case a person waits is one of each.
const DefaultEventSyncInterval = 2 * time.Second

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
	pulled, next := s.pullTaskEvents(ctx, t, s.offsets[t.ID], s.syncedFiles[t.ID])
	events := append([]TaskEvent{statusEvent(t)}, pulled...)

	if err := s.repo.AppendEvents(ctx, t, events); err != nil {
		// Retried whole-batch next pass; dedup makes the retry idempotent.
		// The offset is deliberately NOT advanced here, so the events this
		// batch carried are pulled again rather than skipped.
		s.logger.WarnContext(ctx, "agenttask: event append failed, retrying next pass",
			"task_id", t.ID, "error", err)
		return
	}
	s.offsets[t.ID] = next
	s.rememberFiles(t.ID, events)
	s.remember(t)
}

// FlushTask appends everything t's session can still tell us, read from the
// beginning of its event log, and is the one pass that has to be complete.
//
// The poller calls it immediately before it records a terminal status
// (PollerConfig.FlushEvents). That ordering is the whole point: the chat
// transcript stops following a run the moment it reads a terminal status
// (followCoworkRun in vendor/open-webui/src/lib/components/chat/Chat.svelte),
// so a step written afterwards is written for nobody. Before this existed,
// the terminal transition and the tail pull were two loops on two unrelated
// schedules, and a run shorter than the sync interval lost that race every
// time: it rendered as a blank box for its whole life and then as a bare
// summary with no steps at all, which is issue #1504's live observation.
//
// Reading from zero rather than from the tracked offset is what makes this
// the reconciliation pass: dedup makes the overlap free, and anything the
// incremental pulls missed still lands. Failures are logged and swallowed,
// because this must never be able to stop a terminal status from being
// recorded: a run whose outcome is known and unwritten is worse than a run
// with missing steps.
// Called from the poller's goroutine while this syncer's own loop may be
// running, and deliberately touches none of the syncer's per-task bookkeeping
// (it reads from zero and skips nothing), so there is no shared mutable state
// between the two callers and nothing here needs a lock.
func (s *EventSyncer) FlushTask(ctx context.Context, t Task) {
	flushTaskEvents(ctx, s.repo, s.src, s.logger, t)
}

// flushTaskEvents is the shared body of that flush, because there are TWO
// writers of a terminal status and the invariant has to hold for both: the
// poller, for a run the engine finished, and Service.Cancel, for one the user
// stopped. A guarantee that holds on one of them is not a guarantee, and a
// cancelled run whose steps were recorded afterwards is the same blank box
// this change is about.
//
// Never able to stop a terminal status from being written: a nil source, a
// task that never launched, an unreadable session and a failed append all
// return quietly. A run whose outcome is known and unwritten is worse than a
// run with missing steps.
func flushTaskEvents(ctx context.Context, repo Repository, src EventSource, logger *slog.Logger, t Task) {
	if src == nil || t.EngineSessionRef == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	events, _ := pullSessionEvents(ctx, src, logger, t, 0, nil)
	if len(events) == 0 {
		return
	}
	if err := repo.AppendEvents(ctx, t, events); err != nil {
		logger.WarnContext(ctx, "agenttask: final event flush failed",
			"task_id", t.ID, "error", err)
	}
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
func (s *EventSyncer) pullTaskEvents(ctx context.Context, t Task, offset int, skipFiles map[string]bool) (events []TaskEvent, next int) {
	return pullSessionEvents(ctx, s.src, s.logger, t, offset, skipFiles)
}

// pullSessionEvents is that pull with no syncer attached, so the shared flush
// above can use it too.
func pullSessionEvents(ctx context.Context, src EventSource, logger *slog.Logger, t Task, offset int, skipFiles map[string]bool) (events []TaskEvent, next int) {
	next = offset

	sandboxEvents, err := src.Events(ctx, t.EngineSessionRef)
	if err != nil {
		logger.WarnContext(ctx, "agenttask: event sync pull failed",
			"task_id", t.ID, "error", err)
		return events, next
	}
	// The conversation's event log only grows, so what this pass has not seen
	// is its tail. A shorter log than the offset (a source that reset under
	// us) reads as everything being new rather than as a negative slice.
	next = len(sandboxEvents)
	if offset > 0 && offset <= len(sandboxEvents) {
		sandboxEvents = sandboxEvents[offset:]
	}
	for _, se := range sandboxEvents {
		if ev, ok := mapSandboxEvent(se); ok {
			events = append(events, ev)
		}
	}

	files, ferr := src.Files(ctx, t.EngineSessionRef)
	if ferr != nil {
		logger.WarnContext(ctx, "agenttask: workspace listing failed",
			"task_id", t.ID, "error", ferr)
		return events, next
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
		ev := fileEvent(f)
		// Unchanged since the last successful append, so the row is already
		// stored and re-sending it only asks the database to reject it. Skipped
		// only on the incremental path: skipFiles is empty for FlushTask, which
		// has to write the complete listing.
		if skipFiles[ev.SourceEventID] {
			continue
		}
		events = append(events, ev)
	}
	return events, next
}

// rememberFiles records the file event ids in an appended batch, so the next
// pass can skip the entries that have not changed. Called only after the batch
// landed: an entry recorded ahead of a failed append would be skipped forever.
func (s *EventSyncer) rememberFiles(id uuid.UUID, events []TaskEvent) {
	for _, ev := range events {
		if ev.Kind != EventFile {
			continue
		}
		if s.syncedFiles[id] == nil {
			s.syncedFiles[id] = make(map[string]bool)
		}
		s.syncedFiles[id][ev.SourceEventID] = true
	}
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
			tail, _ := s.pullTaskEvents(ctx, final, s.offsets[id], s.syncedFiles[id])
			events := append(tail, statusEvent(final))
			if aerr := s.repo.AppendEvents(ctx, t, events); aerr != nil {
				s.logger.WarnContext(ctx, "agenttask: final event append failed",
					"task_id", id, "error", aerr)
			}
		}
		delete(s.seen, id)
		delete(s.offsets, id)
		delete(s.syncedFiles, id)
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
