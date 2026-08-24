import Link from "next/link";
import type { ReactNode } from "react";
import { ExternalLink, ScrollText } from "lucide-react";

// Observability link-out tiles for the analytics page. Grafana runs on the
// deployment box behind the monitoring profile; its externally reachable
// origin is operator-provided via GRAFANA_BASE_URL (server-side runtime env,
// read here in a Server Component so a URL change needs no rebuild). When it
// is unset the dashboards render as an explicit not-configured state instead
// of a link that 404s.
const GRAFANA_BASE_URL = (process.env.GRAFANA_BASE_URL ?? "").replace(/\/+$/, "");

interface Tile {
  title: string;
  description: string;
  href: string | null;
  icon: ReactNode;
}

function tiles(): Tile[] {
  return [
    {
      title: "Request logs",
      description: "Per-request usage browser with filters and CSV export.",
      href: "/console/logs",
      icon: <ScrollText size={16} />,
    },
    {
      title: "Platform overview",
      description: "Live API health, latency and provider status dashboards.",
      href: GRAFANA_BASE_URL === "" ? null : `${GRAFANA_BASE_URL}/d/hive-platform-overview`,
      icon: <ExternalLink size={16} />,
    },
    {
      title: "Rate limits",
      description: "Rejection rates by tier and top rejected keys.",
      href: GRAFANA_BASE_URL === "" ? null : `${GRAFANA_BASE_URL}/d/hive-rate-limit`,
      icon: <ExternalLink size={16} />,
    },
  ];
}

export function ObservabilityTiles() {
  const items = tiles();
  const grafanaConfigured = GRAFANA_BASE_URL !== "";

  return (
    <section aria-label="Observability" className="flex flex-col gap-3">
      <h2 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
        Observability
      </h2>
      <div className="grid gap-3 sm:grid-cols-3">
        {items.map((tile) =>
          tile.href ? (
            <Link
              key={tile.title}
              href={tile.href}
              target={tile.href.startsWith("/") ? undefined : "_blank"}
              rel={tile.href.startsWith("/") ? undefined : "noreferrer"}
              className="group rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-4 transition-colors hover:bg-[var(--color-surface-inset)]"
            >
              <div className="flex flex-col gap-1">
                <span className="flex items-center gap-2 text-sm font-medium text-[var(--color-ink)]">
                  <span className="text-[var(--color-accent)]">{tile.icon}</span>
                  {tile.title}
                </span>
                <span className="text-2xs leading-relaxed text-[var(--color-ink-3)]">
                  {tile.description}
                </span>
              </div>
            </Link>
          ) : (
            <div
              key={tile.title}
              aria-disabled="true"
              className="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-4"
            >
              <div className="flex flex-col gap-1">
                <span className="flex items-center gap-2 text-sm font-medium text-[var(--color-ink-3)]">
                  <span>{tile.icon}</span>
                  {tile.title}
                </span>
                <span className="text-2xs leading-relaxed text-[var(--color-ink-3)]">
                  Not configured on this deployment (set GRAFANA_BASE_URL).
                </span>
              </div>
            </div>
          ),
        )}
      </div>
    </section>
  );
}
