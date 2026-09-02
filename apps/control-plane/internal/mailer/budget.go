package mailer

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// The mail budget: every ceiling that spends the relay account, added up in one
// place and checked against what the account actually allows.
//
// Individually reasonable caps that sum past the allowance is how this fails,
// and nothing noticed before: the invitation ceiling was sized against the relay
// and the auth mailer's was sized against nothing, so the total was whatever the
// two happened to add to. A number nobody sums is not a budget.
//
// The allowance defaults to the smallest plan this deployment could be on, so
// the check is meaningful without knowing the tier. Raise it once the tier is
// confirmed, in one place.
const (
	DefaultPlanDailyAllowance = 300

	// GoTrue's own default, in requests per hour, used when the auth mailer's
	// cap is not configured. Its config accepts either a bare number (per hour)
	// or an events/interval form such as 50/24h.
	defaultAuthRatePerHour = 30
)

// Budget is the deployment's daily outbound mail accounting.
type Budget struct {
	InvitationPerDay int
	AuthPerDay       int
	Allowance        int
	// AuthUncapped records that the auth mailer's cap exists in configuration
	// but is not enforced, which is the state whenever GoTrue autoconfirms:
	// its rate limit is skipped entirely for an autoconfirming deployment, so
	// the configured number is a plan, not a bound.
	AuthUncapped bool
}

// Total is the mail this deployment can emit in a day if every ceiling is spent.
func (b Budget) Total() int { return b.InvitationPerDay + b.AuthPerDay }

// OverAllowance reports whether the ceilings add up past what the relay allows.
func (b Budget) OverAllowance() bool { return b.Allowance > 0 && b.Total() > b.Allowance }

// Summary is the one line an operator reads to see the whole picture.
func (b Budget) Summary() string {
	return fmt.Sprintf("mail budget: invitations %d/day + auth %d/day = %d/day against a stated allowance of %d/day",
		b.InvitationPerDay, b.AuthPerDay, b.Total(), b.Allowance)
}

// Warnings are the conditions worth a WARN at boot, in order. Empty when the
// budget fits and every ceiling in it is actually enforced.
func (b Budget) Warnings() []string {
	var warnings []string
	if b.OverAllowance() {
		warnings = append(warnings, fmt.Sprintf(
			"WARN mail budget exceeds the stated allowance by %d/day: exhausting it fails account verification, "+
				"one-time codes, invoices and balance alerts along with invitations. Lower a ceiling "+
				"(HIVE_MAIL_RELAY_CAP_PER_DAY, ENTERPRISE_RATE_LIMIT_EMAIL_SENT) or raise "+
				"HIVE_MAIL_PLAN_DAILY_ALLOWANCE once the relay plan is confirmed",
			b.Total()-b.Allowance))
	}
	if b.AuthUncapped {
		warnings = append(warnings, "WARN the auth mailer's daily cap is configured but not enforced: "+
			"GoTrue skips its email rate limit entirely while ENTERPRISE_MAILER_AUTOCONFIRM is true, "+
			"so auth mail is bounded only per address and per client address")
	}
	return warnings
}

// BudgetFromEnv reads what every ceiling is configured to, so the sum is drawn
// from the same values the services run with rather than from a number kept in
// step by hand.
func BudgetFromEnv(caps RelayCaps) Budget {
	return Budget{
		InvitationPerDay: caps.PerDay,
		AuthPerDay:       authPerDayFromEnv(),
		Allowance:        capFromEnv("HIVE_MAIL_PLAN_DAILY_ALLOWANCE", DefaultPlanDailyAllowance),
		AuthUncapped:     strings.EqualFold(strings.TrimSpace(os.Getenv("ENTERPRISE_MAILER_AUTOCONFIRM")), "true"),
	}
}

// authPerDayFromEnv converts the auth mailer's configured rate into a daily
// number. GoTrue's format is a bare float (events per hour) or events/interval,
// and this reads both so the two sides cannot drift.
func authPerDayFromEnv() int {
	raw := strings.TrimSpace(os.Getenv("ENTERPRISE_RATE_LIMIT_EMAIL_SENT"))
	if raw == "" {
		return defaultAuthRatePerHour * 24
	}
	events, interval, found := strings.Cut(raw, "/")
	count, err := strconv.ParseFloat(strings.TrimSpace(events), 64)
	if err != nil || count < 0 {
		return defaultAuthRatePerHour * 24
	}
	if !found {
		return int(count) * 24
	}
	window, err := time.ParseDuration(strings.TrimSpace(interval))
	if err != nil || window <= 0 {
		return defaultAuthRatePerHour * 24
	}
	return int(count * float64(24*time.Hour) / float64(window))
}
