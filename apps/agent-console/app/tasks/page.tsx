import { headers } from "next/headers";

import { isCoworkEnabled } from "@/lib/edge-api/gate";
import { HIVE_EMBED_HEADER } from "@/lib/embed";
import { TaskConsole } from "@/components/task-console";
import { AppHeader } from "@/components/brand";

export default async function TasksPage() {
  /*
   * The chat shell renders this page as its Agents destination, inside the one
   * sidebar and on the same origin. When it does, this app must not draw a
   * second brand row and a "back to chat" link over the shell's own chrome.
   * The theme half is handled once in the root layout.
   */
  const embedded = (await headers()).get(HIVE_EMBED_HEADER) === "1";
  const enabled = await isCoworkEnabled();

  return (
    <div className="min-h-screen bg-canvas">
      {embedded ? null : <AppHeader />}
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        {/*
          Secondary text is --color-ink-2 throughout, not --color-ink-3. On
          both palettes ink-3 measures roughly 3.6:1 against the surface,
          which fails WCAG AA for body copy at these sizes; ink-2 clears 6:1.
          ink-3 is kept for non-text furniture only.
        */}
        <header className="flex flex-col gap-2 border-b border-[var(--color-border)] pb-6">
          <span className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-2)]">
            Workspace
          </span>
          <h1 className="text-2xl leading-tight text-[var(--color-ink)]">
            Give the agent a task
          </h1>
          <p className="max-w-xl text-sm leading-relaxed text-[var(--color-ink-2)]">
            Describe what you want done. The task runs in its own sandbox, and
            it stays on this page so you can pick it up from any session.
          </p>
        </header>
        {enabled ? (
          <TaskConsole />
        ) : (
          <div className="flex flex-col gap-1.5 rounded-lg border border-[var(--color-border)] border-l-2 border-l-[var(--color-warning)] bg-[var(--color-warning-soft)] px-4 py-3.5">
            <p className="text-sm font-medium text-[var(--color-ink)]">
              The agent workspace is turned off for your organization
            </p>
            <p className="max-w-prose text-xs leading-relaxed text-[var(--color-ink-2)]">
              Nothing is wrong with your account. An administrator can enable
              this workspace for your organization, and it will appear here once
              they do.
            </p>
          </div>
        )}
      </main>
    </div>
  );
}
