package agenttask

// The per-run event subscription (issue #1622).
//
// Everything else on this handler is a request and an answer. This one is a
// connection the server writes to for as long as a run is going, and it is
// here because the alternative is the shape PR #1709 shipped and said was not
// enough: a cursor the browser re-asks every three seconds, which quantises
// every step to that interval on top of the syncer's own. A step a person is
// waiting to watch happen cannot arrive on a schedule the person's browser
// picked.
//
// It is deliberately a tail of this process's own database rather than a
// subscription to anything further in. The syncer already writes each step as
// the sandbox produces it; the only thing missing between that row and a
// reader was a channel that does not wait to be asked. Adding a push all the
// way from the launcher would be a second delivery mechanism for the same
// rows, and the rows are already the durable record a reconnect resumes from.
//
// What this cannot carry, and no faster read of this table ever could:
// token-level model deltas. StreamingDeltaEvent is published to the agent
// server's PubSub and never persisted to the conversation event log, so it
// does not reach agent_task_events at all. That needs a relay out of the
// launcher, which is separate work on a separate seam.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	// defaultStreamTick is how often an open stream re-reads its task. Both
	// reads are indexed and scoped to one task, and they only happen while
	// somebody is watching, so this is bounded by open connections rather
	// than by rows.
	//
	// It is deliberately shorter than DefaultEventSyncInterval (2s): this
	// loop's job is to add as little as possible to the delay the syncer
	// already imposes, not to double it.
	defaultStreamTick = 500 * time.Millisecond

	// minStreamTick is a floor rather than a default. WithStreamTick is a
	// test seam today, but a seam that accepts 0 or a microsecond is one
	// spin loop away from being the reason this endpoint gets pulled.
	minStreamTick = time.Millisecond

	// streamCeiling bounds one connection. A task wedged in `running` would
	// otherwise hold a connection for as long as the browser tab is open,
	// which is the ceiling COWORK_FOLLOW_CEILING_MS already imposes on the
	// client side; this is the same number enforced on the side that pays
	// for it.
	streamCeiling = 30 * time.Minute

	// streamHeartbeat bounds how long a stream stays silent. Proxies and
	// load balancers close an idle connection, and a run thinking for a
	// minute is idle by their reckoning. A comment line is not an event, so
	// no client has to know about it.
	streamHeartbeat = 15 * time.Second

	// maxStreamPagesPerPass bounds one pass's catch-up. A conversation
	// reopened onto a long run drains over several passes rather than
	// holding the loop for an unbounded number of reads.
	maxStreamPagesPerPass = 5
)

// WithStreamTick sets how often an open event stream re-reads its task and
// returns the handler for chaining, matching the WithBilling shape used on
// the edge-api handlers. Values below minStreamTick are clamped.
func (h *Handler) WithStreamTick(d time.Duration) *Handler {
	if d < minStreamTick {
		d = minStreamTick
	}
	h.streamTick = d
	return h
}

func (h *Handler) tick() time.Duration {
	if h.streamTick <= 0 {
		return defaultStreamTick
	}
	return h.streamTick
}

