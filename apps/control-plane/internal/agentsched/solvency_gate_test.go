package agentsched

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// providerTokens are the strings a refusal must never carry. The gate refuses
// on a credit balance, which is Hive's own state; any of these appearing in a
// customer-readable message or column would mean an upstream detail escaped
// through the refusal path (CLAUDE.md, "Provider-blind errors").
var providerTokens = []string{
	"openrouter", "groq", "litellm", "openai", "anthropic",
	"system_fingerprint", "gpt-", "claude-", "llama",
}

func assertProviderBlind(t *testing.T, what, s string) {
	t.Helper()
	lower := strings.ToLower(s)
	for _, tok := range providerTokens {
		if strings.Contains(lower, tok) {
			t.Fatalf("%s leaks provider detail %q: %q", what, tok, s)
		}
	}
}

func dueSchedule(now time.Time) Schedule {
	return Schedule{
		ID: idA, TenantID: tenantA, UserID: userA,
		Name: "n", Instructions: "do the thing", Schedule: "daily",
		Enabled:   true,
		NextRunAt: ptrTime(now.Add(-time.Minute)),
	}
}

func validCreateInput() CreateInput {
	return CreateInput{Name: "Morning digest", Instructions: "Summarize inbox", Schedule: "daily"}
}

// --- creation gate -------------------------------------------------------

func TestService_Create_ChecksSolvencyAtTheLaunchFloorBeforeWriting(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo()
	gate := solvent()
	svc := NewService(repo, gate, fixedClock(now))

	if _, err := svc.Create(context.Background(), tenantA, userA, validCreateInput()); err != nil {
		t.Fatalf("Create with a solvent tenant: %v", err)
	}
	if gate.callCount() != 1 {
		t.Fatalf("solvency checked %d times, want exactly 1", gate.callCount())
	}
	if gate.lastTenant != tenantA {
		t.Fatalf("solvency checked tenant %s, want the creating tenant %s", gate.lastTenant, tenantA)
	}
	if gate.lastFloor != launchFloor {
		t.Fatalf("solvency floor = %d, want launchFloor %d", gate.lastFloor, launchFloor)
	}
}

func TestService_Create_RefusesInsolventTenantAndWritesNoRow(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo()
	svc := NewService(repo, &fakeSolvency{insufficient: true}, fixedClock(now))

	_, err := svc.Create(context.Background(), tenantA, userA, validCreateInput())
	if !errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("Create error = %v, want ErrInsufficientCredits", err)
	}
	assertProviderBlind(t, "insolvent create error", err.Error())

	// The row must be ABSENT because the gate runs before repo.Create: a
	// refusal that still persists the schedule leaves a routine that the tick
	// would then have to refuse forever, which is the row the issue's
	// "no schedule row survives the refusal" criterion is about.
	if len(repo.created) != 0 {
		t.Fatalf("repo.Create called %d times on a refusal, want 0", len(repo.created))
	}
	if got, err := repo.Get(context.Background(), tenantA, userA, idA); err == nil {
		t.Fatalf("a schedule survived the refusal: %+v", got)
	}
}

func TestService_Create_RefusesOnLookupErrorRatherThanFailingOpen(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo()
	lookupErr := errors.New("agentsched: read balance: connection reset")
	svc := NewService(repo, &fakeSolvency{lookupErr: lookupErr}, fixedClock(now))

	_, err := svc.Create(context.Background(), tenantA, userA, validCreateInput())
	if err == nil {
		t.Fatal("Create succeeded on a solvency lookup failure; the gate failed open")
	}
	// Distinguishable from insolvency, which is the whole point: an operator
	// reading "insufficient credits" for what was a database blip would go
	// looking at the tenant's balance instead of at the database.
	if errors.Is(err, ErrInsufficientCredits) {
		t.Fatalf("lookup failure reported as ErrInsufficientCredits: %v", err)
	}
	if !errors.Is(err, lookupErr) {
		t.Fatalf("lookup failure lost its cause: %v", err)
	}
	if len(repo.created) != 0 {
		// Absent because an unknown answer must be treated as a refusal, not
		// as a pass; writing the row here would admit the tenant on a blip.
		t.Fatalf("repo.Create called %d times on a lookup failure, want 0", len(repo.created))
	}
}

func TestNewService_PanicsOnNilSolvency(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewService with a nil solvency must panic rather than admit every tenant")
		}
	}()
	NewService(newFakeRepo(), nil, nil)
}

