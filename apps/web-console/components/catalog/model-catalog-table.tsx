import Link from "next/link";
import { ArrowUpRight } from "lucide-react";

import type { CatalogModel } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { chatModelUrl } from "@/lib/chat-link";
import { formatCachePrice, formatModelPrice } from "@/lib/format/model-pricing";

interface ModelCatalogTableProps {
  models: CatalogModel[];
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
          <Link
            href={`/console/catalog/${encodeURIComponent(row.id)}`}
            className="text-sm font-medium text-[var(--color-ink)] underline-offset-4 hover:underline"
          >
            {row.display_name || row.id}
          </Link>
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
      cell: (row) => formatModelPrice(row.pricing.input_price_credits, row.pricing.pricing_mode),
    },
    {
      key: "output",
      header: "Output / 1M",
      numeric: true,
      align: "right",
      cell: (row) => formatModelPrice(row.pricing.output_price_credits, row.pricing.pricing_mode),
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
    {
      key: "try",
      header: "",
      cell: (row) => (
        <a
          href={chatModelUrl(row.id)}
          target="_blank"
          rel="noopener noreferrer"
          title={`Opens Hive Chat with ${row.display_name || row.id} preselected`}
          className="inline-flex items-center gap-1 text-xs font-medium text-[var(--color-accent)] underline-offset-4 hover:underline"
        >
          Try in chat
          <ArrowUpRight size={12} aria-hidden="true" />
        </a>
      ),
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
