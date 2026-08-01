package audio_test

// #616, reached through the release sites rather than the finalize site.
//
// settleReservation was already fixed to run finalize and its fallback release
// on their own fresh, bounded background contexts (#637), because by the time
// settlement runs the client may have disconnected and cancelled the request
// context, which makes the accounting call abort before it is ever sent. Every
// release-on-error site in the handler had the opposite shape: it passed the
// request context and discarded the returned error, so a cancelled context
// produced a silently stranded hold. #616 stranded 32 holds that way and
// refused every subsequent request for three days.
//
// These tests hold the release sites to the same contract as settlement: a
// live bounded context of their own, and a logged failure rather than a
// dropped one.

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureLogs redirects the standard logger for the duration of one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buf
}

// The unpriceable-response refusal is the release site most exposed to a
// disconnected client: it runs after the upstream request has completed.
func TestUnpriceableResponseReleasesOnItsOwnBoundedContext(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"no duration reported"}`), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code == http.StatusOK {
		t.Fatalf("expected a refusal, got 200: %s", w.Body.String())
	}
	if !acc.releaseCalled {
		t.Fatal("expected the hold to be released")
	}
	if acc.releaseCtxErr != nil {
		t.Fatalf("release ran on a dead context: %v", acc.releaseCtxErr)
	}
	if !acc.releaseCtxHasDeadline {
		t.Fatal("release ran on the unbounded request context: it must take its own bounded context, so a client disconnect cannot strand the hold (#616)")
	}
}

// A release that genuinely fails must be visible. Discarding the error is what
// turned #616 into three silent days.
func TestFailedReleaseIsLogged(t *testing.T) {
	logs := captureLogs(t)

	mock := newMockLiteLLMAudio([]byte(`{"text":"no duration reported"}`), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")
	acc.releaseErr = context.DeadlineExceeded

	w := postAudioMultipart(t, h, "/v1/audio/transcriptions", "hive-stt", "")

	if w.Code == http.StatusOK {
		t.Fatalf("expected a refusal, got 200: %s", w.Body.String())
	}
	if !strings.Contains(logs.String(), "release reservation failed") {
		t.Fatalf("a failed release was dropped instead of logged; logs were: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "res-priced") {
		t.Fatalf("the log does not identify the stranded reservation; logs were: %s", logs.String())
	}
}

// A client that has already disconnected must not be able to prevent its own
// hold from being released. Every release site in the handler is reachable in
// this state, so this covers the sibling sites as well as the one above: with
// the request context cancelled the upstream dispatch fails immediately, which
// is the release-on-upstream-error path.
func TestReleaseSurvivesCancelledRequestContext(t *testing.T) {
	mock := newMockLiteLLMAudio([]byte(`{"text":"never reached","duration":30.0}`), 200, "application/json")
	defer mock.Close()

	h, acc, _ := buildPricedAudioHandler(mock.server.URL, sttCreditsPerMillionSeconds, "seconds")

	body, contentType := multipartAudioBody(t, "hive-stt", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client is gone before dispatch, exactly as a disconnect leaves it

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body).WithContext(ctx)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a failure once the request context is cancelled, got 200: %s", w.Body.String())
	}
	if !acc.releaseCalled {
		t.Fatal("expected the hold to be released after the cancelled dispatch")
	}
	if acc.releaseCtxErr != nil {
		t.Fatalf("release inherited the cancelled request context (%v): the hold would strand (#616)", acc.releaseCtxErr)
	}
}