// --- launch gate ---------------------------------------------------------

func TestScheduler_SkipsLaunchForInsolventTenantAndRecordsCreditError(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo(dueSchedule(now))
	tasks := &fakeTasks{}
	s := newSchedulerWithSolvency(repo, tasks, &fakeSolvency{insufficient: true})
	ctx := context.Background()

	if fired := s.RunOnce(ctx, now); fired != 0 {
		t.Fatalf("fired=%d for an insolvent tenant, want 0", fired)
	}
	tasks.mu.Lock()
	calls := tasks.calls
	tasks.mu.Unlock()
	if calls != 0 {
		t.Fatalf("CreateTask called %d times for an insolvent tenant, want 0", calls)
	}

	got, _ := repo.Get(ctx, tenantA, userA, idA)
	if got.LastError != insufficientCreditsMessage {
		t.Fatalf("last_error = %q, want the credit message %q", got.LastError, insufficientCreditsMessage)
	}
	assertProviderBlind(t, "last_error on an insolvent launch", got.LastError)

	if got.NextRunAt == nil || !got.NextRunAt.After(now) {
		t.Fatal("a refused launch must leave next_run_at one cadence out, not due again")
	}
	if fired := s.RunOnce(ctx, now); fired != 0 {
		t.Fatal("retry tick must not hot-loop the refused schedule")
	}
}

func TestScheduler_SkipsLaunchOnLookupErrorRatherThanFailingOpen(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo(dueSchedule(now))
	tasks := &fakeTasks{}
	s := newSchedulerWithSolvency(repo, tasks, &fakeSolvency{lookupErr: errors.New("dial tcp: connection refused")})
	ctx := context.Background()

	if fired := s.RunOnce(ctx, now); fired != 0 {
		t.Fatalf("fired=%d when solvency was unknown, want 0", fired)
	}
	tasks.mu.Lock()
	calls := tasks.calls
	tasks.mu.Unlock()
	if calls != 0 {
		t.Fatalf("CreateTask called %d times when solvency was unknown, want 0; the gate failed open", calls)
	}

	got, _ := repo.Get(ctx, tenantA, userA, idA)
	if got.LastError != runFailureMessage {
		t.Fatalf("last_error = %q, want the retry message %q", got.LastError, runFailureMessage)
	}
	if got.LastError == insufficientCreditsMessage {
		t.Fatal("a lookup failure must not be reported to the tenant as insufficient credits")
	}
	// The raw cause must be ABSENT from the persisted column: last_error is
	// customer-readable and a driver string can carry an internal hostname.
	assertProviderBlind(t, "last_error on a lookup failure", got.LastError)
	if strings.Contains(got.LastError, "connection refused") {
		t.Fatalf("last_error echoed the raw lookup failure: %q", got.LastError)
	}
}

func TestScheduler_SolventTenantStillLaunches(t *testing.T) {
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo(dueSchedule(now))
	tasks := &fakeTasks{}
	gate := solvent()
	s := newSchedulerWithSolvency(repo, tasks, gate)

	if fired := s.RunOnce(context.Background(), now); fired != 1 {
		t.Fatalf("fired=%d for a solvent tenant, want 1", fired)
	}
	if gate.callCount() != 1 {
		t.Fatalf("solvency checked %d times, want 1 per launch", gate.callCount())
	}
	if gate.lastTenant != tenantA {
		t.Fatalf("solvency checked tenant %s, want the schedule's tenant %s", gate.lastTenant, tenantA)
	}
	if gate.lastFloor != launchFloor {
		t.Fatalf("solvency floor = %d, want launchFloor %d", gate.lastFloor, launchFloor)
	}
}

func TestNewScheduler_PanicsOnNilSolvency(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewScheduler with a nil solvency must panic rather than launch for every tenant")
		}
	}()
	NewScheduler(newFakeRepo(), &fakeTasks{}, nil, SchedulerConfig{Logger: quietLogger()})
}

