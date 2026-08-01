package main

import (
	"strings"
	"testing"
)

// The command must refuse to run against a Hive Enterprise deployment. Without
// this gate it reaches signup.ensurePersonalTenant, which writes
// deployment = 'HIVE_CLOUD', so an operator pointing it at an Enterprise
// database would mint tenants that Enterprise posture forbids and mislabel
// them on the way in. Raised independently by two reviewers on PR #644.
func TestCheckPostureRefusesEnterpriseDeployment(t *testing.T) {
	err := checkPosture("/etc/hive/license.jwt")
	if err == nil {
		t.Fatal("expected a refusal when LICENSE_FILE_PATH is set")
	}
	// The operator has to learn WHY, otherwise a refusal is just a failure.
	for _, want := range []string{"LICENSE_FILE_PATH", "Hive Enterprise", "refuses to run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal message must explain itself, missing %q in:\n%s", want, err)
		}
	}
}

// Cloud posture (no license file) must still run. Asserted so the fix above
// cannot silently disable the backfill entirely, which would leave the
// locked-out accounts from #625 unfixed while looking healthy.
func TestCheckPostureAllowsCloudDeployment(t *testing.T) {
	if err := checkPosture(""); err != nil {
		t.Fatalf("Hive Cloud posture must run the backfill, got refusal: %v", err)
	}
}
