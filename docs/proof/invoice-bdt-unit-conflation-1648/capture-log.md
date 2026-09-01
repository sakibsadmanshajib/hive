# Invoice BDT unit conflation (issue #1648): capture log

Date: 2026-09-01. Branch `fix/invoice-bdt-unit-conflation`, PR #1657.

## What the screenshots show

Two renders of `/console/billing/invoices` for the same workspace and the same
ledger period, differing only in the code that turned the ledger into the
number on screen.

- `shot-before.png`: **৳5,246,533.38**. That is the credit sum, 524,653,338,
  read as a paisa count. It is the figure the live console displayed on
  console-hive.scubed.co on 2026-09-01 for the 2026-08-01 to 2026-09-01 period,
  reproduced here from the same arithmetic.
- `shot-after.png`: **৳64.60**. The same 524,653,338 credits converted at the
  credit unit (1,000,000,000 credits to the USD, so 0.5247 USD) and then at
  123.13 BDT per USD.

The console's own analytics page reported $0.525 of spend for that traffic, so
the after figure is the one that agrees with the rest of the product.

## Method

The number under test is produced by the Go control-plane, not by the console,
so the backend half of this capture is real rather than mocked:

1. A throwaway Postgres (`pgvector/pgvector:pg17`) was bootstrapped with
   `scripts/ci-throwaway-db.sh`, which applied all 125 migrations in the chain
   including this branch's `20260901_01_invoices_usd_bdt_rate.sql`.
2. Ledger rows totalling 524,653,338 credits of `usage_charge` were seeded
   across two model buckets inside the August 2026 window.
3. The real `invoices.Service.GenerateInvoiceForPeriod` ran against the real
   `pgxRepository` with `HIVE_USD_BDT_RATE=123.13`, and the resulting invoice
   was serialised through the real `toInvoiceWire`, the exact JSON the console
   receives. It reported `raw credits=524653338 after subunits=6460
   rate=123.130000`.
4. The "before" payload is the same wire object with the total replaced by the
   raw credit sum read straight out of the database, which is what
   `origin/main`'s `AggregateByModel` stores.
5. Both payloads were fed to the real `/console/billing/invoices` page
   component tree (`app/console/billing/invoices/page.tsx`, `InvoiceRow`,
   `formatTakaSubunits`, `ConsoleShell`), rendered with
   `react-dom/server`'s `renderToStaticMarkup`, mocked only at
   `@/lib/control-plane/client`, the same seam the committed unit tests mock.
6. The HTML was linked against the real compiled Tailwind chunk from
   `docker compose run --no-deps --build web-console npm run build` and
   screenshotted in headless Chrome at 1280x900.

The two harness files (a Go capture test and a vitest render file) were
temporary and are not part of the pull request diff.

## Why this is not a live-stack capture

No full local stack is reachable from this sandbox: this machine's `.env`
carries empty `SUPABASE_URL`, `SUPABASE_DB_URL`, `S3_ENDPOINT` and
`NEXT_PUBLIC_SUPABASE_URL`, so `control-plane` exits at boot on
`storage unavailable`, and the self-hosted data plane on the demo box has no
public hostname by design (`deploy/docker/Caddyfile.supabase` serves
`/rest/v1` and `/storage/v1` on the in-network listener only). That is the
documented state in the repo's own `worktree-compose-stack` skill, and it is
not something this change causes or fixes. The fallback used here is the one
that skill prescribes, with the backend half upgraded from a mock to a real
service against a real migrated database.

## URLs and credentials

No credential appears in either screenshot or in this log. The pages were
opened from local `file://` paths with no query string, and the rendered
workspace and user are fixtures (`Acme`, `owner@acme.invalid`). No session was
minted and no account password was set, reset or rotated.

## Files

- `shot-before.png`, `shot-after.png`, posted to PR #1657 as permanent release
  assets through `scripts/post-pr-visual-proof.sh`, not committed to git.

## Review round, 2026-09-01: which rate the captured figure is

The independent money review asked why the invoice did not use the rate the
customer bought its credits at, which `payments.FXService` records in
`public.fx_snapshots` at checkout. It now does: an invoice resolves the
account's most recent snapshot first, and the platform rate is the fallback for
an account that has never transacted through a BDT rail.

The capture above is unchanged and still accurate. It is the FALLBACK arm: the
seeded account has no FX snapshot, so the figure is the same 524,653,338 credits
at 123.13 BDT per USD, ৳64.60. What a screenshot can show is that the page
renders a converted taka figure rather than a credit count read as paisa, and
that is the same in both arms. The snapshot arm changes only which rate the
conversion runs at, which is arithmetic rather than rendering, and is covered by
`TestGenerateInvoice_PrefersTheAccountsPurchaseRate` (a snapshot of 129.586500
produces ৳129.59 for one USD of credits) and by `TestLatestUSDBDTRate_Live`
against real Postgres.
