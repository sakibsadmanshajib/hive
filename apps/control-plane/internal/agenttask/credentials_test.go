package agenttask_test

// Regression guard for issue #1507: an agent task charged its tenant nothing.
//
// The three live observations in that issue were a 100,000,000 credit hold,
// the same amount released 14 to 20 milliseconds later and about 18 seconds
// before the task even started, and no usage_charge row at all. The first two
// are edge-api's submit-time solvency probe, which takes a hold and hands it
// straight back in the same call, and they are correct. The third is this
// package's problem: the sandbox authenticated its model calls with the
// launcher's one process-wide Hive-owned key, so the metering at the far end
// settled correctly against the wrong account.
//
// What these tests pin is the attribution seam that closes it. Every
// assertion below checks the credential's VALUE together with its PROVENANCE
// in the same comparison, because a fix that hands the sandbox some non-empty
// key that resolves to the wrong account would pass an "is it set" test while
// leaving the customer uncharged, which is the defect verbatim.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/apikeys"
)

// mintedCredential records one Mint call with everything that decides whether
// the credential is attributable: which task asked for it, whose tenant it was
// minted for, which billing account it settles against, and the secret handed
// back. Tests compare the secret AND these fields together.
type mintedCredential struct {
	taskID    uuid.UUID
	tenantID  uuid.UUID
	accountID uuid.UUID
	secret    string
}

// fakeCredentials is an agenttask.TaskCredentials stub that models the one
// property under test: a credential belongs to exactly one tenant's billing
// account. Each tenant gets a stable synthetic account, and the secret it
// returns embeds that account, so a test can tell a credential minted for the
// right tenant apart from any other non-empty string.
type fakeCredentials struct {
	mu        sync.Mutex
	accounts  map[uuid.UUID]uuid.UUID
	minted    []mintedCredential
	revoked   []uuid.UUID
	mintErr   error
	revokeErr error
}

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{accounts: make(map[uuid.UUID]uuid.UUID)}
}

func (f *fakeCredentials) accountFor(tenantID uuid.UUID) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accountLocked(tenantID)
}

func (f *fakeCredentials) accountLocked(tenantID uuid.UUID) uuid.UUID {
	acct, ok := f.accounts[tenantID]
	if !ok {
		acct = uuid.New()
		f.accounts[tenantID] = acct
	}
	return acct
}

func (f *fakeCredentials) Mint(_ context.Context, t agenttask.Task) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mintErr != nil {
		return "", f.mintErr
	}
	acct := f.accountLocked(t.TenantID)
	rec := mintedCredential{
		taskID:    t.ID,
		tenantID:  t.TenantID,
		accountID: acct,
		// Deliberately NOT the shape a real secret has, and deliberately not
		// prefixed "hk_": this value encodes the account and the task so one
		// assertion can check a credential's value and its provenance at once,
		// and that is exactly what a real secret must never do. The production
		// minter uses 32 crypto/rand bytes unrelated to any identifier, pinned
		// by apikeys.TestCreateKeyWithIDSecretIsRandomAndUnrelatedToTheIDs.
		secret: "fake-fixture-not-a-secret/account=" + acct.String() + "/task=" + t.ID.String(),
	}
	f.minted = append(f.minted, rec)
	return rec.secret, nil
}

func (f *fakeCredentials) Revoke(_ context.Context, t agenttask.Task) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, t.ID)
	return f.revokeErr
}

func (f *fakeCredentials) mintedFor(taskID uuid.UUID) (mintedCredential, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.minted {
		if m.taskID == taskID {
			return m, true
		}
	}
	return mintedCredential{}, false
}

func (f *fakeCredentials) mintCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.minted)
}

func (f *fakeCredentials) revokedTasks() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.revoked...)
}

