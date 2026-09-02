package webtools

// The money path for the two web tools (issue #1695).
//
// Both tools cost Hive real money and cost the customer nothing until this
// file existed: web_search is an HTTP call to self hosted SearXNG, and
// web_fetch embeds any page past MaxCallChars. Neither reached a settlement
// path at all, so the handler served provider spend with no hold, no charge
// and no usage row.
//
// Three rules shape everything here, and all three are the repo's existing
// ones rather than new inventions.
//
//  1. The price lives in the CATALOG, never as a literal in Go. A per-call
//     price is a model_aliases row with price_unit 'calls'
//     (supabase/migrations/20260902_01_web_tool_call_pricing.sql), read
//     through the same column and the same charge arithmetic every other
//     non-token modality uses. Issues #617 and #627 are what moved this repo
//     off flat literals; reintroducing one here would undo that.
//  2. Fail closed (D-034). A call that cannot be priced is REFUSED, never
//     served free: a missing alias row, a unit this endpoint does not meter, a
//     non-positive price, a pricing mode with no per-call meaning, a price
//     lookup that failed, and a money path that was never wired all refuse
//     before the backend is touched.
//  3. Hold before, settle after. A failed or errored tool call hands the hold
//     back rather than charging for it, which is also how Anthropic treats an
//     errored search, and every reservation reaches a terminal state exactly
//     once (#616).
//
// CHOKEPOINT. The charge is taken in this package's own two HTTP handlers,
// which are the only entry to either tool: Handler.search and Handler.fetch
// are unexported, set once by NewHandler, and reached only from handleSearch
// and handleFetch. The issue named rag.HTTPEmbedder.EmbedBatch as the other
// candidate, and it is the wrong one for a PER CALL charge: it sees no search
// at all (a Go web_search embeds nothing), it fires a variable number of times
// per fetch, and RAG ingest and query share it, so charging there would price
// this surface by embedding volume and bill three unrelated surfaces on one
// change. The gap EmbedBatch covers, unmetered RAG embeddings, is issue #1644
// and stays open.
//
// IDEMPOTENCY. One HTTP call is one charge, and it is terminal exactly once:
// charged or released, never both, and control-plane's FinalizeReservation is
// itself idempotent, so the retry inside Settlement.Finalize cannot produce a
// second charge. A client that re-POSTs the same arguments is a SECOND call
// that ran a second SearXNG query or fetched the page again, and it is charged
// as one, because this surface carries no client-supplied idempotency key to
// tell a retry from a repeat. What bounds a repeat loop is the per-turn budget
// and TenantCallsPerMinute, which run before the hold is taken.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling"
)

// errNoPricer reports a CatalogPricer built without a routing client. It is a
// wiring mistake, and it refuses rather than returning a zero price that would
// read as free.
var errNoPricer = errors.New("webtools: catalog pricer has no routing client")

// PriceUnitCalls is the model_aliases.price_unit value this package meters:
// credits per million CALLS. Neither tool meters tokens, characters or
// seconds, and a row quoted in any of those has no honest conversion to the
// price of one call, so it is refused rather than converted (D-033).
const PriceUnitCalls = "calls"

// AliasWebSearch and AliasWebFetch are the catalog rows the two tools are
// priced from. They are price carriers, not models: visibility 'internal'
// keeps them out of every model listing and every tenant entitlement verdict,
// and they have no provider_routes row because nothing dispatches to them.
const (
	AliasWebSearch = "hive-web-search"
	AliasWebFetch  = "hive-web-fetch"
)

// AliasPrice is one catalog row's answer, reduced to what a per-call charge
// needs. CreditsPerMillion is model_aliases.output_price_credits, the column
// every non-token unit carries its single price in (the
// model_aliases_single_unit_price CHECK forbids a nonzero input price on such
// a row, so there is exactly one price and no guessing which side applies).
type AliasPrice struct {
	PriceUnit         string
	CreditsPerMillion int64
	PricingMode       string
}

