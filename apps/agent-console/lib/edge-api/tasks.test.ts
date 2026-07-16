import { describe, it, expect, vi, afterEach } from "vitest";

import {
  AgentTaskError,
  cancelTask,
  createTask,
  getTask,
  listTasks,
  TERMINAL_STATUSES,
  isTaskPack,
} from "./tasks";

const BASE_URL = "http://edge-api.test";
const TOKEN = "test-token";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const TASK = {
  id: "11111111-1111-1111-1111-111111111111",
  pack: "coding-pack",
  status: "queued",
  engine_session_ref: "",
  result_summary_ref: "",
  error_message: "",
  created_at: "2026-07-16T00:00:00Z",
  updated_at: "2026-07-16T00:00:00Z",
  started_at: null,
  finished_at: null,
};

describe("edge-api tasks client", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("listTasks sends a Bearer-authorized GET and decodes the tasks array", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ tasks: [TASK] }));
    vi.stubGlobal("fetch", fetchMock);

    const tasks = await listTasks(BASE_URL, TOKEN);

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE_URL}/v1/agent/tasks`,
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: `Bearer ${TOKEN}` }),
      }),
    );
    expect(tasks).toEqual([TASK]);
  });

  it("listTasks drops malformed entries instead of throwing", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ tasks: [TASK, { id: "bad" }] }));
    vi.stubGlobal("fetch", fetchMock);

    const tasks = await listTasks(BASE_URL, TOKEN);
    expect(tasks).toEqual([TASK]);
  });

  it("createTask POSTs only the pack (the contract has no prompt field) and returns the decoded task", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(TASK, 201));
    vi.stubGlobal("fetch", fetchMock);

    const task = await createTask(BASE_URL, TOKEN, "coding-pack");

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE_URL}/v1/agent/tasks`,
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ pack: "coding-pack" }),
      }),
    );
    expect(task).toEqual(TASK);
  });

  it("getTask fetches a single task by id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(TASK));
    vi.stubGlobal("fetch", fetchMock);

    const task = await getTask(BASE_URL, TOKEN, TASK.id);

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE_URL}/v1/agent/tasks/${TASK.id}`,
      expect.anything(),
    );
    expect(task).toEqual(TASK);
  });

  it("cancelTask POSTs to the cancel sub-route", async () => {
    const cancelled = { ...TASK, status: "cancelled" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(cancelled));
    vi.stubGlobal("fetch", fetchMock);

    const task = await cancelTask(BASE_URL, TOKEN, TASK.id);

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE_URL}/v1/agent/tasks/${TASK.id}/cancel`,
      expect.objectContaining({ method: "POST" }),
    );
    expect(task.status).toBe("cancelled");
  });

  it("throws AgentTaskError with the upstream status and nested error.message on failure", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(
        jsonResponse({ error: { code: "INVALID_REQUEST", message: "invalid pack" } }, 400),
      );
    vi.stubGlobal("fetch", fetchMock);

    let caught: unknown;
    try {
      await listTasks(BASE_URL, TOKEN);
    } catch (err) {
      caught = err;
    }
    if (!(caught instanceof AgentTaskError)) {
      throw new Error("expected listTasks to reject with AgentTaskError");
    }
    expect(caught.status).toBe(400);
    expect(caught.message).toBe("invalid pack");
  });

  it("isTaskPack narrows only the two known pack literals", () => {
    expect(isTaskPack("coding-pack")).toBe(true);
    expect(isTaskPack("knowledge-work-pack")).toBe(true);
    expect(isTaskPack("something-else")).toBe(false);
  });

  it("TERMINAL_STATUSES marks succeeded/failed/cancelled as terminal", () => {
    expect(TERMINAL_STATUSES.has("succeeded")).toBe(true);
    expect(TERMINAL_STATUSES.has("failed")).toBe(true);
    expect(TERMINAL_STATUSES.has("cancelled")).toBe(true);
    expect(TERMINAL_STATUSES.has("queued")).toBe(false);
    expect(TERMINAL_STATUSES.has("running")).toBe(false);
  });
});
