# Issues #1682 and #1681, invoice credit units

Captured 2026-09-02 against a throwaway Postgres carrying the full migration
chain, and against the web console's own production build. No credential
appears in any URL or in any screenshot below, so nothing is redacted; every
identifier here is synthetic.

This capture was redone after the owner's 2026-09-02 scope amendment. The data
repair is unchanged. What changed is the display: a usage period is a prepaid
draw down that raises no charge, and pricing the consumed credits at the
internal peg would disclose the internal value of a subscription's credit
grant, which is confidential. So the customer-facing surfaces now state the
credit quantity and no money at all.

## What the screenshots are, exactly

The console images are the real `/console/billing/invoices` page from a
production `next build` of this branch: the real page component, the real
`InvoiceRow`, the real formatter, the real stylesheet. The upstream is stubbed,
which is the one thing that is not live. The stub answers the three endpoints
the page calls (`/api/v1/viewer`, `/api/v1/accounts/current/profile`,
`/api/v1/invoices`) with the wire JSON the control-plane's own handler produces
for these rows, and the rig sets `PROOF_RIG_BYPASS_AUTH=1`, which short-circuits
the Supabase session lookup so the page renders without a GoTrue instance. That
flag exists only in the rig's uncommitted copies of `lib/control-plane/client.ts`,
`middleware.ts` and `app/console/layout.tsx`; it is not in this pull request and
nothing below the auth plumbing was altered.

The PDF image is not stubbed at all: it is the output of
`invoices.NewGofpdfRenderer().Render`, the production renderer, on the repaired
invoice.

## 1. Before the repair, on the amended surface

The August 2026 row still holds its credit count in `total_bdt_subunits` and has
no recorded credit quantity, so the statement has nothing true to show for it.
The wrong taka figure the owner saw is not rendered here, because no
customer-facing surface renders a fiat figure any more.

```
PERIOD                      HIVE CREDITS USED   MODELS    DOWNLOAD
2026-08-01 -> 2026-09-01    —                   1 model   Download PDF
2026-07-01 -> 2026-08-01    —                   1 model   Download PDF
```

Screenshot: `proof-01-console-before.png`.

The figure the owner actually saw, 5,246,533.38 taka, came from the old row
rendered by the old surface. It is evidenced in section 4 below, where the
stored value is read straight out of Postgres, rather than re-staged here: the
column that produced it no longer exists on any build of this branch.

## 2. After the repair, the same page

The credit quantity is on the row and is stated as credits. No money appears.

```
PERIOD                      HIVE CREDITS USED   MODELS    DOWNLOAD
2026-08-01 -> 2026-09-01    524,653,338         1 model   Download PDF
2026-07-01 -> 2026-08-01    —                   1 model   Download PDF
```

Screenshot: `proof-03-console-after.png`.

The July row is the second case on purpose. It was generated after the #1648 fix
and never recorded a credit quantity. It renders as an em dash, not as a zero,
because a month of no consumption and an unrecorded quantity are different
claims and only one of them is true.

## 3. The regenerated document

Rendered by the production renderer on the repaired invoice. Credits only, and
it says in plain words that nothing is owed.

```
HIVE  --  Usage Statement
Workspace: Hive Demo Workspace
Period: 2026-08-01 -- 2026-09-01

Model       Requests   Hive credits
hive-fast   412        524,653,338
                       ------------
Total                  524,653,338

Consumption is metered in Hive credits and drawn from the balance already
purchased. No payment is due for this period.
```

Screenshot: `proof-02-usage-statement-pdf.png`.

## 4. The row read back out of Postgres

Not from the repair job's log. `TestRepairUnconvertedInvoices_Live` seeds the
conflated row with a raw INSERT, seeds the matching ledger entry, runs the
repair, then SELECTs the money columns straight out of `public.invoices` and
reconciles the credit figure against `AggregateByModel`. Against a throwaway
pgvector/pgvector:pg17 carrying all 131 migrations, with the platform rate
pinned to 100 BDT per USD by the package's TestMain:

```
=== RUN   TestRepairUnconvertedInvoices_Live
INFO invoice rate resolved workspace_id=c5c8bda0-55d2-4036-93c2-49758fc74055 rate=100.000000 source=env
INFO invoice repaired invoice_id=f0173b2f-63e9-4cc9-a1f2-e136bc8fe782 workspace_id=c5c8bda0-55d2-4036-93c2-49758fc74055 period_start=2026-08-01 credits=524653338 total_bdt_subunits=5247 rate=100.000000 rate_source=env
INFO invoice repair: pass complete unconverted_seen=1 invoices_repaired=1
--- PASS: TestRepairUnconvertedInvoices_Live
=== RUN   TestUpdateConverted_RefusesAnAlreadyConvertedRow_Live
--- PASS: TestUpdateConverted_RefusesAnAlreadyConvertedRow_Live
=== RUN   TestRepairLeavesConvertedRowsUntouched_Live
--- PASS: TestRepairLeavesConvertedRowsUntouched_Live
```

Before: `total_bdt_subunits=524653338`, `total_credits=NULL`, `usd_bdt_rate=NULL`.
That stored 524,653,338 is the number the console used to divide by one hundred
and print as 5,246,533.38 taka.

After: `total_bdt_subunits=5247`, `total_credits=524653338`,
`usd_bdt_rate=100.000000`, `usd_bdt_rate_source=env`. The 5,247 paisa figure is
52.47 taka, the same 0.52 USD of consumption at the rate the tests pin. The taka
column is now correct and is audit data; it is not sent to a customer.

## 5. The migration through its own applier

```
::group::applying 20260902_01_invoices_credit_columns.sql
ALTER TABLE
ALTER TABLE
COMMENT
COMMENT
::endgroup::
applied 20260902_01_invoices_credit_columns.sql
applied 131 migration(s)
throwaway database ready: 131 of 131 migrations executed
```

`scripts/apply-migrations.sh --check` separately reports
`baseline file OK: applied=61 pending=70 migrations=131`.