// TestCreateTask_LaunchesWithACredentialMintedOnItsOwnTenantsAccount is the
// central guard. Both packs, because issue #1507 observed the zero charge on
// knowledge-work-pack twice and coding-pack once, so a fix proven on one pack
// proves nothing about the other.
func TestCreateTask_LaunchesWithACredentialMintedOnItsOwnTenantsAccount(t *testing.T) {
	for _, pack := range []agenttask.Pack{agenttask.PackKnowledgeWork, agenttask.PackCoding} {
		t.Run(string(pack), func(t *testing.T) {
			engine := &fakeEngine{sessionRef: "session-" + string(pack)}
			creds := newFakeCredentials()
			svc := agenttask.NewService(newFakeRepository(), engine, agenttask.WithTaskCredentials(creds))
			tenantID, userID := uuid.New(), uuid.New()

			created, err := svc.CreateTask(context.Background(), tenantID, userID, pack, "do the thing", uuid.Nil, "")
			if err != nil {
				t.Fatalf("CreateTask: %v", err)
			}
			svc.WaitIdle()

			minted, ok := creds.mintedFor(created.ID)
			if !ok {
				t.Fatalf("no credential was minted for task %s: the sandbox would spend the launcher's Hive-owned key and the tenant would be charged nothing (#1507)", created.ID)
			}
			// Value and provenance in one comparison: the secret the engine
			// received must be the one minted for THIS task, on THIS tenant,
			// against the account that tenant bills to. Any of the three being
			// wrong is the same defect wearing a different hat.
			launched := engine.lastLaunchedTask()
			if launched.LLMAPIKey != minted.secret ||
				minted.tenantID != tenantID ||
				minted.accountID != creds.accountFor(tenantID) {
				t.Fatalf("engine got LLMAPIKey %q minted for tenant %s on account %s; want %q minted for tenant %s on account %s",
					launched.LLMAPIKey, minted.tenantID, minted.accountID,
					minted.secret, tenantID, creds.accountFor(tenantID))
			}
		})
	}
}

