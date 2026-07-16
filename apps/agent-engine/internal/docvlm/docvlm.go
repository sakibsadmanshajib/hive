// Package docvlm builds the OpenAI-compatible chat-completion request that
// the doc-layout knowledge-work-pack skill sends to the doc-layout vision
// route (ModelName, deploy/litellm/config.yaml) for contract and PDF page
// understanding. It only builds and validates the request; the agent's own
// shell/HTTP tool inside the sandbox issues the actual call against Hive's
// OpenAI-compatible chat completions endpoint (see
// apps/agent-engine/packs/knowledge-work-pack/skills/doc-layout/AGENTS.md for
// the exact invocation and the response JSON shape the model is asked for).
package docvlm

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ModelName is the deploy/litellm/config.yaml model_list entry this skill
// targets. It must name a vision-capable route (see
// TestLiteLLMConfigHasDocVLMRoute).
const ModelName = "route-doc-vlm"

// MaxImageBytes bounds one page's decoded image size (10 MiB), mirroring
// apps/edge-api/internal/artifacts.MaxHTMLSize's request-body-cap pattern.
const MaxImageBytes = 10 << 20

// allowedMimeTypes are the image types the doc-layout vision route accepts.
var allowedMimeTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// systemPrompt frames the model's task: return structured layout, never
// prose. Kept in code (not a template file) so it stays byte-for-byte in
// sync with the request the tests exercise.
const systemPrompt = `You are a document-layout extraction model. Given one ` +
	`or more page images from a contract or PDF, identify structural ` +
	`elements (heading, paragraph, table, figure, signature_block, ` +
	`page_number) with the page index and the extracted text for each. ` +
	`Respond with a single JSON object only, no prose: ` +
	`{"pages":[{"page":0,"elements":[{"type":"...","text":"..."}]}]}.`

// Page is one document page image to send to the vision model.
type Page struct {
	Index    int    // 0-based page number
	ImageB64 string // base64-encoded image bytes, no data: URI prefix
	MimeType string // e.g. "image/png", "image/jpeg"
}

// ImageURL carries a data: URI image (no external fetch, matches the
// sandbox's locked-down egress: the page bytes travel inline in the
// request body, never as a fetchable URL).
type ImageURL struct {
	URL string `json:"url"`
}

// ContentPart is one part of a multi-part chat message (OpenAI vision
// content-array shape).
type ContentPart struct {
	Type     string    `json:"type"` // "text" or "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// Message is one chat-completion message. Content is always a part array
// (rather than a bare string for non-vision roles): every mainstream
// OpenAI-compatible provider accepts array content on any role, and a
// single shape keeps this package's one job (building this one request)
// boring.
type Message struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

// ResponseFormat asks for a JSON object response, not free-form prose;
// json_object mode (rather than a strict json_schema) is used for
// broadest compatibility with the free-tier vision route.
type ResponseFormat struct {
	Type string `json:"type"`
}

// Request is the OpenAI-compatible chat-completion request body this
// package builds.
type Request struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	ResponseFormat ResponseFormat `json:"response_format"`
}

// BuildRequest validates pages and renders them, together with
// instructions, into a Request targeting ModelName. instructions is the
// caller's extraction goal (e.g. "extract the signature block") and is
// embedded as the first content part of the user message.
func BuildRequest(pages []Page, instructions string) (Request, error) {
	if len(pages) == 0 {
		return Request{}, errors.New("docvlm: at least one page is required")
	}
	if strings.TrimSpace(instructions) == "" {
		return Request{}, errors.New("docvlm: instructions must not be blank")
	}

	userParts := make([]ContentPart, 0, len(pages)+1)
	userParts = append(userParts, ContentPart{Type: "text", Text: instructions})
	for _, p := range pages {
		if !allowedMimeTypes[p.MimeType] {
			return Request{}, fmt.Errorf("docvlm: page %d has unsupported mime type %q (allowed: image/png, image/jpeg, image/webp)", p.Index, p.MimeType)
		}
		decoded, err := base64.StdEncoding.DecodeString(p.ImageB64)
		if err != nil {
			return Request{}, fmt.Errorf("docvlm: page %d has invalid base64 image data: %w", p.Index, err)
		}
		if len(decoded) == 0 {
			return Request{}, fmt.Errorf("docvlm: page %d has no image bytes", p.Index)
		}
		if len(decoded) > MaxImageBytes {
			return Request{}, fmt.Errorf("docvlm: page %d image is %d bytes, exceeds the %d byte cap", p.Index, len(decoded), MaxImageBytes)
		}
		userParts = append(userParts, ContentPart{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: fmt.Sprintf("data:%s;base64,%s", p.MimeType, p.ImageB64)},
		})
	}

	return Request{
		Model: ModelName,
		Messages: []Message{
			{Role: "system", Content: []ContentPart{{Type: "text", Text: systemPrompt}}},
			{Role: "user", Content: userParts},
		},
		ResponseFormat: ResponseFormat{Type: "json_object"},
	}, nil
}
