// Package auth — OWUI body-metadata to Authorization header unwrap.
//
// Open WebUI sets the upstream Authorization header from a single static
// shim key (`OPENAI_API_KEY`) and does not let pipelines or admin config
// inject a per-user header for the upstream OpenAI-compatible endpoint.
// The Hive OWUI pipeline (`hive_jwt_forward.py`) therefore writes the
// signed-in user's Supabase JWT into the JSON request body under
// `__metadata.upstream_auth` and the edge-api unwraps it back to an
// Authorization header here, before the selector decides JWT vs API-key.
//
// Without this middleware every chat/embeddings request originating from
// OWUI would carry the shim key in Authorization, route through the
// API-key path, and bind to the shim's principal — defeating per-user
// audit attribution, RLS, and tenant scoping.
//
// The same middleware accepts that token on the UpstreamAuthHeader request
// header, because a JSON body is not a carrier every request has. Three of
// the four agent-task calls the chat container proxies are bodyless (GET
// /v1/agent/tasks, GET /v1/agent/tasks/{id}, and the bodyless POST
// .../cancel — see apps/edge-api/internal/agenttask/handler.go), so
// `__metadata.upstream_auth` cannot reach them at all. The header is a
// second carrier on the same trust boundary, not a second boundary: it is
// honoured only when Authorization is exactly the shim key, which is the
// identical gate the body carrier already sits behind.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// owuiUnwrappedKey marks a request context as having had its Authorization
// header rewritten by OWUIUnwrap. It is only ever set below, on the server
// side, from the cloned request -- never derived from any client-supplied
// header -- so it cannot be spoofed by an inbound request.
type owuiUnwrappedKey struct{}

// IsOWUIUnwrapped reports whether ctx belongs to a request whose
// Authorization header OWUIUnwrap rewrote from the shim key to a per-user
// token. JWTMiddleware uses this to scope its tenant_id fallback (#269,
// see TenantFallback) to the OWUI shim path only.
func IsOWUIUnwrapped(ctx context.Context) bool {
	v, _ := ctx.Value(owuiUnwrappedKey{}).(bool)
	return v
}

// maxOWUIUnwrapBody caps the body we buffer for metadata extraction.
// OWUI chat-completions bodies are typically small (~kilobytes); a 2 MiB
// ceiling is well past the largest realistic prompt + attachments
// without giving an attacker a memory-amplification primitive.
const maxOWUIUnwrapBody = 2 << 20 // 2 MiB

// maxOWUIBearerToken caps the token length extracted from
// `__metadata.upstream_auth`. A Supabase JWT is typically ~1 KB; 8 KiB
// is a generous ceiling that still prevents header-amplification
// attacks via a crafted body. RFC 7230 §3.2.5 leaves header length to
// servers; downstream JWKS validation would reject anything insane
// anyway but failing early here keeps the JWT path cheap.
const maxOWUIBearerToken = 8 << 10 // 8 KiB

// UpstreamAuthHeader carries the signed-in user's token on requests that
// have no JSON body to hide it in. It is meaningful to this middleware and
// to nothing else, so it is removed from every request that passes through
// here — honoured, ignored, or rejected — before any handler, log or audit
// sink can read it. Exported for the tests that assert exactly that.
const UpstreamAuthHeader = "X-Hive-Upstream-Auth"

// OWUIUnwrapConfig configures the OWUI body-metadata Authorization
// rewrite. ShimKey is the static OPENAI_API_KEY value Open WebUI sends
// on every upstream call; when this exact token arrives, the body is
// peeked for a per-user JWT to swap onto the Authorization header.
//
// Leave ShimKey empty to disable the middleware entirely (e.g. in
// non-OWUI deployments). An empty ShimKey makes the middleware a no-op
// rather than rewriting on any Bearer credential, which would let an
// attacker smuggle a JWT into any request.
type OWUIUnwrapConfig struct {
	ShimKey string
}

