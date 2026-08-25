package byok

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository integration tests run only when CI's throwaway Postgres is
// wired via HIVE_TEST_DB_URL (see .github/workflows/ci.yml bootstrap step).
// They exist because the fakeRepo unit suite re-implements the account filter
// itself, so only real SQL can catch a dropped WHERE account_id predicate or
// a swapped scanTarget column mapping.
const repoTestDSNEnv = "HIVE_TEST_DB_URL"

func requireRepoTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(repoTestDSNEnv)
	if dsn == "" {
		t.Skip(repoTestDSNEnv + " not set")
	}
	return dsn
}

func seedAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tag string) uuid.UUID {
	t.Helper()
	userID := uuid.Must(uuid.NewV7())
	accountID := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth.users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		userID, "byok-"+tag+"-"+userID.String()+"@hive-test.invalid"); err != nil {
		t.Fatalf("seed auth.users: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES ($1, $2, 'byok repository test', 'business', $3) ON CONFLICT (id) DO NOTHING`,
		accountID, "byok-repo-test-"+accountID.String(), userID); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM public.tenant_provider_keys WHERE account_id = $1`, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM auth.users WHERE id = $1`, userID)
	})
	return accountID
}

func repoTestCipher(t *testing.T) *Cipher {
	t.Helper()
	c, err := LoadCipher("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3OGFhYmNkZWY=")
	if err != nil {
		t.Fatalf("LoadCipher: %v", err)
	}
	return c
}

func TestRepositoryIsolationAgainstRealSQL(t *testing.T) {
	dsn := requireRepoTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("test database unreachable: %v", err)
	}
	defer pool.Close()

	repo := NewPgxRepository(pool).(*pgxRepository)
	accountA := seedAccount(t, ctx, pool, "a")
	accountB := seedAccount(t, ctx, pool, "b")

	blob := []byte("ciphertext-bytes-for-isolation-test")
	created, err := repo.Create(ctx, Key{
		AccountID:       accountA,
		Label:           "iso-a",
		BaseURL:         strPtr("https://a.example/v1"),
		ModelMap:        map[string]string{},
		EncryptedAPIKey: blob,
		KeyLast4:        "st4t",
		Status:          StatusActive,
		CreatedBy:       uuid.Must(uuid.NewV7()),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil || created.CreatedAt.IsZero() {
		t.Fatal("Create did not return DB-generated columns")
	}
	if string(created.EncryptedAPIKey) != string(blob) {
		t.Fatal("bytea round trip through scanTarget corrupted ciphertext bytes")
	}

	// Cross-account Get and Revoke are indistinguishable from missing rows.
	if _, err := repo.Get(ctx, accountB, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account Get = %v, want ErrNotFound", err)
	}
	if _, err := repo.Revoke(ctx, accountB, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account Revoke = %v, want ErrNotFound", err)
	}
	got, err := repo.Get(ctx, accountA, created.ID)
	if err != nil || got.Status != StatusActive {
		t.Fatalf("victim row must stay active after cross-account attempts, got %v status=%q", err, got.Status)
	}

	// List scoping.
	listA, err := repo.ListByAccount(ctx, accountA)
	if err != nil || len(listA) != 1 {
		t.Fatalf("ListByAccount(A) = %d rows err %v, want exactly the one A row", len(listA), err)
	}
	listB, err := repo.ListByAccount(ctx, accountB)
	if err != nil || len(listB) != 0 {
		t.Fatalf("ListByAccount(B) = %d rows err %v, want zero (isolation)", len(listB), err)
	}
	all, err := repo.ListAll(ctx)
	if err != nil || len(all) < 1 {
		t.Fatalf("ListAll = %d rows err %v, want at least the seeded row", len(all), err)
	}

	// Own revoke works once, then the row is gone for revoke purposes.
	revoked, err := repo.Revoke(ctx, accountA, created.ID)
	if err != nil || revoked.Status != StatusRevoked {
		t.Fatalf("own Revoke = %v status=%q, want revoked", err, revoked.Status)
	}
	if _, err := repo.Revoke(ctx, accountA, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Revoke = %v, want ErrNotFound (status filter)", err)
	}
}

func TestRepositoryUniqueConstraintsAgainstRealSQL(t *testing.T) {
	dsn := requireRepoTestDSN(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("test database unreachable: %v", err)
	}
	defer pool.Close()

	repo := NewPgxRepository(pool).(*pgxRepository)
	accountA := seedAccount(t, ctx, pool, "u")

	mk := func(label string) Key {
		return Key{
			AccountID:       accountA,
			Label:           label,
			ModelMap:        map[string]string{},
			EncryptedAPIKey: []byte("ct"),
			KeyLast4:        "st4t",
			Status:          StatusActive,
			CreatedBy:       uuid.Must(uuid.NewV7()),
		}
	}

	first, err := repo.Create(ctx, withSlug(mk("slug-1"), "openrouter"))
	if err != nil {
		t.Fatalf("first slug insert: %v", err)
	}
	if _, err := repo.Create(ctx, withSlug(mk("slug-dup"), "openrouter")); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate active slug = %v, want ErrConflict", err)
	}
	if _, err := repo.Revoke(ctx, accountA, first.ID); err != nil {
		t.Fatalf("revoke first slug row: %v", err)
	}
	// After revoke the partial unique index frees the slot.
	if _, err := repo.Create(ctx, withSlug(mk("slug-again"), "openrouter")); err != nil {
		t.Fatalf("re-register after revoke = %v, want success", err)
	}

	urlOne, err := repo.Create(ctx, withURL(mk("url-1"), "https://one.example/v1"))
	if err != nil {
		t.Fatalf("first url insert: %v", err)
	}
	dupURL, err := repo.Create(ctx, withURL(mk("url-dup"), "https://one.example/v1"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate active base_url = %v, want ErrConflict", err)
	}
	if dupURL.ID == uuid.Nil {
		t.Fatal("conflict path must not return a row")
	}
	_ = urlOne

	badTarget := mk("bad-target")
	badTarget.BaseURL = strPtr("")
	if _, err := repo.Create(ctx, badTarget); err == nil || errors.Is(err, ErrConflict) {
		t.Fatal("empty base_url must violate the target CHECK at the storage boundary")
	}
}

func withSlug(k Key, slug string) Key { k.ProviderSlug = strPtr(slug); return k }

func withURL(k Key, u string) Key { k.BaseURL = strPtr(u); return k }
