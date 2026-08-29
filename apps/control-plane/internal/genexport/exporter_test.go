package genexport_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/genexport"
)

// fakeSource stands in for the Postgres reader so the loop can be tested
// without a database. It records every call so a test can assert that a
// disabled exporter touches it not at all.
type fakeSource struct {
	mu         sync.Mutex
	rows       []genexport.Row
	fetchCalls int
	advanced   []genexport.Cursor
	fetchErr   error
	drained    bool
}

func (f *fakeSource) Fetch(_ context.Context, limit int) ([]genexport.Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetchCalls++
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if f.drained {
		return nil, nil
	}
	if limit < len(f.rows) {
		return f.rows[:limit], nil
	}
	return f.rows, nil
}

func (f *fakeSource) Advance(_ context.Context, to genexport.Cursor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advanced = append(f.advanced, to)
	f.drained = true
	return nil
}

func (f *fakeSource) calls() (int, []genexport.Cursor) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fetchCalls, append([]genexport.Cursor(nil), f.advanced...)
}

func rowsFixture(n int) []genexport.Row {
	rows := make([]genexport.Row, 0, n)
	for i := 0; i < n; i++ {
		attempt := completedAttempt()
		attempt.ID = fmt.Sprintf("attempt-%02d", i)
		attempt.RequestID = fmt.Sprintf("req_%02d", i)
		event := completedEvent()
		event.ID = fmt.Sprintf("event-%02d", i)
		event.CreatedAt = testEnd.Add(time.Duration(i) * time.Second)
		rows = append(rows, genexport.Row{Attempt: attempt, Event: event})
	}
	return rows
}

type recorder struct {
	posts    atomic.Int64
	lastPath string
	lastAuth string
	lastType string
	lastBody []byte
	status   int
	mu       sync.Mutex
}

func (r *recorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.posts.Add(1)
		r.lastPath = req.URL.Path
		r.lastAuth = req.Header.Get("Authorization")
		r.lastType = req.Header.Get("Content-Type")
		r.lastBody = body
		status := r.status
		r.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (r *recorder) body(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out map[string]any
	require.NoError(t, json.Unmarshal(r.lastBody, &out))
	return out
}

// TestDrainOncePostsBatchAndAdvancesCursor: ten settled rows go out in one
// POST and the cursor lands on the last of them.
func TestDrainOncePostsBatchAndAdvancesCursor(t *testing.T) {
	rec := &recorder{}
	srv := rec.server(t)
	src := &fakeSource{rows: rowsFixture(10)}

	exp := genexport.New(genexport.Config{
		Host:      srv.URL,
		PublicKey: "pk-test",
		SecretKey: "sk-test",
		Source:    src,
		HTTP:      srv.Client(),
	})

	n, err := exp.DrainOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, int64(1), rec.posts.Load(), "ten rows must go out in exactly one POST")

	assert.Equal(t, "/api/public/ingestion", rec.lastPath)
	assert.Equal(t, "application/json", rec.lastType)
	assert.NotEmpty(t, rec.lastAuth, "the ingestion API is authenticated with the key pair")

	batch, ok := rec.body(t)["batch"].([]any)
	require.True(t, ok, "the posted body must carry a batch array")
	assert.Len(t, batch, 20, "each settled row contributes a trace event and a generation event")

	_, advanced := src.calls()
	require.Len(t, advanced, 1)
	assert.Equal(t, "event-09", advanced[0].EventID)
	assert.Equal(t, testEnd.Add(9*time.Second), advanced[0].CreatedAt)
}

// TestDrainOnceLeavesCursorUnmovedOnServerError is the durability property: a
// Langfuse outage advances no cursor and loses no data, so the backlog drains
// when it comes back.
func TestDrainOnceLeavesCursorUnmovedOnServerError(t *testing.T) {
	rec := &recorder{status: http.StatusInternalServerError}
	srv := rec.server(t)
	src := &fakeSource{rows: rowsFixture(3)}

	exp := genexport.New(genexport.Config{
		Host:      srv.URL,
		PublicKey: "pk-test",
		SecretKey: "sk-test",
		Source:    src,
		HTTP:      srv.Client(),
	})

	n, err := exp.DrainOnce(context.Background())
	require.Error(t, err, "a non-2xx must surface as an error, never as a silent success")
	assert.Zero(t, n)

	_, advanced := src.calls()
	assert.Empty(t, advanced, "the cursor must not move unless the POST returned 2xx")
}

