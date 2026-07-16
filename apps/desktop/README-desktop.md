# apps/desktop

Tauri 2 desktop shell for Hive (blueprint Step 4.1, issues #311 / #305).
Windows and Linux only for this step; macOS is deferred (owner decision,
see the agent-subsystem blueprint). x86-64 only, no ARM builds.

## What this is

A thin native shell around the Wave 3 agent panel
(`apps/agent-console`, deployed under a Hive server's `/agent-workspace`
path behind Caddy). This app has no agent UI of its own: it is a
first-run settings screen (enter your organization's Hive server address
once) plus a webview that then points straight at that server's
`/agent-workspace` console on every subsequent launch.

- `src-tauri/` -- Rust backend. `settings.rs` validates, normalizes, and
  persists the server URL to the OS app-data directory; `main.rs` reads it
  on startup and creates the main window pointing either at the local
  settings page (`index.html`, nothing saved yet) or directly at the
  remote console URL (already configured).
- `src/` -- TypeScript frontend. Vanilla, no framework: one form, one
  submit handler. `settings.ts` mirrors the Rust validation for instant
  client-side feedback; the Rust command re-validates and is the source of
  truth for what gets persisted.

## Auth model (important: simpler than a typical desktop OAuth shell)

`apps/agent-console`'s current sign-in
(`apps/agent-console/app/auth/sign-in/page.tsx`) is a plain email/password
form calling Supabase's `signInWithPassword`, entirely in-page, with no
external-browser OAuth redirect. That means this desktop shell needs no
deep-link callback handling for Step 4.1: the webview simply loads the
remote console URL and the console's own cookie-based Supabase session
(set via `@supabase/ssr` in `middleware.ts`) works the same as it does in
a browser tab. No tokens are read, stored, or touched on the Rust side.

If a future OWUI/console change adds SSO or a magic-link flow that
redirects through an external browser, that would need
`tauri-plugin-deep-link` (or an in-app `WebviewWindow` navigation guard)
to catch the redirect back into the app. Not needed today, and not added
speculatively.

## Step 4.3 seam (#310, out of scope here)

`src-tauri/src/entitlements.rs` marks where the desktop auth and
feature-gate/license fetch belongs: after the server URL is resolved (so
the fetch target is known) and before the window is created (so no
plugin pack surface renders until gates/license state comes back).
`main.rs`'s `setup()` calls the stub at that exact point. Step 4.3
replaces the stub with a real call to the control-plane featuregate
(Step 1.1) and licensing (Step 1.4) APIs.

Reconfiguring the server after first run is also out of scope here: the
Rust command `reset_server_url` already exists (and is tested) for a
follow-up settings UI to call; today the only way to reconfigure is to
delete the app's `settings.json` (see path below) and relaunch.

## Versions pinned (checked against crates.io / npm on 2026-07-16)

| Package | Version |
|---|---|
| `tauri` (crate) | 2.11.5 |
| `tauri-build` (crate) | 2.6.3 |
| `serde` | 1.0.228 |
| `serde_json` | 1.0.150 |
| `url` | 2.5.8 |
| `@tauri-apps/cli` | 2.11.4 |
| `@tauri-apps/api` | 2.11.1 |
| `vite` | 6.4.3 |
| `vitest` | 3.2.7 |
| `typescript` | 5.9.3 |
| `jsdom` | 26.1.0 |

`typescript`/`vitest`/`jsdom` are pinned to the same major line already in
use by `apps/agent-console` for consistency, not to the newest majors
published (TypeScript 7 and Vitest 4 exist but are unproven elsewhere in
this repo).

## Build and run

### Frontend only (fast checks, no system deps beyond Node)

```bash
cd apps/desktop
npm install
npx tsc --noEmit      # typecheck
npm run test:unit     # vitest, settings.ts validation
npm run build         # tsc + vite build -> dist/
```

### Rust backend (needs a Linux system WebKitGTK toolchain)

`cargo check`/`build` for a Tauri app links against WebKitGTK on Linux
(via the `webkit2gtk-4.1`/`javascriptcoregtk-4.1`/`libsoup-3.0` pkg-config
files), which this repo's existing Go-only
`deploy/docker/Dockerfile.toolchain` does not provide. Use the new
`deploy/docker/Dockerfile.desktop-linux` image instead:

```bash
docker build -f deploy/docker/Dockerfile.desktop-linux \
  -t hive-desktop-linux-build:local .

docker run --rm -v "$(pwd)/apps/desktop:/workspace/apps/desktop" \
  -w /workspace/apps/desktop/src-tauri \
  hive-desktop-linux-build:local "cargo check && cargo test"
```

### Linux packaging (deb, AppImage)

```bash
docker run --rm -v "$(pwd)/apps/desktop:/workspace/apps/desktop" \
  -w /workspace/apps/desktop \
  hive-desktop-linux-build:local \
  "npm install && npx tauri build --target x86_64-unknown-linux-gnu --bundles deb,appimage"
```

Output lands in `apps/desktop/src-tauri/target/x86_64-unknown-linux-gnu/release/bundle/`.

### Windows packaging (msi, nsis)

Not buildable from this WSL2 environment (no Windows GUI/MSVC toolchain
available here, and cross-compiling a Windows GUI bundle from Linux is
not a supported Tauri path). On a Windows host or CI runner with the
Tauri prerequisites (Rust MSVC target, WebView2, NSIS):

```powershell
cd apps/desktop
npm install
npx tauri build --target x86_64-pc-windows-msvc --bundles msi,nsis
```

## What actually ran where (verification for this PR)

Verified in this WSL2 environment:
- `npx tsc --noEmit` -- passes.
- `npm run test:unit` (Vitest) -- 11/11 passing, covers URL validation
  edge cases (empty, whitespace, missing scheme, `javascript:`/`ftp:`
  scheme rejection, missing host, path/query/fragment stripping,
  whitespace trimming, explicit port preservation).
- `npm run build` (tsc + vite build) -- produces `dist/`.
- `cargo check` and `cargo test` for `src-tauri` -- run inside the new
  `Dockerfile.desktop-linux` image (this WSL2 host has no system Rust
  toolchain and no `sudo`, so WebKitGTK dev headers could not be
  installed on the bare host; `rustup` was installed under `$HOME/.cargo`
  to have a toolchain at all, but linking still needs the container's
  WebKitGTK package set). Rust unit tests cover the same URL validation
  edge cases as the TS tests, plus settings file load/save/remove
  round-trips (missing file, corrupt file, idempotent remove).

Needs the lab machine (or a CI runner with GUI/packaging support), not
verifiable here:
- Actually launching the app and seeing a window (WSL2 has no display
  server attached to this environment; `cargo tauri dev`/`build` may
  compile but a real window and WebKitGTK render cannot be exercised
  headlessly here).
- The Linux `deb`/AppImage bundle producing an installable artifact end
  to end (bundling step itself, beyond `cargo check`/`test`).
- Any Windows build or packaging (msi/nsis), which needs a Windows host.
- The full acceptance check ("app launches on Windows and Linux and
  shows the agent panel driving a task") against a real deployed Hive
  server -- needs a running server plus the lab machines per the spike
  plan.

## Settings file location

`settings.json` under the OS-standard app data directory Tauri resolves
via `app.path().app_data_dir()` (Linux:
`~/.local/share/co.scubed.hive.desktop/`, Windows:
`%APPDATA%\co.scubed.hive.desktop\`). Delete it to force the first-run
settings screen again.
