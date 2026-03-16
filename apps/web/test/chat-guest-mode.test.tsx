// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { signInWithPasswordMock, signInWithOAuthMock, signUpMock } = vi.hoisted(() => ({
  signInWithPasswordMock: vi.fn(),
  signInWithOAuthMock: vi.fn(),
  signUpMock: vi.fn(),
}));

vi.mock("../src/lib/supabase-client", () => ({
  createSupabaseBrowserClient: () => ({
    auth: {
      signInWithPassword: signInWithPasswordMock,
      signInWithOAuth: signInWithOAuthMock,
      signUp: signUpMock,
    },
  }),
  useSupabaseAuthSessionSync: () => undefined,
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
  }),
}));

vi.mock("../src/components/ui/select", async () => {
  const React = await import("react");
  const Ctx = React.createContext({ value: "", onValueChange: (_v: string) => {}, open: false, setOpen: (_o: boolean) => {} });

  function Select({ value = "", onValueChange = () => {}, children }: { value?: string; onValueChange?: (v: string) => void; children: React.ReactNode }) {
    const [open, setOpen] = React.useState(false);
    return React.createElement(Ctx.Provider, { value: { value, onValueChange, open, setOpen } }, React.createElement("div", null, children));
  }
  const SelectTrigger = React.forwardRef<HTMLButtonElement, React.ButtonHTMLAttributes<HTMLButtonElement>>(({ children, ...props }, ref) => {
    const { open, setOpen } = React.useContext(Ctx);
    return React.createElement("button", { ref, role: "combobox", "aria-expanded": open, type: "button", onClick: () => setOpen(!open), ...props }, children);
  });
  function SelectValue({ placeholder }: { placeholder?: string }) {
    const { value } = React.useContext(Ctx);
    return React.createElement("span", null, value || placeholder);
  }
  function SelectContent({ children }: { children: React.ReactNode }) {
    const { open } = React.useContext(Ctx);
    if (!open) return null;
    return React.createElement("div", { role: "listbox" }, children);
  }
  const SelectItem = React.forwardRef<HTMLDivElement, { value: string; children: React.ReactNode }>(({ value, children, ...props }, ref) => {
    const ctx = React.useContext(Ctx);
    return React.createElement("div", { ref, role: "option", "aria-selected": ctx.value === value, onClick: () => { ctx.onValueChange(value); ctx.setOpen(false); }, ...props }, children);
  });
  const noop = ({ children }: { children?: React.ReactNode }) => React.createElement(React.Fragment, null, children);
  return { Select, SelectContent, SelectGroup: noop, SelectItem, SelectLabel: noop, SelectSeparator: () => null, SelectTrigger, SelectValue };
});

import HomePage from "../src/app/page";
import { clearAuthSession } from "../src/features/auth/auth-session";
import { clearGuestSession } from "../src/features/auth/guest-session";

function mockJsonResponse(ok: boolean, body: unknown) {
  const bodyStr = JSON.stringify(body);
  return {
    ok,
    status: ok ? 200 : 400,
    headers: new Headers({ "content-type": "application/json" }),
    text: async () => bodyStr,
    json: async () => body,
  };
}

function createGuestFetchMock() {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.toString();
    const method = (init?.method ?? (typeof input === "object" && input instanceof Request ? input.method : undefined)) ?? "GET";

    if (url.endsWith("/v1/models")) {
      return mockJsonResponse(true, {
        data: [
          { id: "guest-free", capability: "chat", costType: "free" },
          { id: "fast-chat", capability: "chat", costType: "fixed" },
        ],
      });
    }

    if (url.endsWith("/api/guest-session")) {
      return mockJsonResponse(true, {
        guestId: "guest_123",
        issuedAt: "2026-03-13T00:00:00.000Z",
        expiresAt: "2026-03-20T00:00:00.000Z",
      });
    }

    if (url.endsWith("/api/chat/guest")) {
      return mockJsonResponse(true, {
        choices: [{ message: { content: "Guest reply" } }],
      });
    }

    if (url.includes("/api/chat/guest/sessions") && url.endsWith("/messages")) {
      return mockJsonResponse(true, {
        id: "chat_sess_1",
        title: "New Chat",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:01:00.000Z",
        lastMessageAt: "2026-03-15T10:01:00.000Z",
        messages: [
          { id: "m1", role: "user", content: "hello from guest", createdAt: "2026-03-15T10:00:30.000Z", sequence: 1, sessionId: "chat_sess_1" },
          { id: "m2", role: "assistant", content: "Guest reply", createdAt: "2026-03-15T10:01:00.000Z", sequence: 2, sessionId: "chat_sess_1" },
        ],
      });
    }

    if (url.endsWith("/api/chat/guest/sessions") && method === "POST") {
      return mockJsonResponse(true, {
        id: "chat_sess_1",
        title: "New Chat",
        createdAt: "2026-03-15T10:00:00.000Z",
        updatedAt: "2026-03-15T10:00:00.000Z",
        lastMessageAt: null,
      });
    }

    if (url.includes("/api/chat/guest/sessions") && !url.endsWith("/messages")) {
      const getMatch = url.match(/\/api\/chat\/guest\/sessions\/([^/]+)$/);
      if (getMatch && method === "GET") {
        return mockJsonResponse(true, {
          id: getMatch[1],
          title: "New Chat",
          createdAt: "2026-03-15T10:00:00.000Z",
          updatedAt: "2026-03-15T10:00:00.000Z",
          lastMessageAt: null,
          messages: [],
        });
      }
      if (url.endsWith("/api/chat/guest/sessions")) {
        return mockJsonResponse(true, { object: "list", data: [] });
      }
    }

    return mockJsonResponse(false, { error: `Unhandled URL: ${url}` });
  });
}

