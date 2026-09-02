/**
 * The one currency pattern the chat front end's no-currency guards match
 * against. Twin of apps/web-console/tests/support/currency-mark.ts, which
 * carries the reasoning; the two builds cannot share a module.
 *
 * Nothing imports this at runtime. It lives beside the sources it guards
 * rather than in a test file so that both test files can import it without
 * either one importing the other's suite.
 */
export const CURRENCY_MARK =
  /[$৳€£¥₹]|\b(USD|BDT|EUR|GBP|JPY|INR|TK|TAKA)\b/i;
