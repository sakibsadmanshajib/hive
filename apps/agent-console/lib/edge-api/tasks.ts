// Agent task lifecycle client. Browser-safe: called directly from the
// client component with the signed-in user's Supabase access token, the
// same way any direct edge-api caller (SDK, curl) authenticates -- no BFF
// proxy route needed since edge-api is already customer-facing (unlike
// control-plane, which web-console proxies because it's internal-only).
//
// Wire shape and routes re-verified against
// apps/control-plane/internal/agenttask/SYNC_CONTRACT.md and
// apps/edge-api/internal/agenttask/{types,handler}.go. The earlier note here
// claimed create takes only `{"pack": "..."}` and that no free-text prompt
// field existed. That has not been true since the #311 follow-up: both the
// contract and `createTaskRequest` in edge-api's handler.go carry
// `instructions`, the free-text goal the task's conversation starts from,
// forwarded to the engine as the conversation's `initial_message`. It is
// optional on the wire (empty string means no prompt), but this console
// always sends one -- a task with no stated goal is not a task a person can
// express. Status values are queued/running/succeeded/failed/cancelled.

import {
  isJsonObject,
  parseJsonValue,
  readArrayField,
  readObjectField,
  readStringField,
  readResponseText,
  type JsonObject,
} from "./json";

export type TaskPack = "coding-pack" | "knowledge-work-pack";
export type TaskStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";

export interface AgentTask {
  id: string;
  pack: TaskPack;
  instructions: string;
  status: TaskStatus;
  engine_session_ref: string;
  result_summary_ref: string;
  error_message: string;
  created_at: string;
  updated_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export class AgentTaskError extends Error {
  public readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "AgentTaskError";
    this.status = status;
  }
}

const PACKS: ReadonlySet<string> = new Set(["coding-pack", "knowledge-work-pack"]);
const STATUSES: ReadonlySet<string> = new Set([
  "queued",
  "running",
  "succeeded",
  "failed",
  "cancelled",
]);

export function isTaskPack(value: string): value is TaskPack {
  return PACKS.has(value);
}

function isTaskStatus(value: string): value is TaskStatus {
  return STATUSES.has(value);
}

function decodeTask(value: JsonObject): AgentTask | null {
  const id = readStringField(value, "id");
  const pack = readStringField(value, "pack");
  const status = readStringField(value, "status");
  const createdAt = readStringField(value, "created_at");
  const updatedAt = readStringField(value, "updated_at");

  if (!id || !pack || !isTaskPack(pack) || !status || !isTaskStatus(status) || !createdAt || !updatedAt) {
    return null;
  }

  return {
    id,
    pack,
    // Nullable column server-side; the contract promises "" rather than null
    // on read, but decode defensively so an older row cannot crash the list.
    instructions: readStringField(value, "instructions") ?? "",
    status,
    engine_session_ref: readStringField(value, "engine_session_ref") ?? "",
    result_summary_ref: readStringField(value, "result_summary_ref") ?? "",
    error_message: readStringField(value, "error_message") ?? "",
    created_at: createdAt,
    updated_at: updatedAt,
    started_at: readStringField(value, "started_at"),
    finished_at: readStringField(value, "finished_at"),
  };
}

async function throwTaskError(response: Response, fallback: string): Promise<never> {
  const payload = parseJsonValue(await readResponseText(response));
  const errorBody = isJsonObject(payload) ? readObjectField(payload, "error") : null;
  const message = errorBody ? readStringField(errorBody, "message") : null;
  throw new AgentTaskError(response.status, message ?? `${fallback}: ${response.status}`);
}

function authHeaders(accessToken: string): Record<string, string> {
  return {
    Authorization: `Bearer ${accessToken}`,
    "Content-Type": "application/json",
  };
}

