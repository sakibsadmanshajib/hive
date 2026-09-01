package inference

import (
	"encoding/json"
	"testing"
)

// ToolParamInBody (session chat path) and firstToolParam (API-key path) decide
// the same question from the same field list, one over a raw body and one over
// a parsed request. A disagreement is silent and dangerous: a body one calls
// tool-shaped and the other does not is a body dispatched without the
// capability check the other applied. This holds them equal.
func TestToolParamInBodyAgreesWithFirstToolParam(t *testing.T) {
	bodies := []string{
		`{"model":"m","messages":[]}`,
		`{"model":"m","tools":[{"type":"function"}]}`,
		`{"model":"m","tools":[]}`,
		`{"model":"m","tools":null}`,
		`{"model":"m","tool_choice":"auto"}`,
		`{"model":"m","response_format":{"type":"json_object"}}`,
		`{"model":"m","functions":[{"name":"f"}]}`,
		`{"model":"m","function_call":"auto"}`,
		`{"model":"m","parallel_tool_calls":true}`,
		`{"model":"m","parallel_tool_calls":false}`,
		`{"model":"m","parallel_tool_calls":null}`,
		`{"model":"m","tool_choice":"auto","tools":[{"type":"function"}]}`,
	}

	for _, body := range bodies {
		var req ChatCompletionRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatalf("fixture body does not parse: %s: %v", body, err)
		}
		want := firstToolParam(&req)
		got := ToolParamInBody([]byte(body))
		if got != want {
			t.Errorf("body %s: ToolParamInBody = %q, firstToolParam = %q. The two surfaces would gate the same request differently", body, got, want)
		}
	}
}

// A body that is not a JSON object cannot carry a tool block, and must not be
// reported as if it did.
func TestToolParamInBodyOnUnparseableInput(t *testing.T) {
	for _, body := range []string{"", "null", "[]", "not json at all"} {
		if got := ToolParamInBody([]byte(body)); got != "" {
			t.Errorf("input %q reported tool param %q", body, got)
		}
	}
}
