package payments

import (
	"testing"

	"github.com/hivegpt/hive/apps/control-plane/internal/profiles"
)

func TestCalculateTax_NonBDReturnsNoTax(t *testing.T) {
	bp := profiles.BillingProfile{CountryCode: "US"}
	result := CalculateTax(bp)
	if result.TaxTreatment != "no_tax" {
		t.Errorf("expected no_tax, got %s", result.TaxTreatment)
	}
	if result.TaxRate != "0.00" {
		t.Errorf("expected 0.00, got %s", result.TaxRate)
	}
	if result.ReverseCharge {
		t.Error("expected ReverseCharge=false for non-BD")
	}
}

func TestCalculateTax_BDIndividualReturns15Percent(t *testing.T) {
	bp := profiles.BillingProfile{
		CountryCode:     "BD",
		LegalEntityType: "individual",
		VATNumber:       "",
	}
	result := CalculateTax(bp)
	if result.TaxTreatment != "bd_vat_15" {
		t.Errorf("expected bd_vat_15, got %s", result.TaxTreatment)
	}
	if result.TaxRate != "0.15" {
		t.Errorf("expected 0.15, got %s", result.TaxRate)
	}
	if !result.TaxIncluded {
		t.Error("expected TaxIncluded=true for bd_vat_15")
	}
}

func TestCalculateTax_BDBusinessWithBINReturnsReverseCharge(t *testing.T) {
	bp := profiles.BillingProfile{
		CountryCode:     "BD",
		LegalEntityType: "private_company",
		VATNumber:       "123456789",
	}
	result := CalculateTax(bp)
	if result.TaxTreatment != "bd_reverse_charge" {
		t.Errorf("expected bd_reverse_charge, got %s", result.TaxTreatment)
	}
	if result.TaxRate != "0.00" {
		t.Errorf("expected 0.00, got %s", result.TaxRate)
	}
	if !result.ReverseCharge {
		t.Error("expected ReverseCharge=true")
	}
}

func TestCalculateTax_BDSoleProprietorNoVATReturns15Percent(t *testing.T) {
	bp := profiles.BillingProfile{
		CountryCode:     "BD",
		LegalEntityType: "sole_proprietor",
		VATNumber:       "",
	}
	result := CalculateTax(bp)
	if result.TaxTreatment != "bd_vat_15" {
		t.Errorf("expected bd_vat_15, got %s", result.TaxTreatment)
	}
	if result.TaxRate != "0.15" {
		t.Errorf("expected 0.15, got %s", result.TaxRate)
	}
}

func TestApplyTax_InclusiveVATExtraction(t *testing.T) {
	// amount 11500 paisa (inclusive of 15% VAT)
	// tax = 11500 - (11500 * 100 / 115) = 11500 - 10000 = 1500
	tax := TaxResult{TaxRate: "0.15", TaxTreatment: "bd_vat_15", TaxIncluded: true}
	got := ApplyTax(11500, tax)
	if got != 1500 {
		t.Errorf("expected 1500, got %d", got)
	}
}

func TestApplyTax_ReverseChargeReturnsZero(t *testing.T) {
	tax := TaxResult{TaxRate: "0.00", TaxTreatment: "bd_reverse_charge", ReverseCharge: true}
	got := ApplyTax(11500, tax)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}
