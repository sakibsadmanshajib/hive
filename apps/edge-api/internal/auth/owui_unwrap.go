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
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// maxOWUIUnwrapBody caps the body we buffer for metadata extraction.
// OWUI chat-completions bodies are typically small (~kilobytes); a 2 MiB
// ceiling is well past the largest realistic prompt + attachments
// without giving an attacker a memory-amplification primitive.
const maxOWUIUnwrapBody = 2 << 20 // 2 MiB

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
// header carries the OWUI shim key, extracts `__metadata.upstream_auth`
// from the JSON body, replaces Authorization with that token, and
// strips the metadata from the forwarded body so it never reaches the
// chat handler or any sink/log.
//
// Behaviour matrix:
//
//   - Authorization != shim key                                → pass through unchanged.
//   - ShimKey == "" (disabled)                                 → pass through unchanged.
//   - Body unreadable / not JSON / no __metadata.upstream_auth → pass through with shim Authorization
//     intact; the selector will route it to the API-key path
//     and authz will reject the shim key as a real credential.
//   - Body > maxOWUIUnwrapBody                                 → 413 Payload Too Large.
//
// We intentionally only rewrite when the inbound credential is exactly
// the shim key. Any other Bearer value (a real hk_* API key, a real
// Supabase JWT) flows through untouched so this middleware cannot be
// used to override an already-authenticated request.
func OWUIUnwrap(cfg OWUIUnwrapConfig) func(http.Handler) http.Handler {
	shimKey := strings.TrimSpace(cfg.ShimKey)
	return func(next http.Handler) http.Handler {
		if shimKey == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hasShimAuthorization(r.Header.Get("Authorization"), shimKey) {
				next.ServeHTTP(w, r)
				return
			}
			if r.Body == nil {
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
			rewritten, token := unwrapOWUIBody(raw)
			r.Body = io.NopCloser(bytes.NewReader(rewritten))
			r.ContentLength = int64(len(rewritten))
			r.Header.Set("Content-Length", itoa(len(rewritten)))
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// hasShimAuthorization reports whether the Authorization header value
// is exactly "Bearer <shimKey>". The scheme word is matched case-
// insensitively per RFC 7235 §2.1; the token body is compared
// case-sensitively because the shim key is opaque to this layer.
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

// unwrapOWUIBody parses raw JSON, removes `__metadata.upstream_auth` if
// present, removes an empty `__metadata` object after extraction, and
// returns the rewritten body plus the bearer token (without the
// "Bearer " prefix). On any parse failure or missing metadata it
// returns the input body unchanged and an empty token so the caller
// can fall through to the API-key path.
func unwrapOWUIBody(raw []byte) (rewritten []byte, token string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw, ""
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return raw, ""
	}
	metaRaw, ok := body["__metadata"]
	if !ok {
		return raw, ""
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return raw, ""
	}
	authRaw, ok := meta["upstream_auth"]
	if !ok {
		return raw, ""
	}
	var authStr string
	if err := json.Unmarshal(authRaw, &authStr); err != nil {
		return raw, ""
	}
	authStr = strings.TrimSpace(authStr)
	delete(meta, "upstream_auth")
	if len(meta) == 0 {
		delete(body, "__metadata")
	} else {
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return raw, ""
		}
		body["__metadata"] = metaBytes
	}
	out, err := json.Marshal(body)
	if err != nil {
		return raw, ""
	}
	scheme, tokenPart, hasSpace := strings.Cut(authStr, " ")
	if hasSpace && strings.EqualFold(scheme, "Bearer") {
		return out, strings.TrimSpace(tokenPart)
	}
	// Tolerate raw token (no scheme word). The pipeline writes
	// "Bearer <jwt>" today but a future revision may drop the prefix.
	return out, authStr
}

// itoa avoids importing strconv for a single Content-Length write.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
