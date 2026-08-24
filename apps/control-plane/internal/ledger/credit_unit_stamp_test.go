package ledger

import "testing"

// TestStampCreditUnit pins the per-row unit audit trail: metadata written by
// the current binary is stamped with credit_unit=v2-1usd-1e9 unless the
// caller named a unit, a caller-supplied value is preserved, and the
// caller's map is never mutated. This is what makes the migration's
// post-deploy straggler detector exact: any nonzero entry WITHOUT the key
// was written by an unstamping (pre-rescale) binary, i.e. an old-unit
// straggler from the deploy window.
func TestStampCreditUnit(t *testing.T) {
	if got := stampCreditUnit(nil)["credit_unit"]; got != CreditUnitV2 {
		t.Fatalf("nil metadata stamped as %v, want %q", got, CreditUnitV2)
	}

	caller := map[string]any{"credit_unit": "legacy-1usd-100k-credits", "src": "x"}
	stamped := stampCreditUnit(caller)
	if stamped["credit_unit"] != "legacy-1usd-100k-credits" {
		t.Fatalf("caller-supplied credit_unit lost: %+v", stamped)
	}
	if stamped["src"] != "x" {
		t.Fatalf("caller metadata dropped: %+v", stamped)
	}
	if len(caller) != 2 {
		t.Fatalf("caller map mutated: %+v", caller)
	}

	already := map[string]any{"credit_unit": CreditUnitV2}
	if stampCreditUnit(already)["credit_unit"] != CreditUnitV2 {
		t.Fatal("existing v2 stamp replaced")
	}
}
