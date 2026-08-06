import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/react";

import { TaskConsole } from "./task-console";
import {
  ENGINE_LAUNCH_FAILED_MESSAGE,
  ENGINE_UNAVAILABLE_MESSAGE,
} from "@/lib/edge-api/tasks";

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
  instructions: "Audit the webhook handlers for unvalidated input.",
  status: "queued",
  engine_session_ref: "",
  result_summary_ref: "",
  error_message: "",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
  started_at: null,
  finished_at: null,
};

function agoIso(ms: number): string {
  return new Date(Date.now() - ms).toISOString();
}

/*
 * Routes by URL and method rather than by call order.
 *
 * The console polls the list every three seconds, so an ordered
 * mockResolvedValueOnce chain runs out mid-test and starts resolving
 * `undefined`, which surfaces as a spurious "Could not load your tasks" alert.
 * Routing removes that class of flake entirely.
 */
function stubFetch(routes: {
  tasks?: unknown[];
  create?: () => Promise<Response>;
  cancel?: unknown;
  listStatus?: number;
}) {
  const fetchMock = vi.fn((url: string | URL, init?: RequestInit) => {
    const href = String(url);
    const method = init?.method ?? "GET";

    if (href.endsWith("/cancel")) {
      return Promise.resolve(jsonResponse(routes.cancel ?? {}));
    }
    if (method === "POST") {
      return routes.create
        ? routes.create()
        : Promise.resolve(jsonResponse(QUEUED_TASK, 201));
    }
    if (routes.listStatus && routes.listStatus >= 400) {
      return Promise.resolve(new Response("nope", { status: routes.listStatus }));
    }
    return Promise.resolve(jsonResponse({ tasks: routes.tasks ?? [] }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

/** The body of the one POST that created a task. */
function createBody(fetchMock: ReturnType<typeof stubFetch>): unknown {
  const call = fetchMock.mock.calls.find(
    ([url, init]) =>
      (init as RequestInit | undefined)?.method === "POST" &&
      !String(url).endsWith("/cancel"),
  );
  if (!call) throw new Error("no create call was made");
  return JSON.parse(String((call[1] as RequestInit).body));
}

describe("TaskConsole composer", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("gives the prompt field a real, associated label", async () => {
    stubFetch({});
    render(<TaskConsole />);

    const field = await screen.findByLabelText("What should the agent do?");
    expect(field.tagName).toBe("TEXTAREA");
    expect(field.getAttribute("aria-required")).toBe("true");
  });

  it("sends the typed instructions and the selected pack", async () => {
    const fetchMock = stubFetch({});
    render(<TaskConsole />);

    const field = await screen.findByLabelText("What should the agent do?");
    fireEvent.change(field, { target: { value: "  Summarize the audit log.  " } });
    fireEvent.click(screen.getByRole("button", { name: "Start task" }));

    await waitFor(() =>
      expect(createBody(fetchMock)).toEqual({
        pack: "coding-pack",
        instructions: "Summarize the audit log.",
      }),
    );
  });

  it("sends knowledge-work-pack when that pack is chosen", async () => {
    const fetchMock = stubFetch({});
    render(<TaskConsole />);

    const field = await screen.findByLabelText("What should the agent do?");
    fireEvent.change(field, { target: { value: "Research the market." } });
    fireEvent.click(screen.getByRole("radio", { name: "Knowledge work" }));
    fireEvent.click(screen.getByRole("button", { name: "Start task" }));

    await waitFor(() =>
      expect(createBody(fetchMock)).toEqual({
        pack: "knowledge-work-pack",
        instructions: "Research the market.",
      }),
    );
  });

  it("refuses an empty prompt instead of launching a goalless task", async () => {
    const fetchMock = stubFetch({});
    render(<TaskConsole />);

    await screen.findByLabelText("What should the agent do?");
    fireEvent.click(screen.getByRole("button", { name: "Start task" }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Describe the task first");
    expect(
      screen.getByLabelText("What should the agent do?").getAttribute("aria-invalid"),
    ).toBe("true");

    const posted = fetchMock.mock.calls.filter(
      ([, init]) => (init as RequestInit | undefined)?.method === "POST",
    );
    expect(posted).toEqual([]);
  });

  it("submits on Ctrl+Enter from the prompt field", async () => {
    const fetchMock = stubFetch({});
    render(<TaskConsole />);

    const field = await screen.findByLabelText("What should the agent do?");
    fireEvent.change(field, { target: { value: "Ship it." } });
    fireEvent.keyDown(field, { key: "Enter", ctrlKey: true });

    await waitFor(() =>
      expect(createBody(fetchMock)).toEqual({
        pack: "coding-pack",
        instructions: "Ship it.",
      }),
    );
  });

  it("clears the prompt once the task is accepted", async () => {
    stubFetch({});
    render(<TaskConsole />);

    const field = await screen.findByLabelText("What should the agent do?");
    fireEvent.change(field, { target: { value: "Ship it." } });
    fireEvent.click(screen.getByRole("button", { name: "Start task" }));

    await waitFor(() => expect((field as HTMLTextAreaElement).value).toBe(""));
  });

  it("keeps the typed text when the create call fails", async () => {
    stubFetch({
      create: () => Promise.resolve(new Response("boom", { status: 500 })),
    });
    render(<TaskConsole />);

    const field = await screen.findByLabelText("What should the agent do?");
    fireEvent.change(field, { target: { value: "Do not lose this." } });
    fireEvent.click(screen.getByRole("button", { name: "Start task" }));

    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain(
        "Your text is still here",
      ),
    );
    expect((field as HTMLTextAreaElement).value).toBe("Do not lose this.");
  });

  it("shows a submitting state while the create request is in flight", async () => {
    let release: (r: Response) => void = () => {};
    stubFetch({
      create: () =>
        new Promise<Response>((resolve) => {
          release = resolve;
        }),
    });
    render(<TaskConsole />);

    const field = await screen.findByLabelText("What should the agent do?");
    fireEvent.change(field, { target: { value: "Take your time." } });
    fireEvent.click(screen.getByRole("button", { name: "Start task" }));

    const busy = await screen.findByRole("button", { name: "Starting…" });
    expect((busy as HTMLButtonElement).disabled).toBe(true);

    release(jsonResponse(QUEUED_TASK, 201));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Start task" })).toBeTruthy(),
    );
  });
});

describe("TaskConsole states", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("empty: explains that tasks will appear and persist", async () => {
    stubFetch({ tasks: [] });
    render(<TaskConsole />);

    expect(await screen.findByText("Nothing submitted yet")).toBeTruthy();
  });

  it("queued: says what it is waiting for", async () => {
    stubFetch({ tasks: [QUEUED_TASK] });
    render(<TaskConsole />);

    expect(await screen.findByText("Queued")).toBeTruthy();
    expect(screen.getByText("Waiting for a sandbox to pick it up.")).toBeTruthy();
    // The brief the user wrote is the row's headline.
    expect(screen.getByText(QUEUED_TASK.instructions)).toBeTruthy();
  });

  it("queued: a long-stale row admits that nothing is picking it up", async () => {
    stubFetch({
      tasks: [{ ...QUEUED_TASK, created_at: agoIso(3 * 60 * 60 * 1000) }],
    });
    render(<TaskConsole />);

    const detail = await screen.findByText(/nothing picking it up/i);
    expect(detail.textContent).toContain("Queued for 3h");
  });

  it("running: promises polling rather than a live transcript", async () => {
    stubFetch({
      tasks: [{ ...QUEUED_TASK, status: "running", engine_session_ref: "sess-1" }],
    });
    render(<TaskConsole />);

    expect(await screen.findByText("Running")).toBeTruthy();
    expect(screen.getByText(/checks for a result every few seconds/i)).toBeTruthy();
  });

  it("succeeded: shows the result reference and drops the cancel control", async () => {
    stubFetch({
      tasks: [{ ...QUEUED_TASK, status: "succeeded", result_summary_ref: "ref-1" }],
    });
    render(<TaskConsole />);

    expect(await screen.findByText("Done")).toBeTruthy();
    expect(screen.getByText("Result: ref-1")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });

  it("failed: surfaces a genuine server error message", async () => {
    stubFetch({
      tasks: [
        {
          ...QUEUED_TASK,
          status: "failed",
          error_message: "the sandbox ran out of disk",
        },
      ],
    });
    render(<TaskConsole />);

    expect(await screen.findByText("Failed")).toBeTruthy();
    expect(screen.getByText("the sandbox ran out of disk")).toBeTruthy();
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("engine unavailable: reads as configuration, never as the raw sentinel", async () => {
    stubFetch({
      tasks: [
        {
          ...QUEUED_TASK,
          status: "failed",
          error_message: ENGINE_UNAVAILABLE_MESSAGE,
        },
      ],
    });
    render(<TaskConsole />);

    const notice = await screen.findByRole("status");
    expect(notice.textContent).toContain(
      "The agent runtime is not configured on this deployment",
    );
    expect(notice.textContent).toContain("An administrator needs to configure");

    // Blocked, not Failed: nothing ran and the user did nothing wrong.
    expect(screen.getByText("Blocked")).toBeTruthy();
    expect(screen.queryByText(ENGINE_UNAVAILABLE_MESSAGE)).toBeNull();
  });

  it("engine launch failure: blocked, without claiming the deployment is unconfigured", async () => {
    stubFetch({
      tasks: [
        {
          ...QUEUED_TASK,
          status: "failed",
          error_message: ENGINE_LAUNCH_FAILED_MESSAGE,
        },
      ],
    });
    render(<TaskConsole />);

    expect(await screen.findByText("Blocked")).toBeTruthy();
    expect(screen.getByText(/refused to start this task/i)).toBeTruthy();
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByText(ENGINE_LAUNCH_FAILED_MESSAGE)).toBeNull();
  });

  it("cancelled: names who stopped it", async () => {
    stubFetch({ tasks: [{ ...QUEUED_TASK, status: "cancelled" }] });
    render(<TaskConsole />);

    expect(await screen.findByText("Cancelled")).toBeTruthy();
    expect(screen.getByText(/You stopped this task/i)).toBeTruthy();
  });

  it("cancels a non-terminal task through the cancel endpoint", async () => {
    const fetchMock = stubFetch({
      tasks: [QUEUED_TASK],
      cancel: { ...QUEUED_TASK, status: "cancelled" },
    });
    render(<TaskConsole />);

    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.getByText("Cancelled")).toBeTruthy());
    const cancelCall = fetchMock.mock.calls.find(([url]) =>
      String(url).endsWith(`/v1/agent/tasks/${QUEUED_TASK.id}/cancel`),
    );
    expect(cancelCall).toBeTruthy();
  });

  it("load failure: says it is retrying rather than going blank", async () => {
    stubFetch({ listStatus: 500 });
    render(<TaskConsole />);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Retrying automatically");
  });

  it("keeps exactly one polite live region for the whole screen", async () => {
    // Per-row aria-live was the old shape: with a three-second poll it
    // re-announced every unchanged status forever.
    stubFetch({ tasks: [QUEUED_TASK, { ...QUEUED_TASK, id: "22222222-2222-2222-2222-222222222222", status: "running" }] });
    const { container } = render(<TaskConsole />);

    await screen.findByText("Running");
    expect(container.querySelectorAll('[aria-live="polite"]').length).toBe(1);
  });
});
