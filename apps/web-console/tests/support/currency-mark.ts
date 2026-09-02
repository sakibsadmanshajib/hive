/**
 * The one currency pattern every no-currency guard in this build matches
 * against.
 *
 * Issue #1694 landed with this regular expression written out five times
 * across two builds, and three of the five differed: two omitted EUR, none
 * carried the rupee sign or a spelled out taka. A guard that varies by copy is
 * a guard that gets fixed in one place and missed in four, so the console's
 * copies are consolidated here.
 *
 * The chat front end keeps its own copy in
 * vendor/open-webui/src/lib/hive/currency-mark.ts, because that build cannot
 * import from this one. The two are kept honest by nothing but this comment,
 * which is acceptable in a way a divergent FORMATTER would not be: a stale
 * pattern in one build weakens a guard there, it does not render a wrong
 * figure to a customer.
 *
 * Deliberately wide. The rule it enforces (owner ruling, .wolf/decisions.md
 * D-070) is that a balance, usage or spend surface renders Hive credits and no
 * currency at all, so any currency is a violation, not only the two this
 * product bills in.
 */
export const CURRENCY_MARK =
  /[$৳€£¥₹]|\b(USD|BDT|EUR|GBP|JPY|INR|TK|TAKA)\b/i;
