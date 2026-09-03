package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Dispatch time budgets (issue #928 defect 3).
//
// http.Client.Timeout is a TOTAL budget: it covers the dial, the request write,
// the response headers AND every byte of the response body. That is the right
// shape for a buffered request, whose body is a single JSON document the
// gateway reads in one go, and the wrong shape for a stream, whose body is the
// answer itself and legitimately takes as long as the answer takes.
//
// Before this split every dispatch shared one 120 second total timeout, so any
// streamed turn still running two minutes after dispatch had its connection cut
// mid-stream. Buffered, that ceiling produced a clean 502 with a provider-blind
// body a client SDK can retry. Streamed, it produced a TRUNCATION: the caller's
// 200 and its SSE headers were committed long before, so there was no status
// left to fail with, and the OpenHands SDK feeds whatever arrived to
// litellm.stream_chunk_builder, which reassembles a partially streamed tool call
// into a malformed or empty one rather than an error. Agent turns are the first
// traffic that crosses two minutes routinely (PR #920).
//
// WHAT BOUNDS A STREAM NOW, since the total bound is gone from it:
//
//  1. streamDialTimeout bounds reaching the proxy at all, and
//     streamResponseHeaderTimeout bounds an upstream that accepts a connection
//     and then never answers. Both are per-attempt, not per-request-lifetime.
//  2. streamIdleTimeout (stream_idle.go) bounds SILENCE once the body is open.
//     The watchdog is installed HERE, on the response body, rather than at the
//     relay, because the relay is not the first thing to read that body:
//     dispatchWithRetry's peekBody and the relay's own non-2xx io.ReadAll both
//     read it first, and a 500 followed by silence hung in that gap when the
//     watchdog sat at the relay (review finding). A second, narrower bound in
//     the relay covers a provider that sends keepalive comments and no data;
//     see streamDataDeadlineExceeded.
//  3. The caller's request context. A client disconnect cancels it and tears
//     down the in-flight body read.
//  4. Control-plane's reservation TTL reaper, the backstop for a hold whose
//     settlement never reached it at all.
//
// So a stalled provider still releases; what it no longer does is cut a healthy
// long answer at exactly two minutes.
const (
	// bufferedRequestTimeout is the TOTAL budget for a non-streaming dispatch,
	// unchanged from the single timeout this pair replaces.
	bufferedRequestTimeout = 120 * time.Second
	// streamDialTimeout bounds connection establishment (and the TLS
	// handshake) for a streaming dispatch.
	streamDialTimeout = 10 * time.Second
	// streamResponseHeaderTimeout bounds the wait for response headers on a
	// streaming dispatch: an upstream that accepts the connection and then says
	// nothing at all fails here rather than hanging forever. It is deliberately
	// the old total budget, because time-to-first-header is the part of a
	// stream that genuinely should not take minutes.
	streamResponseHeaderTimeout = 120 * time.Second
)

// LiteLLMClient dispatches inference requests to the LiteLLM proxy.
type LiteLLMClient struct {
	baseURL   string
	masterKey string
	// httpClient serves every BUFFERED dispatch and keeps the total timeout.
	httpClient *http.Client
	// streamClient serves every STREAMING dispatch and carries no total
	// timeout at all; see the block comment above for what bounds it instead.
	streamClient *http.Client
}

// NewLiteLLMClient creates a new LiteLLMClient.
func NewLiteLLMClient(baseURL, masterKey string) *LiteLLMClient {
	return &LiteLLMClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		masterKey:  masterKey,
		httpClient: &http.Client{Timeout: bufferedRequestTimeout},
		streamClient: &http.Client{
			// Timeout deliberately absent. A total timeout on a streaming
			// exchange is a truncation waiting to happen; the transport
			// timeouts below plus the relay's idle watchdog are the bounds.
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   streamDialTimeout,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   streamDialTimeout,
				ResponseHeaderTimeout: streamResponseHeaderTimeout,
				ExpectContinueTimeout: 1 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConnsPerHost:   32,
			},
		},
	}
}

// ChatCompletion dispatches a chat completion request to LiteLLM.
// The caller owns closing the returned response body.
func (c *LiteLLMClient) ChatCompletion(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/chat/completions", litellmModel, body)
}

