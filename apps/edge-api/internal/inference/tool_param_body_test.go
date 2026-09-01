package inference

import (
	"encoding/json"
	"testing"
)

// ToolParamInBody (session chat path) and firstToolParam (API-key path) must
// decide the same question about the same body. A disagreement is silent and
// dangerous: a body one calls tool-shaped and the other does not is a body
// dispatched without the capability check the other applied.
//
// The mixed-case rows are the ones that matter, and they were absent when this
// test was first written, which is why the table read as evidence while the
// invariant was false. encoding/json matches struct field names case
// insensitively, so `{"Tools": ...}` is tool-shaped to the typed decoder; an
// exact-key map lookup called it plain. These rows fail against that
// implementation and pass against the delegating one.
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
		`{"model":"m","Tools":[{"type":"function"}]}`,
		`{"model":"m","TOOLS":[{"type":"function"}]}`,
		`{"model":"m","Tool_Choice":"auto"}`,
		`{"model":"m","Response_Format":{"type":"json_object"}}`,
		`{"model":"m","Parallel_Tool_Calls":true}`,
		`{"model":"m","tools":[{"type":"function"}],"tools":null}`,
		// Two SPELLINGS of one parameter. Both readers answer "" here, because
		// the typed decoder lets the last spelling win and the null overwrites
		// the array, while a case-sensitive decoder downstream still sees a real
		// tools array. Pinned deliberately: it is a known blind spot, recorded
		// on ToolParamInBody with the reasoning for leaving it, and these rows
		// exist so it is found as a documented answer rather than rediscovered
		// as a surprise. The rows still carry their weight for the case-folding
		// defect: they must not start answering differently on the two surfaces.
		`{"model":"m","tools":[{"type":"function"}],"Tools":null}`,
		`{"model":"m","tool_choice":"auto","Tool_Choice":null}`,
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

// The fallback arm, which is the only place a hand-maintained field list still
// lives. A body that fails the typed decode never reaches firstToolParam on the
// API-key surface, so nothing else can catch a stale list here.
//
// Each fixture carries a wrongly typed `stream` so the typed decode fails, plus
// one tool parameter that must still be seen. If the fallback list ever drifts
// from the struct tags, these go red.
func TestToolParamInBodyFallbackStillSeesEveryToolParam(t *testing.T) {
	cases := map[string]string{
		"tools":               `{"model":"m","stream":"yes","tools":[{"type":"function"}]}`,
		"tool_choice":         `{"model":"m","stream":"yes","tool_choice":"auto"}`,
		"response_format":     `{"model":"m","stream":"yes","response_format":{"type":"json_object"}}`,
		"functions":           `{"model":"m","stream":"yes","functions":[{"name":"f"}]}`,
		"function_call":       `{"model":"m","stream":"yes","function_call":"auto"}`,
		"parallel_tool_calls": `{"model":"m","stream":"yes","parallel_tool_calls":true}`,
	}

	for want, body := range cases {
		var req ChatCompletionRequest
		if err := json.Unmarshal([]byte(body), &req); err == nil {
			t.Fatalf("fixture %s was meant to fail the typed decode and did not, so it exercises the delegating arm and not the fallback", body)
		}
		if got := ToolParamInBody([]byte(body)); got != want {
			t.Errorf("body %s: ToolParamInBody = %q, want %q. An undecodable body carrying a tool block must not read as plain", body, got, want)
		}
	}

	// Case folding on the fallback arm too, since that is what the typed
	// decoder would have done with the same key.
	if got := ToolParamInBody([]byte(`{"model":"m","stream":"yes","Tools":[{"type":"function"}]}`)); got != "tools" {
		t.Errorf("mixed-case tool key on the fallback arm read as %q, want \"tools\"", got)
	}

	// And a plain undecodable body stays plain: the fallback must not gate
	// everything it cannot parse.
	if got := ToolParamInBody([]byte(`{"model":"m","stream":"yes"}`)); got != "" {
		t.Errorf("a plain undecodable body reported tool param %q", got)
	}
}
