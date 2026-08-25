package main

// Wire-contract tests for the daemon mode (issue #780): the SOCKET ARM is the
// only task-launch path any shipped deployment of this repo actually runs
// (CLAUDE.md, "Agent-engine runtime"), so this file tests exactly the pieces
// that arm exposes across the Unix socket to control-plane: authentication,
// request/response encoding, env-driven configuration defaults, and error
// propagation. None of it launches a real Apptainer sandbox: the fake
// control-plane below always fails the one call (egress-policy resolution)
// that happens before engine.Launch would ever touch exec, so every /launch
// case here fails at that exact point on purpose, deterministically, with no
// dependency on Apptainer being installed.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- pure helper function tests (no socket, no goroutine) -----------------

func TestAuthorized(t *testing.T) {
	const configured = "the-real-token"

	cases := []struct {
		name           string
		configuredTok  string
		givenHeader    string
		wantAuthorized bool
		wantStatus     int
	}{
		{"no configured token refuses even a correct-looking header", "", configured, false, http.StatusServiceUnavailable},
		{"no configured token refuses an empty header too", "", "", false, http.StatusServiceUnavailable},
		{"missing header is refused", configured, "", false, http.StatusUnauthorized},
		{"wrong header, same length, is refused", configured, "the-fake-token", false, http.StatusUnauthorized},
		{"wrong header, different length, is refused", configured, "x", false, http.StatusUnauthorized},
		{"correct header authorizes", configured, configured, true, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/launch", nil)
			// Setting an empty header is behaviorally identical to omitting
			// it (http.Header.Get returns "" either way), so every case
			// including the "missing header" ones can set it uniformly.
			req.Header.Set(InternalTokenHeader, c.givenHeader)
			rec := httptest.NewRecorder()

			got := authorized(rec, req, c.configuredTok)
			if got != c.wantAuthorized {
				t.Fatalf("authorized() = %v, want %v", got, c.wantAuthorized)
			}

			if c.wantAuthorized {
				// A successful authorization must not have written any
				// response itself — the handler writes the real one.
				if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
					t.Fatalf("authorized(true) must not write a response, got status=%d body=%q", rec.Code, rec.Body.String())
				}
				return
			}

			if rec.Code != c.wantStatus {
				t.Fatalf("expected status %d, got %d", c.wantStatus, rec.Code)
			}
			// The refusal must never leak the configured token, its
			// length as a distinguishable echo, or anything beyond a
			// generic error string.
			if configured != "" && strings.Contains(rec.Body.String(), configured) {
				t.Fatalf("refusal response leaked the configured token: %q", rec.Body.String())
			}
		})
	}
}

