# Visual proof: console numerals and sign-in mark, 2026-07-27

Captured with Playwright against the developer console running from this
branch (`next dev`, real Supabase project, real control plane on
`localhost:8081`), signed in as the seeded `e2e-verified` fixture account in
the `Acme Labs` workspace. Element crops are at device scale so the numerals
are legible; the sign-in and dark overview shots are full viewport at 1280 by
900.

| File | Shows |
|------|-------|
| `01-signin-mark-light.png` | Sign-in screen. The wordmark already renders the real Hive mark, the enclosure and cell lifted from the chat favicon, not a lettered placeholder. Recorded here because the placeholder was reported as still present; it was removed in the same change that introduced the shared mark. |
| `02-wordmark-light.png` / `03-wordmark-dark.png` | The same wordmark cropped in both palettes. It is drawn in `currentColor`, so it inverts with the theme rather than carrying its own brand colour. |
| `04-credits-before.png` / `05-credits-after.png` | Overview credits card. The headline figure was already a slashed zero in Geist Mono before the change. The `Posted 0 and Reserved 0` line beneath it was set in the sans, where the zero is an unslashed oval; it now runs through the console's `metric` rule and carries the slash. |
| `06-catalog-table-before.png` / `07-catalog-table-after.png` | Model catalog price columns, the densest numeric table in the console. Before, numeric cells were sans with tabular figures, so `0` in a price column read as an uppercase O. After, they use the mono numerals, and every zero is slashed. |
| `08-console-overview-after-dark.png` | Overview in the dark palette after the change, confirming the mark and the slashed numerals both hold when the palette inverts. |
| `10-invoice-total-before.png` / `11-invoice-total-after.png` | Workspace invoice total, the highest-stakes figure in the console because it is real customer money in taka. Before, the total was sans, so all five zeros in a taka amount read as letters. After, the total is mono and every zero is slashed. |
| `09-font-zero-probe.png` | Why the fix routes numbers to the mono rather than adding a CSS feature to the sans. Rows one and two are Geist Sans with and without `font-variant-numeric: slashed-zero`; the declaration has no effect because the family does not ship the feature. Rows three and four are the mono fallback stack, same result. Only Geist Mono draws a slashed zero, and it does so by default. |

Notes so the conditions are on the record:

- The dev overlay indicator, the small dark circle at the lower left of the
  full viewport shots, is the Next.js development badge, not console chrome.
- The `before` catalog table was captured by reverting the one line change in
  `components/ui/data-table.tsx` on the running dev server, photographing the
  table, then restoring it. Both shots are the same page with the same data.
- Balances read zero because the fixture workspace has never been topped up.
  That is what makes it a useful capture for a zero glyph.
- The invoice shots needed a row to photograph, and the fixture workspace has
  never been invoiced. One obviously synthetic invoice, one taka thousand for
  the June 2026 period, was inserted into the fixture workspace, photographed
  before and after, then deleted. The `invoices` table is back to zero rows.
  No real customer or billing data appears in any capture.
- The spend alert threshold cell and the budget alert banner carry the same one
  token change as the invoice total and are not photographed separately. Both
  need a seeded alert to render at all, and the invoice capture already shows
  the glyph change the rule produces.