// OWUIUnwrap returns middleware that, when the request Authorization
// header carries the OWUI shim key AND the Content-Type is JSON,
// extracts `__metadata.upstream_auth` from the JSON body, replaces
// Authorization with that token, and strips the entire `__metadata`
// object from the forwarded body so it never reaches the chat handler,
// audit log, or any sink.
//
// Behaviour matrix:
//
//   - UpstreamAuthHeader present                               → always removed
//     from the forwarded request, on every branch below. Honoured
//     as the per-user token only under the shim key, in which case
//     it wins over the body carrier: it is read before the body so
//     that a bodyless request, the case it exists for, is not
//     rejected before the header is ever consulted.
//   - Authorization != shim key                                → pass through unchanged.
//   - ShimKey == "" (disabled)                                 → pass through unchanged.
//   - Content-Type not application/json (multipart, audio,
//     image, etc.) on a path that does not require a per-user
//     token → pass through unchanged; the body is opaque to this
//     layer so we cannot rewrite it. Such requests legitimately
//     reach the API-key path with the shim key.
//   - Content-Type not application/json on a path that DOES
//     require a per-user token (see requiresPerUserAuth) → 401.
//     The token can only arrive in a JSON __metadata block, so
//     such a request can never be legitimate, and passing it
//     through would be a way around the fail-closed check below.
//   - Body unreadable                                          → 400 (fail closed).
//   - Body > maxOWUIUnwrapBody                                 → 413.
//   - Body is JSON but missing __metadata.upstream_auth        → 401 on a
//     path requiring a per-user token, otherwise pass through
//     with shim Authorization intact. Either way emit a
//     structured warn log so a regression in the OWUI pipeline
//     is visible instead of degrading silently to a 401 cascade.
//   - upstream_auth present but token longer than
//     maxOWUIBearerToken                                       → 401.
//
// We only rewrite when the inbound credential is EXACTLY the shim key.
// Any other Bearer value (a real hk_* API key, a real Supabase JWT)
// flows through untouched so this middleware cannot be used to
// override an already-authenticated request.
func OWUIUnwrap(cfg OWUIUnwrapConfig) func(http.Handler) http.Handler {
	shimKey := strings.TrimSpace(cfg.ShimKey)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Take the carrier off the request before anything else runs,
			// including when the middleware is disabled and when the
			// credential is not the shim key. It is ours alone, so a handler
			// that can read it is a handler that could later be taught to
			// trust it, and a header a client controls must never become a
			// principal by that route.
			// Presence is tested on the header map rather than on the trimmed
			// value, so a whitespace-only or repeated header is removed too.
			// Header.Del drops every value under the key, which is what makes
			// "stripped on every branch" true rather than nearly true.
			//
			// The removal is in place, on the inbound request, and not on a
			// clone. A clone would only protect code reachable through `next`,
			// and this header has to disappear for everyone: an outer
			// middleware still holding the original pointer would otherwise
			// keep seeing a live per-user token, and no branch that answers
			// without calling `next` at all, which is every rejection path
			// below, would strip anything observable. In place makes the
			// invariant one fact rather than one fact per branch, and lets a
			// test assert it on the rejection paths too. Mutating the inbound
			// header map is safe here because net/http never re-reads request
			// headers after the handler returns, and this header is ours: no
			// caller upstream of edge-api has any reason to send it, and none
			// is permitted to.
			_, carrierPresent := r.Header[http.CanonicalHeaderKey(UpstreamAuthHeader)]
			carrier := strings.TrimSpace(r.Header.Get(UpstreamAuthHeader))
			if carrierPresent {
				r.Header.Del(UpstreamAuthHeader)
			}
			if shimKey == "" || !hasShimAuthorization(r.Header.Get("Authorization"), shimKey) {
				next.ServeHTTP(w, r)
				return
			}
			// Fail closed on a carrier that is present but unusable. The only
			// thing that sets this header is our own forwarder, so a value it
			// cannot mean is a broken forwarder, and forwarding with the shim
			// key still on Authorization would bill and audit the call against
			// the shim's principal.
			headerToken := ""
			if carrierPresent {
				if len(carrier) <= maxOWUIBearerToken {
					headerToken = normalizeUpstreamAuth(carrier)
				}
				if headerToken == "" {
					writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid token")
					return
				}
			}
			// The rejection has to come before the pass-through, not after it.
			// With no carrier on the header, a per-user path can only receive
			// its user token in a JSON __metadata block, so a shim-key request
			// there with a missing or non-JSON Content-Type can never be
			// legitimate. Passing it through first would let any caller skip
			// the rejection simply by omitting Content-Type.
			jsonBody := r.Body != nil && isJSONContent(r.Header.Get("Content-Type"))
			if !jsonBody {
				// The bodyless case the header carrier exists for. Nothing to
				// read, nothing to strip, so forward immediately.
				if headerToken != "" {
					forwardUnwrapped(w, r, next, headerToken, nil)
					return
				}
				if requiresPerUserAuth(r.URL.Path) {
					warnMissingUpstreamAuth(r, 0, true)
					writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", missingUserTokenMessage)
					return
				}
				// Non-JSON shim requests (multipart uploads for audio
				// or images) cannot carry __metadata; pass through.
				next.ServeHTTP(w, r)
				return
			}
			// Cap the read at maxOWUIUnwrapBody+1 so we can detect
			// over-limit bodies without sucking an unbounded payload
			// into memory.
			limited := io.LimitReader(r.Body, maxOWUIUnwrapBody+1)
			raw, err := io.ReadAll(limited)
			closeErr := r.Body.Close()
			if err != nil || closeErr != nil {
				// Cannot recover; fail closed so a partial body cannot
				// be silently forwarded with the shim key still on the
				// header.
				writeAuthError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
				return
			}
			if len(raw) > maxOWUIUnwrapBody {
				writeAuthError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body too large")
				return
			}
			// The body is parsed even when the header already supplied the
			// token, because __metadata must be stripped either way. Forwarding
			// it would leak proxy-layer fields to downstream handlers, to the
			// audit chain and on to a provider, which is the reason the strip
			// is unconditional in unwrapOWUIBody.
			rewritten, bodyToken, status := unwrapOWUIBody(raw)
			token := bodyToken
			if headerToken != "" {
				// The header wins. It is the carrier our own forwarder sets on
				// the request line, and a body that also carries one is either
				// a second forwarder or a caller trying its luck; neither
				// should be able to displace the credential already accepted.
				token = headerToken
				forwardUnwrapped(w, r, next, token, rewritten)
				return
			}
			switch status {
			case unwrapTokenTooLong:
				writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid token")
				return
			case unwrapNoMetadata:
				warnMissingUpstreamAuth(r, len(raw), requiresPerUserAuth(r.URL.Path))
				if requiresPerUserAuth(r.URL.Path) {
					// Fail closed and name the real cause. Forwarding is
					// wrong in both possible states of the shim key: if it
					// resolves, the completion is billed and audited
					// against the shim's own account instead of the
					// signed-in user, defeating the entire reason this
					// middleware exists; if it does not resolve, the
					// caller gets "Incorrect API key provided", which
					// blames the customer for a missing server-side
					// Function. The customer-facing message stays free of
					// internal detail; the operator-facing detail is in
					// the warn log above.
					writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", missingUserTokenMessage)
					return
				}
				// Body restored from raw bytes; shim key remains on
				// Authorization, which is the intended credential for
				// this path (see requiresPerUserAuth).
			}
			forwardUnwrapped(w, r, next, token, rewritten)
		})
	}
}

