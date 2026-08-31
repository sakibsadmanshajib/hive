package rag

import (
	"context"
	"errors"
	"os"
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
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), "DELETE FROM auth.users WHERE id = $1", id)
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
