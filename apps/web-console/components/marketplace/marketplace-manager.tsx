"use client";

import * as React from "react";
import { AlertCircle, Trash2 } from "lucide-react";

import { cn } from "@/lib/cn";
import type { MarketplaceEntry } from "@/lib/control-plane/client";

interface MarketplaceManagerProps {
  entries: MarketplaceEntry[];
}

// Minimal JSON value type + type guard for parsing the admin curation form's
// free-form config textarea. Mirrors the JsonValue/isJsonObject convention in
// lib/control-plane/client.ts (this file cannot import that module's runtime
// code — it is a client component and that module calls next/headers — so
// the shape is duplicated rather than shared).
type JsonPrimitive = string | number | boolean | null;
interface JsonObjectValue {
  [key: string]: JsonValue;
}
type JsonArrayValue = JsonValue[];
type JsonValue = JsonPrimitive | JsonObjectValue | JsonArrayValue;

function isJsonObjectValue(value: JsonValue): value is JsonObjectValue {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

type RowStatus = "idle" | "saving" | "error";

const KIND_LABELS: Record<string, string> = {
  mcp_server: "MCP servers",
  rule: "Rules",
  skill: "Skills",
  prompt_template: "Prompt templates",
};

const KIND_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: "mcp_server", label: "MCP server" },
  { value: "rule", label: "Rule" },
  { value: "skill", label: "Skill" },
  { value: "prompt_template", label: "Prompt template" },
];

function formatKind(kind: string): string {
  return KIND_LABELS[kind] ?? kind;
}

interface KindGroup {
  kind: string;
  entries: MarketplaceEntry[];
}

// groupByKind keeps the server's (kind, name) order: entries arrive
// pre-sorted, so first-seen kind order is the display order.
function groupByKind(entries: MarketplaceEntry[]): KindGroup[] {
  const groups: KindGroup[] = [];
  for (const entry of entries) {
    const existing = groups.find((group) => group.kind === entry.kind);
    if (existing) {
      existing.entries = [...existing.entries, entry];
    } else {
      groups.push({ kind: entry.kind, entries: [entry] });
    }
  }
  return groups;
}