// Pricer resolves a tool alias's catalog price. *CatalogPricer is the
// production implementation; the interface is here because this is where the
// behaviour is needed.
type Pricer interface {
	AliasPrice(ctx context.Context, aliasID string) (AliasPrice, error)
}

// Billing is the money path the two tool routes settle through. All three
// fields are required: a handler holding an incomplete one refuses every call
// rather than serving unbilled traffic, which is the #746 failure mode.
type Billing struct {
	// Accounting and Resolver are the same two dependencies session chat, RAG
	// chat and agent-task submission settle through, so every JWT surface asks
	// one lifecycle the same question.
	Accounting *inference.AccountingClient
	Resolver   sessionbilling.Resolver
	Pricer     Pricer
}

func (b *Billing) wired() bool {
	return b != nil && b.Accounting != nil && b.Resolver != nil && b.Pricer != nil
}

// CatalogPricer reads a tool alias's price from the control-plane catalog.
type CatalogPricer struct {
	routing *inference.RoutingClient
}

// NewCatalogPricer wraps the routing client edge-api already holds.
func NewCatalogPricer(routing *inference.RoutingClient) *CatalogPricer {
	return &CatalogPricer{routing: routing}
}

// AliasPrice implements Pricer against GET /internal/routing/alias-price.
func (p *CatalogPricer) AliasPrice(ctx context.Context, aliasID string) (AliasPrice, error) {
	if p == nil || p.routing == nil {
		return AliasPrice{}, errNoPricer
	}
	result, err := p.routing.AliasPrice(ctx, aliasID)
	if err != nil {
		return AliasPrice{}, err
	}
	return AliasPrice{
		PriceUnit:         result.PriceUnit,
		CreditsPerMillion: result.Pricing.OutputCredits(),
		PricingMode:       result.Pricing.PricingMode,
	}, nil
}

// aliasForTool maps a tool name to the catalog row that prices it.
func aliasForTool(tool string) string {
	if tool == ToolWebFetch {
		return AliasWebFetch
	}
	return AliasWebSearch
}

// creditsForCall converts one call into whole credits at the catalog rate, or
// reports that this row cannot price a call at all.
//
// The arithmetic is metering.ChargeCredits, the single copy in the tree (per
// million units, math/big, round half up, D-031), not a second one that could
// round differently. The floor at one credit mirrors the audio path: work that
// actually happened is never free.
func creditsForCall(price AliasPrice) (int64, bool) {
	if price.PriceUnit != PriceUnitCalls {
		return 0, false
	}
	// A variable-price row settles from the cost an upstream reports for a
	// generation. A tool call has no such report, so there would be nothing to
	// settle against and the hold would become the charge by accident. An empty
	// mode is fixed, matching SelectRoutePricing's own reading, so a
	// control-plane that predates the column does not become variable-priced.
	if price.PricingMode != "" && price.PricingMode != inference.PricingModeFixed {
		return 0, false
	}
	if price.CreditsPerMillion <= 0 {
		return 0, false
	}
	credits := metering.ChargeCredits(metering.UnitCharge{
		Quantity:          1,
		CreditsPerMillion: price.CreditsPerMillion,
	})
	if credits < 1 {
		credits = 1
	}
	return credits, true
}

// toolCharge is one tool call's live accounting state. A nil one, or one whose
// settlement is nil, is the non-billable Enterprise posture (D-027): every
// method is a no-op, so the caller does not branch on posture.
type toolCharge struct {
	settlement *sessionbilling.Settlement
	credits    int64
	tool       string
}

// commit charges the call. A charge that cannot land hands the hold back
// instead, so the reservation is terminal exactly once either way (#616).
func (c *toolCharge) commit() {
	if c == nil || c.settlement == nil {
		return
	}
	// Zero tokens on every component: this is a per-call charge and the row
	// should say so rather than imply a token count nobody metered.
	// Confirmed, because one call is measured truth rather than an estimate.
	if !c.settlement.Finalize(c.credits, true, 0, 0, 0, 0) {
		c.settlement.Release("finalize_failed")
	}
}

