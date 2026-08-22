package authz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/cpauth"
)

type snapshotStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
}

type redisSnapshotStore struct {
	client *redis.Client
}

func (s *redisSnapshotStore) Get(ctx context.Context, key string) (string, error) {
	if s == nil || s.client == nil {
		return "", redis.Nil
	}
	return s.client.Get(ctx, key).Result()
}

func (s *redisSnapshotStore) Set(ctx context.Context, key string, value string, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Set(ctx, key, value, ttl).Err()
}

// HashBearerToken extracts the raw token from a Bearer header and returns its SHA-256 hash.
func HashBearerToken(authHeader string) string {
	raw := strings.TrimPrefix(authHeader, "Bearer ")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	h := sha256.Sum256([]byte(raw))
	return strings.ToLower(hex.EncodeToString(h[:]))
}

// isControlPlaneErrorEnvelope reports whether body decodes as control-plane's
// own error envelope ({"error": "<non-empty>"} -- apikeys/http.go's writeJSON
// and every handleKeyError branch write exactly this shape). Content-Type
// alone is a convention an intermediary in front of an operator-supplied
// CONTROL_PLANE_URL (reverse proxy, API gateway, CDN) can satisfy by
// accident on its own unrouted-404 response; this body-shape check is the
// signature that specific control-plane handler actually produced the
// response, not merely that something along the path called itself JSON
// (PR #903 second-pass security review).
func isControlPlaneErrorEnvelope(body []byte) bool {
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return strings.TrimSpace(envelope.Error) != ""
}

// ErrUpstreamUnavailable classifies a Resolve failure that says nothing about
// whether the presented key is valid: a transport failure (dial/connection
// error), a client timeout, a canceled or expired context, or a non-2xx
// status from control-plane that is not itself a verdict on the key (its own
// request failed or was canceled, e.g. a 500 mid-cold-start). Authorize maps
// this distinctly from a genuine invalid-key rejection (401) so a slow or
// momentarily unreachable control-plane cannot masquerade as "this key does
// not exist" -- see the 2026-08-14 live-box incident, where a cold
// control-plane container answered /internal/apikeys/resolve too slowly and
// a perfectly valid key was told it was invalid.
var ErrUpstreamUnavailable = errors.New("authz: resolve upstream unavailable")

// ErrInternalTokenRejected classifies a resolve failure caused by
// control-plane's RequireInternalToken middleware rejecting edge-api's own
// X-Internal-Token (a mismatched or missing CONTROL_PLANE_INTERNAL_TOKEN on
// one side). This always wraps ErrUpstreamUnavailable too -- the customer
// still sees the same retryable 503, since it is not their key's fault
// either -- but it is a permanent misconfiguration, not a transient
// condition, and will not self-resolve by retrying the way a cold-start
// timeout does. Classified distinctly so it can be logged loudly instead of
// blending into ordinary transient-failure noise, per PR #903 security
// review.
var ErrInternalTokenRejected = errors.New("authz: control-plane rejected our internal token")

// snapshotTTL bounds how long the edge will honor a cached auth snapshot when
// active control-plane invalidation is missed. The control-plane DELETEs the
// snapshot key on revoke/disable/rotate (primary path); this TTL is the
// revocation backstop and is kept ≤60s so a revoked key cannot keep
// authorizing beyond the SLA in the worst case (issue #113).
const snapshotTTL = 60 * time.Second

// Client orchestrates reading AuthSnapshots from Redis with fallback to the control plane.
type Client struct {
	cache      snapshotStore
	httpClient *http.Client
	baseURL    string

	// ResolveOverride is a test hook for bypassing Redis/control-plane I/O.
	ResolveOverride func(ctx context.Context, rawToken string) (AuthSnapshot, error)

	// resolveDegraded tracks whether the most recent resolve attempt that
	// actually reached Redis or control-plane came back ErrUpstreamUnavailable
	// rather than a real verdict. Fed by real traffic on Authorize's hot path
	// and BudgetGate's workspace-identity resolver (the two callers that share
	// this Client), so it costs nothing beyond an atomic store per call and
	// needs no separate probe. Zero value is healthy: a freshly built Client
	// that has served no traffic yet must not report degraded for having no
	// data. See Degraded.
	resolveDegraded atomic.Bool
}

