import type { CatalogModel } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { formatCredits } from "@/lib/format/credits";

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

function statusBadge(lifecycle: string): { label: string; tone: ToneName } {
  if (lifecycle === "active") {
    return { label: "Available", tone: "success" };
  }
  if (lifecycle === "preview") {
    return { label: "Preview", tone: "warning" };
  }
  return { label: "Unavailable", tone: "neutral" };
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
      cell: (row) => formatCredits(row.pricing.input_price_credits),
    },
    {
      key: "output",
      header: "Output / 1M",
      numeric: true,
      align: "right",
      cell: (row) => formatCredits(row.pricing.output_price_credits),
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