// refund hands the whole hold back for a call that was never delivered.
func (c *toolCharge) refund(reason string) {
	if c == nil || c.settlement == nil {
		return
	}
	c.settlement.Release(reason)
}

// beginCharge prices this call from the catalog and takes the hold, or writes
// the customer's refusal and reports that the call must not run.
//
// It runs AFTER the per-turn and per-tenant budgets, which are in-memory and
// cost nothing, and after the "is this tool wired at all" checks, so a call
// this deployment was never going to serve does not create a reservation.
func (h *Handler) beginCharge(ctx context.Context, w http.ResponseWriter, user *auth.User, tool string) (*toolCharge, bool) {
	if !h.billing.wired() {
		// A gateway that cannot charge must not serve. Reaching here means the
		// handler was constructed without its money path, which is a wiring
		// mistake rather than a customer error.
		slog.Error("web tools money path not wired, refusing call", "tool", tool)
		writeEnvelope(w, http.StatusServiceUnavailable, NewError(CodeBillingUnavailable, msgBillingUnavailable, 0))
		return nil, false
	}

	alias := aliasForTool(tool)
	price, err := h.billing.Pricer.AliasPrice(ctx, alias)
	if err != nil {
		// The price is unknown, not known-absent. Serving anyway is how free
		// traffic happens.
		slog.Error("web tool price lookup failed, refusing call", "err", err, "tool", tool, "alias", alias)
		writeEnvelope(w, http.StatusServiceUnavailable, NewError(CodeBillingUnavailable, msgBillingUnavailable, 0))
		return nil, false
	}
	credits, ok := creditsForCall(price)
	if !ok {
		slog.Error("web tool alias cannot price a call, refusing",
			"tool", tool, "alias", alias, "price_unit", price.PriceUnit,
			"credits_per_million", price.CreditsPerMillion, "pricing_mode", price.PricingMode)
		writeEnvelope(w, http.StatusServiceUnavailable, NewError(CodeBillingUnavailable, msgBillingUnavailable, 0))
		return nil, false
	}

	settlement, refusal := sessionbilling.ReserveCharge(ctx, sessionbilling.Input{
		Accounting: h.billing.Accounting,
		Billing:    h.billing.Resolver,
		TenantID:   user.TenantID,
		Alias:      alias,
		Endpoint:   tool,
		RequestID:  uuid.New(),
		HoldFloor:  credits,
		Surface:    "web tools",
	})
	if refusal != nil {
		writeBillingRefusal(w, refusal.Reason)
		return nil, false
	}
	// A nil settlement with no refusal is the Enterprise posture: no prepaid
	// relationship, so the call is served unheld and unbilled by decision
	// (D-027), not by omission.
	return &toolCharge{settlement: settlement, credits: credits, tool: tool}, true
}

// writeBillingRefusal renders a sessionbilling verdict in this surface's own
// envelope shape, which is what the shim parses.
//
// The three classes stay three, deliberately. "Add credits", "this workspace
// was never set up" and "the accounting seam is down" are different facts with
// different actions behind them, and collapsing them into one code is the
// same defect criterion B9 forbids on the fetch side. None of them names an
// amount, a balance, a provider or an internal service.
func writeBillingRefusal(w http.ResponseWriter, reason string) {
	switch reason {
	case "insufficient_quota":
		writeEnvelope(w, http.StatusPaymentRequired, NewError(CodeInsufficientCredit, msgInsufficientCredit, 0))
	case "no_billing_account":
		writeEnvelope(w, http.StatusForbidden, NewError(CodeBillingNotConfigured, msgBillingNotConfigured, 0))
	default:
		writeEnvelope(w, http.StatusServiceUnavailable, NewError(CodeBillingUnavailable, msgBillingUnavailable, 0))
	}
}
