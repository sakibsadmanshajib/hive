export type ValidationResult =
  | { ok: true; previewUrl: string }
  | { ok: false; error: string };

/**
 * Client-side pre-check mirroring the authoritative validation in
 * src-tauri/src/settings.rs (`validate_and_normalize`). This only gives
 * instant UI feedback; the Rust command re-validates and is the source of
 * truth for what gets persisted and loaded on the next launch.
 *
 * The preview is the deployment origin and nothing else. It used to append
 * `/agent-workspace`, the standalone agent console, which was a second whole
 * application with a sign in of its own; that path is no longer served (issue
 * #540, D-045), and the deployment's one shell lives at the origin root.
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

  return { ok: true, previewUrl: `${parsed.protocol}//${parsed.host}` };
}
