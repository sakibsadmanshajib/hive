package anthropic

import (
	"encoding/json"
	"net/http"
	"time"
)

// ModelsListResponse is the Anthropic GET /v1/models response envelope.
// Verified against the live specification on 2026-08-28
// (https://docs.claude.com/en/api/models-list): a data array of model objects
// each carrying type/id/display_name/created_at, plus has_more and the
// nullable first_id / last_id pagination cursors.
//
// FirstID and LastID are pointers because the specification types them as
// "string or null" and a real client distinguishes an empty page from a page
// whose cursor happens to be the empty string.
type ModelsListResponse struct {
	Data    []ModelInfo `json:"data"`
	HasMore bool        `json:"has_more"`
	FirstID *string     `json:"first_id"`
	LastID  *string     `json:"last_id"`
}

// ModelInfo is one entry in an Anthropic model list.
type ModelInfo struct {
	Type        string `json:"type"` // always "model"
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"` // RFC 3339
}

// openAIModelList is the OpenAI-shaped body this package translates FROM. Only
// the three fields that have an Anthropic counterpart are read; owned_by and
// the rest are deliberately dropped rather than passed through, which also
// keeps this translation strictly leak-reducing with respect to the
// provider-blind invariant.
type openAIModelList struct {
	Data []struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Name    string `json:"name"`
	} `json:"data"`
}

// IsAnthropicClient reports whether a request came from an Anthropic-shaped
// client rather than an OpenAI-shaped one.
//
// Both headers are load-bearing. anthropic-version is sent by every official
// Anthropic SDK on every request and by nothing else, so it identifies the
// dialect even when the caller authenticates with Authorization: Bearer.
// x-api-key is the SDK's default credential header and catches a hand-rolled
// client that copied the documented curl invocation. An OpenAI-shaped caller
// sends neither, so the OpenAI response shape stays the default and Open WebUI
// (which is what actually consumes GET /v1/models on this deployment) is
// unaffected.
func IsAnthropicClient(r *http.Request) bool {
	return r.Header.Get("anthropic-version") != "" || r.Header.Get("x-api-key") != ""
}

// ModelsCompat wraps the OpenAI-shaped GET /v1/models handler so an Anthropic
// SDK client gets an Anthropic-shaped answer: the documented list envelope on
// success, and the Anthropic error envelope ({"type":"error","error":{...}})
// instead of the OpenAI one on a refusal, which is what
// anthropic.APIStatusError actually reads its .type from (issue #1259).
//
// It wraps rather than modifies the underlying handler so authentication,
// tenant filtering and the OpenAI-shaped contract every other caller depends
// on all stay exactly where they are: this only re-shapes bytes that handler
// already decided to emit. Buffering the whole body is safe here and only
// here, because a model list is small and never streamed.
func ModelsCompat(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// One URL, two representations, chosen by request headers. Nothing on
		// this route sets Cache-Control and the route needs a credential, so no
		// correct cache stores it today, but any intermediary keying on URL
		// alone (or an edge cache added in front of Caddy later) could hand an
		// Anthropic-shaped body to an OpenAI-shaped caller and empty Open
		// WebUI's model picker. Declared on both branches, before either
		// writes, since a Vary set after WriteHeader is a Vary nobody sends.
		w.Header().Set("Vary", "anthropic-version, x-api-key")

		if !IsAnthropicClient(r) {
			next.ServeHTTP(w, r)
			return
		}

		rec := &headerlessRecorder{}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		if rec.status < 200 || rec.status > 299 {
			// reshapeInto, not a bare reshape: a refusal from
			// authorizeAliasRequest carries retry-after and the x-ratelimit-*
			// family on the recorder, and an Anthropic client reads them for
			// its backoff exactly as an OpenAI-shaped one does.
			rec.reshapeInto(w)
			return
		}

		var list openAIModelList
		if err := json.Unmarshal(rec.body.Bytes(), &list); err != nil {
			writeAnthropicError(w, http.StatusBadGateway, "the model list could not be returned", "upstream_error")
			return
		}

		out := ModelsListResponse{Data: make([]ModelInfo, 0, len(list.Data))}
		for _, m := range list.Data {
			display := m.Name
			if display == "" {
				display = m.ID
			}
			out.Data = append(out.Data, ModelInfo{
				Type:        "model",
				ID:          m.ID,
				DisplayName: display,
				CreatedAt:   time.Unix(m.Created, 0).UTC().Format(time.RFC3339),
			})
		}
		if len(out.Data) > 0 {
			first := out.Data[0].ID
			last := out.Data[len(out.Data)-1].ID
			out.FirstID = &first
			out.LastID = &last
		}
		// HasMore stays false: this route serves the caller's whole entitled
		// catalog in one response and honours no pagination cursor, so
		// claiming another page exists would send a client into a loop.

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(out); err != nil {
			return
		}
	})
}
