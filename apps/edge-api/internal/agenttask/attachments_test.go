package agenttask

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Issue #1065. POST /v1/agent/tasks is the surface a Cowork run is submitted
// on, and until this change it accepted a pack and a prompt and nothing else,
// so a document the person attached in the composer had nowhere to go and the
// composer refused the send outright.
//
// These assert what the handler forwards, not merely that it answers 201: the
// defect this closes is a value that is accepted and then never arrives.

// attachmentReq builds a create request with the given body, authenticated as
// an ordinary tenant user, the way createReq does for the pack-only case.
func attachmentReq(t *testing.T, body map[string]any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(raw))
	return req.WithContext(userCtx(uuid.New()))
}

func TestHandler_Create_ForwardsAttachments(t *testing.T) {
	fc := newFakeClient()
	h := billedHandler(t, fc)

	req := attachmentReq(t, map[string]any{
		"pack":         "knowledge-work-pack",
		"instructions": "Summarise the attached inventory.",
		"attachments": []map[string]any{
			{"name": "inventory.txt", "content": "QAFILE7731"},
		},
	})
	rr := httptest.NewRecorder()
	h.routeTasks(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if len(fc.lastAttachments) != 1 {
		t.Fatalf("control-plane received %d attachments, want 1", len(fc.lastAttachments))
	}
	if fc.lastAttachments[0].Name != "inventory.txt" || fc.lastAttachments[0].Content != "QAFILE7731" {
		t.Fatalf("attachment forwarded as %+v", fc.lastAttachments[0])
	}
}

// A name that is a path is refused before control-plane is called at all. The
// launcher validates it again (it is the process that turns it into a path),
// but a request that cannot be honoured should not take a credit hold or
// create a row first.
func TestHandler_Create_RefusesAttachmentNamesThatAreNotFileNames(t *testing.T) {
	for _, name := range []string{
		"", "  ", "..", "../escape.txt", "nested/file.txt", `back\slash.txt`,
		// A newline in a name would forge a line in the bullet list the run's
		// initial message repeats the names back to the model as.
		"a\x00b.txt", "a\nb.txt",
	} {
		t.Run(name, func(t *testing.T) {
			fc := newFakeClient()
			h := billedHandler(t, fc)
			req := attachmentReq(t, map[string]any{
				"pack":        "knowledge-work-pack",
				"attachments": []map[string]any{{"name": name, "content": "x"}},
			})
			rr := httptest.NewRecorder()
			h.routeTasks(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d for name %q, want 400", rr.Code, name)
			}
			if fc.createCalled {
				t.Fatalf("a refused attachment name still reached control-plane")
			}
		})
	}
}

func TestHandler_Create_RefusesAnEmptyAttachment(t *testing.T) {
	fc := newFakeClient()
	h := billedHandler(t, fc)
	req := attachmentReq(t, map[string]any{
		"pack":        "knowledge-work-pack",
		"attachments": []map[string]any{{"name": "empty.txt", "content": ""}},
	})
	rr := httptest.NewRecorder()
	h.routeTasks(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if fc.createCalled {
		t.Fatalf("an empty attachment still reached control-plane")
	}
}

// The ceiling is stated rather than discovered. Truncating would hand the
// agent a document that silently stops mid sentence, which is worse than a
// refusal the person can act on.
func TestHandler_Create_RefusesAttachmentsOverTheTotalCap(t *testing.T) {
	fc := newFakeClient()
	h := billedHandler(t, fc)
	req := attachmentReq(t, map[string]any{
		"pack":        "knowledge-work-pack",
		"attachments": []map[string]any{{"name": "big.txt", "content": strings.Repeat("a", maxAttachmentBytes+1)}},
	})
	rr := httptest.NewRecorder()
	h.routeTasks(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if fc.createCalled {
		t.Fatalf("an oversized attachment still reached control-plane")
	}
}

func TestHandler_Create_RefusesTooManyAttachments(t *testing.T) {
	fc := newFakeClient()
	h := billedHandler(t, fc)
	items := make([]map[string]any, 0, maxAttachments+1)
	for i := 0; i <= maxAttachments; i++ {
		items = append(items, map[string]any{"name": fmt.Sprintf("f%d.txt", i), "content": "x"})
	}
	req := attachmentReq(t, map[string]any{"pack": "knowledge-work-pack", "attachments": items})
	rr := httptest.NewRecorder()
	h.routeTasks(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if fc.createCalled {
		t.Fatalf("too many attachments still reached control-plane")
	}
}

// A document is larger than a prompt. The route's old 64 KiB body reader
// would have rejected a perfectly legal attachment as malformed JSON, which
// reads to the user as a broken upload rather than a limit.
func TestHandler_Create_AcceptsABodyLargerThanAPrompt(t *testing.T) {
	fc := newFakeClient()
	h := billedHandler(t, fc)
	body := strings.Repeat("b", 200<<10)
	req := attachmentReq(t, map[string]any{
		"pack":        "knowledge-work-pack",
		"attachments": []map[string]any{{"name": "long.txt", "content": body}},
	})
	rr := httptest.NewRecorder()
	h.routeTasks(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if len(fc.lastAttachments) != 1 || fc.lastAttachments[0].Content != body {
		t.Fatalf("the attachment did not survive the body reader intact")
	}
}
