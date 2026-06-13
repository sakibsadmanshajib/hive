package litellmconfig_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/litellmconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSyncService satisfies litellmconfig.SyncRunner for HTTP handler tests.
type fakeSyncService struct {
	calls int
	err   error
}

func (f *fakeSyncService) Sync(ctx context.Context) error {
	f.calls++
	return f.err
}

func TestSyncHandlerReturns200OnSuccess(t *testing.T) {
	svc := &fakeSyncService{}
	h := litellmconfig.NewSyncHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/litellm/sync", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, 1, svc.calls)
}

func TestSyncHandlerReturns500OnSyncFailure(t *testing.T) {
	svc := &fakeSyncService{err: context.DeadlineExceeded}
	h := litellmconfig.NewSyncHandler(svc)

	req := httptest.NewRequest(http.MethodPost, "/internal/litellm/sync", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestSyncHandlerReturns405OnGet(t *testing.T) {
	svc := &fakeSyncService{}
	h := litellmconfig.NewSyncHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/internal/litellm/sync", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Equal(t, 0, svc.calls)
}
