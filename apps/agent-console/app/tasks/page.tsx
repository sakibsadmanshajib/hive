import { isCoworkEnabled } from "@/lib/edge-api/gate";
import { TaskConsole } from "@/components/task-console";

export default async function TasksPage() {
  const enabled = await isCoworkEnabled();

  return (
    <main className="mx-auto flex min-h-screen max-w-2xl flex-col gap-6 px-6 py-10">
      <h1 className="text-xl font-semibold">Agent workspace</h1>
      {enabled ? (
        <TaskConsole />
      ) : (
        <p className="text-sm text-neutral-600">
          The agent workspace is not enabled for your organization. Contact your
          administrator.
        </p>
      )}
    </main>
  );
}