// forwardUnwrapped hands the request on with the per-user token on
// Authorization and the context marked unwrapped, which is what lets
// JWTMiddleware apply its tenant fallback (#269). An empty token forwards
// unchanged credentials, which is the pass-through case on a path where the
// shim key is itself the intended credential.
//
// A nil body leaves the request's own body alone, for the bodyless carrier
// case where there is nothing to rewrite; a non-nil body replaces it with the
// version that has __metadata stripped.
//
// Forwarding a clone rather than mutating the inbound request in place keeps
// this middleware side-effect free for any handler or middleware that retains
// a reference to the original *http.Request. r.Clone deep-copies the header
// map.
func forwardUnwrapped(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	token string,
	body []byte,
) {
	r2 := r.Clone(r.Context())
	if body != nil {
		r2.Body = io.NopCloser(bytes.NewReader(body))
		r2.ContentLength = int64(len(body))
		r2.Header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	if token != "" {
		r2.Header.Set("Authorization", "Bearer "+token)
		r2 = r2.WithContext(context.WithValue(r2.Context(), owuiUnwrappedKey{}, true))
	}
	next.ServeHTTP(w, r2)
}

// missingUserTokenMessage is the customer-facing reason for a shim-key
// request on a per-user path that carries no user token. Deliberately free of
// internal detail and of any provider identity; the operator-facing detail
// goes to warnMissingUpstreamAuth. Shared by both rejection sites so the two
// cannot drift apart.
const missingUserTokenMessage = "This chat session is not carrying a signed-in user token. Sign in again and retry; if it persists, contact your administrator."

// warnMissingUpstreamAuth records a shim-key request that arrived without
// __metadata.upstream_auth. Logged whether or not the request is rejected, so
// that an OWUI pipeline regression which stops injecting the JWT is loud
// rather than silently 401-cascading. contentLength is 0 when the body was
// never read, which is the non-JSON case.
func warnMissingUpstreamAuth(r *http.Request, contentLength int, rejected bool) {
	slog.Warn("owui shim request missing upstream_auth metadata",
		"path", r.URL.Path,
		"content_length", contentLength,
		"rejected", rejected)
}

// hasShimAuthorization reports whether the Authorization header value
// is exactly "Bearer <shimKey>". The scheme word is matched case-
// insensitively per RFC 7235 §2.1; the token body is compared
// case-sensitively because the shim key is opaque to this layer.
//
// Deliberately unexported: no route outside this middleware should branch
// on "is this the shim key". Every /v1 route resolves its credential
// through the normal API-key or JWT path, and the shim key is a real
// minted API key there like any other.
func hasShimAuthorization(header, shimKey string) bool {
	scheme, rest, ok := strings.Cut(header, " ")
	if !ok {
		return false
	}
	if !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	return rest == shimKey
}

// requiresPerUserAuth reports whether a shim-key request on this path must
// carry a per-user token, i.e. whether a missing `__metadata.upstream_auth`
// is a failure rather than the expected shape.
//
// Two families qualify, for the same reason. Chat completions are the path
// the `hive_jwt_forward` Function decorates. The agent-task lifecycle is the
// path the chat container's own agent proxy decorates
// (deploy/docker/owui-patches/hive_agent_proxy.py). On both, running under
// the shim principal silently mis-attributes billing and audit, and on the
// agent path it would additionally list and cancel the shim account's tasks
// rather than the signed-in user's. Open WebUI's other upstream calls
// (document-RAG embeddings via RAG_OPENAI_API_KEY, and text-to-speech) are
// never decorated and authenticate as the shim account by design, so they
// must keep passing through.
//
// The agent arm is a prefix rather than an exact list because the subtree
// carries a task id: /v1/agent/tasks/{id} and /v1/agent/tasks/{id}/cancel.
func requiresPerUserAuth(path string) bool {
	return path == "/v1/chat/completions" ||
		path == "/v1/agent/tasks" ||
		strings.HasPrefix(path, "/v1/agent/tasks/")
}

// normalizeUpstreamAuth reduces a carried credential to a bare token,
// tolerating both "Bearer <token>" (what the forwarders write today) and a
// bare token (so a future revision that drops the scheme word is not a
// silent auth failure). Returns "" for anything with no token in it, which
// every caller treats as fail-closed.
//
// Shared by the header carrier and the body carrier so the two cannot drift
// into accepting different shapes of the same credential.
func normalizeUpstreamAuth(value string) string {
	value = strings.TrimSpace(value)
	scheme, rest, hasSpace := strings.Cut(value, " ")
	if hasSpace && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(rest)
	}
	// A scheme word and nothing else is not a bare token. Without this, a
	// carrier of "Bearer " arrives here as "Bearer" after the trim above, takes
	// the bare-token arm, and is promoted to the credential "Bearer Bearer" --
	// a nonsense token that then fails JWKS validation and reports itself as
	// the user's session being invalid. Refusing it here names the real cause
	// at the boundary that produced it.
	if strings.EqualFold(value, "Bearer") {
		return ""
	}
	return value
}

