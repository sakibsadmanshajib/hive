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
		if shimKey == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HasShimAuthorization(r.Header.Get("Authorization"), shimKey) {
				next.ServeHTTP(w, r)
				return
			}
			// This check has to come before the pass-through below, not
			// after it. A per-user path can only receive its user token in a
			// JSON __metadata block, so a shim-key request there with a
			// missing or non-JSON Content-Type can never be legitimate.
			// Passing it through first would let any caller skip the
			// rejection further down simply by omitting Content-Type.
			jsonBody := r.Body != nil && isJSONContent(r.Header.Get("Content-Type"))
			if !jsonBody {
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
			rewritten, token, status := unwrapOWUIBody(raw)
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
			// Forward a clone rather than mutating the inbound request
			// in place — keeps this middleware side-effect free for any
			// handler or middleware that retains a reference to the
			// original *http.Request. r.Clone deep-copies the header map.
			r2 := r.Clone(r.Context())
			r2.Body = io.NopCloser(bytes.NewReader(rewritten))
			r2.ContentLength = int64(len(rewritten))
			r2.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
			if token != "" {
				r2.Header.Set("Authorization", "Bearer "+token)
				r2 = r2.WithContext(context.WithValue(r2.Context(), owuiUnwrappedKey{}, true))
			}
			next.ServeHTTP(w, r2)
		})
	}
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

// HasShimAuthorization reports whether the Authorization header value
// is exactly "Bearer <shimKey>". The scheme word is matched case-
// insensitively per RFC 7235 §2.1; the token body is compared
// case-sensitively because the shim key is opaque to this layer.
//
// Exported so the /v1/models handler can recognise the same credential
// this middleware recognises, rather than re-deriving the comparison and
// risking a looser match. Callers must treat an empty shimKey as
// "shim disabled" before calling; this function does not, because
// OWUIUnwrap already short-circuits on an empty key.
func HasShimAuthorization(header, shimKey string) bool {
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
// Chat completions are the only path the `hive_jwt_forward` Function
// decorates, and the only path where running under the shim principal
// silently mis-attributes billing and audit. Open WebUI's other upstream
// calls (document-RAG embeddings via RAG_OPENAI_API_KEY, and text-to-speech)
// are never decorated and authenticate as the shim account by design, so
// they must keep passing through.
//
// ponytail: exact-path list rather than a prefix rule, because /v1/chat is
// the only prefix at stake today. If a second per-user OWUI path appears
// (for example a future OWUI RAG chat route), add it here.
func requiresPerUserAuth(path string) bool {
	return path == "/v1/chat/completions"
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
	scheme, tokenPart, hasSpace := strings.Cut(authStr, " ")
	if hasSpace && strings.EqualFold(scheme, "Bearer") {
		return out, strings.TrimSpace(tokenPart), unwrapOK
	}
	// Tolerate raw token (no scheme word). The pipeline writes
	// "Bearer <jwt>" today but a future revision may drop the prefix.
	return out, authStr, unwrapOK
}