// TestCreateTask_NeverPersistsTheTaskCredential mirrors the guard the bearer
// JWT already has. The secret is a live gateway credential on a customer's
// billing account, so a Get/List response that carried it would hand every
// reader of the task row the ability to spend that customer's credits.
func TestCreateTask_NeverPersistsTheTaskCredential(t *testing.T) {
	repo := newFakeRepository()
	creds := newFakeCredentials()
	svc := agenttask.NewService(repo, &fakeEngine{sessionRef: "session-1"}, agenttask.WithTaskCredentials(creds))
	tenantID, userID := uuid.New(), uuid.New()

	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding, "", uuid.Nil, "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc.WaitIdle()

	// Asserted ABSENT deliberately, and the reason is the point of the test:
	// LLMAPIKey is not a column on public.agent_tasks, so a value here would
	// mean the secret had been written somewhere a read path can return it.
	reloaded, err := repo.Get(context.Background(), tenantID, userID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.LLMAPIKey != "" {
		t.Fatalf("task loaded from the repository carries LLMAPIKey %q; the credential must never be persisted", reloaded.LLMAPIKey)
	}
}

// TestCreateTask_FailsClosedWhenTheCredentialCannotBeMinted is the D-034 half:
// a sandbox launched without an attributable credential falls back to the
// launcher's Hive-owned key and serves the customer free, which is exactly
// what #1507 measured. Refusing the launch is the only honest answer.
func TestCreateTask_FailsClosedWhenTheCredentialCannotBeMinted(t *testing.T) {
	engine := &fakeEngine{sessionRef: "session-1"}
	creds := newFakeCredentials()
	creds.mintErr = errors.New("billing account lookup failed")
	svc := agenttask.NewService(newFakeRepository(), engine, agenttask.WithTaskCredentials(creds))

	task := createSettled(t, svc, uuid.New(), uuid.New(), agenttask.PackKnowledgeWork)

	if task.Status != agenttask.StatusFailed {
		t.Errorf("status = %v, want failed: a task whose spend cannot be attributed must not launch", task.Status)
	}
	if engine.lastLaunchedTask().ID != uuid.Nil {
		t.Error("the engine was asked to launch a sandbox for a task with no attributable credential")
	}
	if task.ErrorMessage == "" {
		t.Error("expected a non-empty customer-visible error_message")
	}
	if strings.Contains(strings.ToLower(task.ErrorMessage), "credential") ||
		strings.Contains(strings.ToLower(task.ErrorMessage), "billing") {
		t.Errorf("error_message must stay provider-blind and free of internal money-path detail, got %q", task.ErrorMessage)
	}
}

// TestCreateTask_FailsClosedWithNoCredentialSeamWired guards the wiring
// itself, the same way sessionbilling refuses a handler built without its
// accounting seam. A Service assembled without WithTaskCredentials used to be
// the shipped configuration, and it is the configuration that charges nothing.
func TestCreateTask_FailsClosedWithNoCredentialSeamWired(t *testing.T) {
	engine := &fakeEngine{sessionRef: "session-1"}
	svc := agenttask.NewService(newFakeRepository(), engine)

	task := createSettled(t, svc, uuid.New(), uuid.New(), agenttask.PackCoding)

	if task.Status != agenttask.StatusFailed {
		t.Errorf("status = %v, want failed when no credential seam is wired", task.Status)
	}
	if engine.lastLaunchedTask().ID != uuid.Nil {
		t.Error("a Service with no credential seam launched a sandbox anyway")
	}
}

// TestCreateTask_NilCredentialsCannotDisarmTheSeam pins that passing nil to
// the option leaves the fail-closed default in place rather than installing a
// nil that would panic or, worse, be treated as "no credential needed".
func TestCreateTask_NilCredentialsCannotDisarmTheSeam(t *testing.T) {
	engine := &fakeEngine{sessionRef: "session-1"}
	svc := agenttask.NewService(newFakeRepository(), engine, agenttask.WithTaskCredentials(nil))

	task := createSettled(t, svc, uuid.New(), uuid.New(), agenttask.PackCoding)

	if task.Status != agenttask.StatusFailed {
		t.Errorf("status = %v, want failed: a nil credential seam is an unwired one", task.Status)
	}
}

// TestCancel_RevokesTheTaskCredential: a cancelled task's sandbox is stopped,
// so its credential has no further work to authorize and must not outlive it.
func TestCancel_RevokesTheTaskCredential(t *testing.T) {
	creds := newFakeCredentials()
	svc := agenttask.NewService(newFakeRepository(), &fakeEngine{sessionRef: "session-1"},
		agenttask.WithTaskCredentials(creds))
	tenantID, userID := uuid.New(), uuid.New()

	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding, "", uuid.Nil, "")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	svc.WaitIdle()

	if _, err := svc.Cancel(context.Background(), tenantID, userID, created.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	svc.WaitIdle()

	if got := creds.revokedTasks(); len(got) != 1 || got[0] != created.ID {
		t.Fatalf("revoked %v, want exactly [%s]", got, created.ID)
	}
}

// TestPoller_RevokesTheCredentialOfATaskThatReachedTerminal covers the path
// almost every task actually takes: it finishes on its own and the poller
// records it.
func TestPoller_RevokesTheCredentialOfATaskThatReachedTerminal(t *testing.T) {
	for _, status := range []agenttask.Status{agenttask.StatusSucceeded, agenttask.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			repo := newFakeRepository()
			task := newActiveTask(repo, agenttask.StatusRunning, "session-1")
			checker := &fakeStatusChecker{responses: map[string]checkerResponse{
				"session-1": {status: status},
			}}
			creds := newFakeCredentials()
			p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{
				Logger:      quietPollerLogger(),
				Credentials: creds,
			})

			if _, err := p.RunOnce(context.Background()); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if got := creds.revokedTasks(); len(got) != 1 || got[0] != task.ID {
				t.Fatalf("revoked %v, want exactly [%s] once the task reached %s", got, task.ID, status)
			}
		})
	}
}

