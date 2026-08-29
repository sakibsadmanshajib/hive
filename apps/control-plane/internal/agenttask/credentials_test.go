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
	"context"
	"errors"
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
	mu       sync.Mutex
	accounts map[uuid.UUID]uuid.UUID
	minted   []mintedCredential
	revoked  []uuid.UUID
	mintErr  error
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
		secret:    "hk_test_account_" + acct.String() + "_task_" + t.ID.String(),
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

			created, err := svc.CreateTask(context.Background(), tenantID, userID, pack, "do the thing", "")
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

	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding, "", "")
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

	created, err := svc.CreateTask(context.Background(), tenantID, userID, agenttask.PackCoding, "", "")
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
	createdInput     apikeys.CreateKeyInput
	createErr        error

	revokedOnAccount uuid.UUID
	revokedKeyID     uuid.UUID
	revokeErr        error
}

func (f *fakeKeyIssuer) CreateKeyWithID(_ context.Context, accountID, actorUserID, keyID uuid.UUID, input apikeys.CreateKeyInput) (apikeys.CreateKeyResult, error) {
	f.createdOnAccount, f.createdActor, f.createdKeyID, f.createdInput = accountID, actorUserID, keyID, input
	if f.createErr != nil {
		return apikeys.CreateKeyResult{}, f.createErr
	}
	return apikeys.CreateKeyResult{Key: apikeys.APIKey{ID: keyID, AccountID: accountID}, Secret: "hk_live_secret"}, nil
}

func (f *fakeKeyIssuer) RevokeKey(_ context.Context, accountID, _, keyID uuid.UUID) (apikeys.APIKey, error) {
	f.revokedOnAccount, f.revokedKeyID = accountID, keyID
	if f.revokeErr != nil {
		return apikeys.APIKey{}, f.revokeErr
	}
	return apikeys.APIKey{ID: keyID, AccountID: accountID}, nil
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
	if secret != "hk_live_secret" || keys.createdOnAccount != accountID ||
		keys.createdKeyID != taskID || keys.createdActor != userID || db.gotTenant != tenantID {
		t.Fatalf("minted secret %q on account %s under key id %s for actor %s (looked up tenant %s); want %q on %s / %s / %s / %s",
			secret, keys.createdOnAccount, keys.createdKeyID, keys.createdActor, db.gotTenant,
			"hk_live_secret", accountID, taskID, userID, tenantID)
	}
	if keys.createdInput.ExpiresAt == nil {
		t.Fatal("minted credential has no expiry: nothing else bounds it when control-plane dies before revoking")
	}
	if until := time.Until(*keys.createdInput.ExpiresAt); until <= time.Hour {
		t.Fatalf("expiry is %s away, which is inside the range a long task can run; a credential that expires mid-run breaks the run", until)
	}
	if !strings.Contains(keys.createdInput.Nickname, taskID.String()) {
		t.Fatalf("nickname %q does not name the task it belongs to", keys.createdInput.Nickname)
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

func TestPgxTaskCredentials_RevokeIsIdempotent(t *testing.T) {
	tenantID, taskID, accountID := uuid.New(), uuid.New(), uuid.New()
	db := &fakeAccountDB{accountByTenant: map[uuid.UUID]uuid.UUID{tenantID: accountID}}
	task := agenttask.Task{ID: taskID, TenantID: tenantID, UserID: uuid.New()}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"already revoked", apikeys.ErrRevoked},
		{"never minted", apikeys.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys := &fakeKeyIssuer{revokeErr: tc.err}
			creds := agenttask.NewPgxTaskCredentials(db, keys)
			if err := creds.Revoke(context.Background(), task); err != nil {
				t.Fatalf("Revoke: %v, want nil (a terminal transition can fire twice)", err)
			}
			if keys.revokedKeyID != taskID || keys.revokedOnAccount != accountID {
				t.Fatalf("revoked key %s on account %s, want %s on %s",
					keys.revokedKeyID, keys.revokedOnAccount, taskID, accountID)
			}
		})
	}
}