func TestDecode(t *testing.T) {
	t.Run("valid body decodes all fields including UUIDs", func(t *testing.T) {
		id, tenant, user := uuid.New(), uuid.New(), uuid.New()
		body, _ := json.Marshal(launchRequest{ID: id, TenantID: tenant, UserID: user, Pack: "coding-pack", Instructions: "do the thing", BearerJWT: "jwt"})
		req := httptest.NewRequest(http.MethodPost, "/launch", bytes.NewReader(body))
		rec := httptest.NewRecorder()

		var got launchRequest
		if ok := decode(rec, req, &got); !ok {
			t.Fatalf("expected decode to succeed, got status %d body %q", rec.Code, rec.Body.String())
		}
		if got.ID != id || got.TenantID != tenant || got.UserID != user || got.Pack != "coding-pack" || got.Instructions != "do the thing" || got.BearerJWT != "jwt" {
			t.Fatalf("round-tripped value mismatch: %+v", got)
		}
	})

	t.Run("malformed JSON is rejected with 400, not a panic", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader("{not json"))
		rec := httptest.NewRecorder()

		var got launchRequest
		if ok := decode(rec, req, &got); ok {
			t.Fatal("expected decode to reject malformed JSON")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("a JSON array where an object is expected is rejected, not panicked", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(`["not","an","object"]`))
		rec := httptest.NewRecorder()

		var got launchRequest
		if ok := decode(rec, req, &got); ok {
			t.Fatal("expected decode to reject a top-level array")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("an invalid UUID string in a UUID field is rejected, not silently zeroed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(`{"id":"not-a-uuid","tenant_id":"`+uuid.New().String()+`","user_id":"`+uuid.New().String()+`","pack":"coding-pack"}`))
		rec := httptest.NewRecorder()

		var got launchRequest
		if ok := decode(rec, req, &got); ok {
			t.Fatalf("expected decode to reject an invalid UUID field, got %+v", got)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unknown fields are ignored, not rejected (documents current permissive contract)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(`{"pack":"coding-pack","surprise_field":"ignored"}`))
		rec := httptest.NewRecorder()

		var got launchRequest
		if ok := decode(rec, req, &got); !ok {
			t.Fatalf("expected an unknown extra field to be ignored, got status %d body %q", rec.Code, rec.Body.String())
		}
		if got.Pack != "coding-pack" {
			t.Fatalf("expected Pack to decode despite the extra field, got %+v", got)
		}
	})

	t.Run("a body one byte over the 1 MiB cap is rejected", func(t *testing.T) {
		// A big string value padded to push the whole body past the
		// MaxBytesReader(..., 1<<20) limit decode() enforces.
		pad := strings.Repeat("a", 1<<20)
		body := `{"pack":"` + pad + `"}`
		req := httptest.NewRequest(http.MethodPost, "/launch", strings.NewReader(body))
		rec := httptest.NewRecorder()

		var got launchRequest
		if ok := decode(rec, req, &got); ok {
			t.Fatal("expected a body over the 1 MiB cap to be rejected")
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusTeapot, errorResponse{Error: "boom"})

	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected status %d, got %d", http.StatusTeapot, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
	var got errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not valid JSON: %v (%q)", err, rec.Body.String())
	}
	if got.Error != "boom" {
		t.Fatalf("expected Error %q, got %q", "boom", got.Error)
	}
}

func TestMessageTypesRoundTripJSON(t *testing.T) {
	t.Run("launchRequest", func(t *testing.T) {
		want := launchRequest{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(), Pack: "knowledge-work-pack", Instructions: "write a memo", BearerJWT: "secret-jwt"}
		raw, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var got launchRequest
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got != want {
			t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
		}
	})

	t.Run("launchResponse", func(t *testing.T) {
		want := launchResponse{SessionRef: uuid.New().String()}
		raw, _ := json.Marshal(want)
		var got launchResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got != want {
			t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
		}
	})

	t.Run("sessionRequest", func(t *testing.T) {
		want := sessionRequest{SessionRef: uuid.New().String()}
		raw, _ := json.Marshal(want)
		var got sessionRequest
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got != want {
			t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
		}
	})

	t.Run("statusResponse", func(t *testing.T) {
		want := statusResponse{Status: "failed", ResultSummary: "", ErrorMessage: "boom"}
		raw, _ := json.Marshal(want)
		var got statusResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got != want {
			t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
		}
	})

	t.Run("errorResponse", func(t *testing.T) {
		want := errorResponse{Error: "engine: resolve egress policy, refusing to launch: boom"}
		raw, _ := json.Marshal(want)
		var got errorResponse
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got != want {
			t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
		}
	})
}

func TestHostOf(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty input returns empty, not an error", "", "", false},
		{"http URL returns hostname", "http://litellm:4000", "litellm", false},
		{"https URL with path returns hostname only", "https://api.example.com/v1", "api.example.com", false},
		{"non-http(s) scheme is rejected", "ftp://example.com", "", true},
		{"no scheme at all is rejected", "example.com", "", true},
		{"http scheme with no host is rejected", "http:///v1", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := hostOf(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got host %q", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("hostOf(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestEnvInt would catch a regression in the shared parsing logic behind
// every one of this package's documented quota/limit defaults
// (HIVE_QUOTA_TENANT_CONCURRENCY=4, HIVE_QUOTA_USER_CONCURRENCY=2,
// HIVE_SANDBOX_PIDS_LIMIT=512 — CLAUDE.md, engine.go's withDefaults): a
// concurrency cap that silently stops applying when the env var is unset,
// empty, non-numeric, or non-positive is a resource-exhaustion vector, not a
// cosmetic bug.
func TestEnvInt(t *testing.T) {
	const key = "HIVE_TEST_ENVINT_VAR"
	cases := []struct {
		name     string
		setEnv   bool
		value    string
		fallback int
		want     int
	}{
		{"unset falls back", false, "", 4, 4},
		{"empty string falls back", true, "", 2, 2},
		{"valid positive integer is parsed", true, "8", 4, 8},
		{"zero is rejected, falls back (non-positive values fall back to defaults per engine.Config's doc)", true, "0", 4, 4},
		{"negative is rejected, falls back", true, "-3", 4, 4},
		{"non-numeric is rejected, falls back", true, "not-a-number", 512, 512},
		{"a numeric prefix followed by garbage is accepted (documents fmt.Sscanf's lenient prefix match, not a validation gap this test introduces)", true, "5abc", 4, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.setEnv {
				t.Setenv(key, c.value)
			} else {
				os.Unsetenv(key) //nolint:errcheck
			}
			if got := envInt(key, c.fallback); got != c.want {
				t.Fatalf("envInt(%q, %d) = %d, want %d", key, c.fallback, got, c.want)
			}
		})
	}
}

// TestEnvOr covers the string-typed defaults (HIVE_SANDBOX_MEMORY_LIMIT=4G,
// HIVE_SANDBOX_CPU_LIMIT=2).
func TestEnvOr(t *testing.T) {
	const key = "HIVE_TEST_ENVOR_VAR"
	cases := []struct {
		name     string
		setEnv   bool
		value    string
		fallback string
		want     string
	}{
		{"unset falls back", false, "", "4G", "4G"},
		{"empty string falls back", true, "", "2", "2"},
		{"explicit value overrides the fallback", true, "8G", "4G", "8G"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.setEnv {
				t.Setenv(key, c.value)
			} else {
				os.Unsetenv(key) //nolint:errcheck
			}
			if got := envOr(key, c.fallback); got != c.want {
				t.Fatalf("envOr(%q, %q) = %q, want %q", key, c.fallback, got, c.want)
			}
		})
	}
}

// --- serve() direct-call test: the missing-env-var fail-fast path ---------

// requiredServeEnvVars mirrors serve()'s own check exactly, so a table test
// can flip exactly one off at a time.
var requiredServeEnvVars = []string{
	"HIVE_AGENT_ENGINE_SIF_PATH",
	"HIVE_AGENT_ENGINE_PACKS_DIR",
	"HIVE_AGENT_ENGINE_WORKSPACE_ROOT",
	"HIVE_AGENT_ENGINE_RUN_DIR",
	"HIVE_AGENT_ENGINE_LLM_MODEL",
}

// TestServe_MissingRequiredEnvVar would catch a regression where serve()
// started up broken (or with a vague error) instead of failing fast and
// naming exactly the variable an operator needs to set — the failure mode
// CLAUDE.md calls out by name ("a boot WARN names what was missing").
func TestServe_MissingRequiredEnvVar(t *testing.T) {
	for _, missing := range requiredServeEnvVars {
		t.Run("missing "+missing, func(t *testing.T) {
			for _, v := range requiredServeEnvVars {
				if v == missing {
					t.Setenv(v, "")
				} else {
					t.Setenv(v, "dummy-value")
				}
			}

			err := serve(filepath.Join(t.TempDir(), "s.sock"), "http://127.0.0.1:1", "tok")
			if err == nil {
				t.Fatal("expected serve() to fail fast when a required env var is missing")
			}
			if !strings.Contains(err.Error(), missing) {
				t.Fatalf("expected the error to name %q, got: %v", missing, err)
			}
			for _, v := range requiredServeEnvVars {
				if v != missing && strings.Contains(err.Error(), v) {
					t.Fatalf("expected the error to name only %q, but it also names %q: %v", missing, v, err)
				}
			}
		})
	}

	t.Run("all missing are all named", func(t *testing.T) {
		for _, v := range requiredServeEnvVars {
			t.Setenv(v, "")
		}
		err := serve(filepath.Join(t.TempDir(), "s.sock"), "http://127.0.0.1:1", "tok")
		if err == nil {
			t.Fatal("expected serve() to fail fast when every required env var is missing")
		}
		for _, v := range requiredServeEnvVars {
			if !strings.Contains(err.Error(), v) {
				t.Fatalf("expected the error to name %q among the missing vars, got: %v", v, err)
			}
		}
	})
}

// --- full end-to-end test over a real Unix socket --------------------------

// unixHTTPClient dials socketPath for every request, mirroring
// apps/control-plane/internal/agentengine.Remote's own transport
// construction exactly (the real caller on the other end of this contract).
func unixHTTPClient(socketPath string) *http.Client {
	dialer := &net.Dialer{}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// waitForSocket polls until socketPath is dialable or t fails.
func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for socket %s", socketPath)
}

func postJSON(t *testing.T, client *http.Client, socketURL, path, token string, body any) *http.Response {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, socketURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(InternalTokenHeader, token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", http.MethodPost, path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(raw)
}

// TestServeSocket_WireContract runs one real daemon (serve()) over a real
// Unix socket, backed by a fake control-plane that always fails the
// egress-policy lookup, and drives it exactly as
// apps/control-plane/internal/agentengine.Remote does. It never reaches
// sandbox.BuildArgv or os/exec: every /launch case here fails deterministically
// at the egress-policy step, which is the point right before Launch would
// touch anything Apptainer-shaped.
func TestServeSocket_WireContract(t *testing.T) {
	const token = "the-real-internal-token"

	// Fake control-plane: every /internal/egress-policy/... lookup fails,
	// so Launch always errors out at ResolveEgressHosts, never reaching
	// exec. This is the real HTTP client egressclient.Client uses, pointed
	// at a server this test controls instead of a live control-plane.
	fakeControlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(fakeControlPlane.Close)

	dir := t.TempDir()
	t.Setenv("HIVE_AGENT_ENGINE_SIF_PATH", "/nonexistent.sif")
	t.Setenv("HIVE_AGENT_ENGINE_PACKS_DIR", filepath.Join(dir, "packs"))
	t.Setenv("HIVE_AGENT_ENGINE_WORKSPACE_ROOT", filepath.Join(dir, "workspace"))
	t.Setenv("HIVE_AGENT_ENGINE_RUN_DIR", filepath.Join(dir, "run"))
	t.Setenv("HIVE_AGENT_ENGINE_LLM_MODEL", "test-model")

	socketPath := filepath.Join(dir, "s.sock")
	go func() {
		// Left running for the lifetime of the test binary rather than
		// gracefully shut down: serve() has no externally reachable stop
		// hook short of an OS signal, and terminating one test process's
		// worth of these is an acceptable trade for not reaching into
		// production code to add a shutdown seam it does not otherwise
		// need. ponytail: accepted goroutine leak, bounded by this test
		// binary's own lifetime.
		_ = serve(socketPath, fakeControlPlane.URL, token)
	}()
	waitForSocket(t, socketPath)

	client := unixHTTPClient(socketPath)
	const socketURL = "http://agent-engine.internal" // host is never resolved; DialContext always dials the socket

	t.Run("health is reachable without any auth", func(t *testing.T) {
		resp, err := client.Get(socketURL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("launch with no token header is refused with 401, token not leaked", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/launch", "", launchRequest{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(), Pack: "coding-pack"})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body %q", resp.StatusCode, body)
		}
		if strings.Contains(body, token) {
			t.Fatalf("response leaked the configured token: %q", body)
		}
	})

	t.Run("launch with the wrong token is refused with 401", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/launch", "totally-wrong", launchRequest{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(), Pack: "coding-pack"})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body %q", resp.StatusCode, body)
		}
	})

	t.Run("launch with the correct token reaches the engine and propagates a specific 502, not a silent success", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/launch", token, launchRequest{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(), Pack: "coding-pack"})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("expected 502 (the egress-policy lookup was made to fail on purpose), got %d body %q", resp.StatusCode, body)
		}
		var got errorResponse
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("body is not valid JSON: %v (%q)", err, body)
		}
		if got.Error == "" {
			t.Fatal("expected a non-empty, specific error message, not an empty or generic one")
		}
		if !strings.Contains(got.Error, "egress") {
			t.Fatalf("expected the error to name the egress-policy step it actually failed at, got: %q", got.Error)
		}
	})

	t.Run("launch with malformed JSON is rejected with 400", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, socketURL+"/launch", strings.NewReader("{not json"))
		req.Header.Set(InternalTokenHeader, token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /launch: %v", err)
		}
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body %q", resp.StatusCode, body)
		}
	})

	t.Run("status for an unknown session reference maps to 404, never to success", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/status", token, sessionRequest{SessionRef: uuid.New().String()})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body %q", resp.StatusCode, body)
		}
	})

	t.Run("status for a session reference that is not even a UUID also maps to 404, not 400 or a panic", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/status", token, sessionRequest{SessionRef: "definitely-not-a-uuid"})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body %q", resp.StatusCode, body)
		}
	})

	t.Run("cancel for an unknown session reference maps to 404", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/cancel", token, sessionRequest{SessionRef: uuid.New().String()})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body %q", resp.StatusCode, body)
		}
	})

	t.Run("events for an unknown session reference maps to 404", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/events", token, map[string]any{"session_ref": uuid.New().String(), "offset": 0})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body %q", resp.StatusCode, body)
		}
	})

	t.Run("files for an unknown session reference maps to 404", func(t *testing.T) {
		resp := postJSON(t, client, socketURL, "/files", token, sessionRequest{SessionRef: uuid.New().String()})
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body %q", resp.StatusCode, body)
		}
	})
}