// TestPoller_RevocationFailureDoesNotBlockTheTerminalTransition: the row's
// terminal state is the customer-visible fact and the credential carries an
// expiry, so a revocation that fails is an operator concern, never a reason to
// leave a finished task looking unfinished.
func TestPoller_RevocationFailureDoesNotBlockTheTerminalTransition(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-1")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-1": {status: agenttask.StatusSucceeded},
	}}
	creds := newFakeCredentials()
	creds.revokeErr = errors.New("api key service unavailable")
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{
		Logger:      quietPollerLogger(),
		Credentials: creds,
	})

	advanced, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if advanced != 1 {
		t.Fatalf("advanced = %d, want 1", advanced)
	}
	reloaded, err := repo.Get(context.Background(), task.TenantID, task.UserID, task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.Status != agenttask.StatusSucceeded {
		t.Fatalf("status = %v, want succeeded", reloaded.Status)
	}
}

// TestPoller_WithNoCredentialSeamStillAdvancesTasks: an unwired poller leaves
// revocation to the credential's expiry rather than refusing to record what
// the engine reported.
func TestPoller_WithNoCredentialSeamStillAdvancesTasks(t *testing.T) {
	repo := newFakeRepository()
	newActiveTask(repo, agenttask.StatusRunning, "session-1")
	checker := &fakeStatusChecker{responses: map[string]checkerResponse{
		"session-1": {status: agenttask.StatusSucceeded},
	}}
	p := agenttask.NewPoller(repo, checker, agenttask.PollerConfig{Logger: quietPollerLogger()})

	advanced, err := p.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if advanced != 1 {
		t.Fatalf("advanced = %d, want 1", advanced)
	}
}

// --- PgxTaskCredentials, the production minter ---

// fakeAccountDB answers the one tenant-to-billing-account query
// PgxTaskCredentials makes.
type fakeAccountDB struct {
	accountByTenant map[uuid.UUID]uuid.UUID
	gotTenant       uuid.UUID
}

type fakeRow struct {
	accountID uuid.UUID
	err       error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	out, ok := dest[0].(*uuid.UUID)
	if !ok {
		return errors.New("fakeRow: unexpected scan destination")
	}
	*out = r.accountID
	return nil
}

func (d *fakeAccountDB) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	tenantID, _ := args[0].(uuid.UUID)
	d.gotTenant = tenantID
	acct, ok := d.accountByTenant[tenantID]
	if !ok {
		return fakeRow{err: pgx.ErrNoRows}
	}
	return fakeRow{accountID: acct}
}

// fakeKeyIssuer records what PgxTaskCredentials asks apikeys.Service for.
type fakeKeyIssuer struct {
	createdOnAccount uuid.UUID
	createdKeyID     uuid.UUID
	createdActor     uuid.UUID
	createdExpiresAt *time.Time
	createErr        error

	revokedKeyID uuid.UUID
	revokeErr    error
	revokeCalled bool
	// revokedLastUsedAt is what the revoked key reports for last_used_at. nil
	// is the zero-charge case: accounting stamps that column only when a
	// reservation on the key reaches a terminal charge.
	revokedLastUsedAt *time.Time
}

func (f *fakeKeyIssuer) CreateAgentTaskKey(_ context.Context, accountID, actorUserID, taskID uuid.UUID, expiresAt *time.Time) (apikeys.CreateKeyResult, error) {
	f.createdOnAccount, f.createdActor, f.createdKeyID, f.createdExpiresAt = accountID, actorUserID, taskID, expiresAt
	if f.createErr != nil {
		return apikeys.CreateKeyResult{}, f.createErr
	}
	return apikeys.CreateKeyResult{
		Key:    apikeys.APIKey{ID: taskID, AccountID: accountID, Kind: apikeys.KindAgentTask},
		Secret: "fake-issued-secret",
	}, nil
}

func (f *fakeKeyIssuer) RevokeAgentTaskKey(_ context.Context, keyID uuid.UUID) (apikeys.APIKey, error) {
	f.revokedKeyID, f.revokeCalled = keyID, true
	if f.revokeErr != nil {
		return apikeys.APIKey{}, f.revokeErr
	}
	return apikeys.APIKey{ID: keyID, AccountID: f.createdOnAccount, LastUsedAt: f.revokedLastUsedAt}, nil
}

