import type { CatalogModel } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { formatCredits } from "@/lib/format/credits";

interface ModelCatalogTableProps {
  models: CatalogModel[];
}

// A model priced from actual upstream cost has no per-million rate to show.
// Rendering 0 there would read as "free", so the absence is shown as what it
// is. formatCredits still owns every real number.
//
// The pricing mode decides which kind of absence this is. A null price on a
// variable-price alias is the design; a null price on a fixed one means the
// lookup failed, and calling that "Variable" would present a broken decode as a
// deliberate pricing model on the very screen an admin uses to check pricing.
function formatPrice(credits: number | null, pricingMode: string): string {
  if (credits === null) {
    return pricingMode === "upstream_actual" ? "Variable" : "Unknown";
  }
  return formatCredits(credits);
}

// Cache prices carry a third kind of absence the input and output columns do
// not have. A fixed-price alias that publishes no cache rate is neither broken
// nor variable, it simply has no cache rate to charge, and OpenRouter renders
// exactly that case as a dash in its own `Cache read /M` column. Rendering 0
// instead would say the alias caches for free, which is the expensive kind of
// wrong on a pricing screen. A variable-price alias still reads "Variable",
// because its cache component is variable for the same reason its input is.
function formatCachePrice(credits: number | null, pricingMode: string): string {
  if (credits === null) {
    return pricingMode === "upstream_actual" ? "Variable" : "—";
  }
  return formatCredits(credits);
}

type ToneName = "neutral" | "accent" | "success" | "warning" | "danger";

function capabilityTone(badge: string): ToneName {
  const lowered = badge.toLowerCase();
  if (lowered.includes("chat")) return "accent";
  if (lowered.includes("embed")) return "neutral";
  if (lowered.includes("vision") || lowered.includes("image")) return "success";
  if (lowered.includes("audio") || lowered.includes("voice")) return "warning";
  if (lowered.includes("tool") || lowered.includes("function"))
    return "warning";
  return "neutral";
}

// Lifecycle values come from the database, which constrains the column to
// `('stable', 'preview', 'hidden')`
// (supabase/migrations/20260331_01_model_catalog.sql). "active" was never one
// of them, so every seeded alias (all of which are 'stable') fell through to
// "Unavailable" and the catalog read as an outage.
//
// "active" is still mapped because lib/control-plane/client.ts substitutes it
// when the lifecycle field is absent from a payload; dropping it here would
// reintroduce the same false "Unavailable" for that case.
const LIFECYCLE_BADGES: Record<string, { label: string; tone: ToneName }> = {
  stable: { label: "Available", tone: "success" },
  active: { label: "Available", tone: "success" },
  preview: { label: "Preview", tone: "warning" },
  hidden: { label: "Hidden", tone: "neutral" },
};

export function statusBadge(lifecycle: string): {
  label: string;
  tone: ToneName;
} {
  return (
    LIFECYCLE_BADGES[lifecycle] ?? { label: "Unavailable", tone: "neutral" }
  );
}

export function ModelCatalogTable({ models }: ModelCatalogTableProps) {
  if (models.length === 0) {
    return (
      <EmptyState
        title="No models available"
        description="The model catalog is empty for this workspace. Check back soon."
      />
    );
  }

  const columns: Column<CatalogModel>[] = [
    {
      key: "model",
      header: "Model",
      cell: (row) => (
        <div className="flex flex-col gap-0.5">
          <span className="text-sm font-medium text-[var(--color-ink)]">
            {row.display_name}
          </span>
          <code className="font-mono text-2xs text-[var(--color-ink-3)]">
            {row.id}
          </code>
          {row.summary ? (
            <span className="text-xs text-[var(--color-ink-3)]">
              {row.summary}
            </span>
          ) : null}
        </div>
      ),
    },
    {
      key: "capabilities",
      header: "Capabilities",
      cell: (row) =>
        row.capability_badges.length === 0 ? (
          <span className="text-xs text-[var(--color-ink-3)]">—</span>
        ) : (
          <div className="flex flex-wrap gap-1">
            {row.capability_badges.map((badge) => (
              <Badge key={badge} tone={capabilityTone(badge)}>
                {badge}
              </Badge>
            ))}
          </div>
        ),
    },
    {
      key: "input",
      header: "Input / 1M",
      numeric: true,
      align: "right",
      cell: (row) => formatPrice(row.pricing.input_price_credits, row.pricing.pricing_mode),
    },
    {
      key: "output",
      header: "Output / 1M",
      numeric: true,
      align: "right",
      cell: (row) => formatPrice(row.pricing.output_price_credits, row.pricing.pricing_mode),
    },
    {
      key: "cache_read",
      header: "Cache read / 1M",
      numeric: true,
      align: "right",
      cell: (row) =>
        formatCachePrice(
          row.pricing.cache_read_price_credits,
          row.pricing.pricing_mode,
        ),
    },
    {
      key: "cache_write",
      header: "Cache write / 1M",
      numeric: true,
      align: "right",
      cell: (row) =>
        formatCachePrice(
          row.pricing.cache_write_price_credits,
          row.pricing.pricing_mode,
        ),
    },
    {
      key: "status",
      header: "Status",
      cell: (row) => {
        const { label, tone } = statusBadge(row.lifecycle);
        return <Badge tone={tone}>{label}</Badge>;
      },
    },
  ];

  return (
    <DataTable<CatalogModel>
      rows={models}
      columns={columns}
      rowKey={(row) => row.id}
    />
  );
}
