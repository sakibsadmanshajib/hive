package metering

import (
	"encoding/json"
	"fmt"
)

// RewriteBody forces stream_options.include_usage to true on an outbound
// LiteLLM request body. This is the ONE customer-visible byte this package
// is allowed to touch (design brief section 1, spec decision 10): without
// it, the session-chat path's terminal usage block never arrives and shadow
// mode grades itself green on zero, purely because it never saw a token
// count -- not because nothing was billable. Adding a usage block to an
// OpenAI-compatible streaming response is spec-conformant and not a
// behavior change a client has to handle differently.
//
// Every other field the caller sent survives untouched, including any other
// stream_options subfield. A denylist step (stripping client-supplied
// fields the client should never set) was named in the design brief's
// section 3.1 heading, but no concrete field list has been specified as of
// this PR -- see PR body. Nothing is stripped here until one is; only
// include_usage forcing is implemented.
//
// Callers decide when to invoke this (typically only for streaming
// dispatches); it is a pure, allocation-only function with no I/O, so it
// adds no synchronous network call to any request path.
func RewriteBody(raw []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("metering: decode body: %w", err)
	}

	var streamOptions map[string]json.RawMessage
	if existing, ok := fields["stream_options"]; ok && len(existing) > 0 {
		if err := json.Unmarshal(existing, &streamOptions); err != nil {
			return nil, fmt.Errorf("metering: decode stream_options: %w", err)
		}
	}
	if streamOptions == nil {
		streamOptions = map[string]json.RawMessage{}
	}
	streamOptions["include_usage"] = json.RawMessage("true")

	rewritten, err := json.Marshal(streamOptions)
	if err != nil {
		return nil, fmt.Errorf("metering: encode stream_options: %w", err)
	}
	fields["stream_options"] = rewritten

	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("metering: encode body: %w", err)
	}
	return out, nil
}