// TestDrainOnceAdvancesOnlyOn2xx pins the rule for every non-2xx class, not
// just 500. A 4xx is a rejected batch, not an accepted one.
func TestDrainOnceAdvancesOnlyOn2xx(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			rec := &recorder{status: status}
			srv := rec.server(t)
			src := &fakeSource{rows: rowsFixture(2)}

			exp := genexport.New(genexport.Config{
				Host: srv.URL, PublicKey: "pk", SecretKey: "sk",
				Source: src, HTTP: srv.Client(),
			})

			_, err := exp.DrainOnce(context.Background())
			require.Error(t, err)
			_, advanced := src.calls()
			assert.Empty(t, advanced)
		})
	}
}

// TestDrainOnceIsIdempotentAfterSuccess: the second pass finds nothing above
// the cursor and posts nothing.
func TestDrainOnceIsIdempotentAfterSuccess(t *testing.T) {
	rec := &recorder{}
	srv := rec.server(t)
	src := &fakeSource{rows: rowsFixture(4)}

	exp := genexport.New(genexport.Config{
		Host: srv.URL, PublicKey: "pk", SecretKey: "sk",
		Source: src, HTTP: srv.Client(),
	})

	n, err := exp.DrainOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 4, n)

	n, err = exp.DrainOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, int64(1), rec.posts.Load(), "an empty read must not post an empty batch")
}

// TestDisabledExporterTouchesNothing is the ships-dark guarantee. With no
// LANGFUSE_HOST the exporter reads no row and opens no connection, so the
// feature can land on main with zero runtime footprint.
func TestDisabledExporterTouchesNothing(t *testing.T) {
	rec := &recorder{}
	srv := rec.server(t)
	src := &fakeSource{rows: rowsFixture(5)}

	exp := genexport.New(genexport.Config{
		Host:      "", // the whole switch
		PublicKey: "pk",
		SecretKey: "sk",
		Source:    src,
		HTTP:      srv.Client(),
	})

	assert.False(t, exp.Enabled())

	n, err := exp.DrainOnce(context.Background())
	require.NoError(t, err)
	assert.Zero(t, n)

	fetches, advanced := src.calls()
	assert.Zero(t, fetches, "a disabled exporter must not touch the database")
	assert.Empty(t, advanced)
	assert.Equal(t, int64(0), rec.posts.Load(), "a disabled exporter must not touch the network")
}

// TestDisabledExporterRunReturnsImmediately: a disabled exporter's Run is not
// a spinning goroutine either.
func TestDisabledExporterRunReturnsImmediately(t *testing.T) {
	exp := genexport.New(genexport.Config{Host: "", Source: &fakeSource{}})

	done := make(chan struct{})
	go func() {
		exp.Run(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run on a disabled exporter must return immediately, not block")
	}
}

// TestRunStopsOnContextCancel: the loop is owned by the process lifecycle.
func TestRunStopsOnContextCancel(t *testing.T) {
	rec := &recorder{}
	srv := rec.server(t)
	src := &fakeSource{rows: rowsFixture(1)}

	exp := genexport.New(genexport.Config{
		Host: srv.URL, PublicKey: "pk", SecretKey: "sk",
		Source: src, HTTP: srv.Client(), PollInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		exp.Run(ctx)
		close(done)
	}()

	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run must return when its context is cancelled")
	}

	fetches, _ := src.calls()
	assert.Positive(t, fetches, "the loop must actually have polled")
}

// TestConfigDefaults keeps the documented defaults honest.
func TestConfigDefaults(t *testing.T) {
	exp := genexport.New(genexport.Config{Host: "http://langfuse.invalid", Source: &fakeSource{}})
	assert.Equal(t, genexport.DefaultBatchSize, exp.BatchSize())
	assert.Equal(t, genexport.DefaultPollInterval, exp.PollInterval())
}

// TestMissingCredentialsDisableTheExporter: a host with no key pair cannot
// authenticate, so it is off rather than posting unauthenticated batches into
// the void on every tick.
func TestMissingCredentialsDisableTheExporter(t *testing.T) {
	for name, cfg := range map[string]genexport.Config{
		"no public key": {Host: "http://langfuse.invalid", SecretKey: "sk"},
		"no secret key": {Host: "http://langfuse.invalid", PublicKey: "pk"},
	} {
		t.Run(name, func(t *testing.T) {
			src := &fakeSource{rows: rowsFixture(1)}
			cfg.Source = src
			exp := genexport.New(cfg)
			assert.False(t, exp.Enabled())

			n, err := exp.DrainOnce(context.Background())
			require.NoError(t, err)
			assert.Zero(t, n)
			fetches, _ := src.calls()
			assert.Zero(t, fetches)
		})
	}
}
