import Link from "next/link";
import { ScrollText } from "lucide-react";

// Observability section for the analytics page: the contextual path from the
// aggregate charts above to per-request detail.
//
// This section used to carry two further tiles linking to Grafana dashboards,
// gated on a GRAFANA_BASE_URL that no compose file, workflow or .env.example in
// this repository ever set, so both rendered a permanent "not available on this
// deployment" state for every account that opened Analytics.
//
// They were removed rather than wired up, and the reason matters more than the
// dead state did. Analytics sits in the nav's build group with no role guard, so
// every member of every tenant renders this section. The Grafana instance this
// repo ships runs with GF_AUTH_ANONYMOUS_ENABLED=true at the Viewer role
// (deploy/docker/docker-compose.yml), which was confirmed live: an
// unauthenticated request to the running instance returns its full dashboard
// listing. Its rate-limit dashboard queries
// `topk(10, sum by (key_id, tier) (rate(rate_limit_exceeded_total[5m])))`, which
// names API key identifiers and is not scoped to the viewer's tenant, and its
// overview dashboard queries `max by (job) (up)` and
// `process_resident_memory_bytes{job=~...}`, which name internal services and
// their memory footprint.
//
// So completing the wiring would have converted a cosmetic dead tile into a live
// unauthenticated cross-tenant link: the shared-instance exposure family of
// issues #947, #948 and #949. Grafana is operator tooling, reached on the
// deployment host, and is deliberately not linked from a customer's console.
// Re-exposing it safely is a separate owner decision needing real authentication
// and a per-tenant answer for the key_id panel, none of which is console code.
//
// observability-tiles.test.tsx guards this: it sets GRAFANA_BASE_URL and asserts
// that no Grafana link appears, so re-wiring these tiles fails a test rather than
// shipping the leak.
export function ObservabilityTiles() {
  return (
    <section aria-label="Observability" className="flex flex-col gap-3">
      <h2 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
        Observability
      </h2>
      <Link
        href="/console/logs"
        className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-4 py-4 transition-colors hover:bg-[var(--color-surface-inset)] sm:max-w-sm"
      >
        <div className="flex flex-col gap-1">
          <span className="flex items-center gap-2 text-sm font-medium text-[var(--color-ink)]">
            <span className="text-[var(--color-accent)]">
              <ScrollText size={16} />
            </span>
            Request logs
          </span>
          <span className="text-2xs leading-relaxed text-[var(--color-ink-3)]">
            Per-request usage browser with filters and CSV export.
          </span>
        </div>
      </Link>
    </section>
  );
}
