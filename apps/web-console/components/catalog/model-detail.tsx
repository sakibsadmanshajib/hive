import Link from "next/link";
import type { ReactNode } from "react";

import type { CatalogModel, UsageSummaryRow } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { buttonVariants } from "@/components/ui/button";
import { statusBadge } from "@/components/catalog/model-catalog-table";
import { formatCredits, formatTokens } from "@/lib/format/credits";
import {
  formatCachePrice,
  formatInOutPrice,
  formatModelPrice,
} from "@/lib/format/model-pricing";
import { cn } from "@/lib/cn";

export interface ModelDetailProps {
  model: CatalogModel;
  /**
   * This account's own usage of this alias over `usageWindowLabel`, or null
   * when the account has sent no requests to it in that window. Distinct from
   * `usageUnavailable`: no rows means zero requests, which is a fact worth
   * stating; an unavailable analytics call means we do not know.
   */
  usage: UsageSummaryRow | null;
  usageUnavailable: boolean;
  usageWindowLabel: string;
}

interface PriceRow {
  dimension: string;
  credits: number | null;
  note: string;
  /**
   * Cache dimensions follow the cache absence policy: a missing rate on a
   * fixed alias is "no such rate" (a dash), not a broken lookup. Input and
   * output keep the plain price policy, where the same null reads "Unknown".
   */
  cache?: boolean;
}

// Every figure on this page is a rate per one million metered tokens, the
// same unit the catalog list prints, so the unit is stated once per section
// rather than repeated in every cell.
//
// Both units are shown, and deliberately. Dollars lead because that is the
// figure a customer comparing gateways reads, and the raw credit integer is
// kept alongside it because credits are the unit the ledger actually moves:
// an account reconciling a bill against its usage needs the exact integer,
// which a dollar figure rounded for legibility cannot give back.
const PER_MILLION = "Per 1M tokens";

/**
 * A capability Hive's control plane does not publish per model today. Rendered
 * as a named absence rather than omitted, because an evaluator comparing this
 * page against OpenRouter's needs to see which columns are missing and why. A
 * zero or a dash in these slots would read as a measurement.
 */
function NotPublished({
  title,
  reason,
}: {
  title: string;
  reason: string;
}): ReactNode {
  return (
    <div className="flex flex-col gap-1 rounded-md border border-dashed border-[var(--color-border)] px-4 py-3">
      <span className="text-xs font-medium text-[var(--color-ink-2)]">
        {title}
      </span>
      <span className="text-2xs leading-relaxed text-[var(--color-ink-3)]">
        {reason}
      </span>
    </div>
  );
}

function Tile({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}): ReactNode {
  return (
    <div className="flex flex-col gap-1.5 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-3">
      <span className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
        {label}
      </span>
      <div className="text-sm text-[var(--color-ink)]">{children}</div>
    </div>
  );
}

function Stat({
  label,
  value,
}: {
  label: string;
  value: string;
}): ReactNode {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
        {label}
      </span>
      <span className="text-lg tabular-nums text-[var(--color-ink)]">
        {value}
      </span>
    </div>
  );
}

function priceRows(model: CatalogModel): PriceRow[] {
  return [
    {
      dimension: "Input",
      credits: model.pricing.input_price_credits,
      note: "Prompt tokens sent to the model.",
    },
    {
      dimension: "Output",
      credits: model.pricing.output_price_credits,
      note: "Tokens the model generates.",
    },
    {
      dimension: "Cache read",
      credits: model.pricing.cache_read_price_credits,
      note: "Prompt tokens served from an upstream prompt cache.",
      cache: true,
    },
    {
      dimension: "Cache write",
      credits: model.pricing.cache_write_price_credits,
      note: "Prompt tokens written into an upstream prompt cache.",
      cache: true,
    },
  ];
}

