import { isCoworkEnabled } from "@/lib/edge-api/gate";
import { TaskConsole } from "@/components/task-console";
import { AppHeader } from "@/components/brand";

export default async function TasksPage() {
  const enabled = await isCoworkEnabled();

  return (
    <>
      <AppHeader />
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        <header className="flex flex-col gap-2 border-b border-[var(--color-border)] pb-6">
          <span className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
            Workspace
          </span>
          <h1 className="text-2xl leading-tight text-[var(--color-ink)]">
            Agent tasks
          </h1>
          <p className="max-w-xl text-sm leading-relaxed text-[var(--color-ink-3)]">
            Start a sandboxed agent run and watch it through to a result. Each
            task runs in its own isolated environment.
          </p>
        </header>
        {enabled ? (
          <TaskConsole />
        ) : (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface)] px-6 py-12 text-center">
            <p className="text-sm font-semibold text-[var(--color-ink)]">
              Agent workspace not enabled
            </p>
            <p className="max-w-sm text-xs leading-relaxed text-[var(--color-ink-3)]">
              This workspace is not enabled for your organization. Contact your
              administrator to turn it on.
            </p>
          </div>
        )}
      </main>
    </>
  );
}
