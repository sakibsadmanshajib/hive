// Command backfill-tenants gives a tenant to every account that holds an
// active API key but has no billing mapping, which is the state that answers
// 403 account_not_provisioned on every request after PR #620. Issue #625.
//
//	SUPABASE_DB_URL=<DB_URL> go run ./apps/control-plane/cmd/backfill-tenants
//
// Operator run, deliberately not wired into server startup: it writes tenant
// rows, and a tenancy write that happens automatically on every deploy is not
// something anyone reviews. Idempotent, so re-running is safe and a second run
// reports nothing newly provisioned.
//
// It calls signup.BackfillPersonalTenants, the same code path a live signup
// runs, rather than a bespoke SQL predicate that could drift from it. Accounts
// whose billing account is genuinely ambiguous are listed as skipped with a
// reason and left alone: a wrong mapping is permanent and unrecoverable, and
// is worse than a 403.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
)

func main() {
	dsn := os.Getenv("SUPABASE_DB_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "backfill-tenants: SUPABASE_DB_URL is required")
		os.Exit(2)
	}

	// Deployment posture, read from the same env var
	// platform/config.Config.LicenseFilePath reads. An operator pointing this
	// at a Hive Enterprise database must not mint personal tenants there:
	// Enterprise posture is that membership is administered. The sweep still
	// runs, because retrying the billing mapping for an owner who already has
	// a tenant is posture-neutral, but it creates nothing and reports the
	// accounts it therefore cannot help.
	selfServeTenants := os.Getenv("LICENSE_FILE_PATH") == ""
	if !selfServeTenants {
		fmt.Println("LICENSE_FILE_PATH is set, so this is a Hive Enterprise deployment:")
		fmt.Println("no personal tenant will be created, mapping retries only.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill-tenants: connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	report, err := signup.BackfillPersonalTenants(ctx, pool, selfServeTenants)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill-tenants:", err)
		os.Exit(1)
	}

	fmt.Printf("provisioned %d account(s):\n", len(report.Provisioned))
	for _, id := range report.Provisioned {
		fmt.Printf("  %s\n", id)
	}

	skipped := make([]string, 0, len(report.Skipped))
	for id, reason := range report.Skipped {
		skipped = append(skipped, fmt.Sprintf("  %s  %s", id, reason))
	}
	sort.Strings(skipped)
	fmt.Printf("skipped %d account(s), left unmapped on purpose:\n", len(skipped))
	for _, line := range skipped {
		fmt.Println(line)
	}
}
