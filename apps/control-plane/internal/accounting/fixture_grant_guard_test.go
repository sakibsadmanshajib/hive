package accounting

import (
	"errors"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The e2e fixture funds every test tenant with a flat credit grant, and
// /v1/chat/completions refuses any account that cannot cover its flat
// authorization hold before it dispatches. Those two numbers live in
// different languages, in different modules, and nothing used to hold them
// together.
//
// The 2026-08-23 credit unit rescale (D-046) moved the unit from 1 USD =
// 100,000 credits to 1 USD = 1e9 credits and multiplied every stored credits
// column by 10,000. It rescaled rows. It could not reach a constant in
// JavaScript source, so the fixture went on granting a literal 1,000,000,
// which stopped meaning 10.00 USD and started meaning 0.001 USD, one
// hundredth of the hold. Every fixture account seeded after that migration
// was refused on quota at its first turn, on every alias, because the hold is
// flat and is taken before the alias price is consulted (issue #1441).
//
// Both directions have to survive every future change to either number:
//
//	ADMIT  a fixture-funded account can pay the hold it will actually be asked for
//	REFUSE an account below the hold is still refused, because serving free is worse
//
// Reading both figures out of their own source files, rather than copying
// them here, is what makes this guard track the shipped values instead of a
// snapshot of them.

// documentedHoldsPerFixtureGrant is the margin the seeder's comment states
// outright, so a spec that sends more than one turn does not fail on a later
// one. It is the documented figure rather than a loose floor on purpose: a
// looser bound would let a regression land anywhere between one hold and this
// one while a guard that promised a hundred stayed green, which is the same
// class of silent drift this file exists to catch.
//
// If the flat hold is deliberately raised, this goes red, and the fix is to
// re-tune FIXTURE_GRANT_CREDITS in the seeder rather than to lower the number
// here.
const documentedHoldsPerFixtureGrant = 100

// readInt64Constant pulls a single integer constant out of a source file as
// shipped, so this guard tracks the real value rather than a copy of it.
func readInt64Constant(t *testing.T, path, pattern, label string) int64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s, which this guard depends on: %v", path, err)
	}
	m := regexp.MustCompile(pattern).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("could not find %s in %s; if it was renamed, moved or made dynamic, move this guard with it", label, path)
	}
	value, err := strconv.ParseInt(strings.ReplaceAll(string(m[1]), "_", ""), 10, 64)
	if err != nil {
		t.Fatalf("%s value %q is not a number: %v", label, m[1], err)
	}
	return value
}

// readFixtureGrantCredits pulls the grant out of the e2e seeder as shipped.
func readFixtureGrantCredits(t *testing.T) int64 {
	t.Helper()
	return readInt64Constant(t,
		"../../../../apps/web-console/tests/e2e/support/e2e-fixture-seed.mjs",
		`FIXTURE_GRANT_CREDITS\s*=\s*([0-9_]+)`,
		"FIXTURE_GRANT_CREDITS")
}

// readDefaultHoldText pulls the flat chat hold out of edge-api. edge-api is a
// separate Go module, so the constant cannot be imported from control-plane;
// the hold guards inside edge-api read foreign files the same way.
func readDefaultHoldText(t *testing.T) int64 {
	t.Helper()
	return readInt64Constant(t,
		"../../../../apps/edge-api/internal/inference/pricing.go",
		`DefaultHoldText\s+int64\s*=\s*([0-9_]+)`,
		"DefaultHoldText")
}

func TestFixtureGrantCoversTheFlatChatHold(t *testing.T) {
	grant := readFixtureGrantCredits(t)
	hold := readDefaultHoldText(t)

	if hold <= 0 {
		t.Fatalf("flat chat hold is %d, so this guard would prove nothing", hold)
	}

	// ADMIT: the real refusal rule, driven with the real shipped numbers.
	if err := enforcePolicy(PolicyModeStrict, grant, hold); err != nil {
		t.Errorf("a fixture-funded account cannot pay the flat chat hold: grant=%d hold=%d: %v.\n"+
			"The fixture grant is stated in credits, and the credit unit was rescaled on 2026-08-23 "+
			"to 1 USD = 1e9 credits (D-046). If the unit or the hold moved again, restate "+
			"FIXTURE_GRANT_CREDITS in the current unit rather than lowering this guard.",
			grant, hold, err)
	}

	// Margin: the seeder documents a hundred holds' worth, so hold it to that
	// rather than to a floor it could quietly fall through.
	//
	// math/big rather than int64 division because both operands are credit
	// amounts, and this repository computes on money with math/big. Quo
	// truncates toward zero, which is the floor-rather-than-round behaviour
	// the ledger uses everywhere else, so a grant worth 99.9 holds reports 99
	// and fails, which is the direction this guard wants to fail in.
	holds := new(big.Int).Quo(big.NewInt(grant), big.NewInt(hold))
	if holds.Cmp(big.NewInt(documentedHoldsPerFixtureGrant)) < 0 {
		t.Errorf("fixture grant covers only %s whole holds (grant=%d hold=%d), want at least %d: "+
			"a spec that sends more than one turn would start failing partway through. "+
			"Re-tune FIXTURE_GRANT_CREDITS in the seeder rather than lowering this bound.",
			holds, grant, hold, documentedHoldsPerFixtureGrant)
	}
}

func TestAnAccountBelowTheFlatChatHoldIsStillRefused(t *testing.T) {
	hold := readDefaultHoldText(t)

	// REFUSE: one credit short is short. Serving this request free is the
	// worse defect (#669), so this direction is not optional, and it is what
	// stops the guard above from being satisfied by simply never refusing.
	err := enforcePolicy(PolicyModeStrict, hold-1, hold)
	if err == nil {
		t.Fatalf("an account one credit below the flat hold (%d) was admitted; the money path must fail closed (D-034)", hold)
	}
	var policyErr *PolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("refusal was %T (%v), want *PolicyError so the caller maps it to a quota refusal rather than a 500", err, err)
	}
}
