import Link from "next/link";

import type { ApiKey } from "@/lib/control-plane/client";
import { cn } from "@/lib/cn";

export interface LogsFilterState {
  window: string | null;
  model: string | null;
  status: string | null;
  key: string | null;
  errors: boolean;
}

interface LogsFiltersProps {
  state: LogsFilterState;
  // Model aliases offered in the select, sourced from the catalog the
  // workspace can already see.
  models: string[];
  // null when the key list could not be read. An empty array is an account
  // with no keys; the select says which of the two it is (issue #494).
  keys: ApiKey[] | null;
}

// Status options mirror the usage event statuses the gateway writes. "All"
// is the empty value.
const STATUS_OPTIONS = [
  "completed",
  "accepted",
  "dispatching",
  "streaming",
  "failed",
  "cancelled",
  "interrupted",
] as const;

const WINDOW_PRESETS = [
  { label: "All", value: "" },
  { label: "1h", value: "1h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
] as const;

function buildLogsUrl(state: LogsFilterState): string {
  const params = new URLSearchParams();
  if (state.window) params.set("window", state.window);
  if (state.model) params.set("model", state.model);
  if (state.status) params.set("status", state.status);
  if (state.key) params.set("key", state.key);
  if (state.errors) params.set("errors", "true");
  const qs = params.toString();
  return `/console/logs${qs ? `?${qs}` : ""}`;
}

const chipClass = cn(
  "inline-flex h-8 items-center rounded-md border px-3 text-xs",
  "transition-colors",
);

export function LogsFilters({ state, models, keys }: LogsFiltersProps) {
  return (
    <div className="flex flex-col gap-3">
      {/* Time range presets */}
      <div className="flex flex-wrap items-center gap-2">
        {WINDOW_PRESETS.map((preset) => {
          const isActive = (state.window ?? "") === preset.value;
          return (
            <Link
              key={preset.label}
              href={buildLogsUrl({
                ...state,
                window: preset.value || null,
              })}
              className={cn(
                chipClass,
                isActive
                  ? "border-[var(--color-border-strong)] bg-[var(--color-surface-inset)] text-[var(--color-ink)]"
                  : "border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-ink-3)] hover:border-[var(--color-border-strong)] hover:text-[var(--color-ink)]"
              )}
            >
              {preset.label}
            </Link>
          );
        })}
        <Link
          href={buildLogsUrl({ ...state, errors: !state.errors })}
          className={cn(
            chipClass,
            state.errors
              ? "border-[var(--color-danger)] bg-[var(--color-danger-soft)] text-[var(--color-danger)]"
              : "border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-ink-3)] hover:border-[var(--color-border-strong)] hover:text-[var(--color-ink)]"
          )}
          aria-pressed={state.errors}
        >
          Errors only
        </Link>
      </div>

      {/* Model / status / key selects. A plain GET form keeps this working
          without client JS; submitting resets to page one by dropping the
          cursor. */}
      <form method="get" action="/console/logs" className="flex flex-wrap items-center gap-2">
        {state.window ? (
          <input type="hidden" name="window" value={state.window} />
        ) : null}
        {state.errors ? (
          <input type="hidden" name="errors" value="true" />
        ) : null}

        <label className="sr-only" htmlFor="logs-model-filter">
          Model
        </label>
        <select
          id="logs-model-filter"
          name="model"
          defaultValue={state.model ?? ""}
          className="h-8 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-xs text-[var(--color-ink)]"
        >
          <option value="">All models</option>
          {models.map((alias) => (
            <option key={alias} value={alias}>
              {alias}
            </option>
          ))}
        </select>

        <label className="sr-only" htmlFor="logs-status-filter">
          Status
        </label>
        <select
          id="logs-status-filter"
          name="status"
          defaultValue={state.status ?? ""}
          className="h-8 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-xs text-[var(--color-ink)]"
        >
          <option value="">Any status</option>
          {STATUS_OPTIONS.map((status) => (
            <option key={status} value={status}>
              {status}
            </option>
          ))}
        </select>

        <label className="sr-only" htmlFor="logs-key-filter">
          API key
        </label>
        <select
          id="logs-key-filter"
          name="key"
          defaultValue={state.key ?? ""}
          className="h-8 rounded-md border border-[var(--color-border)] bg-[var(--color-surface)] px-2 text-xs text-[var(--color-ink)]"
        >
          <option value="">
            {keys === null ? "Keys unavailable" : "All keys"}
          </option>
          {(keys ?? []).map((key) => (
            <option key={key.id} value={key.id}>
              {key.nickname || `…${key.redacted_suffix}`}
            </option>
          ))}
        </select>

        <button
          type="submit"
          className="inline-flex h-8 items-center rounded-md border border-[var(--color-border-strong)] bg-[var(--color-surface-inset)] px-3 text-xs font-medium text-[var(--color-ink)] transition-colors hover:bg-[var(--color-surface)]"
        >
          Apply
        </button>

        {(state.model || state.status || state.key) ? (
          <Link
            href={buildLogsUrl({
              ...state,
              model: null,
              status: null,
              key: null,
            })}
            className="text-xs text-[var(--color-ink-3)] underline-offset-2 hover:text-[var(--color-ink)] hover:underline"
          >
            Clear
          </Link>
        ) : null}
      </form>
    </div>
  );
}