func TestPgxTaskCredentials_MintsOnTheTenantsBillingAccountUnderTheTaskID(t *testing.T) {
	tenantID, userID, taskID, accountID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	db := &fakeAccountDB{accountByTenant: map[uuid.UUID]uuid.UUID{tenantID: accountID}}
	keys := &fakeKeyIssuer{}
	creds := agenttask.NewPgxTaskCredentials(db, keys)

	secret, err := creds.Mint(context.Background(), agenttask.Task{ID: taskID, TenantID: tenantID, UserID: userID})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// One assertion, value and provenance together: the secret handed back
	// must be the one issued on the account THIS tenant bills to, under this
	// task's own id, by this task's own user.
	if secret != "fake-issued-secret" || keys.createdOnAccount != accountID ||
		keys.createdKeyID != taskID || keys.createdActor != userID || db.gotTenant != tenantID {
		t.Fatalf("minted secret %q on account %s under key id %s for actor %s (looked up tenant %s); want %q on %s / %s / %s / %s",
			secret, keys.createdOnAccount, keys.createdKeyID, keys.createdActor, db.gotTenant,
			"fake-issued-secret", accountID, taskID, userID, tenantID)
	}
	if keys.createdExpiresAt == nil {
		t.Fatal("minted credential has no expiry: nothing else bounds it when control-plane dies before revoking")
	}
	if until := time.Until(*keys.createdExpiresAt); until <= 30*time.Minute {
		t.Fatalf("expiry is %s away, which is inside the range a long task can run; a credential that expires mid-run breaks the run", until)
	}
}

func TestPgxTaskCredentials_MintFailsClosedWithNoBillingAccount(t *testing.T) {
	tenantID := uuid.New()
	db := &fakeAccountDB{accountByTenant: map[uuid.UUID]uuid.UUID{}}
	keys := &fakeKeyIssuer{}
	creds := agenttask.NewPgxTaskCredentials(db, keys)

	_, err := creds.Mint(context.Background(), agenttask.Task{ID: uuid.New(), TenantID: tenantID, UserID: uuid.New()})
	if !errors.Is(err, agenttask.ErrNoBillingAccount) {
		t.Fatalf("Mint error = %v, want ErrNoBillingAccount", err)
	}
	if keys.createdKeyID != uuid.Nil {
		t.Error("a key was issued for a tenant with no billing account")
	}
}

func TestPgxTaskCredentials_RevokeDistinguishesAlreadyGoneFromCouldNotFind(t *testing.T) {
	tenantID, taskID, accountID := uuid.New(), uuid.New(), uuid.New()
	db := &fakeAccountDB{accountByTenant: map[uuid.UUID]uuid.UUID{tenantID: accountID}}
	task := agenttask.Task{ID: taskID, TenantID: tenantID, UserID: uuid.New()}

	// The distinction this pins is the whole point. "Already revoked" is an
	// observed row in a known terminal state, so it is genuinely success. "Not
	// found" is not: it means a credential this process minted on a real
	// billing account could not be located to destroy, so it is still live and
	// still spendable. Collapsing the second into the first is how a bad
	// outcome comes back wearing a good signal, which is what #1507 is.
	for _, tc := range []struct {
		name      string
		issuerErr error
		wantErr   error
	}{
		{"already revoked is success", apikeys.ErrRevoked, nil},
		{"not found is not success", apikeys.ErrNotFound, agenttask.ErrCredentialUnaccountedFor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := &fakeKeyIssuer{revokeErr: tc.issuerErr}
			creds := agenttask.NewPgxTaskCredentials(db, keys)
			err := creds.Revoke(context.Background(), task)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("Revoke: %v, want nil (a terminal transition can fire twice)", err)
				}
			} else if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Revoke error = %v, want %v: a credential that could not be found is still live, and must never read as a clean revocation", err, tc.wantErr)
			}
			if keys.revokedKeyID != taskID {
				t.Fatalf("revoked key %s, want %s (the credential id is the task id, and revocation resolves it by primary key alone)",
					keys.revokedKeyID, taskID)
			}
		})
	}
}

