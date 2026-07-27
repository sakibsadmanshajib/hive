"use client";

import * as React from "react";

import { createClient } from "@/lib/supabase/browser";
import { buttonClass } from "@/components/ui";
import {
  cancelTask,
  createTask,
  listTasks,
  TERMINAL_STATUSES,
  type AgentTask,
  type TaskPack,
  type TaskStatus,
} from "@/lib/edge-api/tasks";

const POLL_INTERVAL_MS = 3000;

const PACKS: Array<{
  value: TaskPack;
  label: string;
  variant: "accent" | "secondary";
}> = [
  { value: "coding-pack", label: "Coding pack", variant: "accent" },
  {
    value: "knowledge-work-pack",
    label: "Knowledge-work pack",
    variant: "secondary",
  },
];

const PACK_LABELS: Record<TaskPack, string> = {
  "coding-pack": "Coding pack",
  "knowledge-work-pack": "Knowledge-work pack",
};

/*
 * Status is the only thing on this screen that carries colour meaning, so it
 * gets the semantic tokens rather than the flat neutral pill the first cut
 * used -- a failed run and a queued run looked identical at a glance. `dot` is
 * the swatch, `pill` the surrounding chip.
 */
const STATUS_STYLES: Record<TaskStatus, { dot: string; pill: string }> = {
  queued: {
    dot: "bg-[var(--color-ink-4)]",
    pill: "bg-[var(--color-surface-inset)] text-[var(--color-ink-2)]",
  },
  running: {
    dot: "bg-[var(--color-accent)]",
    pill: "bg-[var(--color-accent-soft)] text-[var(--color-accent-ink)]",
  },
  succeeded: {
    dot: "bg-[var(--color-success)]",
    pill: "bg-[var(--color-success-soft)] text-[var(--color-success)]",
  },
  failed: {
    dot: "bg-[var(--color-danger)]",
    pill: "bg-[var(--color-danger-soft)] text-[var(--color-danger)]",
  },
  cancelled: {
    dot: "bg-[var(--color-ink-4)]",
    pill: "bg-[var(--color-surface-inset)] text-[var(--color-ink-3)]",
  },
};

const SECTION_LABEL =
  "text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]";

async function getAccessToken(
  supabase: ReturnType<typeof createClient>,
): Promise<string | null> {
  const {
    data: { session },
  } = await supabase.auth.getSession();
  return session?.access_token ?? null;
}

export function TaskConsole() {
  const supabase = React.useMemo(() => createClient(), []);
  const baseUrl = process.env.NEXT_PUBLIC_EDGE_API_BASE_URL ?? "";

  const [tasks, setTasks] = React.useState<AgentTask[]>([]);
  const [starting, setStarting] = React.useState<TaskPack | null>(null);
  const [error, setError] = React.useState<string | null>(null);

  const refresh = React.useCallback(async () => {
    const token = await getAccessToken(supabase);
    if (!token) {
      setError("Session expired. Sign in again.");
      return;
    }
    try {
      const next = await listTasks(baseUrl, token);
      setTasks(next);
      setError(null);
    } catch {
      setError("Could not load tasks.");
    }
  }, [supabase, baseUrl]);

  React.useEffect(() => {
    void refresh();
    // ponytail: one unconditional poll loop rather than per-task timers or a
    // websocket -- demo-scale task counts, and the sync contract
    // (SYNC_CONTRACT.md) ships no push channel yet. Add SSE against
    // GET /v1/agent/tasks/{id}/events if/when that lands.
    const interval = setInterval(() => {
      void refresh();
    }, POLL_INTERVAL_MS);
    return () => clearInterval(interval);
  }, [refresh]);

  async function handleStart(pack: TaskPack) {
    setStarting(pack);
    setError(null);
    const token = await getAccessToken(supabase);
    if (!token) {
      setError("Session expired. Sign in again.");
      setStarting(null);
      return;
    }
    try {
      const task = await createTask(baseUrl, token, pack);
      setTasks((prev) => [task, ...prev]);
    } catch {
      setError("Could not start task.");
    } finally {
      setStarting(null);
    }
  }

  async function handleCancel(id: string) {
    const token = await getAccessToken(supabase);
    if (!token) {
      setError("Session expired. Sign in again.");
      return;
    }
    try {
      const updated = await cancelTask(baseUrl, token, id);
      setTasks((prev) => prev.map((t) => (t.id === updated.id ? updated : t)));
    } catch {
      setError("Could not cancel task.");
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <section aria-label="Start a new agent task" className="flex flex-col gap-3">
        <h2 className={SECTION_LABEL}>Start a task</h2>
        <div className="flex flex-wrap gap-3">
          {PACKS.map(({ value, label, variant }) => (
            <button
              key={value}
              type="button"
              disabled={starting !== null}
              onClick={() => void handleStart(value)}
              className={buttonClass(variant, "lg")}
            >
              {starting === value ? "Starting…" : `Start ${label.toLowerCase()}`}
            </button>
          ))}
        </div>
      </section>

      {error ? (
        <p
          role="alert"
          className="rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger-soft)] px-4 py-3 text-sm text-[var(--color-danger)]"
        >
          {error}
        </p>
      ) : null}

      <section aria-label="Tasks" className="flex flex-col gap-3">
        <h2 className={SECTION_LABEL}>Tasks</h2>
        {tasks.length === 0 ? (
          <div className="flex flex-col items-center gap-1.5 rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-10 text-center">
            <p className="text-sm font-medium text-[var(--color-ink-2)]">
              No tasks yet.
            </p>
            <p className="text-xs text-[var(--color-ink-3)]">
              Start a pack above and it will appear here with live status.
            </p>
          </div>
        ) : (
          <ul className="flex flex-col divide-y divide-[var(--color-border)] overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-xs)]">
            {tasks.map((task) => {
              const style = STATUS_STYLES[task.status];
              return (
                <li key={task.id} className="flex flex-col gap-2 px-4 py-3.5">
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-sm font-medium text-[var(--color-ink)]">
                      {PACK_LABELS[task.pack]}
                    </span>
                    <span
                      aria-live="polite"
                      className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 font-mono text-2xs font-medium uppercase tracking-wide ${style.pill}`}
                    >
                      <span
                        aria-hidden="true"
                        className={`h-1.5 w-1.5 shrink-0 rounded-full ${style.dot}`}
                      />
                      {task.status}
                    </span>
                  </div>
                  {task.result_summary_ref ? (
                    <p className="font-mono text-xs text-[var(--color-ink-2)]">
                      Result: {task.result_summary_ref}
                    </p>
                  ) : null}
                  {task.error_message ? (
                    <p className="text-xs text-[var(--color-danger)]">
                      {task.error_message}
                    </p>
                  ) : null}
                  {!TERMINAL_STATUSES.has(task.status) ? (
                    <button
                      type="button"
                      onClick={() => void handleCancel(task.id)}
                      className={`${buttonClass("ghost", "sm")} w-fit -ml-3`}
                    >
                      Cancel
                    </button>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