export async function listTasks(baseUrl: string, accessToken: string): Promise<AgentTask[]> {
  const response = await fetch(`${baseUrl}/v1/agent/tasks`, {
    headers: authHeaders(accessToken),
    cache: "no-store",
  });
  if (!response.ok) {
    await throwTaskError(response, "Failed to load tasks");
  }

  const payload = parseJsonValue(await readResponseText(response));
  if (!isJsonObject(payload)) {
    throw new Error("Failed to parse tasks response");
  }
  const rawTasks = readArrayField(payload, "tasks") ?? [];
  const tasks: AgentTask[] = [];
  for (const item of rawTasks) {
    if (isJsonObject(item)) {
      const decoded = decodeTask(item);
      if (decoded) {
        tasks.push(decoded);
      }
    }
  }
  return tasks;
}

export async function createTask(
  baseUrl: string,
  accessToken: string,
  pack: TaskPack,
  instructions: string,
): Promise<AgentTask> {
  const response = await fetch(`${baseUrl}/v1/agent/tasks`, {
    method: "POST",
    headers: authHeaders(accessToken),
    body: JSON.stringify({ pack, instructions }),
  });
  if (!response.ok) {
    await throwTaskError(response, "Failed to create task");
  }

  const payload = parseJsonValue(await readResponseText(response));
  const decoded = isJsonObject(payload) ? decodeTask(payload) : null;
  if (!decoded) {
    throw new Error("Failed to parse task response");
  }
  return decoded;
}

export async function getTask(
  baseUrl: string,
  accessToken: string,
  id: string,
): Promise<AgentTask> {
  const response = await fetch(`${baseUrl}/v1/agent/tasks/${encodeURIComponent(id)}`, {
    headers: authHeaders(accessToken),
    cache: "no-store",
  });
  if (!response.ok) {
    await throwTaskError(response, "Failed to load task");
  }

  const payload = parseJsonValue(await readResponseText(response));
  const decoded = isJsonObject(payload) ? decodeTask(payload) : null;
  if (!decoded) {
    throw new Error("Failed to parse task response");
  }
  return decoded;
}

export async function cancelTask(
  baseUrl: string,
  accessToken: string,
  id: string,
): Promise<AgentTask> {
  const response = await fetch(`${baseUrl}/v1/agent/tasks/${encodeURIComponent(id)}/cancel`, {
    method: "POST",
    headers: authHeaders(accessToken),
  });
  if (!response.ok) {
    await throwTaskError(response, "Failed to cancel task");
  }

  const payload = parseJsonValue(await readResponseText(response));
  const decoded = isJsonObject(payload) ? decodeTask(payload) : null;
  if (!decoded) {
    throw new Error("Failed to parse task response");
  }
  return decoded;
}

// TERMINAL_STATUSES: polling and the cancel button both stop once a task
// reaches one of these (matches SYNC_CONTRACT.md's state machine).
export const TERMINAL_STATUSES: ReadonlySet<TaskStatus> = new Set([
  "succeeded",
  "failed",
  "cancelled",
]);

/*
 * Deployment-configuration sentinels.
 *
 * Both strings are `const`s in apps/control-plane/internal/agenttask/
 * service.go (`engineUnavailableMessage`, `engineLaunchFailedMessage`),
 * persisted verbatim into `error_message` when Engine.Launch fails. They are
 * deliberately provider-blind and generic, which also makes them stable
 * enough to key presentation off: a task carrying one of them did not fail
 * because of anything the user wrote, it never started at all. Matching them
 * here is what lets this console explain a configuration state in a human
 * sentence instead of showing the raw string.
 *
 * A drifted string degrades to the plain "failed" treatment, which is still
 * honest, so this is a soft dependency rather than a coupling that can break.
 */
export const ENGINE_UNAVAILABLE_MESSAGE =
  "agent engine is not available on this deployment";
export const ENGINE_LAUNCH_FAILED_MESSAGE =
  "agent engine could not start the task";

/** True when the task failed because the deployment has no agent runtime. */
export function isEngineUnavailable(task: AgentTask): boolean {
  return task.status === "failed" && task.error_message === ENGINE_UNAVAILABLE_MESSAGE;
}

/** True when the engine exists but refused or failed to launch the task. */
export function isEngineLaunchFailure(task: AgentTask): boolean {
  return task.status === "failed" && task.error_message === ENGINE_LAUNCH_FAILED_MESSAGE;
}
