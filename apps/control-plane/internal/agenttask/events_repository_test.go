package agenttask_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// Exercises the real migration SQL (20260823_01_agent_task_events.sql):
// append-only grants, RLS tenant scoping through the parent task, dedup on
// source_event_id, and the cursor read. Skips without HIVE_TEST_DB_URL, like
// every other repository test here.

func TestEventsRepositoryAppendAndList(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantID := uuid.New()
	seedTenant(t, tenantID)
	userID := seedUser(t)

	repo := agenttask.NewPgxRepository(pool)
	ctx := context.Background()

	task, err := repo.Create(ctx, tenantID, userID, agenttask.PackCoding, "goal", uuid.Nil)
	mustNoErrRepo(t, err)

	events := []agenttask.TaskEvent{
		{SourceEventID: "status:queued", Kind: agenttask.EventStatus, Payload: []byte(`{"status":"queued"}`)},
		{SourceEventID: "e1", Kind: agenttask.EventToolCall, Payload: []byte(`{"tool_name":"bash"}`)},
	}
	if err := repo.AppendEvents(ctx, task, events); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := repo.ListEvents(ctx, tenantID, userID, task.ID, 0, 100)
	mustNoErrRepo(t, err)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	for i := range got {
		if got[i].Seq != int64(i+1) {
			t.Errorf("event %d seq = %d, want %d (monotonic from 1)", i, got[i].Seq, i+1)
		}
	}

	// Re-append the same ids plus one new one: dedup keeps exactly the new row.
	more := append(append([]agenttask.TaskEvent{}, events...),
		agenttask.TaskEvent{SourceEventID: "e2", Kind: agenttask.EventMessage, Payload: []byte(`{"preview":"done"}`)})
	if err := repo.AppendEvents(ctx, task, more); err != nil {
		t.Fatalf("re-append: %v", err)
	}
	got, err = repo.ListEvents(ctx, tenantID, userID, task.ID, 0, 100)
	mustNoErrRepo(t, err)
	if len(got) != 3 {
		t.Fatalf("after dedup re-append got %d rows, want 3", len(got))
	}

	// Cursor: strictly newer than after_seq.
	got, err = repo.ListEvents(ctx, tenantID, userID, task.ID, 1, 100)
	mustNoErrRepo(t, err)
	if len(got) != 2 || got[0].Seq != 2 {
		t.Fatalf("cursor read = %+v, want seqs 2,3", got)
	}
}

func TestEventsRepositoryRLS(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA, tenantB := uuid.New(), uuid.New()
	seedTenant(t, tenantA)
	seedTenant(t, tenantB)
	userA := seedUser(t)

	repo := agenttask.NewPgxRepository(pool)
	ctx := context.Background()

	taskA, err := repo.Create(ctx, tenantA, userA, agenttask.PackCoding, "a", uuid.Nil)
	mustNoErrRepo(t, err)
	taskB, err := repo.Create(ctx, tenantB, userA, agenttask.PackCoding, "b", uuid.Nil)
	mustNoErrRepo(t, err)

	if err := repo.AppendEvents(ctx, taskA, []agenttask.TaskEvent{
		{SourceEventID: "x1", Kind: agenttask.EventStatus, Payload: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("append A: %v", err)
	}
	if err := repo.AppendEvents(ctx, taskB, []agenttask.TaskEvent{
		{SourceEventID: "y1", Kind: agenttask.EventStatus, Payload: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("append B: %v", err)
	}

	// Tenant A's connection cannot see tenant B's events: the RLS policy on
	// agent_task_events scopes through the parent task.
	got, err := repo.ListEvents(ctx, tenantA, userA, taskB.ID, 0, 100)
	mustNoErrRepo(t, err)
	if len(got) != 0 {
		t.Fatalf("cross-tenant read returned %d rows, want 0 (RLS leak)", len(got))
	}

	// Cross-user: same tenant, different user, must be empty (application
	// layer user scoping).
	userB := seedUser(t)
	got2, err := repo.ListEvents(ctx, tenantA, userB, taskA.ID, 0, 100)
	mustNoErrRepo(t, err)
	if len(got2) != 0 {
		t.Fatalf("cross-user read returned %d rows, want 0", len(got2))
	}
}

func mustNoErrRepo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
