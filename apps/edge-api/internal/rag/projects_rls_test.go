package rag

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedProjectUser inserts a minimal auth.users row so rag_projects.owner_user_id's
// FK is satisfiable. Mirrors apps/control-plane/internal/agenttask/repository_test.go's
// seedUser: an unscoped short-lived connection, because hive_app has no INSERT
// path into the auth schema.
func seedProjectUser(t *testing.T) uuid.UUID {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	var id uuid.UUID
	email := "rag-project-test-" + uuid.NewString() + "@example.invalid"
	err = setup.QueryRow(ctx,
		"INSERT INTO auth.users(id, email, raw_user_meta_data) VALUES (gen_random_uuid(), $1, '"+"{}"+"'::jsonb) RETURNING id",
		email).Scan(&id)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cerr := pgxpool.New(context.Background(), dsn)
		if cerr != nil {
			t.Errorf("cleanup pool for seeded user %s: %v", id, cerr)
			return
		}
		defer cleanup.Close()
		// Projects first. rag_projects.owner_user_id has no ON DELETE action by
		// design, so deleting the user while a project still references it is
		// rejected, and a discarded error there leaves a seeded user behind in
		// the CI database on every run. Documents attached to those projects
		// survive with project_id NULL, which is the referential action under
		// test elsewhere in this file.
		if _, derr := cleanup.Exec(context.Background(),
			"DELETE FROM public.rag_projects WHERE owner_user_id = $1", id); derr != nil {
			t.Errorf("cleanup projects for seeded user %s: %v", id, derr)
			return
		}
		if _, derr := cleanup.Exec(context.Background(),
			"DELETE FROM auth.users WHERE id = $1", id); derr != nil {
			t.Errorf("cleanup seeded user %s: %v", id, derr)
		}
	})
	return id
}

// seedChunk inserts one embedded chunk for an existing document, through an
// unscoped connection (the same shortcut repository_rls_test.go's own seeding
// takes: the point of these tests is the read path, not the write path).
func seedChunk(t *testing.T, tenantID, docID uuid.UUID, index int, content string) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	encoded, err := encodeVector(fixedVector(0.01))
	if err != nil {
		t.Fatalf("encode seed vector: %v", err)
	}
	_, err = setup.Exec(ctx, "INSERT INTO public.rag_chunks (tenant_id, document_id, chunk_index, content, token_count, embedding) VALUES ($1, $2, $3, $4, 1, $5::vector)",
		tenantID, docID, index, content, encoded)
	if err != nil {
		t.Fatalf("seed chunk: %v", err)
	}
	// The document must read as embedded or the handler's guard paths would
	// treat this corpus as pending.
	_, err = setup.Exec(ctx, "UPDATE public.rag_documents SET status = 'embedded' WHERE id = $1", docID)
	if err != nil {
		t.Fatalf("mark document embedded: %v", err)
	}
}

// TestRepo_RLS_GetProjectCannotReadAnotherTenantsProject is the cross-tenant
// refusal proven against a real database with a real policy, rather than
// against a fake that models one.
//
// It runs as hive_app, which is NOT BYPASSRLS, so
// rag_projects_tenant_isolation is genuinely in force. Tenant B's project must
// be invisible to a transaction scoped to tenant A even though the caller names
// the id exactly, and the repository must turn that empty read into
// ErrProjectForbidden rather than a bare no-rows error a caller might mistake
// for something else.
//
// What this proves, and what it does not, stated because the difference is easy
// to misread (issue #1469, with #1444 and #1446). newRLSTestPool reaches
// hive_app through SET ROLE, which succeeds only because
// .github/ci/test-db-bootstrap.sql grants hive_app to postgres; that grant is in
// the CI fixture and nowhere else. Every deployment connects as postgres, which
// carries rolbypassrls, and issues no SET ROLE anywhere under apps/. So this
// proves rag_projects_tenant_isolation is a correct policy, and proves nothing
// about whether tenant A can read tenant B's project on a running box, where
// the policy is never evaluated.
//
// That is not a reason to weaken the test: the policy is right to exist for the
// day #1469 is fixed. It is the reason every statement in projects.go carries an
// explicit tenant_id predicate, which is what actually holds the boundary in
// production, and it is why this test now runs against SQL that would refuse the
// cross-tenant read even with the policy switched off.
func TestRepo_RLS_GetProjectCannotReadAnotherTenantsProject(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := NewRepo(pool, "vector")
	ctx := context.Background()

	tenantA, tenantB := uuid.New(), uuid.New()
	seedRAGTenant(t, tenantA)
	seedRAGTenant(t, tenantB)
	ownerB := seedProjectUser(t)

	projectB, err := repo.CreateProject(ctx, tenantB, ownerB, "tenant B private", "secret instructions")
	if err != nil {
		t.Fatalf("CreateProject for tenant B: %v", err)
	}

	_, err = repo.GetProject(ctx, tenantA, projectB.ID)
	if !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("tenant A read tenant B's project: want ErrProjectForbidden, got err=%v", err)
	}
}