// handleEventStream serves GET .../{task_id}/events/stream?after_seq=N as
// server-sent events: one `status` frame per status change, one `step` frame
// per event row, and one `end` frame when the run reaches a terminal status.
//
// The first read happens before any header is written, so a task that does
// not exist or does not belong to this caller is refused with an HTTP status
// like every other read here. Once the 200 and the event-stream content type
// are out there is no status left to send, and a client would render a
// refusal as a run that did nothing.
func (h *Handler) handleEventStream(w http.ResponseWriter, r *http.Request, tenantID, userID, taskID uuid.UUID) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Nothing here can work without a flush: every write would sit in a
		// buffer until the run ended, which is the defect rather than a
		// degraded version of the fix.
		writeJSON(w, http.StatusInternalServerError, errBody("streaming unsupported"))
		return
	}

	var cursor int64
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeJSON(w, http.StatusBadRequest, errBody(ErrCursor.Error()))
			return
		}
		cursor = parsed
	}

	ctx, cancel := context.WithTimeout(r.Context(), streamCeiling)
	defer cancel()

	task, err := h.svc.Get(ctx, tenantID, userID, taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Named for the proxies between here and a browser: nginx and Caddy both
	// read it, and a buffered event stream is a stream in name only.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	s := &streamWriter{w: w, flusher: flusher}
	status := task.Status
	if !s.send("status", newTaskWire(task)) {
		return
	}

	// Status first, events second, on this pass and on every later one. The
	// flush writes a run's remaining steps immediately before its terminal
	// transition (EventSyncer.FlushTask, called from the poller, from
	// chargeFailureBudget and from Service.Cancel), so a reader that saw the
	// terminal status has necessarily issued its event read afterwards, and
	// so after that flush. Reading the two concurrently reopens issue #1504
	// with every server-side test still green.
	cursor = h.drain(ctx, s, tenantID, userID, taskID, cursor)
	if s.failed {
		return
	}
	if status.Terminal() {
		s.send("end", endWire{Status: string(status)})
		return
	}

	ticker := time.NewTicker(h.tick())
	defer ticker.Stop()
	lastWrite := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		task, err = h.svc.Get(ctx, tenantID, userID, taskID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// The row is gone. End on the last status actually observed
				// rather than holding a connection open on a question nothing
				// can answer, and rather than inventing a status the run never
				// reached. A non-terminal one tells the client to go and look
				// for itself, which is exactly right here.
				s.send("end", endWire{Status: string(status)})
				return
			}
			// A blip. The next pass re-reads, and the ceiling bounds how long
			// this can go on for.
			continue
		}
		if task.Status != status {
			status = task.Status
			if !s.send("status", newTaskWire(task)) {
				return
			}
			lastWrite = time.Now()
		}

		before := s.written
		cursor = h.drain(ctx, s, tenantID, userID, taskID, cursor)
		if s.failed {
			return
		}
		if s.written != before {
			lastWrite = time.Now()
		}

		if status.Terminal() {
			s.send("end", endWire{Status: string(status)})
			return
		}
		if time.Since(lastWrite) >= streamHeartbeat {
			if !s.comment("ping") {
				return
			}
			lastWrite = time.Now()
		}
	}
}

// drain writes every event after cursor as its own frame and returns the new
// cursor. A read failure leaves the cursor where it was and is retried on the
// next pass: progress lines that pause are a worse-looking run, while a
// cursor advanced past rows nobody received is a run missing its middle.
func (h *Handler) drain(ctx context.Context, s *streamWriter, tenantID, userID, taskID uuid.UUID, cursor int64) int64 {
	for page := 0; page < maxStreamPagesPerPass; page++ {
		events, err := h.svc.Events(ctx, tenantID, userID, taskID, cursor, defaultEventsLimit)
		if err != nil || len(events) == 0 {
			return cursor
		}
		for _, ev := range events {
			if !s.send("step", ev) {
				return cursor
			}
			if ev.Seq > cursor {
				cursor = ev.Seq
			}
		}
		if len(events) < defaultEventsLimit {
			return cursor
		}
	}
	return cursor
}

// endWire is the last frame of a stream: the status the run settled in, so a
// client that missed the status frame carrying it still knows how the run
// ended rather than only that it stopped.
type endWire struct {
	Status string `json:"status"`
}

// streamWriter serialises one connection's frames and remembers whether a
// write has already failed, so the loop above can stop rather than spin
// writing into a closed socket.
type streamWriter struct {
	w       io.Writer
	flusher http.Flusher
	written int
	failed  bool
}

// send writes one named frame carrying payload as JSON, and reports whether
// the connection is still usable.
//
// json.Marshal never emits a raw newline inside a string, so one `data:` line
// always holds the whole payload and no frame can be split by its own
// content.
func (s *streamWriter) send(event string, payload any) bool {
	if s.failed {
		return false
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// Nothing honest to put on the wire for this one. Skipping it keeps
		// the rest of the run streaming, which is better than closing the
		// connection over one unencodable row.
		return true
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, body); err != nil {
		s.failed = true
		return false
	}
	s.flusher.Flush()
	s.written++
	return true
}

// comment writes an SSE comment line: bytes on the wire that no client reads
// as an event, which is what keeps an idle connection from being closed by
// something in the middle.
func (s *streamWriter) comment(text string) bool {
	if s.failed {
		return false
	}
	if _, err := fmt.Fprintf(s.w, ": %s\n\n", text); err != nil {
		s.failed = true
		return false
	}
	s.flusher.Flush()
	return true
}
