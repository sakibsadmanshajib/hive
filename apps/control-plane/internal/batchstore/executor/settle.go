package executor

import (
	"fmt"
	"math/big"
)

// SettleInput aggregates the inputs to the per-batch settlement decision.
// ConsumedCredits is summed from per-line usage; ReservedCredits is the cap
// the customer's reservation locked in at submit time.
type SettleInput struct {
	ReservedCredits int64
	ConsumedCredits *big.Int
}

// Settle returns the actual credits to charge the customer plus an
// overconsumed flag. It uses math/big internally — per CLAUDE.md the
// financial calc rule is to avoid float64 corruption — and clamps actual at
// reserved when sum exceeds the reservation. The clamp is the documented
// safety net for the rare case of a model-upgrade mid-batch; the underlying
// per-line consumed_credits stays in batch_lines for accounting reconciliation.
func Settle(input SettleInput) (actualCredits int64, overconsumed bool, err error) {
	if input.ConsumedCredits == nil {
		return 0, false, nil
	}
	if input.ReservedCredits < 0 {
		return 0, false, fmt.Errorf("settle: negative reserved credits %d", input.ReservedCredits)
	}
	reserved := big.NewInt(input.ReservedCredits)
	consumed := new(big.Int).Set(input.ConsumedCredits)
	if consumed.Sign() < 0 {
		return 0, false, fmt.Errorf("settle: negative consumed credits %s", consumed.String())
	}
	if input.ReservedCredits == 0 && consumed.Sign() == 0 {
		return 0, false, nil
	}
	if consumed.Cmp(reserved) > 0 {
		return input.ReservedCredits, true, nil
	}
	if !consumed.IsInt64() {
		// Defensive: consumed_credits won't realistically exceed math.MaxInt64
		// for a single batch, but if it ever does we fall back to the reserved cap.
		return input.ReservedCredits, true, nil
	}
	return consumed.Int64(), false, nil
}