// isJSONContent reports whether the Content-Type media type is
// application/json (with or without parameters). mime.ParseMediaType
// strips parameters and lowercases the type so a Content-Type like
// `application/json; charset=utf-8` is correctly classified.
func isJSONContent(contentType string) bool {
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

type unwrapStatus int

const (
	unwrapOK unwrapStatus = iota
	unwrapNoMetadata
	unwrapTokenTooLong
)

// unwrapOWUIBody parses raw JSON, removes the entire `__metadata` object,
// and returns (rewritten body, bearer token without scheme, status).
//
// We strip the WHOLE `__metadata` object — not just `upstream_auth` —
// because forwarding OWUI-internal fields to downstream handlers and
// audit sinks would leak information about the proxy layer to LLM
// providers and into the audit chain. The pipeline owns __metadata
// end-to-end; nothing past the unwrap should ever see it.
//
// On parse failure or missing __metadata.upstream_auth the input body
// is returned unchanged with status unwrapNoMetadata so the caller can
// fall through to the API-key path (which will reject the shim key).
func unwrapOWUIBody(raw []byte) (rewritten []byte, token string, status unwrapStatus) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, "", unwrapNoMetadata
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return raw, "", unwrapNoMetadata
	}
	metaRaw, ok := body["__metadata"]
	if !ok {
		return raw, "", unwrapNoMetadata
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		// Malformed __metadata — strip it anyway and continue without
		// a token. Defence in depth: never forward an unparseable
		// __metadata that the pipeline may not have written.
		delete(body, "__metadata")
		out, mErr := json.Marshal(body)
		if mErr != nil {
			return raw, "", unwrapNoMetadata
		}
		return out, "", unwrapNoMetadata
	}
	authRaw, hasAuth := meta["upstream_auth"]
	delete(body, "__metadata") // Always strip — never forward.
	out, err := json.Marshal(body)
	if err != nil {
		return raw, "", unwrapNoMetadata
	}
	if !hasAuth {
		return out, "", unwrapNoMetadata
	}
	var authStr string
	if err := json.Unmarshal(authRaw, &authStr); err != nil {
		return out, "", unwrapNoMetadata
	}
	authStr = strings.TrimSpace(authStr)
	if len(authStr) > maxOWUIBearerToken {
		return out, "", unwrapTokenTooLong
	}
	token = normalizeUpstreamAuth(authStr)
	if token == "" {
		// A present-but-empty upstream_auth is a pipeline that stopped
		// forwarding, not a request that meant to run as the shim. Reported
		// as a missing carrier so it takes the fail-closed arm and the warn
		// log, rather than forwarding with the shim key on Authorization.
		return out, "", unwrapNoMetadata
	}
	return out, token, unwrapOK
}
