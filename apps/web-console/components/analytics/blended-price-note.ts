import type { BlendedNoteKind } from "@/lib/analytics/cache-metrics";
import { formatCredits } from "@/lib/format/credits";

export interface BlendedPriceNoteInput {
  kind: BlendedNoteKind;
  creditsPerMillion: number | null;
  windowUnsupportedNote: string | undefined;
}

/**
 * The subtitle under the BLENDED PRICE / 1M tile.
 *
 * Issue #1408: it used to print the rate as a bare noun phrase with no unit
 * ("161,813,971.499 credits"), directly under a tile whose own value is a
 * rate, on a page whose sibling surface shows an account balance in the same
 * shape. A reader scanning quickly read a nine-figure credits number as
 * money held. The three decimal places came from Intl's default and made it
 * worse, since credits are whole numbers everywhere else in the product.
 */
export function blendedPriceNote(input: BlendedPriceNoteInput): string {
  if (input.kind === "no-tokens") {
    return "No tokens served in this window.";
  }
  if (input.kind === "window-unsupported") {
    return input.windowUnsupportedNote ?? "No comparison for this window.";
  }
  if (input.creditsPerMillion === null) {
    return "No blended price for this window.";
  }
  return (
    `${formatCredits(input.creditsPerMillion)} credits per 1M tokens. ` +
    "Credits spent divided by input plus output tokens. Effective, so " +
    "cache reads are already priced in."
  );
}
