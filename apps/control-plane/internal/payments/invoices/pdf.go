package invoices

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"

	"github.com/jung-kurt/gofpdf"
)

// =============================================================================
// Phase 14 — BDT-only PDF renderer.
//
// Uses gofpdf v1.16.2 (pure Go, MIT, no CGO) per locked decision PLAN §1 / 14-AUDIT.md
// Section G. The rendered PDF MUST contain zero USD/FX strings — the
// regulatory tripwire is enforced by pdf_test.go grepping the rendered byte
// stream.
//
// Layout (14-AUDIT.md Section H placeholder):
//
//   HIVE  —  Tax Invoice
//   Workspace: <workspace>
//   Period: YYYY-MM-DD — YYYY-MM-DD
//   Invoice ID: <uuid>
//
//   BIN: TBD (legal review)
//   Mushok-9.4 reference: TBD (legal review)
//
//   Line items table  (model | requests | BDT amount)
//   Total                                       BDT <amount>
//
// All amounts rendered via FormatBDT — emits "BDT <whole>.<paisa>" and never
// any USD-derived string.
// =============================================================================

// gofpdfRenderer is the production PDFRenderer. It is stateless.
type gofpdfRenderer struct{}

// NewGofpdfRenderer returns the production PDF renderer.
func NewGofpdfRenderer() PDFRenderer {
	return &gofpdfRenderer{}
}

// Render produces a BDT-only invoice PDF as a byte slice.
//
// The renderer asserts no USD/FX tokens have leaked into customer-visible
// text. The check runs against the *text* we hand to gofpdf (NOT the raw byte
// stream, which contains opaque PDF object syntax that is invisible to
// readers).
func (r *gofpdfRenderer) Render(inv Invoice, workspaceName string) ([]byte, error) {
	out, _, err := renderInvoice(inv, workspaceName)
	return out, err
}

// renderInvoiceText returns only the customer-visible text renderInvoice draws.
//
// A test seam, and a narrow one: it calls the same function Render does, so a
// figure asserted here is the figure on the page rather than a second
// formatting path that could drift from it.
func renderInvoiceText(inv Invoice, workspaceName string) (string, error) {
	_, text, err := renderInvoice(inv, workspaceName)
	return text, err
}

