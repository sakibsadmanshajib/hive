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

// ---------------------------------------------------------------------------
// SyncModelAccessControl tests (Task 5 — plan 20-04).
// ---------------------------------------------------------------------------

// TC1: non-empty allowedGroupIDs sends the correct access_control JSON body.
func TestSyncModelAccessControl_NonEmpty_SendsCorrectBody(t *testing.T) {
	var captured struct {
		Path string
		Body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "tok"})
	err := c.SyncModelAccessControl(context.Background(), "hive-default", []string{"tenant_abc", "tenant_def"})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/models/", captured.Path)
	require.Equal(t, "hive-default", captured.Body["id"])

	ac, ok := captured.Body["access_control"].(map[string]any)
	require.True(t, ok, "access_control must be an object")

	read, ok := ac["read"].(map[string]any)
	require.True(t, ok, "access_control.read must be an object")

	groupIDs, ok := read["group_ids"].([]any)
	require.True(t, ok, "access_control.read.group_ids must be an array")
	require.Len(t, groupIDs, 2)
	require.Equal(t, "tenant_abc", groupIDs[0])
	require.Equal(t, "tenant_def", groupIDs[1])
}

// TC2: empty allowedGroupIDs sends access_control: null (public model).
func TestSyncModelAccessControl_Empty_SendsNull(t *testing.T) {
	var captured struct {
		Body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "tok"})
	err := c.SyncModelAccessControl(context.Background(), "hive-default", nil)
	require.NoError(t, err)

	// access_control key must be present and null.
	acRaw, exists := captured.Body["access_control"]
	require.True(t, exists, "access_control key must be present in request body")
	require.Nil(t, acRaw, "access_control must be null when allowedGroupIDs is empty")
}

// TC3: nil allowedGroupIDs also sends access_control: null.
func TestSyncModelAccessControl_NilSlice_SendsNull(t *testing.T) {
	var captured struct {
		Body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "tok"})
	var groups []string
	err := c.SyncModelAccessControl(context.Background(), "hive-default", groups)
	require.NoError(t, err)

	acRaw, exists := captured.Body["access_control"]
	require.True(t, exists, "access_control key must be present")
	require.Nil(t, acRaw, "access_control must be null for nil slice")
}

// TC4: HTTP 401 from OWUI returns a typed *owui.APIError with Status 401.
func TestSyncModelAccessControl_401_ReturnsTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"unauthorized"}`))
	}))
	defer srv.Close()

	c := owui.New(owui.Config{BaseURL: srv.URL, AdminToken: "bad-token"})
	err := c.SyncModelAccessControl(context.Background(), "hive-default", []string{"tenant_abc"})
	require.Error(t, err)

	var apiErr *owui.APIError
	require.ErrorAs(t, err, &apiErr, "error must wrap *owui.APIError")
	require.Equal(t, http.StatusUnauthorized, apiErr.Status)
}