// NewClient returns a new Client.
func NewClient(baseURL string, redisURL string) (*Client, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("authz: parse redis URL: %w", err)
	}

	// Clone http.DefaultTransport rather than building a bare &http.Transport{}:
	// a bare struct sets only the one field given and silently zeroes every
	// other default (IdleConnTimeout becomes 0, so idle connections never
	// expire -- exactly the risk across a control-plane container recreate
	// this PR exists to survive; Proxy becomes nil; HTTP/2 negotiation is
	// off). Clone() inherits those deliberately instead of by omission.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxConnsPerHost = resolveMaxConnsPerHost
	// MaxIdleConnsPerHost moves with MaxConnsPerHost: the stdlib default (2)
	// is far below the 64 concurrent connections now permitted, so under
	// real concurrency most of those connections would be opened fresh and
	// then discarded rather than kept alive for reuse -- a fresh TCP (and
	// TLS, if control-plane is behind an https front) handshake per resolve,
	// on the hot path, against the one host this cap exists to protect
	// (PR #903 second-pass security review).
	transport.MaxIdleConnsPerHost = resolveMaxConnsPerHost

	return &Client{
		cache: &redisSnapshotStore{client: redis.NewClient(opt)},
		httpClient: &http.Client{
			Timeout:   resolveClientTimeout,
			Transport: transport,
		},
		baseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

// resolveMaxConnsPerHost bounds concurrent connections to control-plane from
// this one shared *Client, which two per-request paths call: BudgetGate's
// workspace-identity resolver (main.go's buildBudgetGate) and Authorizer's
// own key resolution, so a single incoming request can drive up to two
// Resolve calls, each blocking up to resolveClientTimeout (10s, doubled from
// 5s in the same change that added this bound). With no cap, a sustained
// control-plane outage could grow that to one goroutine and connection per
// in-flight resolve with no ceiling -- at even 50 req/s sustained through a
// 10s-timeout outage that is up to 1000 concurrent connections stacking up
// against the one host that is already struggling, worsening the outage it
// is trying to recover from. Go's Transport blocks new dials once this limit
// is hit rather than erroring (net/http docs: "On limit violation, dials
// will block"), so it turns unbounded growth into bounded queuing that still
// resolves via the existing client Timeout -- no new pooling code needed.
//
// The concurrency ceiling this number actually sets, spelled out so whoever
// raises it later during an incident knows what they are changing (PR #903
// second-pass security review): a connection is held for up to
// resolveClientTimeout each, so the shed threshold scales with which latency
// is actually being served --
//   - at the 5.65s cold-start latency this PR was written around: 64 conns /
//     5.65s ≈ 11.3 resolve calls/sec, and since one incoming request can
//     drive up to two resolves (BudgetGate then Authorizer), that's roughly
//     5.6 incoming requests/sec served before the next one queues;
//   - at a fully wedged control-plane paying the full resolveClientTimeout
//     (10s) per call: 64 / 10s = 6.4 resolve calls/sec, roughly 3.2 incoming
//     requests/sec.
//
// Below the applicable number, a cold start or outage is absorbed; above it,
// additional callers wait behind this cap instead of failing fast -- but
// that wait is charged against resolveClientTimeout, not exempt from it (LOW
// finding, third-pass security review: the earlier wording here said
// "queued, not dropped", which overstated it). A caller that queues for the
// entire timeout still gets the 503 having never actually resolved; "queued"
// describes where it waits, not a reprieve from the deadline. Raising resolveMaxConnsPerHost raises
// both ceilings; raising resolveClientTimeout lowers them for the same
// connection count -- the two constants trade off against each other, not
// independently.
//
// What this cap does NOT bound (PR #903 second-pass security review):
// sockets are bounded, waiting goroutines are not. A dial blocked on this
// limit parks on Go's internal connection-wait queue rather than spawning a
// new connection, which is the improvement, but nothing in front of the HTTP
// handler limits how many requests can be in that waiting state at once, so
// total in-flight request count and the memory behind it are still
// unbounded above the thresholds computed here. A request-concurrency
// limiter ahead of the handler would close that gap; not added here since
// it is a broader change than one HTTP client's Transport.
const resolveMaxConnsPerHost = 64

// resolveClientTimeout bounds a single /internal/apikeys/resolve call. Raised
// from 5s to 10s on 2026-08-14: a control-plane container recreated at
// 16:34:29Z took until 16:35:17Z (measured 5.65s for the first request after
// listening began) to complete its first resolve, so the previous 5s timeout
// was already below the observed worst case and turned that cold-start
// latency into a resolve failure on every deploy's post-deploy replay. 10s
// gives roughly double that observed worst case as headroom. The trade-off is
// real: a genuinely dead control-plane now takes up to 10s (was 5s) to surface
// as a failure on the hot path. That is an acceptable trade now that a resolve
// failure answers a retryable 503 (upstream_unavailable) instead of a
// non-retryable 401 -- the failure being slower matters far less than it being
// honest.
const resolveClientTimeout = 10 * time.Second

// Resolve returns the AuthSnapshot for the given raw token or Bearer header.
// It checks Redis first, then falls back to the control plane.
func (c *Client) Resolve(ctx context.Context, rawToken string) (snap AuthSnapshot, err error) {
	if c != nil && c.ResolveOverride != nil {
		return c.ResolveOverride(ctx, rawToken)
	}
	rawToken = strings.TrimPrefix(rawToken, "Bearer ")
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return AuthSnapshot{}, errors.New("authz: empty token")
	}

	h := sha256.Sum256([]byte(rawToken))
	tokenHash := strings.ToLower(hex.EncodeToString(h[:]))
	redisKey := "auth:key:{" + tokenHash + "}"

	// 1. Try Redis cache.
	if c.cache != nil {
		cached, err := c.cache.Get(ctx, redisKey)
		if err == nil {
			var snap AuthSnapshot
			if err := json.Unmarshal([]byte(cached), &snap); err == nil {
				// A cache hit never contacts control-plane, so it must not
				// touch resolveDegraded either way: recording success here
				// would clear a real, still-ongoing outage without having
				// checked anything, which is exactly the failure mode this
				// tracker exists to prevent. Leave it at whatever the last
				// actual round trip observed (PR #975 review finding 2).
				return snap, nil
			}
			// on decode error, fall through to fetch
		} else if err != redis.Nil {
			// Log error but fall through to fetch if Redis fails
			// TODO: hook up logger
		}
	}

	// Everything from here on actually reaches control-plane, so it is what
	// Degraded() reports on. A nil error, or a non-nil error that is a
	// genuine verdict (not found, revoked, disabled, expired), both mean
	// control-plane answered; only ErrUpstreamUnavailable means it did not.
	defer func() {
		if errors.Is(err, ErrUpstreamUnavailable) {
			c.resolveDegraded.Store(true)
		} else {
			c.resolveDegraded.Store(false)
		}
	}()

	// 2. Fallback to control plane.
	body := fmt.Sprintf(`{"token_hash":%q}`, tokenHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/internal/apikeys/resolve",
		strings.NewReader(body),
	)
	if err != nil {
		// Never reached a control-plane verdict: the request was not even
		// constructed, which on this path means a malformed base URL rather
		// than anything about the caller's key. Classified as
		// ErrUpstreamUnavailable for two reasons. The caller-facing one:
		// authorizer.go answers an unclassified error with a permanent 401
		// "Incorrect API key provided", which would tell every caller to
		// rotate a perfectly good credential because CONTROL_PLANE_BASE_URL
		// is wrong. The health one: the deferred tracker above clears the
		// degraded flag for any error that is not ErrUpstreamUnavailable, so
		// a base URL that fails on every single request would have cleared
		// it on every single request and left /health reporting 200 straight
		// through a total authorization outage -- the exact lie this change
		// exists to remove (PR #975 CodeRabbit CLI finding 2).
		return AuthSnapshot{}, fmt.Errorf("authz: build request: %w: %w", ErrUpstreamUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	cpauth.SetHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// A transport failure (dial error, TLS error, client timeout, canceled
		// context) says nothing about whether rawToken is a valid key -- it
		// never reached a control-plane verdict at all.
		return AuthSnapshot{}, fmt.Errorf("authz: fetch: %w: %w", ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		contentType := resp.Header.Get("Content-Type")
		statusErr := fmt.Errorf("authz: status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		switch {
		case resp.StatusCode == http.StatusNotFound && strings.HasPrefix(contentType, "application/json") && isControlPlaneErrorEnvelope(respBody):
			// A genuine control-plane verdict: apikeys.handleKeyError answers
			// this with a JSON body via writeJSON. Content-Type alone is a
			// convention any intermediary can satisfy by accident (a
			// reverse proxy or API gateway in front of an operator-supplied
			// CONTROL_PLANE_URL can answer its own unrouted-404 as JSON too --
			// Kong and AWS API Gateway both do); requiring the body to also
			// decode as control-plane's own {"error": "..."} envelope, using
			// bytes already read above, is a signature an accidental JSON
			// 404 from something else won't reproduce (PR #903 second-pass
			// security review).
			return AuthSnapshot{}, statusErr
		case resp.StatusCode == http.StatusNotFound:
			// issue #816: a control-plane that booted without a database pool
			// never mounts /internal/apikeys/resolve at all (router.go's
			// RouterConfig.DBReady comment), so Go's own ServeMux answers this
			// 404 before any handler runs, via the stdlib's http.Error (plain
			// text, not JSON) -- indistinguishable from a genuine not-found by
			// status code alone, but not by Content-Type plus body shape.
			// This is exactly the cold/degraded boot state this PR exists to
			// stop lying about, and it is also where any other intermediary's
			// 404 (proxy, gateway, CDN) lands: none of them reproduce
			// control-plane's own JSON error envelope, so none of them are
			// misread as a genuine key verdict either.
			return AuthSnapshot{}, fmt.Errorf("%w: %w", ErrUpstreamUnavailable, statusErr)
		case resp.StatusCode == http.StatusConflict:
			// Kept defensive rather than a live path today: apikeys.
			// handleKeyError answers 409 for ErrRevoked/ErrDisabled/
			// ErrNotActive, but ResolveSnapshot (the only path
			// handleInternalResolve calls) never returns any of those three --
			// it returns a 200 snapshot carrying the key's real Status, and
			// edge-api's own CheckAccess denies a revoked/disabled/expired key
			// from that 200, not from a 409. If ResolveSnapshot's error
			// surface ever changes to return them, this still classifies
			// correctly as a genuine verdict rather than falling through to
			// the default branch below.
			return AuthSnapshot{}, statusErr
		case resp.StatusCode == http.StatusUnauthorized:
			// This endpoint authenticates only edge-api's own X-Internal-Token
			// (RequireInternalToken), never the customer's presented API key,
			// so a 401 here can only mean that shared secret is misconfigured
			// on one side -- a permanent condition, not a transient one.
			return AuthSnapshot{}, fmt.Errorf("%w: %w: %w", ErrUpstreamUnavailable, ErrInternalTokenRejected, statusErr)
		default:
			// Every other status (5xx from a control-plane request that
			// itself failed or was canceled, or anything unexpected) is the
			// same "no verdict reached" shape as a transport failure above.
			return AuthSnapshot{}, fmt.Errorf("%w: %w", ErrUpstreamUnavailable, statusErr)
		}
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		// A 200 status only means the response headers arrived; the client's
		// own Timeout still covers reading the body (net/http cancels the
		// request context when it fires, mid-read), and a slow or truncated
		// body read reaches no usable verdict on the key any more than a
		// transport failure before headers did.
		return AuthSnapshot{}, fmt.Errorf("authz: read response: %w: %w", ErrUpstreamUnavailable, err)
	}

	if err := json.Unmarshal(respBytes, &snap); err != nil {
		// A malformed body is a control-plane-side bug, not a verdict on the
		// key; same "no usable verdict" shape as the cases above.
		return AuthSnapshot{}, fmt.Errorf("authz: decode snapshot: %w: %w", ErrUpstreamUnavailable, err)
	}

	// 3. Cache in Redis (fire and forget).
	// Primary invalidation is active: the control-plane DELETEs this exact key
	// (auth:key:{hash}) on revoke/disable/rotate. snapshotTTL is the backstop —
	// if that DELETE is ever missed (transient Redis error, instance
	// divergence), the TTL still bounds how long a revoked key keeps
	// authorizing. Kept ≤60s so revocation takes effect within the SLA even in
	// the worst case (issue #113).
	if c.cache != nil {
		_ = c.cache.Set(ctx, redisKey, string(respBytes), snapshotTTL)
	}

	return snap, nil
}

// Degraded reports whether the most recent Resolve call that reached Redis or
// control-plane came back ErrUpstreamUnavailable. It is not a synthetic
// probe — it costs nothing beyond an atomic read, and observes only real
// traffic — so /health can react to runtime pool contention on control-plane
// without edge-api adding a single extra connection or request anywhere.
//
// A c == nil receiver reports healthy rather than panicking, so /health stays
// usable even if it is ever wired before the authz client is constructed.
func (c *Client) Degraded() bool {
	if c == nil {
		return false
	}
	return c.resolveDegraded.Load()
}
