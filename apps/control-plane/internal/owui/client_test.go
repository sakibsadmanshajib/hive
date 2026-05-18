package owui_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/owui"
)

func TestClient_AddUserToGroup_PostsExpectedShape(t *testing.T) {
	var captured struct {
		Path   string
		Method string
		Auth   string
		Body   map[string]string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		captured.Method = r.Method
		captured.Auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	c := owui.New(owui.Config{
		BaseURL:    srv.URL,
		AdminToken: "owui-admin-token",
	})

	err := c.AddUserToGroup(context.Background(), "grp-123", "user@office.example")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, captured.Method)
	require.Equal(t, "/api/v1/groups/grp-123/add-user", captured.Path)
	require.Equal(t, "Bearer owui-admin-token", captured.Auth)
	require.Equal(t, "user@office.example", captured.Body["user_email"])
}

func TestClient_AddUserToGroup_4xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"group not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "t"})
	err := c.AddUserToGroup(context.Background(), "grp-404", "user@office.example")
	require.Error(t, err)
}

func TestClient_CreateGroup_Idempotent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/groups" {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"id":"grp-new","name":"tenant_a"}`))
				return
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"detail":"already exists"}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/groups" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"grp-new","name":"tenant_a"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "t"})
	id, err := c.EnsureGroup(context.Background(), "tenant_a")
	require.NoError(t, err)
	require.Equal(t, "grp-new", id)
	id2, err := c.EnsureGroup(context.Background(), "tenant_a")
	require.NoError(t, err)
	require.NotEmpty(t, id2, "EnsureGroup must look up the existing group on 409")
	require.Equal(t, "grp-new", id2)
}
