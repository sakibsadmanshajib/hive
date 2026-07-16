import { invoke } from "@tauri-apps/api/core";

import { validateServerUrl } from "./settings";
import {
  buildCreateRequest,
  formatStatus,
  isLocalTaskView,
  packLabel,
  PACKS,
  type LocalTaskView,
  type TaskPack,
} from "./local-tasks";

/** Mirrors src-tauri/src/entitlements.rs EntitlementsView. */
interface EntitlementsView {
  status: "known" | "awaiting_session" | "unreachable";
  cowork_enabled: boolean;
  reason?: string;
}

function byId<T extends HTMLElement>(id: string): T {
  const el = document.getElementById(id);
  if (!el) throw new Error(`missing #${id}`);
  return el as T;
}

function showError(message: string): void {
  const errorEl = byId<HTMLParagraphElement>("server-error");
  errorEl.textContent = message;
  errorEl.hidden = false;
}

function clearError(): void {
  const errorEl = byId<HTMLParagraphElement>("server-error");
  errorEl.hidden = true;
  errorEl.textContent = "";
}

/**
 * Step 4.3 (#310): if the startup gate fetch (src-tauri/src/entitlements.rs)
 * found the configured server unreachable, show that inline instead of
 * silently leaving the user on a bare "enter your server" form -- the two
 * states look identical otherwise. Not shown for `awaiting_session` (the
 * expected first-run and cold-start state) or `known`.
 */
async function showUnreachableBannerIfNeeded(): Promise<void> {
  const entitlements = await invoke<EntitlementsView>("get_entitlements");
  if (entitlements.status !== "unreachable") return;

  const savedUrl = await invoke<string | null>("get_server_url");
  if (savedUrl) {
    byId<HTMLInputElement>("server-url").value = savedUrl;
  }
  showError(
    `Can't reach ${savedUrl ?? "the configured server"}. Check the address or your connection, then try again.`,
  );
}

/**
 * Blueprint Step 4.4 (#308/#311): the "start a task, local or server"
 * prompt lives on this first-party page, not inside the remote
 * agent-console webview -- see local-tasks.ts's module doc for why (this
 * page already has ordinary Tauri IPC; the remote page's capability
 * intentionally has none, per PR #337's capabilities/default.json note).
 * "Server" is deliberately not a Tauri command: it just navigates to the
 * existing, unmodified agent-console task flow (D8's server-task-syncs
 * half), which already works today.
 */
function renderLocalTasks(tasks: LocalTaskView[]): void {
  const list = byId<HTMLUListElement>("local-task-list");
  list.replaceChildren(
    ...tasks.map((task) => {
      const item = document.createElement("li");
      item.textContent = `[${formatStatus(task.status)}] ${packLabel(task.pack)}: ${task.instructions || "(no instructions)"}`;
      return item;
    }),
  );
}

async function refreshLocalTasks(): Promise<void> {
  const tasks = await invoke<unknown[]>("list_local_tasks");
  renderLocalTasks(tasks.filter(isLocalTaskView));
}

function initLocalTasks(): void {
  const packSelect = byId<HTMLSelectElement>("local-task-pack");
  for (const { value, label } of PACKS) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = label;
    packSelect.appendChild(option);
  }

  const form = byId<HTMLFormElement>("local-task-form");
  const instructions = byId<HTMLTextAreaElement>("local-task-instructions");

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const request = buildCreateRequest(packSelect.value as TaskPack, instructions.value);
    try {
      await invoke("create_local_task", { ...request });
      instructions.value = "";
      await refreshLocalTasks();
    } catch {
      // Best-effort UI: a failed local task still shows up via
      // refreshLocalTasks() as a "Failed" entry once list_local_tasks is
      // called again, so there is no separate error banner here.
    }
  });

  refreshLocalTasks().catch(() => {
    // Non-fatal: worst case the list just starts empty.
  });
}

function init(): void {
  const form = byId<HTMLFormElement>("server-form");
  const input = byId<HTMLInputElement>("server-url");

  initLocalTasks();

  showUnreachableBannerIfNeeded().catch(() => {
    // Non-fatal: worst case the form just shows no banner.
  });

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    clearError();

    const precheck = validateServerUrl(input.value);
    if (!precheck.ok) {
      showError(precheck.error);
      return;
    }

    try {
      // Rust re-validates and persists; it is the source of truth for the
      // URL actually saved and for what the next launch loads directly.
      const consoleUrl = await invoke<string>("set_server_url", {
        url: input.value,
      });
      window.location.href = consoleUrl;
    } catch (err) {
      showError(typeof err === "string" ? err : "Could not save the server URL.");
    }
  });
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