export function MarketplaceManager({ entries: initialEntries }: MarketplaceManagerProps) {
  const [entries, setEntries] = React.useState<MarketplaceEntry[]>(initialEntries);
  const [status, setStatus] = React.useState<Record<string, RowStatus>>({});
  const [formError, setFormError] = React.useState<string | null>(null);
  const [submitting, setSubmitting] = React.useState(false);

  async function toggle(entry: MarketplaceEntry): Promise<void> {
    const next = !entry.enabled;

    setEntries((prev) => prev.map((e) => (e.id === entry.id ? { ...e, enabled: next } : e)));
    setStatus((prev) => ({ ...prev, [entry.id]: "saving" }));

    try {
      const response = await fetch(`/api/console/marketplace/${entry.id}/enable`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled: next }),
      });
      if (!response.ok) {
        throw new Error("request failed");
      }
      const result: { id?: string; enabled?: boolean } = await response.json();
      const appliedId = result.id ?? entry.id;
      const applied = typeof result.enabled === "boolean" ? result.enabled : next;
      setEntries((prev) =>
        prev.map((e) => (e.id === appliedId ? { ...e, enabled: applied } : e)),
      );
      setStatus((prev) => ({ ...prev, [entry.id]: "idle" }));
    } catch {
      setEntries((prev) =>
        prev.map((e) => (e.id === entry.id ? { ...e, enabled: entry.enabled } : e)),
      );
      setStatus((prev) => ({ ...prev, [entry.id]: "error" }));
    }
  }

  async function remove(entry: MarketplaceEntry): Promise<void> {
    setStatus((prev) => ({ ...prev, [entry.id]: "saving" }));
    try {
      const response = await fetch(`/api/console/marketplace/${entry.id}`, {
        method: "DELETE",
      });
      if (!response.ok) {
        throw new Error("request failed");
      }
      setEntries((prev) => prev.filter((e) => e.id !== entry.id));
    } catch {
      setStatus((prev) => ({ ...prev, [entry.id]: "error" }));
    }
  }

  async function handleCreate(event: React.FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();
    setFormError(null);

    // Captured synchronously: a native Event's currentTarget is only valid
    // during dispatch, so it must not be read again after an await below.
    const formEl = event.currentTarget;
    const form = new FormData(formEl);
    const kind = String(form.get("kind") ?? "");
    const name = String(form.get("name") ?? "").trim();
    const description = String(form.get("description") ?? "");
    const configText = String(form.get("config") ?? "").trim();

    if (name === "") {
      setFormError("Name is required.");
      return;
    }

    let config: JsonObjectValue = {};
    if (configText !== "") {
      let parsed: JsonValue;
      try {
        parsed = JSON.parse(configText);
      } catch {
        setFormError("Config must be valid JSON (an object).");
        return;
      }
      if (!isJsonObjectValue(parsed)) {
        setFormError("Config must be valid JSON (an object).");
        return;
      }
      config = parsed;
    }

    setSubmitting(true);
    try {
      const response = await fetch("/api/console/marketplace", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ kind, name, description, config }),
      });
      if (!response.ok) {
        const body: { error?: string } = await response.json().catch(() => ({}));
        setFormError(body.error ?? "Could not create the marketplace entry.");
        return;
      }
      const created: MarketplaceEntry = await response.json();
      setEntries((prev) => [...prev, created]);
      formEl.reset();
    } catch {
      setFormError("Could not create the marketplace entry. Please try again.");
    } finally {
      setSubmitting(false);
    }
  }

  const groups = groupByKind(entries);

  return (
    <div className="flex flex-col gap-10">
      <form
        onSubmit={(event) => {
          void handleCreate(event);
        }}
        className="flex flex-col gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-4"
      >
        <h2 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
          Curate a new entry
        </h2>
        <div className="flex flex-wrap gap-3">
          <select name="kind" defaultValue="mcp_server" className="rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm">
            {KIND_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
          <input
            name="name"
            placeholder="Name (e.g. github)"
            className="min-w-[160px] flex-1 rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
          />
        </div>
        <input
          name="description"
          placeholder="Description"
          className="rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 text-sm"
        />
        <textarea
          name="config"
          placeholder={'{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}'}
          rows={3}
          className="rounded border border-[var(--color-border)] bg-transparent px-2 py-1.5 font-mono text-2xs"
        />
        {formError ? (
          <span className="flex items-center gap-1 text-2xs text-[var(--color-danger,#d64545)]">
            <AlertCircle size={12} />
            {formError}
          </span>
        ) : null}
        <button
          type="submit"
          disabled={submitting}
          className={cn(
            "self-start rounded bg-[var(--color-accent)] px-3 py-1.5 text-sm text-white",
            submitting ? "cursor-wait opacity-70" : "cursor-pointer",
          )}
        >
          {submitting ? "Curating…" : "Curate entry"}
        </button>
      </form>

      {groups.map((group) => (
        <section key={group.kind} className="flex flex-col gap-3">
          <h2 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
            {formatKind(group.kind)}
          </h2>
          <ul className="flex flex-col rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] divide-y divide-[var(--color-border)]">
            {group.entries.map((entry) => {
              const rowStatus = status[entry.id] ?? "idle";
              return (
                <li
                  key={entry.id}
                  className="flex items-center justify-between gap-4 px-4 py-3.5"
                >
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <span className="text-sm text-[var(--color-ink)]">{entry.name}</span>
                    <span className="text-2xs text-[var(--color-ink-3)]">
                      {entry.description}
                    </span>
                    {rowStatus === "error" ? (
                      <span className="mt-0.5 flex items-center gap-1 text-2xs text-[var(--color-danger,#d64545)]">
                        <AlertCircle size={12} />
                        Could not save. Try again.
                      </span>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 items-center gap-3">
                    <span
                      className={cn(
                        "text-2xs tabular-nums transition-opacity",
                        rowStatus === "saving"
                          ? "text-[var(--color-ink-3)] opacity-100"
                          : "opacity-0",
                      )}
                      aria-hidden="true"
                    >
                      Saving…
                    </span>
                    <EnableSwitch
                      checked={entry.enabled}
                      saving={rowStatus === "saving"}
                      label={entry.name}
                      onToggle={() => {
                        void toggle(entry);
                      }}
                    />
                    <button
                      type="button"
                      aria-label={`Delete ${entry.name}`}
                      disabled={rowStatus === "saving"}
                      onClick={() => {
                        void remove(entry);
                      }}
                      className="text-[var(--color-ink-3)] hover:text-[var(--color-danger,#d64545)]"
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );
}

interface EnableSwitchProps {
  checked: boolean;
  saving: boolean;
  label: string;
  onToggle: () => void;
}

function EnableSwitch({ checked, saving, label, onToggle }: EnableSwitchProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={`${label}: ${checked ? "enabled" : "disabled"}`}
      disabled={saving}
      onClick={onToggle}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 items-center rounded-full",
        "transition-colors duration-[var(--duration-fast)]",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-surface)]",
        checked
          ? "bg-[var(--color-accent)]"
          : "bg-[var(--color-border-strong,#c9c9c9)]",
        saving ? "cursor-wait opacity-70" : "cursor-pointer",
      )}
    >
      <span
        className={cn(
          "inline-block h-5 w-5 transform rounded-full bg-white shadow-sm",
          "transition-transform duration-[var(--duration-fast)]",
          checked ? "translate-x-[22px]" : "translate-x-0.5",
        )}
      />
    </button>
  );
}
