package executor

import (
	"math/big"
	"testing"
)

// Test 5: per-line consumed credits summed via math/big — rounding error zero
// across 100 lines.
func TestSettle_SumsExactlyAcrossManyLines(t *testing.T) {
	consumed := big.NewInt(0)
	for i := 0; i < 100; i++ {
		consumed.Add(consumed, big.NewInt(int64(i+7)))
	}
	// Sum of 7..106 = (7+106)*100/2 = 5650.
	want := int64(5650)
	got, over, err := Settle(SettleInput{ReservedCredits: 100000, ConsumedCredits: consumed})
	if err != nil {
		t.Fatal(err)
	}
	if over {
		t.Fatalf("unexpected overconsumed")
	}
	if got != want {
		t.Fatalf("actual=%d want %d", got, want)
	}
}

// Test 6: idempotent — calling Settle twice with the same inputs returns the
// same result.
func TestSettle_Idempotent(t *testing.T) {
	in := SettleInput{ReservedCredits: 1000, ConsumedCredits: big.NewInt(500)}
	a1, o1, err := Settle(in)
	if err != nil {
		t.Fatal(err)
	}
	a2, o2, err := Settle(in)
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 || o1 != o2 {
		t.Fatalf("not idempotent: %d/%v vs %d/%v", a1, o1, a2, o2)
	}
}

// Test 7: when sum > reserved, settle marks overconsumed=true and caps actual
// at reserved (no negative balance).
func TestSettle_Overconsumed(t *testing.T) {
	in := SettleInput{ReservedCredits: 100, ConsumedCredits: big.NewInt(150)}
	actual, over, err := Settle(in)
	if err != nil {
		t.Fatal(err)
	}
	if !over {
		t.Fatalf("expected overconsumed")
	}
	if actual != 100 {
		t.Fatalf("actual=%d want 100", actual)
	}
}

func TestSettle_ZeroConsumed(t *testing.T) {
	in := SettleInput{ReservedCredits: 1000, ConsumedCredits: big.NewInt(0)}
	actual, over, err := Settle(in)
	if err != nil {
		t.Fatal(err)
	}
	if over {
		t.Fatalf("unexpected over")
	}
	if actual != 0 {
		t.Fatalf("actual=%d want 0", actual)
	}
}

func TestSettle_NegativeRejected(t *testing.T) {
	if _, _, err := Settle(SettleInput{ReservedCredits: -1, ConsumedCredits: big.NewInt(0)}); err == nil {
		t.Fatalf("expected error on negative reserved")
	}
	if _, _, err := Settle(SettleInput{ReservedCredits: 100, ConsumedCredits: big.NewInt(-5)}); err == nil {
		t.Fatalf("expected error on negative consumed")
	}
}

func TestSettle_NilConsumedReturnsZero(t *testing.T) {
	actual, over, err := Settle(SettleInput{ReservedCredits: 100, ConsumedCredits: nil})
	if err != nil {
		t.Fatal(err)
	}
	if actual != 0 || over {
		t.Fatalf("unexpected: %d/%v", actual, over)
	}
}
