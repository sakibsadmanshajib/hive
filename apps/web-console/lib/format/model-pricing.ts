import { formatCredits } from "@/lib/format/credits";

/**
 * Render one catalog price for display. Three outcomes, deliberately distinct,
 * because on a pricing surface they mean three different things:
 *
 *   - A number, zero included, is a published rate. Zero means the dimension is
 *     deliberately not charged (hive-default publishes exactly that for cache
 *     write), so it prints as "0" and never as an absence. A customer reading
 *     "0" is reading a decision.
 *   - Null on an `upstream_actual` alias is the design: the price is variable
 *     per request and there genuinely is no per-million rate to publish.
 *   - Null on a `fixed` alias means the lookup came back empty. Calling that
 *     "Variable" would dress a broken decode up as a pricing model on the one
 *     screen a customer opens to check what a model costs.
 *
 * ponytail: components/catalog/model-catalog-table.tsx carries a private twin
 * of this function. It is left alone on purpose while another branch is editing
 * that file; fold it into this module once both have landed.
 */
export function formatModelPrice(
  credits: number | null,
  pricingMode: string,
): string {
  if (credits === null) {
    return pricingMode === "upstream_actual" ? "Variable" : "Unknown";
  }
  return formatCredits(credits);
}

/**
 * The combined "in / out" figure shown in the model header tile, mirroring the
 * shape OpenRouter puts in the same position. Both halves go through
 * formatModelPrice, so a variable-price alias reads "Variable / Variable"
 * rather than borrowing a number from the other side.
 */
export function formatInOutPrice(pricing: {
  input_price_credits: number | null;
  output_price_credits: number | null;
  pricing_mode: string;
}): string {
  const input = formatModelPrice(pricing.input_price_credits, pricing.pricing_mode);
  const output = formatModelPrice(pricing.output_price_credits, pricing.pricing_mode);
  return `${input} / ${output}`;
}