// TestServeSocket_NoConfiguredToken_RefusesEverythingFailClosed is kept
// separate from TestServeSocket_WireContract because it needs its own daemon
// started with an empty controlPlaneToken (the fail-closed, misconfigured-
// deploy case) rather than the correctly-configured one every other subtest
// above shares.
func TestServeSocket_NoConfiguredToken_RefusesEverythingFailClosed(t *testing.T) {
	fakeControlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(fakeControlPlane.Close)

	dir := t.TempDir()
	t.Setenv("HIVE_AGENT_ENGINE_SIF_PATH", "/nonexistent.sif")
	t.Setenv("HIVE_AGENT_ENGINE_PACKS_DIR", filepath.Join(dir, "packs"))
	t.Setenv("HIVE_AGENT_ENGINE_WORKSPACE_ROOT", filepath.Join(dir, "workspace"))
	t.Setenv("HIVE_AGENT_ENGINE_RUN_DIR", filepath.Join(dir, "run"))
	t.Setenv("HIVE_AGENT_ENGINE_LLM_MODEL", "test-model")

	socketPath := filepath.Join(dir, "s.sock")
	go func() {
		_ = serve(socketPath, fakeControlPlane.URL, "") // ponytail: same leaked-goroutine trade-off as above
	}()
	waitForSocket(t, socketPath)

	client := unixHTTPClient(socketPath)
	resp := postJSON(t, client, "http://agent-engine.internal", "/launch", "any-token-at-all", launchRequest{Pack: "coding-pack"})
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (fail-closed: no internal token configured), got %d body %q", resp.StatusCode, body)
	}
}