// TestRepo_RLS_ListProjectsIsOwnerScoped proves the cross-MEMBER boundary at
// the repository level, which row level security cannot provide: both users are
// in the same tenant and present the same app.current_tenant_id, so the owner
// predicate in the SQL is the only thing separating them.
func TestRepo_RLS_ListProjectsIsOwnerScoped(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := NewRepo(pool, "vector")
	ctx := context.Background()

	tenantID := uuid.New()
	seedRAGTenant(t, tenantID)
	ownerA := seedProjectUser(t)
	ownerB := seedProjectUser(t)

	mine, err := repo.CreateProject(ctx, tenantID, ownerA, "mine", "")
	if err != nil {
		t.Fatalf("CreateProject for owner A: %v", err)
	}
	if _, err := repo.CreateProject(ctx, tenantID, ownerB, "theirs", ""); err != nil {
		t.Fatalf("CreateProject for owner B: %v", err)
	}

	listed, err := repo.ListProjects(ctx, tenantID, ownerA)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != mine.ID {
		t.Fatalf("owner A must see exactly their own project, got %d rows: %+v", len(listed), listed)
	}
}

// TestRepo_RLS_ProjectScopedSearchExcludesUnattachedDocuments closes the gap the
// handler tests cannot: the handler proves WHO may filter, and this proves the
// filter itself actually narrows. A project scoped search that quietly ignored
// its scope would satisfy every authorization test in this package and still
// hand the caller the tenant's whole corpus.
//
// It also exercises the join that carries the document name, so a citation
// naming nothing fails here rather than in a browser.
func TestRepo_RLS_ProjectScopedSearchExcludesUnattachedDocuments(t *testing.T) {
	pool := newRLSTestPool(t)
	repo := NewRepo(pool, "vector")
	ctx := context.Background()

	tenantID := uuid.New()
	seedRAGTenant(t, tenantID)
	ownerID := seedProjectUser(t)

	project, err := repo.CreateProject(ctx, tenantID, ownerID, "scoped", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	inProject, err := repo.InsertDocument(ctx, tenantID, "in-project.txt", "text/plain", 10)
	if err != nil {
		t.Fatalf("InsertDocument in project: %v", err)
	}
	outside, err := repo.InsertDocument(ctx, tenantID, "outside.txt", "text/plain", 10)
	if err != nil {
		t.Fatalf("InsertDocument outside project: %v", err)
	}
	if err := repo.AttachDocument(ctx, tenantID, ownerID, project.ID, inProject); err != nil {
		t.Fatalf("AttachDocument: %v", err)
	}

	seedChunk(t, tenantID, inProject, 0, "the in-project passage")
	seedChunk(t, tenantID, outside, 0, "the unattached passage")

	vec := fixedVector(0.01)

	scoped, err := repo.SearchChunks(ctx, tenantID, vec, 10, project.ID)
	if err != nil {
		t.Fatalf("scoped SearchChunks: %v", err)
	}
	if len(scoped) != 1 {
		t.Fatalf("a project scoped search must return only the project's passages, got %d: %+v", len(scoped), scoped)
	}
	if scoped[0].DocumentID != inProject {
		t.Fatalf("scoped search returned the wrong document: %s", scoped[0].DocumentID)
	}
	if scoped[0].DocumentName != "in-project.txt" {
		t.Fatalf("scoped search lost the document name, so a citation cannot name its source: got %q", scoped[0].DocumentName)
	}

	unscoped, err := repo.SearchChunks(ctx, tenantID, vec, 10, uuid.Nil)
	if err != nil {
		t.Fatalf("unscoped SearchChunks: %v", err)
	}
	if len(unscoped) != 2 {
		t.Fatalf("an unscoped search must still see the whole tenant corpus, got %d", len(unscoped))
	}
}

// newBypassRLSTestPool connects WITHOUT SET ROLE, so it runs as whatever
// HIVE_TEST_DB_URL names, which is postgres and carries rolbypassrls. That is
// the role every deployment actually connects as (issue #1469), so a policy is
// never evaluated on this pool and only the SQL a statement carries can refuse
// anything.
//
// The sibling newRLSTestPool exists to prove the policy is correct. This one
// exists to prove the product is correct, which is a different claim, and the
// two together are what makes the tenant boundary provable rather than asserted.
func newBypassRLSTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatal("HIVE_TEST_DB_URL not set in CI: this suite guards a real-SQL proof and must not silently skip")
		}
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse HIVE_TEST_DB_URL: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Asserted rather than assumed: if this pool were ever NOT a bypass role the
	// test below would pass for the wrong reason, proving the policy again
	// instead of the predicate.
	var bypasses bool
	if err := pool.QueryRow(context.Background(),
		"SELECT rolbypassrls FROM pg_roles WHERE rolname = current_user").Scan(&bypasses); err != nil {
		pool.Close()
		t.Fatalf("reading rolbypassrls for the test role: %v", err)
	}
	if !bypasses {
		pool.Close()
		t.Skipf("HIVE_TEST_DB_URL connects as %s, which is not a BYPASSRLS role, so this test cannot isolate the SQL predicate from the policy", "current_user")
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestRepo_ProjectReadsAreTenantScopedUnderBypassRLS is the production shaped
// half of the cross-tenant proof. It runs as the BYPASSRLS role the deployment
// connects as, so rag_projects_tenant_isolation is not evaluated at all and the
// only thing that can refuse a cross-tenant read is the tenant_id predicate in
// the statement itself.
//
// It goes red against the SQL this pull request was first pushed with, where
// ListProjects filtered on owner_user_id alone and GetProject on id alone: a
// user who owns projects in two tenants got both back from whichever workspace
// they were acting in, and the handler rendered the lot.
func TestRepo_ProjectReadsAreTenantScopedUnderBypassRLS(t *testing.T) {
	pool := newBypassRLSTestPool(t)
	repo := NewRepo(pool, "vector")
	ctx := context.Background()

	// One user, two tenants: a personal tenant and a workspace is the ordinary
	// shape here (20260801_10_tenants_personal_owner.sql), not a contrivance.
	tenantA, tenantB := uuid.New(), uuid.New()
	seedRAGTenant(t, tenantA)
	seedRAGTenant(t, tenantB)
	owner := seedProjectUser(t)

	inA, err := repo.CreateProject(ctx, tenantA, owner, "in tenant A", "")
	if err != nil {
		t.Fatalf("CreateProject in tenant A: %v", err)
	}
	inB, err := repo.CreateProject(ctx, tenantB, owner, "in tenant B", "secret instructions")
	if err != nil {
		t.Fatalf("CreateProject in tenant B: %v", err)
	}

	listed, err := repo.ListProjects(ctx, tenantA, owner)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != inA.ID {
		t.Fatalf("listing in tenant A must return only tenant A's project, got %d rows: %+v", len(listed), listed)
	}

	if _, err := repo.GetProject(ctx, tenantA, inB.ID); !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("reading tenant B's project while scoped to tenant A: want ErrProjectForbidden, got err=%v", err)
	}

	// The writes carry the same predicate, so the same cross-tenant reference
	// must be refused there too rather than silently matching on id and owner.
	newName := "renamed across the boundary"
	if _, err := repo.UpdateProject(ctx, tenantA, owner, inB.ID, &newName, nil); !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("updating tenant B's project from tenant A: want ErrProjectForbidden, got err=%v", err)
	}
	if err := repo.DeleteProject(ctx, tenantA, owner, inB.ID); !errors.Is(err, ErrProjectForbidden) {
		t.Fatalf("deleting tenant B's project from tenant A: want ErrProjectForbidden, got err=%v", err)
	}
}
