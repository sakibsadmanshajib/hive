import { invoke } from "@tauri-apps/api/core";

import { validateServerUrl } from "./settings";

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

function init(): void {
  const form = byId<HTMLFormElement>("server-form");
  const input = byId<HTMLInputElement>("server-url");

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