// TestPgxTaskCredentials_RevokeDoesNotReResolveTheBillingAccount pins the fix
// for the defect that made this a merge blocker: Mint and Revoke each
// independently resolving public.tenant_billing_accounts meant a resolution
// that drifted between them left the revoke looking in the wrong place,
// reporting "not found", and a live credential surviving its full lifetime.
// Revocation now goes by primary key, which the credential's own id already is.
func TestPgxTaskCredentials_RevokeDoesNotReResolveTheBillingAccount(t *testing.T) {
	tenantID, taskID := uuid.New(), uuid.New()
	// Asserted through an EMPTY account map, with the reason stated: if Revoke
	// still resolved an account, this lookup would miss and it would fail with
	// ErrNoBillingAccount instead of revoking.
	db := &fakeAccountDB{accountByTenant: map[uuid.UUID]uuid.UUID{}}
	keys := &fakeKeyIssuer{}
	creds := agenttask.NewPgxTaskCredentials(db, keys)

	if err := creds.Revoke(context.Background(), agenttask.Task{ID: taskID, TenantID: tenantID, UserID: uuid.New()}); err != nil {
		t.Fatalf("Revoke: %v, want nil: revocation must not depend on a second billing-account resolution that can disagree with the mint's", err)
	}
	if keys.revokedKeyID != taskID {
		t.Fatalf("revoked key %s, want %s", keys.revokedKeyID, taskID)
	}
	if db.gotTenant != uuid.Nil {
		t.Fatalf("Revoke resolved tenant %s; it must not touch tenant_billing_accounts at all", db.gotTenant)
	}
}

// TestPgxTaskCredentials_RevokeReadsWhetherTheCredentialEverSettledAnything is
// the merge condition for this family of bug: a path that can silently produce
// a zero charge must leave a record with a distinct reason, because #1507 IS a
// zero charge nobody noticed for weeks.
//
// public.api_keys.last_used_at is written by exactly one production caller,
// accounting.Service.finalizeLocked, so it is stamped when and only when a
// reservation on that key reached a terminal charge. A revoked task credential
// with it still NULL means, precisely, that not one model turn ever settled
// against this task. Because the key id is the task id, those rows are the
// durable countable record; Revoke turns them into a loud one.
func TestPgxTaskCredentials_RevokeReadsWhetherTheCredentialEverSettledAnything(t *testing.T) {
	tenantID, taskID := uuid.New(), uuid.New()
	db := &fakeAccountDB{accountByTenant: map[uuid.UUID]uuid.UUID{tenantID: uuid.New()}}
	task := agenttask.Task{ID: taskID, TenantID: tenantID, UserID: uuid.New()}
	used := time.Now().UTC()

	// Asserted as a pair on purpose. A Revoke that reported for every task, or
	// for none, would satisfy either case on its own.
	for _, tc := range []struct {
		name       string
		lastUsedAt *time.Time
		wantReport bool
	}{
		{"settled nothing", nil, true},
		{"settled at least one turn", &used, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The log is captured because the report IS the deliverable here.
			// Asserting only that the issuer was called would hold identically
			// for both rows, so the pair would prove nothing and deleting
			// reportZeroCharge outright would leave this test green.
			var logBuf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
			t.Cleanup(func() { slog.SetDefault(prev) })

			keys := &fakeKeyIssuer{revokedLastUsedAt: tc.lastUsedAt}
			creds := agenttask.NewPgxTaskCredentials(db, keys)
			if err := creds.Revoke(context.Background(), task); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if !keys.revokeCalled || keys.revokedKeyID != taskID {
				t.Fatalf("revoked key %s (called=%v); want %s", keys.revokedKeyID, keys.revokeCalled, taskID)
			}
			got := strings.Contains(logBuf.String(), "agent_task_credential_settled_nothing")
			if got != tc.wantReport {
				t.Fatalf("zero-charge report present = %v, want %v; log: %s", got, tc.wantReport, logBuf.String())
			}
		})
	}
}
