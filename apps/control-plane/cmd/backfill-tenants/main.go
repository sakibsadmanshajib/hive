// Command backfill-tenants gives a tenant to every account that holds an
// active API key but has no billing mapping, which is the state that answers
// 403 account_not_provisioned on every request after PR #620. Issue #625.
//
//	SUPABASE_DB_URL=<DB_URL> go run ./apps/control-plane/cmd/backfill-tenants
//
// Hive Cloud only. It refuses to run when config.IsEnterprisePosture reports
// Hive Enterprise (issue #653), the same posture check cmd/server/main.go
// uses to derive signup.WebhookDeps.SelfServeTenants, because personal
// tenants are a Cloud concept and Enterprise posture is that membership is
// administered. See checkPosture.
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
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/config"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signup"
)

// errEnterprisePosture is returned when this command is pointed at a Hive
// Enterprise deployment. Refusing loudly beats running a reduced sweep: an
// operator who ran it in the wrong place needs to learn that, and a command
// that half worked is the kind of thing nobody re-reads the output of.
var errEnterprisePosture = errors.New(
	"backfill-tenants: LICENSE_FILE_PATH is set, so this is a Hive Enterprise deployment.\n" +
		"Personal tenants are a Hive Cloud concept: Enterprise posture is that membership is\n" +
		"administered, so this command refuses to run here rather than minting tenants the\n" +
		"deployment forbids. Unset LICENSE_FILE_PATH only if this really is a Cloud database.")

// checkPosture calls config.IsEnterprisePosture, the single source of truth
// cmd/server/main.go also uses to derive signup.WebhookDeps.SelfServeTenants
// (issue #653), so the two entry points cannot disagree on deployment
// posture. Split out from main so the refusal is testable without running
// the command.
func checkPosture(licenseFilePath string) error {
	if config.IsEnterprisePosture(licenseFilePath) {
		return errEnterprisePosture
	}
	return nil
}

func main() {
	dsn := os.Getenv("SUPABASE_DB_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "backfill-tenants: SUPABASE_DB_URL is required")
		os.Exit(2)
	}

	if err := checkPosture(os.Getenv("LICENSE_FILE_PATH")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill-tenants: connect:", err)
		os.Exit(1)
	}
	defer pool.Close()

	report, err := signup.BackfillPersonalTenants(ctx, pool, true)
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