export function ModelDetail({
  model,
  usage,
  usageUnavailable,
  usageWindowLabel,
}: ModelDetailProps): ReactNode {
  const status = statusBadge(model.lifecycle);
  const variablePriced = model.pricing.pricing_mode === "upstream_actual";

  const priceColumns: Column<PriceRow>[] = [
    {
      key: "dimension",
      header: "Dimension",
      cell: (row) => (
        <span className="font-medium text-[var(--color-ink)]">
          {row.dimension}
        </span>
      ),
    },
    {
      key: "price",
      header: "Price / 1M",
      numeric: true,
      align: "right",
      cell: (row) =>
        row.cache
          ? formatCachePrice(row.credits, model.pricing.pricing_mode)
          : formatModelPrice(row.credits, model.pricing.pricing_mode),
    },
    {
      key: "credits",
      header: "Credits / 1M",
      numeric: true,
      align: "right",
      cell: (row) => (
        <span className="text-xs text-[var(--color-ink-3)]">
          {row.cache
            ? formatCachePrice(row.credits, model.pricing.pricing_mode, "credits")
            : formatModelPrice(row.credits, model.pricing.pricing_mode, "credits")}
        </span>
      ),
    },
    {
      key: "note",
      header: "What it covers",
      cell: (row) => (
        <span className="text-xs text-[var(--color-ink-3)]">{row.note}</span>
      ),
    },
  ];

  return (
    <div className="flex flex-col gap-8">
      <section aria-label="Model identity" className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <code className="rounded bg-[var(--color-surface-inset)] px-2 py-1 font-mono text-xs text-[var(--color-ink-2)]">
            {model.id}
          </code>
          <Badge tone={status.tone}>{status.label}</Badge>
          {model.capability_badges.map((badge) => (
            <Badge key={badge} tone="outline">
              {badge}
            </Badge>
          ))}
        </div>

        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Tile label="In / out price">
            <span className="tabular-nums">
              {formatInOutPrice(model.pricing)}
            </span>
            <span className="block text-2xs text-[var(--color-ink-3)]">
              {formatInOutPrice(model.pricing, "credits")} credits,{" "}
              {PER_MILLION.toLowerCase()}
            </span>
          </Tile>
          <Tile label="Cache read / write">
            <span className="tabular-nums">
              {formatCachePrice(
                model.pricing.cache_read_price_credits,
                model.pricing.pricing_mode,
              )}
              {" / "}
              {formatCachePrice(
                model.pricing.cache_write_price_credits,
                model.pricing.pricing_mode,
              )}
            </span>
            <span className="block text-2xs text-[var(--color-ink-3)]">
              {formatCachePrice(
                model.pricing.cache_read_price_credits,
                model.pricing.pricing_mode,
                "credits",
              )}
              {" / "}
              {formatCachePrice(
                model.pricing.cache_write_price_credits,
                model.pricing.pricing_mode,
                "credits",
              )}{" "}
              credits, {PER_MILLION.toLowerCase()}
            </span>
          </Tile>
          <Tile label="Context">
            <span className="text-[var(--color-ink-3)]">Not published</span>
            <span className="block text-2xs text-[var(--color-ink-3)]">
              The catalog carries no context window for this alias.
            </span>
          </Tile>
          <Tile label="Pricing mode">
            <span>{variablePriced ? "Variable" : "Fixed"}</span>
            <span className="block text-2xs text-[var(--color-ink-3)]">
              {variablePriced
                ? "Charged from the cost the upstream reports per request."
                : "The published rates above are what you are charged."}
            </span>
          </Tile>
        </div>
      </section>

      <Card>
        <CardHeader>
          <CardTitle>Pricing</CardTitle>
          <CardDescription>
            {variablePriced
              ? "This alias is priced from the actual cost of each generation, so it publishes no per-million rate. The charge is derived per request, not from a table."
              : "Every rate Hive charges for this model, per one million metered tokens, in US dollars and in the credits the ledger moves. A rate of 0 means that dimension is deliberately not charged. Unknown means the catalog holds no rate, which is not the same as free. A dash on a cache row means this alias publishes no cache rate."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <DataTable<PriceRow>
            rows={priceRows(model)}
            columns={priceColumns}
            rowKey={(row) => row.dimension}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Providers and uptime</CardTitle>
          <CardDescription>
            Hive routes each request across the providers configured for this
            alias. Per-provider figures are held in the control plane but are
            not served to the console today, so they are shown as absent rather
            than guessed.
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          <NotPublished
            title="Per-provider pricing"
            reason="Route-level prices exist in the routing snapshot, which no customer-facing endpoint returns."
          />
          <NotPublished
            title="Latency and throughput"
            reason="Per-request timings are recorded, but no per-provider percentile is computed or served."
          />
          <NotPublished
            title="Uptime"
            reason="Route health is tracked for failover decisions only and is not exposed as an availability figure."
          />
          <NotPublished
            title="Benchmarks"
            reason="Hive publishes no independent benchmark scores for catalog models."
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Your usage</CardTitle>
          <CardDescription>
            This workspace&rsquo;s own requests to {model.id} over the{" "}
            {usageWindowLabel}. Platform-wide usage of a model is not published.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          {usageUnavailable ? (
            <p className="text-sm text-[var(--color-ink-3)]">
              Usage analytics could not be loaded, so this is unknown rather
              than zero. Reload to try again.
            </p>
          ) : usage === null ? (
            <p className="text-sm text-[var(--color-ink-3)]">
              No requests to this model from this workspace in the{" "}
              {usageWindowLabel}.
            </p>
          ) : (
            <div className="grid gap-5 sm:grid-cols-4">
              <Stat label="Requests" value={formatTokens(usage.request_count)} />
              <Stat
                label="Input tokens"
                value={formatTokens(usage.total_input_tokens)}
              />
              <Stat
                label="Output tokens"
                value={formatTokens(usage.total_output_tokens)}
              />
              <Stat
                label="Credits spent"
                value={formatCredits(usage.total_credits_spent)}
              />
            </div>
          )}
          <div>
            <Link
              href={`/console/logs?model=${encodeURIComponent(model.id)}`}
              className={cn(buttonVariants({ variant: "secondary", size: "sm" }))}
            >
              View requests in the log
            </Link>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
