"use client";

import * as React from "react";
import { Search } from "lucide-react";

import type { CatalogModel } from "@/lib/control-plane/client";
import { ModelCatalogTable } from "@/components/catalog/model-catalog-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/cn";

interface ModelCatalogBrowserProps {
  models: CatalogModel[];
}

type SortKey =
  | "name"
  | "input_asc"
  | "input_desc"
  | "output_asc"
  | "output_desc"
  | "cache_read_asc";

const SORT_OPTIONS: ReadonlyArray<{ value: SortKey; label: string }> = [
  { value: "name", label: "Name (A to Z)" },
  { value: "input_asc", label: "Input price, low to high" },
  { value: "input_desc", label: "Input price, high to low" },
  { value: "output_asc", label: "Output price, low to high" },
  { value: "output_desc", label: "Output price, high to low" },
  { value: "cache_read_asc", label: "Cache read price, low to high" },
];

// A model with no per-million rate sorts to the end of every price ordering,
// in BOTH directions. It is not the cheapest model and it is not the most
// expensive one, it is a model whose price this screen does not know, and
// letting an unknown win either end of a price sort is how an evaluator picks
// a model on a number that was never there.
//
// This is a partition rather than a sentinel value: a sentinel such as
// Infinity survives the ascending comparator and then loses the descending
// one, because negating the comparison flips the sentinel to the front. The
// unit test for the descending sort exists because that is exactly what the
// first version of this function did.
function comparePrice(
  a: number | null,
  b: number | null,
  descending: boolean,
): number {
  if (a === null && b === null) return 0;
  if (a === null) return 1;
  if (b === null) return -1;
  return descending ? b - a : a - b;
}

function compareModels(a: CatalogModel, b: CatalogModel, sort: SortKey): number {
  switch (sort) {
    case "input_asc":
      return comparePrice(
        a.pricing.input_price_credits,
        b.pricing.input_price_credits,
        false,
      );
    case "input_desc":
      return comparePrice(
        a.pricing.input_price_credits,
        b.pricing.input_price_credits,
        true,
      );
    case "output_asc":
      return comparePrice(
        a.pricing.output_price_credits,
        b.pricing.output_price_credits,
        false,
      );
    case "output_desc":
      return comparePrice(
        a.pricing.output_price_credits,
        b.pricing.output_price_credits,
        true,
      );
    case "cache_read_asc":
      return comparePrice(
        a.pricing.cache_read_price_credits,
        b.pricing.cache_read_price_credits,
        false,
      );
    default:
      return (a.display_name || a.id).localeCompare(b.display_name || b.id);
  }
}

function matchesSearch(model: CatalogModel, needle: string): boolean {
  if (!needle) return true;
  const haystack = [model.id, model.display_name, model.summary]
    .join(" ")
    .toLowerCase();
  return haystack.includes(needle);
}

/**
 * Search, capability filter and sort over the catalog.
 *
 * The whole catalog is already fetched by the server component above, so this
 * filters the array in memory rather than issuing a request per keystroke.
 * That holds while the catalog is tens of aliases; a catalog in the hundreds
 * wants a server-side query and pagination instead.
 *
 * ponytail: in-memory filter, move to a server-side query if the catalog ever
 * outgrows a single page fetch.
 */
export function ModelCatalogBrowser({ models }: ModelCatalogBrowserProps) {
  const [query, setQuery] = React.useState("");
  const [capability, setCapability] = React.useState("");
  const [sort, setSort] = React.useState<SortKey>("name");

  // Offer only the capabilities this catalog actually carries. A hardcoded
  // list would show filters that match nothing.
  const capabilities = React.useMemo(() => {
    const seen = new Set<string>();
    for (const model of models) {
      for (const badge of model.capability_badges) {
        seen.add(badge);
      }
    }
    return Array.from(seen).sort((a, b) => a.localeCompare(b));
  }, [models]);

  const visible = React.useMemo(() => {
    const needle = query.trim().toLowerCase();
    return [...models]
      .filter(
        (model) =>
          matchesSearch(model, needle) &&
          (capability === "" || model.capability_badges.includes(capability)),
      )
      .sort((a, b) => compareModels(a, b, sort));
  }, [models, query, capability, sort]);

  const selectClass = cn(
    "h-9 rounded-md border border-[var(--color-border)]",
    "bg-[var(--color-surface)] px-2 text-sm text-[var(--color-ink)]",
  );

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-[16rem] flex-1">
          <Search
            size={14}
            aria-hidden="true"
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[var(--color-ink-4)]"
          />
          <label className="sr-only" htmlFor="catalog-search">
            Search models
          </label>
          <Input
            id="catalog-search"
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search models…"
            className="pl-8"
          />
        </div>

        <label className="sr-only" htmlFor="catalog-capability">
          Capability
        </label>
        <select
          id="catalog-capability"
          value={capability}
          onChange={(event) => setCapability(event.target.value)}
          className={selectClass}
        >
          <option value="">All capabilities</option>
          {capabilities.map((badge) => (
            <option key={badge} value={badge}>
              {badge}
            </option>
          ))}
        </select>

        <label className="sr-only" htmlFor="catalog-sort">
          Sort
        </label>
        <select
          id="catalog-sort"
          value={sort}
          onChange={(event) => setSort(event.target.value as SortKey)}
          className={selectClass}
        >
          {SORT_OPTIONS.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      </div>

      <p
        className="text-xs text-[var(--color-ink-3)]"
        data-testid="catalog-result-count"
        aria-live="polite"
      >
        {visible.length === models.length
          ? `${models.length} models`
          : `${visible.length} of ${models.length} models`}
      </p>

      {/* ModelCatalogTable's own empty state says the catalog is empty for
          this workspace, which is a different and alarming claim when the
          catalog is fine and the filter simply matched nothing. */}
      {visible.length === 0 && models.length > 0 ? (
        <EmptyState
          title="No models match these filters"
          description="Clear the search or widen the capability filter to see the rest of the catalog."
        />
      ) : (
        <ModelCatalogTable models={visible} />
      )}
    </div>
  );
}