// renderInvoice draws the document and returns both the PDF bytes and the
// concatenation of every customer-visible string it drew.
func renderInvoice(inv Invoice, workspaceName string) ([]byte, string, error) {
	pdf := gofpdf.New("P", "mm", "Letter", "")
	pdf.SetMargins(15, 18, 15)
	pdf.AddPage()

	// textBuf collects every customer-visible string drawn into the PDF.
	// At the end of Render we assertNoFXLeak over its concatenation.
	var textBuf strings.Builder
	emit := func(txt string) {
		textBuf.WriteString(txt)
		textBuf.WriteByte('\n')
	}

	// Note on numeric literals below: gofpdf's CellFormat signature accepts
	// page-dimension widths/heights as floats (mm coordinates) — these are
	// page geometry, NOT money. All money arithmetic in this file uses
	// *big.Int (see FormatBDT). No float type appears in any monetary code
	// path; the PLAN.md float64 audit grep is satisfied because the only
	// numeric literals here are passed inline to gofpdf and never bind to a
	// named float variable in the invoices package.

	// ---------- Header ----------
	pdf.SetFont("Helvetica", "B", 18)
	pdf.CellFormat(0, 10, "HIVE  --  Usage Statement", "", 1, "L", false, 0, "")
	emit("HIVE  --  Usage Statement")
	pdf.SetFont("Helvetica", "", 11)
	pdf.Ln(2)
	t := fmt.Sprintf("Workspace: %s", sanitize(workspaceName))
	pdf.CellFormat(0, 6, t, "", 1, "L", false, 0, "")
	emit(t)
	t = fmt.Sprintf("Period: %s -- %s",
		inv.PeriodStart.Format("2006-01-02"),
		inv.PeriodEnd.Format("2006-01-02"),
	)
	pdf.CellFormat(0, 6, t, "", 1, "L", false, 0, "")
	emit(t)
	t = fmt.Sprintf("Invoice ID: %s", inv.ID.String())
	pdf.CellFormat(0, 6, t, "", 1, "L", false, 0, "")
	emit(t)
	t = fmt.Sprintf("Generated at: %s", inv.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC"))
	pdf.CellFormat(0, 6, t, "", 1, "L", false, 0, "")
	emit(t)

	// The VAT placeholder block that used to sit here (BIN and a Mushok-9.4
	// reference) is gone. Mushok-9.4 is a VAT return form, and printing a
	// reference to one on a document that raises no charge asserts a taxable
	// supply that this page does not describe: a usage period is a draw down
	// against credits already bought and already taxed at purchase. Those
	// fields belong on the purchase invoice, which this package does not yet
	// produce; they are not lost, they are waiting for the right document.
	pdf.Ln(4)

	// ---------- Line items ----------
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetFillColor(230, 230, 230)
	pdf.CellFormat(100, 8, "Model", "1", 0, "L", true, 0, "")
	emit("Model")
	pdf.CellFormat(30, 8, "Requests", "1", 0, "R", true, 0, "")
	emit("Requests")
	pdf.CellFormat(50, 8, "Hive credits", "1", 1, "R", true, 0, "")
	emit("Hive credits")

	pdf.SetFont("Helvetica", "", 10)
	for _, item := range inv.LineItems {
		modelLabel := sanitize(item.ModelID)
		credits := FormatCredits(item.Credits)
		reqCount := fmt.Sprintf("%d", item.RequestCount)
		pdf.CellFormat(100, 7, modelLabel, "1", 0, "L", false, 0, "")
		pdf.CellFormat(30, 7, reqCount, "1", 0, "R", false, 0, "")
		pdf.CellFormat(50, 7, credits, "1", 1, "R", false, 0, "")
		emit(modelLabel)
		emit(reqCount)
		emit(credits)
	}
	if len(inv.LineItems) == 0 {
		pdf.SetFont("Helvetica", "I", 10)
		pdf.CellFormat(180, 7, "No usage in this period.", "1", 1, "C", false, 0, "")
		emit("No usage in this period.")
	}

	// ---------- Total ----------
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(130, 9, "Total", "1", 0, "R", false, 0, "")
	emit("Total")
	totalCredits := FormatCredits(inv.TotalCredits)
	pdf.CellFormat(50, 9, totalCredits, "1", 1, "R", false, 0, "")
	emit(totalCredits)

	// ---------- Unit and draw-down note ----------
	//
	// Credits are the unit (issue #1681), and this document raises no charge
	// (owner ruling, 2026-09-02). Hive credits are bought up front, so a month
	// of consumption is a draw down against a balance the customer has already
	// paid for; printing a fiat total here would read as a second bill for it.
	//
	// It would also disclose something the customer must not be shown. Credits
	// are sold at a markup, and a subscription grants a credit quantity whose
	// internal value is deliberately not published, so converting consumed
	// credits back into money at the internal peg would publish exactly that
	// figure. No customer-visible surface converts a credit quantity into
	// money; assertNoFiatAmount below enforces it on this page.
	pdf.Ln(3)
	pdf.SetFont("Helvetica", "I", 9)
	note := "Consumption is metered in Hive credits and drawn from the balance already purchased. " +
		"No payment is due for this period."
	pdf.MultiCell(180, 5, note, "", "L", false)
	emit(note)

	// ---------- Tripwires ----------
	text := textBuf.String()
	if err := assertNoFXLeak([]byte(text)); err != nil {
		return nil, "", err
	}
	if err := assertNoFiatAmount(text); err != nil {
		return nil, "", err
	}

	// ---------- Buffer ----------
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, "", fmt.Errorf("invoices: pdf output: %w", err)
	}
	return buf.Bytes(), text, nil
}

// =============================================================================
// Currency formatting — BDT-only.
//
// Subunits are paisa (1 BDT = 100 paisa). Output is the canonical Hive form:
//
//	"BDT 1234.56"
//
// No "$", "USD", "Tk.", "৳" suffix, no FX rate. The leading "BDT" is the only
// currency token that appears in the rendered PDF. Negative amounts are not
// expected; we render zero subunits as "BDT 0.00" for safety.
// =============================================================================

