"use client";

import * as React from "react";

import { createClient } from "@/lib/supabase/browser";
import { buttonClass, TEXTAREA_CLASS } from "@/components/ui";
import {
  artifactUrl,
  cancelTask,
  createTask,
  IN_FLIGHT_STATUSES,
  isEngineLaunchFailure,
  isEngineUnavailable,
  listTasks,
  TERMINAL_STATUSES,
  type AgentTask,
  type TaskPack,
} from "@/lib/edge-api/tasks";

const POLL_INTERVAL_MS = 3000;

/*
 * Bounds on the poll.
 *
 * A tab left open on a finished list used to issue a request every three
 * seconds forever, and a sustained edge-api outage turned that into a
 * fixed-rate hammer with no end. Each consecutive failure now doubles the
 * wait up to MAX_POLL_INTERVAL_MS, and after MAX_POLL_FAILURES the loop stops
 * and the screen says so, rather than promising a retry that never comes.
 */
const MAX_POLL_INTERVAL_MS = 30_000;
const MAX_POLL_FAILURES = 5;

/*
 * A queued task only moves when something on the far side of the engine seam
 * picks it up. Rows created before that seam was wired sit at `queued`
 * forever, and the first version of this screen rendered them identically to
 * a task queued four seconds ago. Past this threshold the row says plainly
 * that it is not progressing rather than implying imminent work.
 */
const STALE_QUEUE_AFTER_MS = 10 * 60 * 1000;

// edge-api reads the create body through io.LimitReader(r.Body, 64<<10). This
// cap is far below that: it exists to keep the composer honest about what a
// task brief should be, not to guard the wire.
const MAX_INSTRUCTIONS = 4000;

const PACKS: ReadonlyArray<{
  value: TaskPack;
  label: string;
  hint: string;
}> = [
  {
    value: "coding-pack",
    label: "Coding",
    hint: "Reads and edits a codebase, runs commands, and reports what changed.",
  },
  {
    value: "knowledge-work-pack",
    label: "Knowledge work",
    hint: "Researches, reads documents, and writes up an answer.",
  },
];

const PACK_LABELS: Record<TaskPack, string> = {
  "coding-pack": "Coding",
  "knowledge-work-pack": "Knowledge work",
};

const SECTION_LABEL =
  "text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-2)]";

/*
 * Status colour lives in the dot, never in the label.
 *
 * The first cut painted the whole pill label in --color-danger on
 * --color-danger-soft, which measures about 3.5:1 and fails WCAG AA for text
 * this size, in both palettes. Ink on a tinted ground clears 8:1 everywhere,
 * and the word itself ("Blocked", "Running") already carries the meaning, so
 * hue is reinforcement rather than the sole channel -- which is what 1.4.1
 * asks for anyway.
 */
type Tone = "neutral" | "accent" | "success" | "warning" | "danger";

const TONE_STYLES: Record<Tone, { dot: string; pill: string }> = {
  neutral: {
    dot: "bg-[var(--color-ink-3)]",
    pill: "bg-[var(--color-surface-inset)]",
  },
  accent: {
    dot: "bg-[var(--color-accent)]",
    pill: "bg-[var(--color-accent-soft)]",
  },
  success: {
    dot: "bg-[var(--color-success)]",
    pill: "bg-[var(--color-success-soft)]",
  },
  warning: {
    dot: "bg-[var(--color-warning)]",
    pill: "bg-[var(--color-warning-soft)]",
  },
  danger: {
    dot: "bg-[var(--color-danger)]",
    pill: "bg-[var(--color-danger-soft)]",
  },
};

interface TaskView {
  label: string;
  tone: Tone;
  detail: string;
  /** Running is the only state that animates its dot. */
  live: boolean;
}

/** Coarse elapsed label: "4m", "3h", "6d". Deliberately not Intl -- three
 *  buckets is the whole requirement and a locale-aware formatter here would
 *  be more configuration than information. */
function elapsed(iso: string, nowMs: number): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return "";
  }
  const minutes = Math.floor(Math.max(0, nowMs - then) / 60_000);
  if (minutes < 1) return "less than a minute";
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

/*
 * Maps a wire task onto what a person should read.
 *
 * The two engine sentinels are the reason this function exists. Both are
 * deployment configuration, not user error and not really a task failure:
 * nothing ran, nothing was wrong with the request. They get their own
 * "Blocked" label in the warning register so they never sit next to a genuine
 * failure wearing the same red.
 */
