package payments

import (
	"strconv"

	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
)

// CalculateTax determines the tax treatment for a billing profile.
//
// Rules:
//   - Non-BD country: no_tax, TaxRate 0.00
//   - BD + private_company or public_company with non-empty VATNumber: bd_reverse_charge
//   - BD individual, sole_proprietor, or business without VATNumber: bd_vat_15 (15% inclusive)
func CalculateTax(billingProfile profiles.BillingProfile) TaxResult {
	if billingProfile.CountryCode != "BD" {
		return TaxResult{
			TaxRate:      "0.00",
			TaxTreatment: "no_tax",
		}
	}

	isBusiness := billingProfile.LegalEntityType == "private_company" ||
		billingProfile.LegalEntityType == "public_company"

	if isBusiness && billingProfile.VATNumber != "" {
		return TaxResult{
			TaxRate:       "0.00",
			TaxTreatment:  "bd_reverse_charge",
			ReverseCharge: true,
		}
	}

	return TaxResult{
		TaxRate:      "0.15",
		TaxTreatment: "bd_vat_15",
		TaxIncluded:  true,
	}
}

// ApplyTax computes the tax component from amountLocal.
//
// If ReverseCharge or TaxRate is "0.00", returns 0.
// If TaxIncluded, extracts VAT from inclusive price:
//
//	taxAmount = amountLocal - (amountLocal * 100 / (100 + taxRatePercent))
//
// Otherwise adds tax on top:
//
//	taxAmount = amountLocal * taxRatePercent / 100
func ApplyTax(amountLocal int64, tax TaxResult) (taxAmount int64) {
	if tax.ReverseCharge || tax.TaxRate == "0.00" {
		return 0
	}

	rateFloat, err := strconv.ParseFloat(tax.TaxRate, 64)
	if err != nil || rateFloat == 0 {
		return 0
	}
	taxRatePercent := int64(rateFloat * 100) // e.g. 0.15 -> 15

	if tax.TaxIncluded {
		// Extract VAT from an inclusive amount.
		// inclusive = exclusive + vat = exclusive * (1 + rate)
		// exclusive = inclusive * 100 / (100 + ratePercent)
		exclusive := amountLocal * 100 / (100 + taxRatePercent)
		return amountLocal - exclusive
	}

	return amountLocal * taxRatePercent / 100
}
