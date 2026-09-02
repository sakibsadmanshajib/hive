package mailer_test

import (
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/mailer"
)

// Individually reasonable ceilings that sum past the allowance is how a shared
// relay dies, and nothing was adding them up. This is the check that notices.
func TestBudgetSumsEveryCeilingAgainstTheAllowance(t *testing.T) {
	cases := []struct {
		name        string
		authRate    string
		autoconfirm string
		allowance   string
		perDay      int
		wantAuth    int
		wantTotal   int
		wantOver    bool
	}{
		{
			// The shipped defaults: 250 invitations plus 50 auth is exactly the
			// free-tier allowance, so a default deployment does not warn.
			name: "shipped defaults fit", authRate: "50/24h", allowance: "",
			perDay: 250, wantAuth: 50, wantTotal: 300, wantOver: false,
		},
		{
			// GoTrue's own format, a bare number meaning per hour. 30 an hour
			// is 720 a day, which alone is more than twice the free allowance.
			name: "bare number is read as an hourly rate", authRate: "30", allowance: "",
			perDay: 250, wantAuth: 720, wantTotal: 970, wantOver: true,
		},
		{
			name: "unset auth rate falls back to GoTrue's default", authRate: "", allowance: "",
			perDay: 250, wantAuth: 720, wantTotal: 970, wantOver: true,
		},
		{
			name: "a confirmed larger plan stops the warning", authRate: "30", allowance: "5000",
			perDay: 250, wantAuth: 720, wantTotal: 970, wantOver: false,
		},
		{
			name: "unparseable auth rate does not silently read as zero", authRate: "nonsense", allowance: "",
			perDay: 250, wantAuth: 720, wantTotal: 970, wantOver: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ENTERPRISE_RATE_LIMIT_EMAIL_SENT", tc.authRate)
			t.Setenv("ENTERPRISE_MAILER_AUTOCONFIRM", tc.autoconfirm)
			t.Setenv("HIVE_MAIL_PLAN_DAILY_ALLOWANCE", tc.allowance)

			budget := mailer.BudgetFromEnv(mailer.RelayCaps{PerDay: tc.perDay})
			if budget.AuthPerDay != tc.wantAuth {
				t.Errorf("AuthPerDay = %d, want %d", budget.AuthPerDay, tc.wantAuth)
			}
			if budget.Total() != tc.wantTotal {
				t.Errorf("Total = %d, want %d", budget.Total(), tc.wantTotal)
			}
			if budget.OverAllowance() != tc.wantOver {
				t.Errorf("OverAllowance = %v, want %v (%s)", budget.OverAllowance(), tc.wantOver, budget.Summary())
			}
			if got := len(budget.Warnings()) > 0; got != tc.wantOver {
				t.Errorf("warnings present = %v, want %v", got, tc.wantOver)
			}
		})
	}
}

// A configured cap that the auth mailer never consults is a plan, not a bound,
// and an operator reading only the number would believe otherwise.
func TestBudgetWarnsThatAutoconfirmSkipsTheAuthCap(t *testing.T) {
	t.Setenv("ENTERPRISE_RATE_LIMIT_EMAIL_SENT", "50/24h")
	t.Setenv("ENTERPRISE_MAILER_AUTOCONFIRM", "true")
	t.Setenv("HIVE_MAIL_PLAN_DAILY_ALLOWANCE", "")

	budget := mailer.BudgetFromEnv(mailer.RelayCaps{PerDay: 250})
	if budget.OverAllowance() {
		t.Fatalf("the numbers fit, so only the enforcement warning belongs here: %s", budget.Summary())
	}
	warnings := budget.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "AUTOCONFIRM") {
		t.Fatalf("warnings = %v, want one naming the autoconfirm skip", warnings)
	}
}
