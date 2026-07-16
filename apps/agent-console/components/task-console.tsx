"use client";

import * as React from "react";

import { createClient } from "@/lib/supabase/browser";
import {
  cancelTask,
  createTask,
  listTasks,
  TERMINAL_STATUSES,
  type AgentTask,
  type TaskPack,
} from "@/lib/edge-api/tasks";

const POLL_INTERVAL_MS = 3000;

const PACKS: Array<{ value: TaskPack; label: string }> = [
  { value: "coding-pack", label: "Coding pack" },
  { value: "knowledge-work-pack", label: "Knowledge-work pack" },
];

const PACK_LABELS: Record<TaskPack, string> = {
  "coding-pack": "Coding pack",
  "knowledge-work-pack": "Knowledge-work pack",
};

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
        <h2 className="text-sm font-medium text-neutral-600">Start a task</h2>
        <div className="flex gap-3">
          {PACKS.map(({ value, label }) => (
            <button
              key={value}
              type="button"
              disabled={starting !== null}
              onClick={() => void handleStart(value)}
              className="rounded-md bg-neutral-900 px-4 py-2 text-sm font-medium text-white disabled:opacity-60"
            >
              {starting === value ? "Starting…" : `Start ${label.toLowerCase()}`}
            </button>
          ))}
        </div>
      </section>

      {error ? (
        <p role="alert" className="text-sm text-red-600">
          {error}
        </p>
      ) : null}

      <section aria-label="Tasks" className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-neutral-600">Tasks</h2>
        {tasks.length === 0 ? (
          <p className="text-sm text-neutral-500">No tasks yet.</p>
        ) : (
          <ul className="flex flex-col divide-y divide-neutral-200 rounded-md border border-neutral-200">
            {tasks.map((task) => (
              <li key={task.id} className="flex flex-col gap-1 px-4 py-3">
                <div className="flex items-center justify-between gap-3">
                  <span className="text-sm font-medium">{PACK_LABELS[task.pack]}</span>
                  <span
                    aria-live="polite"
                    className="rounded-full bg-neutral-100 px-2 py-0.5 text-xs font-medium uppercase tracking-wide text-neutral-700"
                  >
                    {task.status}
                  </span>
                </div>
                {task.result_summary_ref ? (
                  <p className="text-xs text-neutral-700">Result: {task.result_summary_ref}</p>
                ) : null}
                {task.error_message ? (
                  <p className="text-xs text-red-600">{task.error_message}</p>
                ) : null}
                {!TERMINAL_STATUSES.has(task.status) ? (
                  <button
                    type="button"
                    onClick={() => void handleCancel(task.id)}
                    className="w-fit text-xs font-medium text-neutral-600 underline-offset-2 hover:underline"
                  >
                    Cancel
                  </button>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
