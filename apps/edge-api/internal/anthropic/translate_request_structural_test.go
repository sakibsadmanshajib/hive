package anthropic_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
)

// assertEveryFieldDispositioned walks v's struct type with reflection and
// fails, naming the field, for any exported field whose JSON name has no
// entry in dispositions. This is the actual structural guard issue #1153
// asked for: TestToOAIRequest_MaximalRequest_NoFieldLost (added earlier in
// this PR) only checks that the fields present in ONE hand-written request
// survive -- it says nothing about a field nobody thought to add to that
// request in the first place, which is exactly how cache_control and the six
// fields in this PR went missing to begin with. Reflection over the struct's
// own field list, not over one instance's populated values, is what closes
// that gap: a field literally cannot exist on the struct without a matching
// map entry, or this test fails and names it.
//
// dispositions values are documentation, not machine-checked against the
// actual translator code (Go has no way to assert "field X flows into
// translator branch Y" without something at parser-level); they exist so a
// reviewer or the next developer can see, at a glance, whether a field is
// carried through unchanged, translated to a different shape/name,
// forwarded as an opaque passthrough, or deliberately rejected -- the same
// four buckets the PR's own field-disposition writeup uses.
//
// t is testing.TB (not *testing.T) so
// TestStructuralGuard_CatchesAnUndispositionedField can pass a recording
// stand-in and observe a failure instead of actually failing.
func assertEveryFieldDispositioned(t testing.TB, v interface{}, dispositions map[string]string) {
	t.Helper()
	typ := reflect.TypeOf(v)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.PkgPath != "" {
			continue // unexported, not part of the wire shape
		}
		tag := f.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			name = f.Name
		}
		if _, ok := dispositions[name]; !ok {
			t.Errorf(
				"%s.%s (json %q) has no disposition entry in this test -- "+
					"a field was added to the struct without deciding what "+
					"ToOAIRequest does with it. Add an entry to the "+
					"dispositions map for this type stating whether it is "+
					"carried through unchanged, translated to an equivalent, "+
					"forwarded as an opaque passthrough, or deliberately "+
					"rejected, and wire it up in translate_request.go to match.",
				typ.Name(), f.Name, name,
			)
		}
	}
}

// messagesRequestDispositions documents every MessagesRequest field's fate
// in ToOAIRequest. See assertEveryFieldDispositioned's doc comment.
var messagesRequestDispositions = map[string]string{
	"model":          "carried: OAIRequest.Model",
	"messages":       "carried: OAIRequest.Messages, each Message recursively lowered by convertMessage",
	"system":         "carried: prepended as an OAIMessage{Role:\"system\"} via systemContent",
	"max_tokens":     "carried: OAIRequest.MaxTokens",
	"tools":          "carried: OAIRequest.Tools via convertTools",
	"tool_choice":    "translated: OAIRequest.ToolChoice via convertToolChoice, plus a ParallelToolCalls side effect from disable_parallel_tool_use; an unrecognized type is rejected with an error, not silently defaulted",
	"temperature":    "carried: OAIRequest.Temperature",
	"top_p":          "carried: OAIRequest.TopP",
	"top_k":          "forwarded: OAIRequest.TopK, opaque passthrough field with no OpenAI-standard equivalent",
	"stop_sequences": "carried: OAIRequest.Stop",
	"stream":         "carried: OAIRequest.Stream",
	"metadata":       "translated: OAIRequest.User (metadata.user_id only; no other metadata sub-field is documented by Anthropic today)",
	"thinking":       "carried: OAIRequest.Thinking, unchanged shape (see ThinkingConfig's own sub-fields, not covered by this reflection pass -- see file doc comment)",
	"cache_control":  "carried: OAIRequest.CacheControl",
	"session_id":     "carried: OAIRequest.SessionID, truncated to 256 bytes at a rune boundary",
}

func TestMessagesRequest_EveryFieldHasADisposition(t *testing.T) {
	assertEveryFieldDispositioned(t, anthropic.MessagesRequest{}, messagesRequestDispositions)
}

