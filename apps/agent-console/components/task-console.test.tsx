import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";

import { TaskConsole } from "./task-console";

vi.mock("@/lib/supabase/browser", () => ({
  createClient: () => ({
    auth: {
      getSession: () =>
        Promise.resolve({ data: { session: { access_token: "test-token" } } }),
    },
  }),
}));

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const QUEUED_TASK = {
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

describe("TaskConsole", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("loads and renders the task list on mount", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ tasks: [QUEUED_TASK] }));
    vi.stubGlobal("fetch", fetchMock);

    render(<TaskConsole />);

    expect(await screen.findByText("Coding pack")).toBeTruthy();
    expect(screen.getByText("queued")).toBeTruthy();
  });

  it("starts a coding-pack task and prepends it to the list", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ tasks: [] }))
      .mockResolvedValueOnce(jsonResponse(QUEUED_TASK, 201));
    vi.stubGlobal("fetch", fetchMock);

    render(<TaskConsole />);
    await waitFor(() => expect(screen.getByText("No tasks yet.")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /start coding pack/i }));

    expect(await screen.findByText("queued")).toBeTruthy();
    const createCall = fetchMock.mock.calls[1];
    expect(String(createCall[0])).toContain("/v1/agent/tasks");
    expect(createCall[1]?.method).toBe("POST");
    expect(JSON.parse(String(createCall[1]?.body))).toEqual({ pack: "coding-pack" });
  });

  it("starts a knowledge-work-pack task with the matching pack literal", async () => {
    const kwTask = { ...QUEUED_TASK, pack: "knowledge-work-pack" };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ tasks: [] }))
      .mockResolvedValueOnce(jsonResponse(kwTask, 201));
    vi.stubGlobal("fetch", fetchMock);

    render(<TaskConsole />);
    await waitFor(() => expect(screen.getByText("No tasks yet.")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /start knowledge-work pack/i }));

    await waitFor(() => {
      const createCall = fetchMock.mock.calls[1];
      expect(JSON.parse(String(createCall[1]?.body))).toEqual({
        pack: "knowledge-work-pack",
      });
    });
  });

  it("shows a cancel button for a non-terminal task and calls the cancel endpoint", async () => {
    const cancelled = { ...QUEUED_TASK, status: "cancelled" };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ tasks: [QUEUED_TASK] }))
      .mockResolvedValueOnce(jsonResponse(cancelled));
    vi.stubGlobal("fetch", fetchMock);

    render(<TaskConsole />);
    const cancelButton = await screen.findByRole("button", { name: /^cancel$/i });
    fireEvent.click(cancelButton);

    await waitFor(() => {
      expect(screen.getByText("cancelled")).toBeTruthy();
    });
    const cancelCall = fetchMock.mock.calls[1];
    expect(String(cancelCall[0])).toContain(`/v1/agent/tasks/${QUEUED_TASK.id}/cancel`);
  });

  it("does not show a cancel button once a task reaches a terminal state", async () => {
    const succeeded = { ...QUEUED_TASK, status: "succeeded", result_summary_ref: "ref-1" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ tasks: [succeeded] }));
    vi.stubGlobal("fetch", fetchMock);

    render(<TaskConsole />);
    await screen.findByText("succeeded");
    expect(screen.queryByRole("button", { name: /^cancel$/i })).toBeNull();
    expect(screen.getByText("Result: ref-1")).toBeTruthy();
  });

  it("shows an error message when the initial load fails", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("nope", { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);

    render(<TaskConsole />);

    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "Could not load tasks.",
    );
  });
});