// ChatCompletionStream is ChatCompletion for a dispatch whose response is an
// SSE stream: same request, no total timeout on the exchange. Every streaming
// relay must use this rather than ChatCompletion, or a long answer is cut at
// bufferedRequestTimeout (issue #928 defect 3).
func (c *LiteLLMClient) ChatCompletionStream(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatchWith(ctx, c.streamClient, "/chat/completions", litellmModel, body, streamIdleTimeout)
}

// Completion dispatches a legacy completion request to LiteLLM.
func (c *LiteLLMClient) Completion(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/completions", litellmModel, body)
}

// CompletionStream is Completion's streaming twin; see ChatCompletionStream.
func (c *LiteLLMClient) CompletionStream(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatchWith(ctx, c.streamClient, "/completions", litellmModel, body, streamIdleTimeout)
}

// Embeddings dispatches an embeddings request to LiteLLM.
func (c *LiteLLMClient) Embeddings(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/embeddings", litellmModel, body)
}

// ImageGeneration dispatches a JSON body to /images/generations.
func (c *LiteLLMClient) ImageGeneration(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/images/generations", litellmModel, body)
}

// ImageEditRaw forwards a pre-built multipart request to /images/edits.
// The caller is responsible for setting the correct Content-Type on the body.
func (c *LiteLLMClient) ImageEditRaw(ctx context.Context, body io.Reader, contentType string) (*http.Response, error) {
	url := c.baseURL + "/images/edits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("litellm: build image edit request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: image edit request failed: %w", err)
	}
	return resp, nil
}

// Speech dispatches a JSON body to /audio/speech and returns the raw binary response.
func (c *LiteLLMClient) Speech(ctx context.Context, litellmModel string, body []byte) (*http.Response, error) {
	return c.dispatch(ctx, "/audio/speech", litellmModel, body)
}

// TranscriptionRaw forwards a pre-built multipart request to /audio/transcriptions.
func (c *LiteLLMClient) TranscriptionRaw(ctx context.Context, body io.Reader, contentType string) (*http.Response, error) {
	url := c.baseURL + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("litellm: build transcription request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: transcription request failed: %w", err)
	}
	return resp, nil
}

// TranslationRaw forwards a pre-built multipart request to /audio/translations.
func (c *LiteLLMClient) TranslationRaw(ctx context.Context, body io.Reader, contentType string) (*http.Response, error) {
	url := c.baseURL + "/audio/translations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("litellm: build translation request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+c.masterKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: translation request failed: %w", err)
	}
	return resp, nil
}

func (c *LiteLLMClient) dispatch(ctx context.Context, path, litellmModel string, body []byte) (*http.Response, error) {
	// Zero: the buffered client's own total timeout is the bound there.
	return c.dispatchWith(ctx, c.httpClient, path, litellmModel, body, 0)
}

// dispatchWith sends the request on the given client. A non-zero idleTimeout
// wraps the response body in the byte-level watchdog, so every read of it --
// dispatchWithRetry's classification peek, a non-2xx error read, and the relay
// loop alike -- is bounded from the moment the response exists.
func (c *LiteLLMClient) dispatchWith(ctx context.Context, client *http.Client, path, litellmModel string, body []byte, idleTimeout time.Duration) (*http.Response, error) {
	rewritten := rewriteModel(body, litellmModel)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, bytes.NewReader(rewritten))
	if err != nil {
		return nil, fmt.Errorf("litellm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.masterKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: request failed: %w", err)
	}
	if idleTimeout > 0 && resp.Body != nil {
		// The watchdog IS the body from here on, and closing it stops the
		// watchdog, so every existing `defer resp.Body.Close()` and
		// drainAndClose already disarms it. Nothing downstream has to know.
		resp.Body = newIdleTimeoutReader(resp.Body, idleTimeout,
			fmt.Sprintf("path=%s model=%s", path, litellmModel))
	}

	return resp, nil
}

// rewriteModel replaces the "model" field in the JSON body with the LiteLLM model name.
func rewriteModel(body []byte, newModel string) []byte {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}

	modelJSON, err := json.Marshal(newModel)
	if err != nil {
		return body
	}
	parsed["model"] = modelJSON

	result, err := json.Marshal(parsed)
	if err != nil {
		return body
	}

	return result
}
