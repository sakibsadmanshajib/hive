import { invoke } from "@tauri-apps/api/core";

import { validateServerUrl } from "./settings";

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

function init(): void {
  const form = byId<HTMLFormElement>("server-form");
  const input = byId<HTMLInputElement>("server-url");

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