// --- resolveMCPConfigPath: the one main.go helper that is real wiring   ---
// --- logic rather than a launch/exec path, and so is reachable without  ---
// --- Apptainer.                                                        ---

// TestResolveMCPConfigPath_WritesConfigWhenEntriesEnabled would catch a
// regression where an enabled MCP server catalog entry silently stopped
// reaching the sandbox's mounted config file.
func TestResolveMCPConfigPath_WritesConfigWhenEntriesEnabled(t *testing.T) {
	tenant := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": tenant,
			"servers": []map[string]any{
				{"name": "filesystem", "config": map[string]any{"command": "mcp-server-fs", "args": []string{"--root", "/workspace"}}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := resolveMCPConfigPath(context.Background(), srv.URL, "tok", tenant, dir)
	if path == "" {
		t.Fatal("expected a non-empty config path when entries are enabled")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the config file to exist at %s: %v", path, err)
	}
	if !strings.Contains(string(raw), "filesystem") || !strings.Contains(string(raw), "mcp-server-fs") {
		t.Fatalf("expected the written config to contain the enabled server, got: %s", raw)
	}
}

// TestResolveMCPConfigPath_NoEntriesReturnsEmptyPathAndWritesNoFile would
// catch a regression where an empty catalog started writing an unnecessary
// (or malformed) file instead of the documented "mount nothing" no-op.
func TestResolveMCPConfigPath_NoEntriesReturnsEmptyPathAndWritesNoFile(t *testing.T) {
	tenant := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tenant_id": tenant, "servers": []map[string]any{}})
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := resolveMCPConfigPath(context.Background(), srv.URL, "tok", tenant, dir)
	if path != "" {
		t.Fatalf("expected an empty path for zero enabled entries, got %q", path)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("expected no file written for zero enabled entries, found: %v", entries)
	}
}

// TestResolveMCPConfigPath_FailsOpenOnClientError would catch a regression
// of the documented fail-open contract (main.go's doc comment): unlike the
// egress allowlist, a marketplace lookup failure must never block or fail
// the sandbox launch, only leave it with no MCP servers configured.
func TestResolveMCPConfigPath_FailsOpenOnClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := resolveMCPConfigPath(context.Background(), srv.URL, "tok", uuid.New(), dir)
	if path != "" {
		t.Fatalf("expected an empty path when the marketplace lookup fails, got %q", path)
	}
}

// TestResolveMCPConfigPath_FailsOpenWhenWriteFails covers the third failure
// branch (a write error, e.g. the target directory does not exist) failing
// open the same way, rather than propagating an error the caller (main())
// has nothing sensible to do with.
func TestResolveMCPConfigPath_FailsOpenWhenWriteFails(t *testing.T) {
	tenant := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": tenant,
			"servers":   []map[string]any{{"name": "x", "config": map[string]any{"command": "y"}}},
		})
	}))
	t.Cleanup(srv.Close)

	// A directory that is never created: os.WriteFile fails because its
	// parent does not exist.
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	path := resolveMCPConfigPath(context.Background(), srv.URL, "tok", tenant, dir)
	if path != "" {
		t.Fatalf("expected an empty path when the write fails, got %q", path)
	}
}
