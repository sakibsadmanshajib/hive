package controlclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/controlclient"
)

// newFakeAgentServer starts an httptest.Server bound to a Unix socket at
// socketPath instead of the usual TCP listener, so tests exercise the exact
// transport apps/agent-engine/internal/sandbox's control channel uses
// (Client.New dials socketPath directly).
func newFakeAgentServer(t *testing.T, handler http.Handler) (socketPath string) {
	t.Helper()
	socketPath = filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return socketPath
}

func TestWaitReady_SucceedsOnceSocketExists(t *testing.T) {
	handler := http.NewServeMux()
	socketPath := newFakeAgentServer(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := controlclient.WaitReady(ctx, socketPath); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
}

func TestWaitReady_TimesOutWhenSocketNeverAppears(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := controlclient.WaitReady(ctx, filepath.Join(t.TempDir(), "never.sock"))
	if err == nil {
		t.Fatal("expected WaitReady to time out for a socket that never appears")
	}
}

func TestClient_StartConversation(t *testing.T) {
	convoID := uuid.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var req controlclient.StartConversationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Workspace.Kind != "LocalWorkspace" || req.Workspace.WorkingDir != "/workspace" {
			t.Fatalf("unexpected workspace in request: %+v", req.Workspace)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(controlclient.ConversationInfo{
			ID:              convoID,
			ExecutionStatus: controlclient.StatusIdle,
		})
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "")

	info, err := client.StartConversation(context.Background(), controlclient.StartConversationRequest{
		Workspace: controlclient.LocalWorkspace("/workspace"),
	})
	if err != nil {
		t.Fatalf("StartConversation: %v", err)
	}
	if info.ID != convoID {
		t.Fatalf("got ID %s, want %s", info.ID, convoID)
	}
	if info.ExecutionStatus != controlclient.StatusIdle {
		t.Fatalf("got status %q, want idle", info.ExecutionStatus)
	}
}

func TestClient_Run_TreatsConflictAsSuccess(t *testing.T) {
	convoID := uuid.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations/"+convoID.String()+"/run", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"detail": "already running"})
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "")

	if err := client.Run(context.Background(), convoID); err != nil {
		t.Fatalf("expected 409 to be treated as success, got %v", err)
	}
}

func TestClient_Run_PropagatesOtherErrors(t *testing.T) {
	convoID := uuid.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations/"+convoID.String()+"/run", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "")

	err := client.Run(context.Background(), convoID)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	var statusErr *controlclient.StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected *StatusError with 404, got %v", err)
	}
}

func TestClient_GetConversation(t *testing.T) {
	convoID := uuid.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations/"+convoID.String(), func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET, got %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(controlclient.ConversationInfo{
			ID:              convoID,
			ExecutionStatus: controlclient.StatusRunning,
		})
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "")

	info, err := client.GetConversation(context.Background(), convoID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if info.ExecutionStatus != controlclient.StatusRunning {
		t.Fatalf("got status %q, want running", info.ExecutionStatus)
	}
}

func TestClient_Interrupt(t *testing.T) {
	convoID := uuid.New()
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations/"+convoID.String()+"/interrupt", func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "")

	if err := client.Interrupt(context.Background(), convoID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if !called {
		t.Fatal("expected interrupt endpoint to be called")
	}
}

func TestClient_FinalResponse(t *testing.T) {
	convoID := uuid.New()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations/"+convoID.String()+"/agent_final_response", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"response": "done"})
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "")

	got, err := client.FinalResponse(context.Background(), convoID)
	if err != nil {
		t.Fatalf("FinalResponse: %v", err)
	}
	if got != "done" {
		t.Fatalf("got %q, want %q", got, "done")
	}
}

func TestClient_SendsSessionAPIKeyHeaderWhenSet(t *testing.T) {
	convoID := uuid.New()
	var gotHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations/"+convoID.String(), func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(controlclient.SessionAPIKeyHeader)
		_ = json.NewEncoder(w).Encode(controlclient.ConversationInfo{ID: convoID})
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "secret-key")

	if _, err := client.GetConversation(context.Background(), convoID); err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if gotHeader != "secret-key" {
		t.Fatalf("got %s header %q, want %q", controlclient.SessionAPIKeyHeader, gotHeader, "secret-key")
	}
}

func TestClient_OmitsSessionAPIKeyHeaderWhenEmpty(t *testing.T) {
	convoID := uuid.New()
	var sawHeader bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/conversations/"+convoID.String(), func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header[controlclient.SessionAPIKeyHeader]
		_ = json.NewEncoder(w).Encode(controlclient.ConversationInfo{ID: convoID})
	})
	socketPath := newFakeAgentServer(t, mux)
	client := controlclient.New(socketPath, "")

	if _, err := client.GetConversation(context.Background(), convoID); err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if sawHeader {
		t.Fatal("expected no session API key header when Client was constructed with an empty key")
	}
}
