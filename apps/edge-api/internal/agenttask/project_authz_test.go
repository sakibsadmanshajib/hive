package agenttask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/rag"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling/billingtest"
)

// fakeProjectAuthorizer models the real authorizer's contract exactly: it
// answers only for the (tenant, owner) pair that actually owns the project, and
// returns rag.ErrProjectForbidden for everything else without distinguishing
// why. Anything looser would let the tests below pass on a handler that never
// consulted it.
type fakeProjectAuthorizer struct {
	tenantID  uuid.UUID
	ownerID   uuid.UUID
	projectID uuid.UUID
	called    bool
	infraErr  error
}

func (f *fakeProjectAuthorizer) AuthorizeProject(_ context.Context, tenantID, userID, projectID uuid.UUID) error {
	f.called = true
	if f.infraErr != nil {
		return f.infraErr
	}
	if tenantID != f.tenantID || userID != f.ownerID || projectID != f.projectID {
		return rag.ErrProjectForbidden
	}
	return nil
}

func projectCreateReq(t *testing.T, tenantID, userID uuid.UUID, projectID string) *http.Request {
	t.Helper()
	body, err := json.Marshal(createTaskRequest{Pack: "coding-pack", ProjectID: projectID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/tasks", bytes.NewReader(body))
	return req.WithContext(auth.WithUser(context.Background(), &auth.User{ID: userID, TenantID: tenantID}))
}

func projectHandler(t *testing.T, client TaskClient, authz ProjectAuthorizer) *Handler {
	t.Helper()
	acct := &billingtest.Accounting{}
	h := NewHandler(client).WithBilling(acct.Client(t), billingtest.Billable())
	if authz != nil {
		h = h.WithProjectAuthorizer(authz)
	}
	return h
}

// TestCreateRefusesAnotherMembersProject is acceptance criterion 6 of the issue
// #1595 spec on the Work mode route.
//
// The attacker is a fully authenticated ordinary member of the SAME tenant as
// the project's owner, so row level security is satisfied for them and cannot
// help. Only the ownership check stands between their run and the owner's
// private documents.
//
// The second assertion is the one that matters. A 404 alone could come from a
// handler that created the task first and refused afterwards, which would have
// launched a sandbox and taken a credit hold against the tenant for a run over
// another member's project.
func TestCreateRefusesAnotherMembersProject(t *testing.T) {
	tenantID := uuid.New()
	ownerID := uuid.New()
	attackerID := uuid.New()
	projectID := uuid.New()

	client := newFakeClient()
	authz := &fakeProjectAuthorizer{tenantID: tenantID, ownerID: ownerID, projectID: projectID}
	h := projectHandler(t, client, authz)

	w := httptest.NewRecorder()
	h.handleCreate(w, projectCreateReq(t, tenantID, attackerID, projectID.String()))

	if w.Code != http.StatusNotFound {
		t.Fatalf("a tenant member attaching another member's project must be refused with 404, got %d: %s", w.Code, w.Body.String())
	}
	if !authz.called {
		t.Fatal("the handler never consulted the project authorizer: it is trusting a client supplied project id")
	}
	if client.createCalled {
		t.Fatal("an unauthorized project reached control-plane: authorization must run before the task is created, not after")
	}
}

// TestCreateRefusesAnotherTenantsProject is the cross-tenant half. The caller
// is authenticated in their own tenant and names a project belonging to a
// different tenant entirely.
//
// What this proves beyond the obvious: the handler turns the refusal into a
// refusal, rather than falling back to launching the run with no project at
// all. That fallback is the quiet failure, because the caller is answered 201
// and believes their project is attached.
func TestCreateRefusesAnotherTenantsProject(t *testing.T) {
	callerTenant := uuid.New()
	callerID := uuid.New()
	foreignTenant := uuid.New()
	foreignProject := uuid.New()

	client := newFakeClient()
	authz := &fakeProjectAuthorizer{tenantID: foreignTenant, ownerID: uuid.New(), projectID: foreignProject}
	h := projectHandler(t, client, authz)

	w := httptest.NewRecorder()
	h.handleCreate(w, projectCreateReq(t, callerTenant, callerID, foreignProject.String()))

	if w.Code != http.StatusNotFound {
		t.Fatalf("a project id from another tenant must be refused with 404, got %d: %s", w.Code, w.Body.String())
	}
	if client.createCalled {
		t.Fatal("a cross-tenant project id produced a task: it must be refused, never silently launched with no project")
	}
}

// TestCreatePassesAnOwnedProjectThrough is the positive control: without it, a
// handler that refused every project id would pass both tests above.
func TestCreatePassesAnOwnedProjectThrough(t *testing.T) {
	tenantID := uuid.New()
	ownerID := uuid.New()
	projectID := uuid.New()

	client := newFakeClient()
	authz := &fakeProjectAuthorizer{tenantID: tenantID, ownerID: ownerID, projectID: projectID}
	h := projectHandler(t, client, authz)

	w := httptest.NewRecorder()
	h.handleCreate(w, projectCreateReq(t, tenantID, ownerID, projectID.String()))

	if w.Code != http.StatusCreated {
		t.Fatalf("the project's owner must be able to attach it, got %d: %s", w.Code, w.Body.String())
	}
	if client.lastProjectID != projectID {
		t.Fatalf("the project id did not reach control-plane: want %s, got %s", projectID, client.lastProjectID)
	}
}

// TestCreateWithoutProjectSkipsAuthorization: the overwhelmingly common case is
// a task with no project, and it must not start depending on an authorizer.
func TestCreateWithoutProjectSkipsAuthorization(t *testing.T) {
	tenantID := uuid.New()
	client := newFakeClient()
	authz := &fakeProjectAuthorizer{tenantID: tenantID, ownerID: uuid.New(), projectID: uuid.New()}
	h := projectHandler(t, client, authz)

	w := httptest.NewRecorder()
	h.handleCreate(w, projectCreateReq(t, tenantID, uuid.New(), ""))

	if w.Code != http.StatusCreated {
		t.Fatalf("a task with no project must still be created, got %d: %s", w.Code, w.Body.String())
	}
	if authz.called {
		t.Fatal("a task with no project consulted the project authorizer")
	}
	if client.lastProjectID != uuid.Nil {
		t.Fatalf("a task with no project must pass uuid.Nil, got %s", client.lastProjectID)
	}
}

// TestCreateRefusesAProjectWhenNoAuthorizerIsWired is the fail-closed case. A
// deployment with no database has no way to learn who owns a project, so the
// only safe answer is the same refusal an unowned id gets. Passing it through
// unverified is the defect itself.
func TestCreateRefusesAProjectWhenNoAuthorizerIsWired(t *testing.T) {
	client := newFakeClient()
	h := projectHandler(t, client, nil)

	w := httptest.NewRecorder()
	h.handleCreate(w, projectCreateReq(t, uuid.New(), uuid.New(), uuid.New().String()))

	if w.Code != http.StatusNotFound {
		t.Fatalf("with no authorizer wired a project id must be refused, got %d: %s", w.Code, w.Body.String())
	}
	if client.createCalled {
		t.Fatal("an unverifiable project id produced a task")
	}
}

// TestCreateDoesNotFlattenAnInfraFailureIntoNotFound. A database failure during
// authorization must be a 500, never the 404 that means "not your project": a
// blip would otherwise look like a permission answer and quietly discard a
// legitimate task, and the operator would have nothing to chase.
func TestCreateDoesNotFlattenAnInfraFailureIntoNotFound(t *testing.T) {
	client := newFakeClient()
	authz := &fakeProjectAuthorizer{infraErr: errors.New("connection refused")}
	h := projectHandler(t, client, authz)

	w := httptest.NewRecorder()
	h.handleCreate(w, projectCreateReq(t, uuid.New(), uuid.New(), uuid.New().String()))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("an authorization infrastructure failure must be 500, got %d: %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("connection refused")) {
		t.Fatal("the internal failure detail leaked to the customer")
	}
	if client.createCalled {
		t.Fatal("a task was created despite authorization failing")
	}
}