// contentBlockDispositions documents every ContentBlock field. ContentBlock
// is a union of six Anthropic block shapes (text, image, tool_use,
// tool_result, thinking, redacted_thinking) folded into one Go struct, so
// "carried" here means "carried when this block's Type selects the branch
// that reads this field", not "always present in the output".
var contentBlockDispositions = map[string]string{
	"type":          "carried: selects the case in convertMessage's block switch",
	"text":          "carried: type=text -> OAIContentPart.Text",
	"source":        "carried: type=image -> OAIContentPart.ImageURL (base64 or url form both lowered to a data/plain URI)",
	"id":            "carried: type=tool_use -> OAIToolCall.ID",
	"name":          "carried: type=tool_use -> OAIToolCall.Function.Name",
	"input":         "carried: type=tool_use -> OAIToolCall.Function.Arguments (JSON-stringified)",
	"tool_use_id":   "carried: type=tool_result -> OAIMessage.ToolCallID",
	"content":       "carried: type=tool_result -> OAIMessage.Content via toolResultText",
	"thinking":      "carried: type=thinking -> OAIThinkingBlock.Thinking",
	"signature":     "carried: type=thinking -> OAIThinkingBlock.Signature",
	"data":          "carried: type=redacted_thinking -> OAIThinkingBlock.Data",
	"cache_control": "carried: any block type -> the corresponding OAI-shaped type's CacheControl field",
}

func TestContentBlock_EveryFieldHasADisposition(t *testing.T) {
	assertEveryFieldDispositioned(t, anthropic.ContentBlock{}, contentBlockDispositions)
}

var toolDispositions = map[string]string{
	"name":          "carried: OAITool.Function.Name",
	"description":   "carried: OAITool.Function.Description",
	"input_schema":  "carried: OAITool.Function.Parameters",
	"cache_control": "carried: OAITool.CacheControl",
}

func TestTool_EveryFieldHasADisposition(t *testing.T) {
	assertEveryFieldDispositioned(t, anthropic.Tool{}, toolDispositions)
}

var toolChoiceDispositions = map[string]string{
	"type":                      "translated: selects OAIToolChoice's sentinel or named form via convertToolChoice",
	"name":                      "carried: type=tool -> OAINamedToolChoiceFunction.Name",
	"disable_parallel_tool_use": "translated: OAIRequest.ParallelToolCalls, inverse boolean, only ever emitted as false",
}

func TestToolChoice_EveryFieldHasADisposition(t *testing.T) {
	assertEveryFieldDispositioned(t, anthropic.ToolChoice{}, toolChoiceDispositions)
}

// TestStructuralGuard_CatchesAnUndispositionedField is the demonstration the
// review asked for: the guard above must actually fail, not just exist, when
// a field is added without a disposition. reflect.StructOf builds a type
// with every MessagesRequest field plus one undocumented extra ("reasoning",
// a plausible-looking future Anthropic field) at runtime, so this test does
// not require hand-editing types.go to prove the point -- it proves the
// guard function itself is sound, independent of the current field list, and
// it runs on every `go test` rather than needing a human to remember to
// revert a manual mutation.
func TestStructuralGuard_CatchesAnUndispositionedField(t *testing.T) {
	real := reflect.TypeOf(anthropic.MessagesRequest{})
	fields := make([]reflect.StructField, 0, real.NumField()+1)
	for i := 0; i < real.NumField(); i++ {
		fields = append(fields, real.Field(i))
	}
	fields = append(fields, reflect.StructField{
		Name: "Reasoning",
		Type: reflect.TypeOf(""),
		Tag:  `json:"reasoning,omitempty"`,
	})
	withExtraField := reflect.New(reflect.StructOf(fields)).Elem().Interface()

	rec := &recordingTB{TB: t}
	assertEveryFieldDispositioned(rec, withExtraField, messagesRequestDispositions)

	if !rec.failed {
		t.Fatal("assertEveryFieldDispositioned did not fail for a field " +
			"(\"reasoning\") missing from the dispositions map -- the " +
			"structural guard is not structural")
	}
	if !strings.Contains(rec.lastMsg, "reasoning") {
		t.Fatalf("failure message did not name the missing field: %q", rec.lastMsg)
	}
}

// recordingTB wraps a real testing.TB and captures Errorf calls instead of
// failing the outer test, so TestStructuralGuard_CatchesAnUndispositionedField
// can assert on the failure itself.
type recordingTB struct {
	testing.TB
	failed  bool
	lastMsg string
}

func (r *recordingTB) Errorf(format string, args ...interface{}) {
	r.failed = true
	r.lastMsg = fmt.Sprintf(format, args...)
}
