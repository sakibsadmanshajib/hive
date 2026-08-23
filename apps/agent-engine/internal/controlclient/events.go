package controlclient

// Events half of controlclient: the OpenHands event-search client and the
// normalization layer. Verified against vendored source:
// event_router.py search_conversation_events returns
// {"items": [transport dumps], "next_page_id"}; ActionEvent carries
// thought/tool_name/tool_call_id/summary; ObservationBaseEvent subclasses
// (ObservationEvent, UserRejectObservation, agent error) carry
// tool_name/tool_call_id; MessageEvent carries llm_message.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Page bounds. maxEventsPageLimit matches the server's own lte=100 constraint.
const (
	maxEventsPageLimit = 100
	maxEventPages      = 50 // 100 x 50 = 5000 events per call; the rest defers to later calls (dedup makes re-pulls free)
)

// Event is one normalized sandbox event: exactly what the six-kind mapping
// consumes plus the raw dump for the unknown-kind fallback. Kind is the
// sandbox kind name ("ActionEvent" etc).
type Event struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Source      string          `json:"source"`
	Timestamp   string          `json:"timestamp"`
	ToolName    string          `json:"tool_name"`
	ToolCallID  string          `json:"tool_call_id"`
	TextPreview string          `json:"text_preview"`
	Raw         json.RawMessage `json:"raw,omitempty"`
}

// WorkspaceFile is one entry of a session workspace listing: name, size,
// mtime only. No file content ever crosses this boundary.
type WorkspaceFile struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// SearchEvents calls GET /api/conversations/{id}/events/search
// (conversation-scoped event store, the sandbox's source of truth for a run)
// and follows next_page_id pagination up to maxEventPages.
func (c *Client) SearchEvents(ctx context.Context, conversationID uuid.UUID) ([]Event, error) {
	var all []Event
	pageID := ""
	for page := 0; ; page++ {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(maxEventsPageLimit))
		if pageID != "" {
			q.Set("page_id", pageID)
		}
		path := fmt.Sprintf("/api/conversations/%s/events/search?%s", conversationID, q.Encode())

		var body struct {
			Items      []json.RawMessage `json:"items"`
			NextPageID string            `json:"next_page_id"`
		}
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &body); err != nil {
			return nil, err
		}

		for _, raw := range body.Items {
			all = append(all, normalizeEvent(raw))
		}
		if body.NextPageID == "" || page+1 >= maxEventPages {
			return all, nil
		}
		pageID = body.NextPageID
	}
}

// normalizeEvent extracts the mapping fields from one transport dump. The
// preview extractor walks the dump generically so SDK schema drift costs
// nothing here: per-kind fields are preferred where the vendored classes name
// them, and everything else falls back to a capped string scan of the payload.
func normalizeEvent(raw json.RawMessage) Event {
	var dump map[string]any
	if err := json.Unmarshal(raw, &dump); err != nil {
		return Event{Kind: "UnparseableEvent", Raw: json.RawMessage(`{"unparseable":true}`)}
	}
	ev := Event{Raw: raw}
	ev.ID = str(dump["id"])
	ev.Kind = str(dump["kind"])
	ev.Source = str(dump["source"])
	ev.Timestamp = str(dump["timestamp"])

	switch ev.Kind {
	case "ActionEvent":
		ev.ToolName = str(dump["tool_name"])
		ev.ToolCallID = str(dump["tool_call_id"])
		var b strings.Builder
		for _, t := range asList(dump["thought"]) {
			if tm, ok := t.(map[string]any); ok {
				b.WriteString(str(tm["text"]))
				b.WriteString(" ")
			}
		}
		if s := str(dump["summary"]); s != "" {
			b.WriteString(s)
		}
		ev.TextPreview = strings.TrimSpace(b.String())
	case "MessageEvent":
		msg, _ := dump["llm_message"].(map[string]any)
		var b strings.Builder
		for _, c := range asList(msg["content"]) {
			cm, _ := c.(map[string]any)
			b.WriteString(str(cm["text"]))
			b.WriteString(" ")
		}
		ev.TextPreview = strings.TrimSpace(b.String())
	case "ObservationEvent", "UserRejectObservation":
		ev.ToolName = str(dump["tool_name"])
		ev.ToolCallID = str(dump["tool_call_id"])
		ev.TextPreview = scanStrings(dump["observation"], 4000)
	default:
		// AgentErrorEvent names its field; other kinds get the generic scan.
		if e := str(dump["error"]); e != "" {
			ev.TextPreview = e
		} else {
			ev.TextPreview = scanStrings(dump, 2000)
		}
	}
	return ev
}

// scanStrings collects string leaves from v depth-first until budget runes,
// joined with spaces. Generic on purpose: it is the drift guard.
func scanStrings(v any, budget int) string {
	var b strings.Builder
	var walk func(n any)
	walk = func(n any) {
		if b.Len() >= budget {
			return
		}
		switch t := n.(type) {
		case string:
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			b.WriteString(t)
		case []any:
			for _, c := range t {
				walk(c)
				if b.Len() >= budget {
					return
				}
			}
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
				if b.Len() >= budget {
					return
				}
			}
		}
	}
	walk(v)
	return b.String()
}

func str(v any) string { s, _ := v.(string); return s }

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}
