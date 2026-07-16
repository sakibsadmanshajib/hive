// Agent task lifecycle client. Browser-safe: called directly from the
// client component with the signed-in user's Supabase access token, the
// same way any direct edge-api caller (SDK, curl) authenticates -- no BFF
// proxy route needed since edge-api is already customer-facing (unlike
// control-plane, which web-console proxies because it's internal-only).
//
// Wire shape and routes verified against origin/feat/311-task-sync-web
// (#311, PR #329) after that branch pushed mid-build:
// apps/control-plane/internal/agenttask/SYNC_CONTRACT.md and
// apps/edge-api/internal/agenttask/{types,handler}.go. Notably: create only
// takes `{"pack": "..."}`, there is no free-text prompt field on this
// contract yet (Engine.Launch is a documented open seam -- see
// SYNC_CONTRACT.md "Engine seam (known gap)" -- so a submitted task stays
// `queued` until a real Engine lands in a later wave); status values are
// queued/running/succeeded/failed/cancelled, not the pending/completed
// naming this blueprint's prose used.

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
): Promise<AgentTask> {
  const response = await fetch(`${baseUrl}/v1/agent/tasks`, {
    method: "POST",
    headers: authHeaders(accessToken),
    body: JSON.stringify({ pack }),
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
