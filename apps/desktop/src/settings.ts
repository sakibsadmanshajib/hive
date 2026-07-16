export type ValidationResult =
  | { ok: true; previewUrl: string }
  | { ok: false; error: string };

const CONSOLE_BASE_PATH = "/agent-workspace";

/**
 * Client-side pre-check mirroring the authoritative validation in
 * src-tauri/src/settings.rs (`validate_and_normalize`). This only gives
 * instant UI feedback; the Rust command re-validates and is the source of
 * truth for what gets persisted and loaded on the next launch.
 */
export function validateServerUrl(input: string): ValidationResult {
  const trimmed = input.trim();
  if (!trimmed) {
    return { ok: false, error: "Server URL is required." };
  }

  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return {
      ok: false,
      error: "Enter a valid URL, e.g. https://hive.example.com",
    };
  }

  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return {
      ok: false,
      error: `Unsupported URL scheme "${parsed.protocol.replace(":", "")}". Use http or https.`,
    };
  }

  if (!parsed.hostname) {
    return { ok: false, error: "Server URL must include a host." };
  }

  const origin = `${parsed.protocol}//${parsed.host}`;
  return { ok: true, previewUrl: `${origin}${CONSOLE_BASE_PATH}` };
}