// TestSolventAtCreationInsolventAtLaunchIsStillRefused is the case that makes
// two gates necessary rather than one, and it is the only test here that spans
// both. It runs the real Service and the real Scheduler over one repository
// and one gate, so the schedule the tick refuses is the same row the create
// legitimately wrote.
//
// The two failure modes stack, and either fix alone leaves the other live.
// Gating only creation admits this launch, because the tenant was solvent when
// the routine was created and nothing looks again; the routine then launches a
// sandbox on every cadence, forever, which is issue #1490's actual complaint.
// Gating only the launch refuses it, but the tenant is told nothing at the
// moment they act and discovers it as a last_error a day later. So this
// asserts the create SUCCEEDED and the later launch was still refused, in one
// sequence, rather than proving each half against its own fixture where the
// gap between them cannot appear.
func TestSolventAtCreationInsolventAtLaunchIsStillRefused(t *testing.T) {
	created := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo()
	gate := solvent()
	svc := NewService(repo, gate, fixedClock(created))
	tasks := &fakeTasks{}
	sched := newSchedulerWithSolvency(repo, tasks, gate)
	ctx := context.Background()

	row, err := svc.Create(ctx, tenantA, userA, validCreateInput())
	if err != nil {
		t.Fatalf("create while solvent: %v", err)
	}
	if len(repo.created) != 1 {
		t.Fatalf("repo.Create called %d times, want 1: the launch refusal below only means something if this row was really written", len(repo.created))
	}

	// The balance runs out somewhere between the routine being created and its
	// first run. Nothing in the system is watching for that; the launch gate is
	// what notices.
	gate.goInsolvent()

	due := created.Add(24 * time.Hour)
	if fired := sched.RunOnce(ctx, due); fired != 0 {
		t.Fatalf("fired=%d for a tenant that went insolvent after creating the routine, want 0", fired)
	}
	tasks.mu.Lock()
	calls := tasks.calls
	tasks.mu.Unlock()
	if calls != 0 {
		t.Fatalf("CreateTask called %d times, want 0: gating creation alone lets every later launch through", calls)
	}

	got, err := repo.Get(ctx, tenantA, userA, row.ID)
	if err != nil {
		t.Fatalf("get schedule after refused launch: %v", err)
	}
	if got.LastError != insufficientCreditsMessage {
		t.Fatalf("last_error = %q, want the credit message", got.LastError)
	}
	if gate.callCount() != 2 {
		t.Fatalf("solvency checked %d times across create plus one launch, want 2", gate.callCount())
	}
}

// --- wire mapping --------------------------------------------------------

func postCreate(t *testing.T, sol Solvency) *httptest.ResponseRecorder {
	t.Helper()
	svc := NewService(newFakeRepo(), sol, fixedClock(time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)))
	h := NewHandler(svc)
	body := `{"name":"Morning digest","instructions":"Summarize inbox","schedule":"daily"}`
	req := httptest.NewRequest(http.MethodPost,
		internalPrefix+tenantA.String()+"/"+userA.String(), strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.InternalMux().ServeHTTP(rec, req)
	return rec
}

func TestHandler_Create_DistinguishesInsolvencyFromLookupFailure(t *testing.T) {
	solventRec := postCreate(t, solvent())
	if solventRec.Code != http.StatusCreated {
		t.Fatalf("solvent create status = %d, want 201", solventRec.Code)
	}

	poorRec := postCreate(t, &fakeSolvency{insufficient: true})
	if poorRec.Code != http.StatusPaymentRequired {
		t.Fatalf("insolvent create status = %d, want 402", poorRec.Code)
	}

	blindRec := postCreate(t, &fakeSolvency{lookupErr: errors.New("dial tcp 10.0.0.7:5432: connection refused")})
	if blindRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup-failure create status = %d, want 503", blindRec.Code)
	}

	// Three distinct codes is the criterion: a lookup failure answering 402
	// tells the tenant to top up an account that may be fully funded, and one
	// answering 201 is the fail-open defect itself.
	if poorRec.Code == blindRec.Code || poorRec.Code == solventRec.Code || blindRec.Code == solventRec.Code {
		t.Fatalf("statuses must differ: solvent=%d insolvent=%d lookup=%d",
			solventRec.Code, poorRec.Code, blindRec.Code)
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"insolvent": poorRec, "lookup failure": blindRec,
	} {
		var out map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s body is not the standard error envelope: %v", name, rec.Body.String())
		}
		if out["error"] == "" {
			t.Fatalf("%s body carries no error message: %s", name, rec.Body.String())
		}
		assertProviderBlind(t, name+" refusal body", rec.Body.String())
	}

	// The driver's raw text must be ABSENT from the 503 body: it carries a
	// private address and port, which no customer-facing payload may include
	// (CLAUDE.md hygiene rule, no IPs in customer-visible output).
	if strings.Contains(blindRec.Body.String(), "10.0.0.7") {
		t.Fatalf("503 body echoed the raw lookup failure: %s", blindRec.Body.String())
	}
}
