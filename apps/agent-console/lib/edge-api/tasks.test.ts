import { describe, it, expect, vi, afterEach } from "vitest";

import {
  AgentTaskError,
  artifactUrl,
  cancelTask,
  createTask,
  ENGINE_LAUNCH_FAILED_MESSAGE,
  ENGINE_UNAVAILABLE_MESSAGE,
  getTask,
  isEngineLaunchFailure,
  isEngineUnavailable,
  listTasks,
  TERMINAL_STATUSES,
  isTaskPack,
  type AgentTask,
} from "./tasks";

const BASE_URL = "http://edge-api.test";
const TOKEN = "test-token";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Typed rather than inferred: the sentinel helpers below take an AgentTask,
// and an untyped literal widens `pack` and `status` to string.
const TASK: AgentTask = {
  id: "11111111-1111-1111-1111-111111111111",
  pack: "coding-pack",
  instructions: "Audit the webhook handlers for unvalidated input.",
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
    vi.unstubAllEnvs();
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

  it("keeps a task whose status this client does not recognise", async () => {
    // A status the console has never heard of must not delete the row. The
    // user submitted that task; a list that quietly omits it reads as
    // deletion, which is the one thing it definitely is not.
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ tasks: [{ ...TASK, status: "expired" }] }));
    vi.stubGlobal("fetch", fetchMock);

    const tasks = await listTasks(BASE_URL, TOKEN);

    expect(tasks).toHaveLength(1);
    expect(tasks[0].id).toBe(TASK.id);
    expect(tasks[0].status).toBe("unknown");
  });

  it("createTask POSTs the pack and the instructions the contract documents", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(TASK, 201));
    vi.stubGlobal("fetch", fetchMock);

    const task = await createTask(BASE_URL, TOKEN, "coding-pack", "Ship the thing.");

    expect(fetchMock).toHaveBeenCalledWith(
      `${BASE_URL}/v1/agent/tasks`,
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ pack: "coding-pack", instructions: "Ship the thing." }),
      }),
    );
    expect(task).toEqual(TASK);
  });

  it("decodes a task whose instructions field is absent as an empty string", async () => {
    const { instructions: _dropped, ...withoutInstructions } = TASK;
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ tasks: [withoutInstructions] }));
    vi.stubGlobal("fetch", fetchMock);

    const tasks = await listTasks(BASE_URL, TOKEN);
    expect(tasks[0].instructions).toBe("");
  });

  it("recognizes the control-plane engine sentinels only on failed tasks", () => {
    const unavailable: AgentTask = {
      ...TASK,
      status: "failed",
      error_message: ENGINE_UNAVAILABLE_MESSAGE,
    };
    const launchFailed: AgentTask = {
      ...TASK,
      status: "failed",
      error_message: ENGINE_LAUNCH_FAILED_MESSAGE,
    };
    const realFailure: AgentTask = {
      ...TASK,
      status: "failed",
      error_message: "the sandbox ran out of disk",
    };

    expect(isEngineUnavailable(unavailable)).toBe(true);
    expect(isEngineUnavailable(launchFailed)).toBe(false);
    expect(isEngineUnavailable(realFailure)).toBe(false);
    expect(isEngineLaunchFailure(launchFailed)).toBe(true);

    // A queued task carrying the string somehow is still not a blocked task.
    expect(isEngineUnavailable({ ...unavailable, status: "queued" })).toBe(false);
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

  describe("artifactUrl", () => {
    it("resolves an artifact ref to an absolute URL when configured", () => {
      vi.stubEnv("NEXT_PUBLIC_ARTIFACTS_BASE_URL", "https://artifacts.example.com");
      expect(artifactUrl("/artifacts/abc-123")).toBe(
        "https://artifacts.example.com/artifacts/abc-123",
      );
    });

    it("resolves a versioned artifact ref", () => {
      vi.stubEnv("NEXT_PUBLIC_ARTIFACTS_BASE_URL", "https://artifacts.example.com");
      expect(artifactUrl("/artifacts/abc-123/v/2")).toBe(
        "https://artifacts.example.com/artifacts/abc-123/v/2",
      );
    });

    it("strips a trailing slash from the configured base", () => {
      vi.stubEnv("NEXT_PUBLIC_ARTIFACTS_BASE_URL", "https://artifacts.example.com/");
      expect(artifactUrl("/artifacts/abc-123")).toBe(
        "https://artifacts.example.com/artifacts/abc-123",
      );
    });

    it("returns null for the plain-text final-response fallback shape", () => {
      vi.stubEnv("NEXT_PUBLIC_ARTIFACTS_BASE_URL", "https://artifacts.example.com");
      expect(artifactUrl("the agent's own summary text")).toBeNull();
    });

    it("returns null when NEXT_PUBLIC_ARTIFACTS_BASE_URL is not configured", () => {
      vi.stubEnv("NEXT_PUBLIC_ARTIFACTS_BASE_URL", "");
      expect(artifactUrl("/artifacts/abc-123")).toBeNull();
    });

    it("returns null for an empty ref", () => {
      vi.stubEnv("NEXT_PUBLIC_ARTIFACTS_BASE_URL", "https://artifacts.example.com");
      expect(artifactUrl("")).toBeNull();
    });
  });
});