// FormatCredits renders a Hive credit quantity with thousand separators, the
// same way every other credit figure in the console reads (issue #1681).
//
// nil is "--", not "0". An invoice generated between the issue #1648 fix and
// issue #1682's repair never recorded its credit quantity, and printing that
// absence as a zero would tell the customer they consumed nothing in a month
// they were charged for.
func FormatCredits(credits *big.Int) string {
	if credits == nil {
		return "--"
	}
	digits := credits.String()
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var out strings.Builder
	out.WriteString(sign)
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// FormatBDT renders a *big.Int subunit count as "BDT <whole>.<paisa>".
func FormatBDT(subunits *big.Int) string {
	if subunits == nil {
		return "BDT 0.00"
	}
	hundred := big.NewInt(100)
	whole, paisa := new(big.Int).QuoRem(new(big.Int).Abs(subunits), hundred, new(big.Int))
	sign := ""
	if subunits.Sign() < 0 {
		sign = "-"
	}
	return fmt.Sprintf("BDT %s%s.%02d", sign, whole.String(), paisa.Int64())
}

// =============================================================================
// FX-leak guard — defence in depth.
// =============================================================================

// fxTripwireTokens lists every customer-USD substring forbidden in any
// customer-visible PDF text. assertNoFXLeak walks the gofpdf-tracked text
// strings (NOT the raw byte stream — PDF object format may contain "$" in
// stream syntax which is invisible to readers).
//
// The "$" check is intentionally textual-only; sanitize() in pdf.go strips it
// from any user-controlled metadata before it ever reaches the page.
var fxTripwireTokens = []string{
	"$",
	"usd",
	"amount_usd",
	"fx_",
	"price_per_credit_usd",
	"exchange_rate",
	"exchange",
}

// fiatTripwireTokens lists the currency markers that must not appear on a usage
// statement. Owner ruling, 2026-09-02: this document is a prepaid draw down and
// raises no charge, and converting the credit quantity into money at the
// internal peg would disclose the internal value of a subscription's credit
// grant, which is confidential. A fiat figure belongs on a purchase invoice,
// which is a different document this package does not yet produce.
//
// "$" and "usd" are already refused by assertNoFXLeak above; this adds the
// local-currency markers that one deliberately allows.
var fiatTripwireTokens = []string{"bdt", "taka", "\u09f3", "paisa"}

func assertNoFiatAmount(text string) error {
	lower := strings.ToLower(text)
	for _, token := range fiatTripwireTokens {
		if strings.Contains(lower, token) {
			return fmt.Errorf("invoices: usage statement carries the fiat marker %q; a credit quantity must not be rendered as money on a customer surface", token)
		}
	}
	return nil
}

func assertNoFXLeak(raw []byte) error {
	lower := strings.ToLower(string(raw))
	for _, token := range fxTripwireTokens {
		if strings.Contains(lower, token) {
			return fmt.Errorf("invoices: FX leak detected — token %q present in rendered PDF", token)
		}
	}
	return nil
}

// sanitize strips any character whose presence in the PDF body would risk a
// false-positive FX leak (e.g. a workspace named "USD Lab"). We replace
// matching forbidden tokens with an underscore-bracketed placeholder so the
// human reader still sees something meaningful but the FX guard does not
// trip on customer-controlled metadata.
func sanitize(in string) string {
	out := in
	lower := strings.ToLower(out)
	// Both token sets, because a workspace legitimately named "Taka Labs" would
	// otherwise trip assertNoFiatAmount and fail the render for that account
	// alone, which is a fail-closed refusal for entirely the wrong reason.
	for _, token := range append(append([]string{}, fxTripwireTokens...), fiatTripwireTokens...) {
		if token == "$" {
			out = strings.ReplaceAll(out, "$", "[symbol]")
			lower = strings.ToLower(out)
			continue
		}
		idx := strings.Index(lower, token)
		for idx >= 0 {
			out = out[:idx] + "[redacted]" + out[idx+len(token):]
			lower = strings.ToLower(out)
			idx = strings.Index(lower, token)
		}
	}
	return out
}
