package userinstructions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// fakeStore records what the handler asked for, so the tests can assert the
// scope the handler used and not only the status code it returned.
type fakeStore struct {
	content string
	getErr  error
	putErr  error

	gotTenant  uuid.UUID
	gotUser    uuid.UUID
	putCalls   int
	putContent string
}

func (f *fakeStore) Instructions(_ context.Context, tenantID, userID uuid.UUID) (string, error) {
	f.gotTenant, f.gotUser = tenantID, userID
	return f.content, f.getErr
}

func (f *fakeStore) Put(_ context.Context, tenantID, userID uuid.UUID, content string) error {
	f.gotTenant, f.gotUser = tenantID, userID
	f.putCalls++
	f.putContent = content
	return f.putErr
}

func signedIn(r *http.Request, user *auth.User) *http.Request {
	return r.WithContext(auth.WithUser(context.Background(), user))
}

func person() *auth.User {
	return &auth.User{ID: uuid.New(), TenantID: uuid.New(), Role: "member", Email: "a@example.com"}
}

func TestGetReturnsStoredContentForTheSignedInPrincipal(t *testing.T) {
	store := &fakeStore{content: "Answer in British English."}
	user := person()
	rec := httptest.NewRecorder()

	NewHandler(store).ServeHTTP(rec, signedIn(httptest.NewRequest(http.MethodGet, "/v1/user/instructions", nil), user))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got instructionsWire
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Content != "Answer in British English." {
		t.Fatalf("content = %q, want the stored text", got.Content)
	}
	// The scope must come from the principal, never from the request.
	if store.gotTenant != user.TenantID || store.gotUser != user.ID {
		t.Fatalf("store scoped to (%s, %s), want the principal's (%s, %s)",
			store.gotTenant, store.gotUser, user.TenantID, user.ID)
	}
}

func TestGetAnswersEmptyWhenNothingIsStored(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeStore{}).ServeHTTP(rec, signedIn(httptest.NewRequest(http.MethodGet, "/v1/user/instructions", nil), person()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != `{"content":""}` {
		t.Fatalf("body = %s, want an empty content field", body)
	}
}

func TestPutStoresSanitizedContentAndEchoesIt(t *testing.T) {
	store := &fakeStore{}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"content":"Be concise.\r\nUse metric units."}`)

	NewHandler(store).ServeHTTP(rec, signedIn(httptest.NewRequest(http.MethodPut, "/v1/user/instructions", body), person()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	want := "Be concise.\nUse metric units."
	if store.putContent != want {
		t.Fatalf("stored %q, want %q", store.putContent, want)
	}
	var got instructionsWire
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Content != want {
		t.Fatalf("echoed %q, want the sanitized text %q", got.Content, want)
	}
}

func TestPutWithEmptyContentClearsRatherThanFailing(t *testing.T) {
	store := &fakeStore{}
	rec := httptest.NewRecorder()

	NewHandler(store).ServeHTTP(rec, signedIn(
		httptest.NewRequest(http.MethodPut, "/v1/user/instructions", strings.NewReader(`{"content":"   \n  "}`)),
		person()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (clearing is a legal request)", rec.Code)
	}
	if store.putCalls != 1 || store.putContent != "" {
		t.Fatalf("put called %d times with %q, want one call with empty content", store.putCalls, store.putContent)
	}
}

func TestPutRefusesOverLongContentWithoutStoringAnything(t *testing.T) {
	store := &fakeStore{}
	rec := httptest.NewRecorder()
	payload, err := json.Marshal(instructionsWire{Content: strings.Repeat("a", MaxContentLen+1)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	NewHandler(store).ServeHTTP(rec, signedIn(
		httptest.NewRequest(http.MethodPut, "/v1/user/instructions", strings.NewReader(string(payload))),
		person()))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if store.putCalls != 0 {
		t.Fatalf("put called %d times, want 0: an over-long body must not be truncated into storage", store.putCalls)
	}
}

func TestRefusesPrincipalsWithoutAPerson(t *testing.T) {
	tests := []struct {
		name string
		user *auth.User
		want int
	}{
		{"no principal at all", nil, http.StatusUnauthorized},
		{"no tenant", &auth.User{ID: uuid.New()}, http.StatusForbidden},
		{"api key, tenant but no person", &auth.User{TenantID: uuid.New()}, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{content: "someone else's instructions"}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/user/instructions", nil)
			if tt.user != nil {
				req = signedIn(req, tt.user)
			}
			NewHandler(store).ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if strings.Contains(rec.Body.String(), "someone else") {
				t.Fatalf("a refused request returned stored content: %s", rec.Body.String())
			}
		})
	}
}

func TestUnsupportedMethodsAreRefused(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		rec := httptest.NewRecorder()
		NewHandler(&fakeStore{}).ServeHTTP(rec, signedIn(httptest.NewRequest(method, "/v1/user/instructions", nil), person()))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, rec.Code)
		}
	}
}

func TestStoreFailuresDoNotLeakTheUnderlyingError(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(&fakeStore{getErr: errors.New("pgx: connection refused to 10.0.0.4:5432")}).
		ServeHTTP(rec, signedIn(httptest.NewRequest(http.MethodGet, "/v1/user/instructions", nil), person()))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "pgx") || strings.Contains(rec.Body.String(), "10.0.0.4") {
		t.Fatalf("response leaked infrastructure detail: %s", rec.Body.String())
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{
			name: "newlines and tabs survive, because instructions are prose",
			in:   "Always:\n\t- cite sources\n\t- use metric units",
			want: "Always:\n\t- cite sources\n\t- use metric units",
		},
		{
			name: "interior blank lines survive as paragraph breaks",
			in:   "Be brief.\n\nNever apologise.",
			want: "Be brief.\n\nNever apologise.",
		},
		{
			name: "CRLF folds to LF rather than leaving a stray carriage return",
			in:   "one\r\ntwo",
			want: "one\ntwo",
		},
		{
			name: "other control characters are dropped",
			in:   "be\x07 concise",
			want: "be concise",
		},
		{
			name: "unicode line separators normalise to newlines",
			in:   "one\u2028two\u2029three",
			want: "one\ntwo\nthree",
		},
		{
			name: "leading and trailing whitespace goes, including blank lines",
			in:   "\n\n  Be brief.  \n\n",
			want: "Be brief.",
		},
		{
			name: "empty input is legal and means cleared",
			in:   "   \n\t ",
			want: "",
		},
		{
			name:    "over the cap is refused, not truncated",
			in:      strings.Repeat("x", MaxContentLen+1),
			wantErr: ErrTooLong,
		},
		{
			name: "exactly at the cap is accepted",
			in:   strings.Repeat("x", MaxContentLen),
			want: strings.Repeat("x", MaxContentLen),
		},
		{
			name: "the cap counts runes, not bytes",
			// Four bytes each, so this is well over MaxContentLen bytes and
			// exactly MaxContentLen characters. char_length in the table's
			// CHECK counts the same way.
			in:   strings.Repeat("😀", MaxContentLen),
			want: strings.Repeat("😀", MaxContentLen),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Sanitize(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && got != tt.want {
				t.Fatalf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
