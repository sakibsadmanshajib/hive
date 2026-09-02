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
	"sort"
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

// A launcher whose packs directory is wrong can serve no task at all since
// issue #1360 made a launch fail closed on it, so it refuses to boot rather
// than reporting healthy and failing every task with a warn line nobody reads.
func TestServe_RefusesToStartWhenThePacksDirIsNotReadable(t *testing.T) {
	// Each case is a packs path that cannot serve a single task. A missing
	// one is the typo or the drifted checkout; a regular file and an
	// unreadable directory both satisfy a plain stat and neither can be
	// listed, which is what a launch actually does to it.
	cases := map[string]func(t *testing.T, dir string) string{
		"missing": func(_ *testing.T, dir string) string {
			return filepath.Join(dir, "packs-that-are-not-there")
		},
		"regular file": func(t *testing.T, dir string) string {
			path := filepath.Join(dir, "packs-is-a-file")
			if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
				t.Fatalf("write file: %v", err)
			}
			return path
		},
		"unreadable directory": func(t *testing.T, dir string) string {
			path := filepath.Join(dir, "packs-unreadable")
			if err := os.Mkdir(path, 0o000); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			// Restored so the test framework can clean the tree up.
			t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
			return path
		},
	}

	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			if name == "unreadable directory" && os.Geteuid() == 0 {
				t.Skip("root reads a 0000 directory regardless of its mode")
			}
			dir := t.TempDir()
			for _, v := range requiredServeEnvVars {
				t.Setenv(v, "dummy-value")
			}
			t.Setenv("HIVE_AGENT_ENGINE_WORKSPACE_ROOT", filepath.Join(dir, "workspace"))
			t.Setenv("HIVE_AGENT_ENGINE_RUN_DIR", filepath.Join(dir, "run"))
			t.Setenv("HIVE_AGENT_ENGINE_PACKS_DIR", setup(t, dir))

			err := serve(filepath.Join(dir, "s.sock"), "http://127.0.0.1:1", "tok")
			if err == nil {
				t.Fatal("expected serve() to refuse to start with an unusable packs directory")
			}
			if !strings.Contains(err.Error(), "HIVE_AGENT_ENGINE_PACKS_DIR") {
				t.Fatalf("expected the error to name the variable, got: %v", err)
			}
		})
	}
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
	// Created, not merely named: serve() refuses to start without a readable
	// packs directory, since a launch fails closed on a missing pack.
	if err := os.MkdirAll(filepath.Join(dir, "packs"), 0o700); err != nil {
		t.Fatalf("mkdir packs: %v", err)
	}
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
		// This case is about AUTH, not readiness. It used to assert 200,
		// which was the same assertion only because /health was a static 200
		// for everyone. Since issue #1510 it answers for the ability to
		// launch, and this fixture deliberately points at a dummy SIF path,
		// so a ready 200 is not available here and demanding one would be
		// asserting the wrong thing.
		//
		// What must hold is that the endpoint is reachable with no token at
		// all: never 401, never 503-for-missing-token, which is what
		// authorized() returns when no token is configured. Anything else
		// would mean the probe and the installer had to authenticate to ask
		// whether the launcher is alive.
		resp, err := client.Get(socketURL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("/health demanded authentication (401); it must be reachable without a token")
		}
		body := readBody(t, resp)
		if strings.Contains(body, "internal token") {
			t.Fatalf("/health answered with an auth error rather than a readiness answer: %s", body)
		}
		// And whichever way it answers, it answers about readiness.
		if !strings.Contains(body, `"status":"ok"`) && !strings.Contains(body, `"status":"unhealthy"`) {
			t.Fatalf("expected a readiness payload from /health, got %s", body)
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
	// Created, not merely named: serve() refuses to start without a readable
	// packs directory, since a launch fails closed on a missing pack.
	if err := os.MkdirAll(filepath.Join(dir, "packs"), 0o700); err != nil {
		t.Fatalf("mkdir packs: %v", err)
	}
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

// --- launch readiness (issue #1510) ---------------------------------------
//
// /health used to be a static 200, which reported that net/http was alive and
// nothing about whether this daemon could launch anything.
//
// Two distinct outages, kept separate here on purpose rather than letting one
// check take credit for both:
//
//   - The launcher PROCESS dies. The socket goes with it, so the five minute
//     probe already catches that before it issues any request. Nothing below
//     is about that case.
//   - The launcher runs perfectly on top of a DELETED or HALF DOWNLOADED
//     IMAGE. Only this endpoint can see that, and only if it actually looks.
//     The installer deliberately keeps the SIF out of its restart fingerprint
//     (hashing a multi-gigabyte image every deploy costs more than the
//     restart it avoids, and sandbox.BuildArgv reads the file per launch), so
//     nothing restarts on it either. With a static 200 every task would fail
//     forever while the supervision reported green forever.

// writeFakeSIF writes a file that passes the header check: the 32 byte launch
// header, then the magic. Not a real image, deliberately, since the check is a
// header check and depending on a real multi-gigabyte artifact would make this
// suite depend on CI having built one.
func writeFakeSIF(t *testing.T, path string) {
	t.Helper()
	header := []byte("#!/usr/bin/env run-singularity\n\x00")
	if len(header) != sifMagicOffset {
		t.Fatalf("fixture launch header is %d bytes, the format puts the magic at %d", len(header), sifMagicOffset)
	}
	body := append(header, []byte(sifMagic)...)
	body = append(body, []byte("\x0001\x0002\x00padding")...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// readyDirs builds a tree where every precondition holds.
func readyDirs(t *testing.T) (sif, packs, workspace, run string) {
	t.Helper()
	root := t.TempDir()
	sif = filepath.Join(root, "agent-engine.sif")
	packs = filepath.Join(root, "packs")
	workspace = filepath.Join(root, "workspaces")
	run = filepath.Join(root, "sessions")
	writeFakeSIF(t, sif)
	for _, d := range []string{packs, workspace, run} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return sif, packs, workspace, run
}

// stubApptainerOnPath points PATH at a directory holding an apptainer stub, so
// the PATH arm is exercised in both directions instead of being skipped
// because the test image has no real apptainer.
func stubApptainerOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "apptainer"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func hasFailureAbout(failures []string, substr string) bool {
	for _, f := range failures {
		if strings.Contains(f, substr) {
			return true
		}
	}
	return false
}

func TestLaunchReadiness(t *testing.T) {
	t.Run("a complete tree reports nothing", func(t *testing.T) {
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		if got := launchReadiness(sif, packs, ws, run); len(got) != 0 {
			t.Fatalf("expected a ready launcher to report nothing, got %v", got)
		}
	})

	t.Run("apptainer missing from PATH", func(t *testing.T) {
		// Total outage: the launcher execs apptainer by name for every task,
		// and its absence is invisible until someone submits one.
		t.Setenv("PATH", t.TempDir())
		sif, packs, ws, run := readyDirs(t)
		if got := launchReadiness(sif, packs, ws, run); !hasFailureAbout(got, "apptainer is not on PATH") {
			t.Fatalf("expected a missing apptainer to be reported, got %v", got)
		}
	})

	t.Run("the image is absent", func(t *testing.T) {
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		if err := os.Remove(sif); err != nil {
			t.Fatal(err)
		}
		if got := launchReadiness(sif, packs, ws, run); !hasFailureAbout(got, "is not readable") {
			t.Fatalf("expected a deleted image to be reported, got %v", got)
		}
	})

	t.Run("the image is empty", func(t *testing.T) {
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		if err := os.WriteFile(sif, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := launchReadiness(sif, packs, ws, run); !hasFailureAbout(got, "is empty") {
			t.Fatalf("expected an empty image to be reported, got %v", got)
		}
	})

	t.Run("the image is truncated mid header", func(t *testing.T) {
		// The half-downloaded case, which is what the installer's own
		// curl-and-unzip path produces when interrupted. Non-empty, so a size
		// check alone would report it healthy.
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		if err := os.WriteFile(sif, []byte("#!/usr/bin/env run-sing"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := launchReadiness(sif, packs, ws, run); !hasFailureAbout(got, "shorter than a SIF header") {
			t.Fatalf("expected a truncated image to be reported, got %v", got)
		}
	})

	t.Run("the image is long enough but is not an image", func(t *testing.T) {
		// An HTML error page saved under the image's name, which is what an
		// artifact download returns once the token has expired. Long enough to
		// pass both a size check and a short-read check.
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		if err := os.WriteFile(sif, []byte(strings.Repeat("<html>not a sif</html>", 16)), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := launchReadiness(sif, packs, ws, run); !hasFailureAbout(got, "does not carry the SIF magic") {
			t.Fatalf("expected a non-image file to be reported, got %v", got)
		}
	})

	t.Run("the packs directory is gone", func(t *testing.T) {
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		if err := os.Remove(packs); err != nil {
			t.Fatal(err)
		}
		if got := launchReadiness(sif, packs, ws, run); !hasFailureAbout(got, "packs directory") {
			t.Fatalf("expected a missing packs directory to be reported, got %v", got)
		}
	})

	t.Run("a state directory is not writable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores the mode, so this case cannot be expressed as root")
		}
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		if err := os.Chmod(run, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(run, 0o700) })
		if got := launchReadiness(sif, packs, ws, run); !hasFailureAbout(got, "run directory") {
			t.Fatalf("expected an unwritable run directory to be reported, got %v", got)
		}
	})

	t.Run("the writability probe leaves nothing behind", func(t *testing.T) {
		// It proves writability by creating a file. A probe that forgot to
		// remove it would deposit 288 files a day into the workspace root.
		stubApptainerOnPath(t)
		sif, packs, ws, run := readyDirs(t)
		for i := 0; i < 3; i++ {
			if got := launchReadiness(sif, packs, ws, run); len(got) != 0 {
				t.Fatalf("unexpected failures: %v", got)
			}
		}
		for _, dir := range []string{ws, run} {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("the probe left %d file(s) in %s", len(entries), dir)
			}
		}
	})

	t.Run("every failure is reported, not just the first", func(t *testing.T) {
		// A check that stopped at the first problem would send an operator to
		// fix one thing and find the launcher still down.
		t.Setenv("PATH", t.TempDir())
		sif, packs, ws, run := readyDirs(t)
		if err := os.Remove(sif); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(packs); err != nil {
			t.Fatal(err)
		}
		got := launchReadiness(sif, packs, ws, run)
		if len(got) < 3 {
			t.Fatalf("expected apptainer, image and packs all reported, got %v", got)
		}
	})

	t.Run("the failure list is ordered", func(t *testing.T) {
		// The directory checks iterate a map. Unsorted output would reorder
		// itself between two probes of the same broken box and read as two
		// different problems in the alert mail.
		t.Setenv("PATH", t.TempDir())
		sif, packs, ws, run := readyDirs(t)
		if err := os.Remove(sif); err != nil {
			t.Fatal(err)
		}
		got := launchReadiness(sif, packs, ws, run)
		if !sort.StringsAreSorted(got) {
			t.Fatalf("expected a stable ordering, got %v", got)
		}
	})
}

func TestHealthEndpointAnswersForTheAbilityToLaunch(t *testing.T) {
	stubApptainerOnPath(t)
	sif, packs, ws, run := readyDirs(t)

	for _, v := range requiredServeEnvVars {
		t.Setenv(v, "dummy-value")
	}
	t.Setenv("HIVE_AGENT_ENGINE_SIF_PATH", sif)
	t.Setenv("HIVE_AGENT_ENGINE_PACKS_DIR", packs)
	t.Setenv("HIVE_AGENT_ENGINE_WORKSPACE_ROOT", ws)
	t.Setenv("HIVE_AGENT_ENGINE_RUN_DIR", run)

	socket := filepath.Join(t.TempDir(), "s.sock")
	go func() { _ = serve(socket, "http://127.0.0.1:1", "tok") }()
	waitForSocket(t, socket)

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	get := func(t *testing.T) (int, string) {
		t.Helper()
		resp, err := client.Get("http://localhost/health")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(body)
	}

	t.Run("a ready launcher answers 200 with the existing payload", func(t *testing.T) {
		// The payload shape is a contract: the installer greps this endpoint
		// and so does the five minute probe.
		code, body := get(t)
		if code != http.StatusOK {
			t.Fatalf("expected 200 from a ready launcher, got %d: %s", code, body)
		}
		if !strings.Contains(body, `"status":"ok"`) {
			t.Fatalf("expected the existing ok payload to be preserved, got %s", body)
		}
	})

	t.Run("deleting the image turns the same endpoint red", func(t *testing.T) {
		// The whole point. Before this change the endpoint answered 200 here,
		// forever, while every task failed.
		if err := os.Remove(sif); err != nil {
			t.Fatal(err)
		}
		code, body := get(t)
		if code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 with the image deleted, got %d: %s", code, body)
		}
		if !strings.Contains(body, `"status":"unhealthy"`) {
			t.Fatalf("expected an unhealthy payload, got %s", body)
		}
		if !strings.Contains(body, "is not readable") {
			t.Fatalf("expected the payload to name the missing image, got %s", body)
		}
	})

	t.Run("and it recovers without a restart", func(t *testing.T) {
		// Readiness is evaluated per request rather than cached at start-up,
		// which matters because the installer deliberately does not restart
		// the daemon when only the image changed.
		writeFakeSIF(t, sif)
		code, body := get(t)
		if code != http.StatusOK {
			t.Fatalf("expected the restored image to make the same process healthy again, got %d: %s", code, body)
		}
	})
}