function describeTask(task: AgentTask, nowMs: number): TaskView {
  switch (task.status) {
    case "queued": {
      const createdMs = Date.parse(task.created_at);
      const stale =
        !Number.isNaN(createdMs) && nowMs - createdMs > STALE_QUEUE_AFTER_MS;
      return {
        label: "Queued",
        tone: "neutral",
        live: false,
        detail: stale
          ? `Queued for ${elapsed(task.created_at, nowMs)} with nothing picking it up. Cancel it to clear it from the list.`
          : "Waiting for a sandbox to pick it up.",
      };
    }
    case "running":
      return {
        label: "Running",
        tone: "accent",
        live: true,
        detail:
          "Working now. There is no live transcript yet, so this view checks for a result every few seconds.",
      };
    case "succeeded":
      return {
        label: "Done",
        tone: "success",
        live: false,
        detail: task.result_summary_ref
          ? "Finished. The result reference is below."
          : "Finished.",
      };
    case "cancelled":
      return {
        label: "Cancelled",
        tone: "neutral",
        live: false,
        detail: "You stopped this task before it finished.",
      };
    case "unknown":
      /*
       * Not a wire status: the decoder maps anything it does not recognise
       * here (see TaskStatus). Saying so plainly is the honest treatment.
       * The alternative this replaced was worse than blank -- the row was
       * dropped from the list, so a task the user submitted simply vanished.
       */
      return {
        label: "Unknown",
        tone: "neutral",
        live: false,
        detail:
          "This task is in a state this page does not recognise, so there is nothing reliable to say about it yet. It is still recorded against your account. Reload to check it again.",
      };
    case "failed":
    default: {
      if (isEngineUnavailable(task)) {
        return {
          label: "Blocked",
          tone: "warning",
          live: false,
          detail:
            "This deployment has no agent runtime configured, so the task was recorded but never started. Nothing was wrong with what you asked for.",
        };
      }
      if (isEngineLaunchFailure(task)) {
        return {
          label: "Blocked",
          tone: "warning",
          live: false,
          detail:
            "The agent runtime refused to start this task, so nothing ran. Trying again may work; if it keeps happening it is a deployment problem, not your task.",
        };
      }
      return {
        label: "Failed",
        tone: "danger",
        live: false,
        detail: task.error_message || "The task stopped before producing a result.",
      };
    }
  }
}

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
  const [instructions, setInstructions] = React.useState("");
  const [pack, setPack] = React.useState<TaskPack>("coding-pack");
  const [submitting, setSubmitting] = React.useState(false);
  const [invalid, setInvalid] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [announcement, setAnnouncement] = React.useState("");
  // Consecutive failed list loads. Drives both the backoff and the message,
  // so the screen cannot claim it is retrying after it has stopped.
  const [failures, setFailures] = React.useState(0);

  const promptRef = React.useRef<HTMLTextAreaElement>(null);

  // Read once per render rather than per row, so every row in one pass agrees
  // on "now". Recomputed on each poll tick because `tasks` changes identity.
  const nowMs = Date.now();

  const refresh = React.useCallback(async () => {
    const token = await getAccessToken(supabase);
    if (!token) {
      // Not counted as a failure and deliberately not rescheduled: there is
      // no session to retry with, and signing in again reloads this screen.
      setError("Your session expired. Sign in again to see your tasks.");
      return;
    }
    try {
      const next = await listTasks(baseUrl, token);
      setTasks(next);
      setError(null);
      setFailures(0);
    } catch {
      setFailures((n) => n + 1);
    }
  }, [supabase, baseUrl]);

  const givenUp = failures >= MAX_POLL_FAILURES;
  // Only a queued or running task can change without the user doing anything.
  const active = tasks.some((task) => IN_FLIGHT_STATUSES.has(task.status));
  const shouldPoll = failures > 0 ? !givenUp : active;

  React.useEffect(() => {
    void refresh();
  }, [refresh]);

  React.useEffect(() => {
    if (!shouldPoll) {
      return;
    }
    /*
     * ponytail: one self-rescheduling timeout rather than setInterval or a
     * websocket -- demo-scale task counts, the sync contract
     * (SYNC_CONTRACT.md) ships no push channel yet, and the delay is not
     * constant. Add SSE against GET /v1/agent/tasks/{id}/events if/when that
     * lands.
     *
     * Exactly one timer exists per effect run and the cleanup clears it, so
     * a loop that stops and later resumes cannot leave a second one armed.
     * The effect re-runs after each completed poll because a success hands
     * `tasks` a fresh array and a failure moves `failures`.
     */
    const delay = Math.min(POLL_INTERVAL_MS * 2 ** failures, MAX_POLL_INTERVAL_MS);
    const timer = setTimeout(() => {
      void refresh();
    }, delay);
    return () => clearTimeout(timer);
  }, [shouldPoll, failures, tasks, refresh]);

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submitting) {
      return;
    }

    const trimmed = instructions.trim();
    if (!trimmed) {
      // Hand-rolled rather than the `required` attribute: the native bubble
      // cannot be styled and disappears on the next click, which is worse
      // than an inline message the user can re-read.
      setInvalid(true);
      setError(null);
      promptRef.current?.focus();
      return;
    }

    setInvalid(false);
    setSubmitting(true);
    setError(null);
    setAnnouncement("Starting task.");

    const token = await getAccessToken(supabase);
    if (!token) {
      setError("Your session expired. Sign in again to start a task.");
      setSubmitting(false);
      setAnnouncement("");
      return;
    }

    try {
      const task = await createTask(baseUrl, token, pack, trimmed);
      // A create that round-trips is direct evidence the endpoint is back, so
      // the failure count is stale. Without this the poll stays given up: the
      // row the user just created would sit at "Queued" forever under an
      // alert telling them to reload, which the create just disproved.
      setFailures(0);
      setTasks((prev) => [task, ...prev]);
      setInstructions("");
      setAnnouncement(`Task submitted. Status: ${describeTask(task, Date.now()).label}.`);
    } catch {
      setError("Could not start the task. Your text is still here, try again.");
      setAnnouncement("");
    } finally {
      setSubmitting(false);
    }
  }

  function handleKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    // The convention every agent surface shares. Enter alone inserts a
    // newline, because a task brief is prose and often more than one line.
    if ((event.metaKey || event.ctrlKey) && event.key === "Enter") {
      event.preventDefault();
      event.currentTarget.form?.requestSubmit();
    }
  }

  async function handleCancel(id: string) {
    // A polite region only speaks when its text changes, so cancelling a
    // second task would otherwise be silent. Clearing before the await puts a
    // real transition between the two identical sentences. Submitting already
    // gets this for free from its own "Starting task." step.
    setAnnouncement("");
    const token = await getAccessToken(supabase);
    if (!token) {
      setError("Your session expired. Sign in again to cancel this task.");
      return;
    }
    try {
      const updated = await cancelTask(baseUrl, token, id);
      setTasks((prev) => prev.map((t) => (t.id === updated.id ? updated : t)));
      setAnnouncement("Task cancelled.");
    } catch {
      setError("Could not cancel that task.");
    }
  }

  /*
   * The load failure reads off the failure count rather than being written
   * into `error`, so the sentence cannot outlive the retry it promises: when
   * the loop gives up, the copy stops claiming a retry is coming.
   */
  const loadFailure =
    failures === 0
      ? null
      : givenUp
        ? "Could not load your tasks. Reload the page to try again."
        : "Could not load your tasks. Retrying automatically.";
  // A stale list outranks a one-off create or cancel error.
  const alertMessage = loadFailure ?? error;

  /*
   * Current state, not history. `tasks` is newest first, so the newest task
   * is the only one that says anything about how this deployment is
   * configured right now. Asking whether ANY task was ever engine-blocked
   * made the notice permanent: the day the runtime came up on the demo box,
   * a task ran to completion while the banner above it still told the user
   * there was no sandbox to run it in, because tasks from the day before
   * were blocked.
   */
  const engineUnavailable = tasks.length > 0 && isEngineUnavailable(tasks[0]);
  const selectedPack = PACKS.find((p) => p.value === pack) ?? PACKS[0];
  const nearLimit = instructions.length > MAX_INSTRUCTIONS * 0.8;

  return (
    <div className="flex flex-col gap-10">
      {/*
        One polite live region for the whole screen. Per-row aria-live was the
        previous approach and it re-announced every unchanged status on each
        three-second poll.
      */}
      <p aria-live="polite" className="sr-only">
        {announcement}
      </p>

      {engineUnavailable ? <EngineNotice /> : null}

      <form onSubmit={handleSubmit} aria-busy={submitting} className="flex flex-col gap-3">
        <div className="relative overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-sm)]">
          {submitting ? (
            <span
              aria-hidden="true"
              className="hive-progress absolute inset-x-0 top-0 h-0.5 bg-[var(--color-accent)]"
            />
          ) : null}

          <div className="flex flex-col gap-2 p-4">
            <label
              htmlFor="task-instructions"
              className="text-sm font-medium text-[var(--color-ink)]"
            >
              What should the agent do?
            </label>
            <textarea
              id="task-instructions"
              ref={promptRef}
              name="instructions"
              rows={4}
              maxLength={MAX_INSTRUCTIONS}
              value={instructions}
              onChange={(e) => {
                setInstructions(e.target.value);
                if (invalid) setInvalid(false);
              }}
              onKeyDown={handleKeyDown}
              aria-required="true"
              aria-invalid={invalid}
              aria-describedby={invalid ? "task-error task-help" : "task-help"}
              placeholder="Describe the task in your own words"
              className={TEXTAREA_CLASS}
            />
            {invalid ? (
              <p
                id="task-error"
                role="alert"
                /*
                 * Ink on a danger tint, not danger-coloured text. The accent
                 * red measures about 3.8:1 against the surface in light and
                 * 3.5:1 in dark, so as text at this size it fails AA in both.
                 * Hue moves to the border and the ground, exactly as the
                 * status pills do, and the text stays readable.
                 */
                className="rounded-sm border-l-2 border-[var(--color-danger)] bg-[var(--color-danger-soft)] px-2.5 py-1.5 text-xs font-medium text-[var(--color-ink)]"
              >
                Describe the task first. The agent needs a goal to work from.
              </p>
            ) : null}
            {/*
              The instruction lives here, in ink-2, rather than only in the
              placeholder: placeholder grey does not clear AA at this size and
              vanishes the moment someone types.
            */}
            <p id="task-help" className="text-xs leading-relaxed text-[var(--color-ink-2)]">
              Be specific about the goal and what a finished result looks like.
              For example: &ldquo;Audit the payment webhook handlers for
              unvalidated input and list what you find.&rdquo; Press{" "}
              <kbd className="rounded-xs border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-1 py-px font-mono text-2xs">
                Ctrl
              </kbd>{" "}
              +{" "}
              <kbd className="rounded-xs border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-1 py-px font-mono text-2xs">
                Enter
              </kbd>{" "}
              to start.
            </p>
          </div>

          <div className="flex flex-wrap items-end justify-between gap-4 border-t border-[var(--color-border)] bg-[var(--color-surface-2)] px-4 py-3">
            <fieldset className="flex flex-col gap-1.5" aria-describedby="pack-hint">
              <legend className={SECTION_LABEL}>Pack</legend>
              <div className="flex w-fit gap-1 rounded-md bg-[var(--color-surface-inset)] p-1">
                {PACKS.map((option) => (
                  <label key={option.value} className="cursor-pointer">
                    <input
                      type="radio"
                      name="pack"
                      value={option.value}
                      checked={pack === option.value}
                      onChange={() => setPack(option.value)}
                      className="peer sr-only"
                    />
                    <span
                      className={[
                        "block rounded-sm px-2.5 py-1 text-xs font-medium text-[var(--color-ink-2)]",
                        "transition-[background,color] duration-[var(--duration-fast)]",
                        "peer-checked:bg-[var(--color-surface)] peer-checked:text-[var(--color-ink)]",
                        "peer-checked:shadow-[var(--shadow-xs)]",
                        "peer-focus-visible:outline peer-focus-visible:outline-2",
                        "peer-focus-visible:outline-offset-2 peer-focus-visible:outline-[var(--color-accent)]",
                      ].join(" ")}
                    >
                      {option.label}
                    </span>
                  </label>
                ))}
              </div>
              <p id="pack-hint" className="text-xs text-[var(--color-ink-2)]">
                {selectedPack.hint}
              </p>
            </fieldset>

            <div className="flex items-center gap-3">
              {nearLimit ? (
                <span className="font-mono text-2xs text-[var(--color-ink-2)]">
                  {instructions.length}/{MAX_INSTRUCTIONS}
                </span>
              ) : null}
              <button type="submit" disabled={submitting} className={buttonClass("accent", "lg")}>
                {submitting ? "Starting…" : "Start task"}
              </button>
            </div>
          </div>
        </div>
      </form>

      {alertMessage ? (
        <p
          role="alert"
          className="rounded-md border-l-2 border-[var(--color-danger)] bg-[var(--color-danger-soft)] px-4 py-3 text-sm text-[var(--color-ink)]"
        >
          {alertMessage}
        </p>
      ) : null}

      <section aria-labelledby="task-history" className="flex flex-col gap-3">
        <h2 id="task-history" className={SECTION_LABEL}>
          Your tasks
        </h2>
        {tasks.length === 0 ? (
          <div className="flex flex-col items-center gap-1.5 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-12 text-center">
            <p className="text-sm font-medium text-[var(--color-ink)]">
              Nothing submitted yet
            </p>
            <p className="max-w-xs text-xs leading-relaxed text-[var(--color-ink-2)]">
              Tasks you start appear here with their status, and stay here when
              you come back in another session.
            </p>
          </div>
        ) : (
          <ul className="flex flex-col divide-y divide-[var(--color-border)] overflow-hidden rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-xs)]">
            {tasks.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                nowMs={nowMs}
                onCancel={() => void handleCancel(task.id)}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function TaskRow({
  task,
  nowMs,
  onCancel,
}: {
  task: AgentTask;
  nowMs: number;
  onCancel: () => void;
}) {
  const view = describeTask(task, nowMs);
  const tone = TONE_STYLES[view.tone];

  return (
    <li className="flex flex-col gap-2.5 px-4 py-4">
      <div className="flex items-start justify-between gap-4">
        {/*
          The brief the user wrote is the row's headline. The pack is metadata
          under it. The previous version had this exactly backwards: the pack
          name was the only thing shown, so two different tasks were
          indistinguishable.
        */}
        <p className="min-w-0 flex-1 text-sm leading-relaxed text-[var(--color-ink)]">
          {task.instructions || (
            <span className="text-[var(--color-ink-2)] italic">
              No description was recorded for this task.
            </span>
          )}
        </p>
        <span
          className={`inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-2xs font-medium tracking-wide text-[var(--color-ink)] ${tone.pill}`}
        >
          <span
            aria-hidden="true"
            className={`h-1.5 w-1.5 shrink-0 rounded-full ${tone.dot} ${view.live ? "hive-pulse" : ""}`}
          />
          {view.label}
        </span>
      </div>

      <p className="text-xs leading-relaxed text-[var(--color-ink-2)]">{view.detail}</p>

      {task.result_summary_ref ? (
        <p className="font-mono text-xs break-all text-[var(--color-ink-2)]">
          Result:{" "}
          {artifactUrl(task.result_summary_ref) ? (
            <a
              href={artifactUrl(task.result_summary_ref)!}
              target="_blank"
              rel="noopener noreferrer"
              className="underline hover:text-[var(--color-ink)]"
            >
              {task.result_summary_ref}
            </a>
          ) : (
            task.result_summary_ref
          )}
        </p>
      ) : null}

      <div className="flex items-center gap-3 pt-0.5">
        <span className="font-mono text-2xs text-[var(--color-ink-2)]">
          {PACK_LABELS[task.pack]} &middot; {elapsed(task.created_at, nowMs)} ago
        </span>
        {!TERMINAL_STATUSES.has(task.status) ? (
          <button type="button" onClick={onCancel} className={buttonClass("ghost", "sm")}>
            Cancel
          </button>
        ) : null}
      </div>
    </li>
  );
}

/*
 * The configuration state, stated plainly.
 *
 * Warning register, not danger: nothing is broken and the user did nothing
 * wrong. It says what is missing, who can fix it, and what happens meanwhile,
 * which is the whole difference between this and the raw sentinel string the
 * screen used to print into a red row.
 */
function EngineNotice() {
  return (
    <div
      role="status"
      className="flex flex-col gap-1.5 rounded-lg border border-[var(--color-border)] border-l-2 border-l-[var(--color-warning)] bg-[var(--color-warning-soft)] px-4 py-3.5"
    >
      <p className="text-sm font-medium text-[var(--color-ink)]">
        The agent runtime is not configured on this deployment
      </p>
      <p className="max-w-prose text-xs leading-relaxed text-[var(--color-ink-2)]">
        Tasks you submit are saved against your account, but there is no sandbox
        to run them in, so each one is marked blocked as soon as it is
        submitted. An administrator needs to configure the agent runtime for
        this deployment. Everything else on this page works, and anything you
        submit now will be here when it is turned on.
      </p>
    </div>
  );
}
