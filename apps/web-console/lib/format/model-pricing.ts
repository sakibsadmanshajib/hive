import { formatCreditDigits } from "@/lib/format/credits";

/**
 * Catalog prices are quoted in Hive credits per million metered tokens, and
 * in nothing else.
 *
 * This module used to carry formatUsdFromCredits and render every rate as US
 * dollars, with the model detail page printing the dollar figure and the
 * credit integer for the same rate side by side. That pairing published the
 * credit peg outright: two renderings of one quantity are a conversion table.
 * The owner ruling recorded as .wolf/decisions.md D-070 removed currency from
 * every surface except an invoice, and a published rate a customer compares
 * against their own credit spend is exactly the surface the ruling is about.
 *
 * A rate is a whole number of credits, so there is no precision policy left
 * to get wrong. The old formatter needed one: at two decimals a real cache
 * read rate of 2,982,000 credits per million printed "$0.00", so it carried a
 * per-value significant-digit rule to keep a non-zero price from reading as
 * free. An integer count cannot round to zero.
 *
 * Digits without the unit word, because every caller states the unit once for
 * a whole column or a whole tile ("Input / 1M credits") rather than repeating
 * it in each cell of a price table.
 */
function renderPrice(credits: number): string {
  return formatCreditDigits(credits);
}

/**
 * Render one catalog price for display. Three outcomes, deliberately distinct,
 * because on a pricing surface they mean three different things:
 *
 *   - A number, zero included, is a published rate. Zero means the dimension is
 *     deliberately not charged (hive-default publishes exactly that for cache
 *     write), so it prints as "0" and never as an absence. A customer
 *     reading zero is reading a decision.
 *   - Null on an `upstream_actual` alias is the design: the price is variable
 *     per request and there genuinely is no per-million rate to publish.
 *   - Null on a `fixed` alias means the lookup came back empty. Calling that
 *     "Variable" would dress a broken decode up as a pricing model on the one
 *     screen a customer opens to check what a model costs.
 */
export function formatModelPrice(
  credits: number | null,
  pricingMode: string,
): string {
  if (credits === null) {
    return pricingMode === "upstream_actual" ? "Variable" : "Unknown";
  }
  return renderPrice(credits);
}

/**
 * Cache prices carry a fourth outcome the input and output dimensions do not.
 * A fixed-price alias that publishes no cache rate is neither broken nor
 * variable, it simply has no cache rate to charge, and the model pages this
 * mirrors render exactly that case as a dash in their own `Cache read /M`
 * column. Rendering zero instead would say the alias caches for free, which is
 * the expensive kind of wrong on a pricing screen. A variable-price alias
 * still reads "Variable", because its cache component is variable for the same
 * reason its input is.
 */
export function formatCachePrice(
  credits: number | null,
  pricingMode: string,
): string {
  if (credits === null) {
    return pricingMode === "upstream_actual" ? "Variable" : "—";
  }
  return renderPrice(credits);
}

/**
 * The combined "in / out" figure shown in the model header tile, mirroring the
 * shape the model pages this parity-matches put in the same position. Both
 * halves go through formatModelPrice, so a variable-price alias reads
 * "Variable / Variable" rather than borrowing a number from the other side.
 */
export function formatInOutPrice(pricing: {
  input_price_credits: number | null;
  output_price_credits: number | null;
  pricing_mode: string;
}): string {
  const input = formatModelPrice(
    pricing.input_price_credits,
    pricing.pricing_mode,
  );
  const output = formatModelPrice(
    pricing.output_price_credits,
    pricing.pricing_mode,
  );
  return input + " / " + output;
}
