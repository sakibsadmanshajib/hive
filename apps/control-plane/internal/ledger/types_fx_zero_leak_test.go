package ledger_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/ledger"
)

// FX-17-02 RED: ledger.InvoiceRow at b87fa24 carries
// `AmountUSD int64 \`json:"amount_usd"\`` (types.go:73). Any customer-facing
// HTTP handler that surfaces this row leaks the USD amount. Task 3 splits a
// wire DTO (or retags `json:"-"`) and turns this subtest GREEN.
func TestInvoiceWireShape_FXZeroLeak(t *testing.T) {
	now := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	row := ledger.InvoiceRow{
		ID:              uuid.New(),
		AccountID:       uuid.New(),
		PaymentIntentID: uuid.New(),
		InvoiceNumber:   "INV-2026-05-0001",
		Status:          "paid",
		Credits:         100_000,
		AmountUSD:       100,
		AmountLocal:     12_500_00,
		LocalCurrency:   "BDT",
		TaxTreatment:    "vat_inclusive",
		Rail:            "bkash",
		LineItems: []map[string]any{
			{"description": "100,000 Hive Credits", "amount_local": 12_500_00},
		},
		CreatedAt: now,
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal InvoiceRow: %v", err)
	}

	bannedKeys := []string{
		"amount_usd",
		"usd_",
		"fx_",
		"price_per_credit_usd",
		"exchange_rate",
	}

	for _, key := range bannedKeys {
		key := key
		t.Run("no_"+key, func(t *testing.T) {
			if bytes.Contains(raw, []byte(key)) {
				t.Errorf("InvoiceRow JSON wire shape contains banned key %q\npayload: %s", key, raw)
			}
		})
	}
}
