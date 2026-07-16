/**
 * Pure helpers for the desktop-local task picker (blueprint Step 4.4,
 * issue #311, decision D8: local tasks stay off cloud sync by default).
 * DOM wiring lives in main.ts (untested, mirroring how main.ts's server-URL
 * form wiring is untested while settings.ts's pure validation is) -- this
 * module is the testable half.
 *
 * `create_local_task`/`list_local_tasks` (src-tauri/src/local_tasks.rs) are
 * first-party Tauri IPC commands, not a fetch to the remote agent-console
 * origin: no capability changes were needed (capabilities/default.json's
 * existing "first-party local content only" grant already covers this
 * page), and no session/auth token is involved -- a local task never
 * leaves this machine.
 */

export type TaskPack = "coding-pack" | "knowledge-work-pack";

export const PACKS: ReadonlyArray<{ value: TaskPack; label: string }> = [
  { value: "coding-pack", label: "Coding pack" },
  { value: "knowledge-work-pack", label: "Knowledge-work pack" },
];

export type LocalTaskStatus = "running" | "failed";

/** Mirrors src-tauri/src/local_tasks.rs's LocalTask (serde output). */
export interface LocalTaskView {
  id: string;
  pack: string;
  instructions: string;
  status: LocalTaskStatus;
  created_at: string;
}

/** Body `create_local_task` expects via `invoke`. */
export interface CreateLocalTaskRequest {
  pack: TaskPack;
  instructions: string;
}

export function buildCreateRequest(pack: TaskPack, instructions: string): CreateLocalTaskRequest {
  return { pack, instructions: instructions.trim() };
}

const STATUS_LABELS: Record<LocalTaskStatus, string> = {
  running: "Running",
  failed: "Failed",
};

export function formatStatus(status: LocalTaskStatus): string {
  return STATUS_LABELS[status] ?? status;
}

export function packLabel(pack: string): string {
  return PACKS.find((p) => p.value === pack)?.label ?? pack;
}

/** Type guard for whatever `invoke("list_local_tasks")` returns, so the UI
 * never renders a malformed entry rather than throwing. */
export function isLocalTaskView(value: unknown): value is LocalTaskView {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return (
    typeof v.id === "string" &&
    typeof v.pack === "string" &&
    typeof v.instructions === "string" &&
    (v.status === "running" || v.status === "failed") &&
    typeof v.created_at === "string"
  );
}
