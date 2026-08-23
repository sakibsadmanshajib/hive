package usermemories_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usermemories"
)

// fakeRepository implements usermemories.Repository over an in-memory map
// keyed by "tenant|user|id". It enforces the same scoping semantics the real
// pgx repository gets from RLS plus explicit WHERE user_id filters, so
// handler-level scoping assertions are meaningful.
type fakeRepository struct {
	memories  map[string]usermemories.Memory
	evictions []int
	failEvict bool
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{memories: map[string]usermemories.Memory{}}
}

func (f *fakeRepository) memKey(tenantID, userID, id uuid.UUID) string {
	return tenantID.String() + "|" + userID.String() + "|" + id.String()
}

func (f *fakeRepository) Create(_ context.Context, tenantID, userID uuid.UUID, content string, sourceChatID *string) (usermemories.Memory, error) {
	m := usermemories.Memory{
		ID:           uuid.New(),
		TenantID:     tenantID,
		UserID:       userID,
		Content:      content,
		SourceChatID: sourceChatID,
		CreatedAt:    time.Now().UTC(),
	}
	m.UpdatedAt = m.CreatedAt
	f.memories[f.memKey(tenantID, userID, m.ID)] = m
	return m, nil
}

func (f *fakeRepository) Get(_ context.Context, tenantID, userID, id uuid.UUID) (usermemories.Memory, error) {
	m, ok := f.memories[f.memKey(tenantID, userID, id)]
	if !ok {
		return usermemories.Memory{}, usermemories.ErrNotFound
	}
	return m, nil
}

func (f *fakeRepository) List(_ context.Context, tenantID, userID uuid.UUID) ([]usermemories.Memory, error) {
	var out []usermemories.Memory
	for k, m := range f.memories {
		prefix := tenantID.String() + "|" + userID.String() + "|"
		if strings.HasPrefix(k, prefix) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeRepository) Update(_ context.Context, tenantID, userID, id uuid.UUID, content string) (usermemories.Memory, error) {
	k := f.memKey(tenantID, userID, id)
	m, ok := f.memories[k]
	if !ok {
		return usermemories.Memory{}, usermemories.ErrNotFound
	}
	m.Content = content
	m.UpdatedAt = time.Now().UTC()
	f.memories[k] = m
	return m, nil
}

func (f *fakeRepository) Delete(_ context.Context, tenantID, userID, id uuid.UUID) error {
	k := f.memKey(tenantID, userID, id)
	if _, ok := f.memories[k]; !ok {
		return usermemories.ErrNotFound
	}
	delete(f.memories, k)
	return nil
}

func (f *fakeRepository) EvictOldest(_ context.Context, tenantID, userID uuid.UUID, keep int) (int64, error) {
	f.evictions = append(f.evictions, keep)
	if f.failEvict {
		return 0, errors.New("evict failed")
	}
	return 0, nil
}

func newTestHandler() (*usermemories.Handler, *fakeRepository) {
	repo := newFakeRepository()
	return usermemories.NewHandler(usermemories.NewService(repo)), repo
}

func doReq(h *usermemories.Handler, method, path string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	w := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(w, req)
	return w
}

func TestHandler_Create_HappyPath(t *testing.T) {
	h, _ := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	w := doReq(h, http.MethodPost,
		"/internal/user-memories/"+tenantID.String()+"/"+userID.String(),
		map[string]string{"content": "Prefers concise answers"})

	require.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "Prefers concise answers", resp["content"])
	require.NotContains(t, resp, "tenant_id")
	require.NotContains(t, resp, "user_id")
}

func TestHandler_Create_EmptyContentRejected(t *testing.T) {
	h, _ := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	w := doReq(h, http.MethodPost,
		"/internal/user-memories/"+tenantID.String()+"/"+userID.String(),
		map[string]string{"content": "   \n\t "})

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_List_ReturnsScopedMemories(t *testing.T) {
	h, repo := newTestHandler()
	tenantID := uuid.New()
	userA, userB := uuid.New(), uuid.New()

	doReq(h, http.MethodPost, memoryURL(tenantID, userA), map[string]string{"content": "A1"})
	doReq(h, http.MethodPost, memoryURL(tenantID, userA), map[string]string{"content": "A2"})
	doReq(h, http.MethodPost, memoryURL(tenantID, userB), map[string]string{"content": "B1"})

	w := doReq(h, http.MethodGet, memoryURL(tenantID, userA), nil)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Memories []map[string]any `json:"memories"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Memories, 2)
	for _, m := range resp.Memories {
		require.NotContains(t, m, "tenant_id")
	}
	// Three creates (two for userA, one for userB), each runs cap eviction.
	require.Len(t, repo.evictions, 3)
}

func TestHandler_Update_HappyPathAndCrossUser(t *testing.T) {
	h, _ := newTestHandler()
	tenantID := uuid.New()
	userA, userB := uuid.New(), uuid.New()

	w := doReq(h, http.MethodPost, memoryURL(tenantID, userA), map[string]string{"content": "before"})
	require.Equal(t, http.StatusCreated, w.Code)
	var created map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&created))
	id := created["id"].(string)

	// Same user: update succeeds.
	w = doReq(h, http.MethodPatch,
		memoryURL(tenantID, userA)+"/"+id,
		map[string]string{"content": "after"})
	require.Equal(t, http.StatusOK, w.Code)

	// Different user in the SAME tenant: 404, never a leak.
	w = doReq(h, http.MethodPatch,
		memoryURL(tenantID, userB)+"/"+id,
		map[string]string{"content": "hijack"})
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_Delete_CrossTenantIs404(t *testing.T) {
	h, _ := newTestHandler()
	tenantA, tenantB := uuid.New(), uuid.New()
	userID := uuid.New()

	w := doReq(h, http.MethodPost, memoryURL(tenantA, userID), map[string]string{"content": "mine"})
	var created map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&created))
	id := created["id"].(string)

	w = doReq(h, http.MethodDelete, memoryURL(tenantB, userID)+"/"+id, nil)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_BadPathsAndMethods(t *testing.T) {
	h, _ := newTestHandler()
	tenantID, userID := uuid.New(), uuid.New()

	require.Equal(t, http.StatusBadRequest, doReq(h, http.MethodGet, "/internal/user-memories/not-a-uuid/"+userID.String(), nil).Code)
	require.Equal(t, http.StatusMethodNotAllowed, doReq(h, http.MethodPut, memoryURL(tenantID, userID), nil).Code)
	require.Equal(t, http.StatusNotFound, doReq(h, http.MethodGet, "/internal/user-memories/", nil).Code)
}

func memoryURL(tenantID, userID uuid.UUID) string {
	return "/internal/user-memories/" + tenantID.String() + "/" + userID.String()
}