describe("chat guest mode", () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    window.localStorage.clear();
    clearAuthSession();
    clearGuestSession();
    vi.restoreAllMocks();
    signInWithPasswordMock.mockReset();
    signInWithOAuthMock.mockReset();
    signUpMock.mockReset();
    if (!HTMLElement.prototype.hasPointerCapture) {
      Object.defineProperty(HTMLElement.prototype, "hasPointerCapture", {
        configurable: true,
        value: () => false,
      });
    }
    if (!HTMLElement.prototype.setPointerCapture) {
      Object.defineProperty(HTMLElement.prototype, "setPointerCapture", {
        configurable: true,
        value: () => undefined,
      });
    }
    if (!HTMLElement.prototype.releasePointerCapture) {
      Object.defineProperty(HTMLElement.prototype, "releasePointerCapture", {
        configurable: true,
        value: () => undefined,
      });
    }
    if (!HTMLElement.prototype.scrollIntoView) {
      Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
        configurable: true,
        value: () => undefined,
      });
    }
  });

  async function waitForFreeModelSelected(fetchMockRef: ReturnType<typeof vi.fn>) {
    await waitFor(() => {
      expect(fetchMockRef.mock.calls.some(([url]: [unknown]) => String(url).endsWith("/v1/models"))).toBe(true);
    });
    // Wait for React to process the fetch response and auto-select the free model
    await waitFor(() => {
      const trigger = screen.getAllByRole("combobox").at(-1)!;
      expect(trigger.textContent).toContain("guest-free");
    });
  }

  async function openModelPicker(fetchMockRef: ReturnType<typeof vi.fn>) {
    await waitForFreeModelSelected(fetchMockRef);
    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    fireEvent.click(modelTrigger);
    // Wait for options to actually render in the listbox
    await screen.findByRole("option", { name: /guest-free/i });
    return modelTrigger;
  }

  it("shows paid models as locked to guests instead of hiding them", async () => {
    const fetchMock = createGuestFetchMock();

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    await openModelPicker(fetchMock);

    expect(await screen.findByRole("option", { name: /fast-chat/i })).toBeInTheDocument();
    expect(screen.getByText(/locked/i)).toBeInTheDocument();
    expect(screen.getByText(/requires account and credits/i)).toBeInTheDocument();
  });

  it("opens a dismissible auth modal when a guest clicks a locked paid model", async () => {
    const fetchMock = createGuestFetchMock();

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    await openModelPicker(fetchMock);
    fireEvent.click(await screen.findByRole("option", { name: /fast-chat/i }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("heading", { name: /login/i })).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: /create account/i })).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: /continue with free models/i }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    expect((await screen.findAllByText(/guest mode is active/i)).length).toBeGreaterThan(0);
  });

  it("shows guest messaging and sends chat through the guest web endpoint", async () => {
    const fetchMock = createGuestFetchMock();

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    expect((await screen.findAllByText(/guest mode is active/i)).length).toBeGreaterThan(0);

    // Wait for models to load and a free model to be auto-selected
    await waitForFreeModelSelected(fetchMock);

    fireEvent.change(screen.getByPlaceholderText(/ask something/i), {
      target: { value: "hello from guest" },
    });
    fireEvent.click(screen.getByRole("button", { name: /send/i }));

    await waitFor(() => {
      const postToGuestSession = fetchMock.mock.calls.some(([url, init]) =>
        /\/api\/guest-session$/.test(String(url)) &&
        typeof init === "object" &&
        init !== null &&
        "method" in init &&
        init.method === "POST"
      );
      const postToSessionMessages = fetchMock.mock.calls.some(([url, init]) =>
        /\/api\/chat\/guest\/sessions\/[^/]+\/messages$/.test(String(url)) &&
        typeof init === "object" &&
        init !== null &&
        "method" in init &&
        init.method === "POST"
      );
      expect(postToGuestSession).toBe(true);
      expect(postToSessionMessages).toBe(true);
    });

    expect(screen.getByText(/guest mode only supports free models/i)).toBeInTheDocument();
  });

  it("renders a guest-safe sign-in action instead of the authenticated profile menu", async () => {
    const fetchMock = createGuestFetchMock();

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    expect(await screen.findByRole("button", { name: /sign in/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /open profile menu/i })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
  });

  it("keeps guest chat unavailable when the catalog request fails in guest mode", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === "string" ? input : input.toString();

      if (url.endsWith("/v1/models")) {
        return mockJsonResponse(false, { error: "catalog unavailable" });
      }

      if (url.endsWith("/api/guest-session")) {
        return mockJsonResponse(true, {
          guestId: "guest_123",
          issuedAt: "2026-03-13T00:00:00.000Z",
          expiresAt: "2026-03-20T00:00:00.000Z",
        });
      }

      return mockJsonResponse(true, {
        choices: [{ message: { content: "Guest reply" } }],
      });
    });

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    await waitFor(() => {
      expect(modelTrigger).toHaveTextContent(/model/i);
    });
    fireEvent.keyDown(modelTrigger, { key: "ArrowDown" });

    expect(screen.queryByRole("option", { name: /fast-chat/i })).not.toBeInTheDocument();
  });

  it("does not invent guest-free when the catalog returns only paid chat models", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();

      if (url.endsWith("/v1/models")) {
        return mockJsonResponse(true, {
          data: [
            { id: "fast-chat", capability: "chat", costType: "fixed" },
            { id: "smart-reasoning", capability: "chat", costType: "variable" },
          ],
        });
      }

      if (url.endsWith("/api/guest-session")) {
        return mockJsonResponse(true, {
          guestId: "guest_123",
          issuedAt: "2026-03-13T00:00:00.000Z",
          expiresAt: "2026-03-20T00:00:00.000Z",
        });
      }

      if (url.endsWith("/api/chat/guest")) {
        return mockJsonResponse(true, {
          choices: [{ message: { content: "Guest reply" } }],
        });
      }

      return mockJsonResponse(false, { error: `Unhandled URL: ${url}` });
    });

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    await waitFor(() => {
      expect(modelTrigger).toHaveTextContent(/model/i);
    });

    fireEvent.change(screen.getByPlaceholderText(/ask something/i), {
      target: { value: "hello from guest" },
    });
    const sendButton = screen.getByRole("button", { name: /send/i });
    expect(sendButton).toBeDisabled();
    fireEvent.click(sendButton);

    await waitFor(() => {
      expect(fetchMock.mock.calls.some(([url]) => /\/api\/chat\/guest$/.test(String(url)))).toBe(false);
    });
  });

  it("unlocks paid models in place after authenticating from the modal", async () => {
    const fetchMock = createGuestFetchMock();
    signInWithPasswordMock.mockResolvedValue({
      data: {
        session: {
          access_token: "auth_token",
        },
        user: {
          email: "demo@example.com",
          user_metadata: { name: "Demo" },
        },
      },
      error: null,
    });

    vi.stubGlobal("fetch", fetchMock);

    render(<HomePage />);

    await openModelPicker(fetchMock);
    fireEvent.click(await screen.findByRole("option", { name: /fast-chat/i }));

    const dialog = await screen.findByRole("dialog");
    fireEvent.change(within(dialog).getAllByPlaceholderText("Email")[0]!, {
      target: { value: "demo@example.com" },
    });
    fireEvent.change(within(dialog).getAllByPlaceholderText("Password")[0]!, {
      target: { value: "password123" },
    });
    fireEvent.click(within(dialog).getAllByRole("button", { name: /login/i }).at(-1)!);

    await waitFor(() => {
      expect(signInWithPasswordMock).toHaveBeenCalledWith({
        email: "demo@example.com",
        password: "password123",
      });
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.queryByText(/guest mode is active/i)).not.toBeInTheDocument();
    });

    await openModelPicker(fetchMock);
    const paidOption = await screen.findByRole("option", { name: /fast-chat/i });
    expect(within(paidOption).queryByText(/locked/i)).not.toBeInTheDocument();
    fireEvent.click(paidOption);

    const modelTrigger = (await screen.findAllByRole("combobox")).at(-1)!;
    await waitFor(() => {
      expect(modelTrigger).toHaveTextContent("fast-chat");
    });
  });
});
