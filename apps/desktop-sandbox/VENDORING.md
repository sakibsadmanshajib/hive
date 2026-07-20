# Vendored OpenAI Codex sandbox crates (issue #306, blueprint Step 4.2)

## Source

- Upstream: https://github.com/openai/codex (Apache-2.0), `codex-rs/` subtree.
- Commit: `a47c661ea9e226fe65e46cf9dbc5c5ed75c2c762`
- Commit date: 2026-07-16T13:57:47Z
- Verified via `gh api repos/openai/codex/contents/codex-rs` (current directory
  layout) and `gh api repos/openai/codex/contents/<path>` per file, against
  this commit (the tip of `main` at vendoring time).

## What was vendored (verbatim, no patches)

Three of `codex-rs`'s workspace members, copied byte-for-byte (package name,
`Cargo.toml`, and source unchanged from upstream):

- `codex-rs/bwrap/` -> `codex-bwrap/`: the `[[bin]] name = "bwrap"` crate that
  compiles the vendored bubblewrap C sources into a standalone binary via
  `cc` + `pkg-config` (`build.rs`), renaming bubblewrap's C `main` to
  `bwrap_main` and calling it from a thin Rust `main`.
- `codex-rs/process-hardening/` -> `codex-process-hardening/`: pre-exec
  hardening (`PR_SET_DUMPABLE`, `RLIMIT_CORE=0`, `LD_*`/`DYLD_*` env
  stripping). Used from `hive-desktop-sandbox::linux::launch`'s `pre_exec`
  closure, before Landlock/seccomp are applied.
- `codex-rs/vendor/bubblewrap/` -> `apps/desktop-sandbox/vendor/bubblewrap/`:
  the four bubblewrap `.c`/`.h` files `codex-bwrap`'s `build.rs`
  compiles (`bubblewrap.c`, `bind-mount.c`, `network.c`, `utils.c`), plus
  bubblewrap's own `LICENSE`/`COPYING` (LGPL-2.1+, distinct from the
  Apache-2.0 code that invokes it as a subprocess: same arrangement Codex
  itself uses, no static linking of GPL code into the Apache-2.0 binary).
- `LICENSE-apache-2.0-codex` and `NOTICE-codex`: copies of `codex-rs`'s
  upstream repo-root `LICENSE` and `NOTICE`.

Not vendored from `codex-rs`: `linux-sandbox/` (Codex's own CLI-agent-specific
sandbox invocation, coupled to Codex's exec-server/policy types we don't
use -- `hive-desktop-sandbox`'s `policy.rs`/`linux.rs` are a from-scratch,
narrower replacement for the parts we need) and `sandboxing/` (Codex's
higher-level policy abstraction, same reason).

## `codex-windows-sandbox/` (Step 3 Wave 1, blueprint `blueprint-step3-elevated-windows-sandbox.md`)

Blueprint Step 3 takes the base non-elevated Windows sandbox above (#395,
`c8c1434b`) toward the elevated variant: a dedicated low-privilege sandbox OS
user, ACL filesystem confinement, and a UAC-elevated helper. Wave 1 (this
pass) vendors the mechanism primitives and ports the one policy seam; it does
NOT wire anything into `windows::launch` yet (see "Newly authored" below and
the crate's own `lib.rs` doc). Same pinned commit as the rest of this file's
vendored code, so the whole vendored `codex-rs` tree stays on one SHA:

- Upstream: `codex-rs/windows-sandbox-rs` (package `codex-windows-sandbox`,
  Apache-2.0).
- Commit: `a47c661ea9e226fe65e46cf9dbc5c5ed75c2c762` (2026-07-16).

### What was vendored (verbatim, byte-for-byte)

Into the new `codex-windows-sandbox/` subcrate (own `Cargo.toml`, own
Apache-2.0 licence inherited from `[workspace.package]`, same reason as
`codex-bwrap`: keeps the licence boundary crisp against
`hive-desktop-sandbox`'s proprietary code):

`token.rs`, `cap.rs`, `proc_thread_attr.rs`, `acl.rs`, `workspace_acl.rs`,
`deny_read_acl.rs`, `deny_read_state.rs`, `dpapi.rs`, `hide_users.rs`,
`winutil.rs`, `sandbox_utils.rs`, `env.rs`, `path_normalization.rs`. Every
module here takes primitive arguments (paths, masks, SID pointers, token
handles) and does not import `codex_protocol`; `codex_protocol` is not a
dependency of this crate. Every `mod` declaration in `lib.rs` is
`#[cfg(windows)]`-gated (matching how `hive-desktop-sandbox`'s own `windows.rs`
is gated), so this crate compiles to an empty crate on non-Windows hosts and
is exercised only by the `-p codex-windows-sandbox` leg of the
`x86_64-pc-windows-gnu` cross-compile CI step below.

`lib.rs` also carries two crate-level `#![allow(...)]`s
(`unsafe_op_in_unsafe_fn`, `clippy::missing_safety_doc`) found necessary by
actually running `cargo clippy --target x86_64-pc-windows-gnu -p
codex-windows-sandbox -- -D warnings` this pass: without them the cross-compile
CI step below fails with 143 errors, ALL from these two lint classes (135
`unsafe_op_in_unsafe_fn`, 8 `missing_safety_doc`; verified no other clippy
lint fired), caused by an edition mismatch (upstream predates edition 2024's
requirement for an explicit `unsafe {}` block inside an `unsafe fn`'s own
body) rather than any bug. Allowed at the crate level, not patched
per-callsite, specifically so the vendored files stay byte-for-byte
diffable against upstream for future re-vendoring passes.

### Local deviations from upstream (CodeRabbit findings, PR #398)

Four of the vendored files above received small bug fixes on top of the
byte-for-byte copy, so they are no longer byte-identical to upstream. Step 3
of "Updating this vendor copy" below re-copies these files verbatim from
upstream on any future re-vendoring pass; check whether upstream has
independently fixed the same bugs, and if not, re-apply these fixes.

- `acl.rs:675` (`allow_null_device`): the null-device path was
  `r"\\\\.\\NUL"`, a raw string literal, so the backslashes are taken
  literally rather than un-escaped. That produced 4 backslashes before the
  dot and 2 after (`\\\\.\\NUL`) as the actual `CreateFileW` path argument,
  instead of the documented Win32 device-namespace path `\\.\NUL` (2
  backslashes before the dot, 1 after). Fixed to `r"\\.\NUL"`. CodeRabbit's
  major finding asked whether `\\.\NUL` or bare `NUL` is the correct form;
  the bug here was the raw-string escaping, not the device-path convention
  itself (`\\.\<name>` is the standard, well-documented Win32 device
  namespace prefix), so this was verified by static byte-level inspection of
  the literal and needs no Windows lab run to trust.
- `env.rs:119` (`ensure_denybin`): the deny-stub `.bat`/`.cmd` files were
  written with `b"@echo off\\r\\nexit /b 1\\r\\n"`. In a normal (non-raw)
  byte-string literal, `\\r` decodes to the two literal ASCII characters
  backslash and `r`, not a carriage-return byte, so the file lands as one
  line with no real line breaks, and `exit /b 1` never runs as its own
  statement (defeating the deny-stub's job of making blocked tools fail with
  a distinct exit code). Fixed to `b"@echo off\r\nexit /b 1\r\n"` (single
  backslash, so Rust's escape processing emits real CR (0x0D) and LF (0x0A)
  bytes).
- `sandbox_utils.rs:42` and its test helper at `sandbox_utils.rs:66`
  (`inject_git_safe_directory` / `safe_directory_value`): both called
  `.replace("\\\\", "/")`, which matches a literal two-backslash sequence.
  `PathBuf::to_string_lossy()` on Windows returns single backslashes as path
  separators (e.g. `C:\Users\foo`), so the replace was a no-op and the
  `GIT_CONFIG_VALUE_*` env var kept native backslashes instead of the
  intended forward-slash form. Fixed both call sites to
  `.replace('\\', "/")` (single-backslash-char pattern). Both sites shared
  the identical bug, so the existing tests (which build their expected value
  with the same helper) still pass unchanged once both are fixed together.
- `token.rs:479-482` (`create_token_with_caps_from`): after
  `CreateRestrictedToken` succeeds, a later failure in `set_default_dacl` or
  `enable_single_privilege` returned `Err` via `?` without closing
  `new_token`, leaking the HANDLE. Fixed to explicitly `CloseHandle(new_token)`
  on both error paths before returning, matching the `CloseHandle`-on-cleanup
  idiom already used elsewhere in this file (line 357) and in `acl.rs`
  (lines 88, 732).

### Deliberately NOT vendored this wave (deviations from the blueprint's Wave 1/2 module list, found while fetching the actual upstream source)

- **`process.rs`** (the `CreateProcessAsUserW` wrapper). The blueprint
  describes it as "verbatim-vendorable... no `codex_protocol` coupling," which
  is true, but it is NOT self-contained: it imports `crate::desktop::LaunchDesktop`
  (constructs one, holds it as a field of its returned spawn handle) and
  `crate::logging` for debug logging. `desktop.rs` is explicitly Step 5
  (CLI/ConPTY) scope, out of bounds for this wave. Vendoring `process.rs` now
  would mean either vendoring `desktop.rs` early or inventing a fake
  `LaunchDesktop` stub that misrepresents a real upstream type -- both worse
  than deferring. `process.rs` is deferred to whichever wave vendors or ports
  `desktop.rs` (Step 5, or whenever a real caller needs it); `hive-desktop-sandbox`'s
  own `windows.rs` already has its own working `CreateProcessAsUserW` call from
  issue #395 for what this wave needs.
- **`deny_read_resolver.rs`** (`resolve_windows_deny_read_paths`, the
  glob-scan-based deny-read carve-out selector). The blueprint's section 0.2
  claims `resolved_permissions.rs` is "the ONLY windows-sandbox module that
  imports `codex_protocol`"; that is incorrect; `deny_read_resolver.rs` also
  imports `codex_protocol::permissions::*` and `codex_utils_absolute_path::AbsolutePathBuf`.
  It is a policy-resolution module (decides WHICH paths get denied), not a
  Win32 mechanism primitive, so it belongs with `resolved_permissions.rs`'s
  port treatment, not the verbatim vendor set -- and porting it is unnecessary
  this wave: `SandboxPolicy` has no secret-path input source yet, so
  `windows_resolve.rs`'s adapter returns an empty deny-read carve-out set (a
  correct, honest default per the blueprint's own mapping table: "secret paths
  (future, from hook/config) -> deny-read carveout set; empty set is valid").
  Note that `deny_read_acl.rs`/`deny_read_state.rs` (vendored) take a plain
  `&[PathBuf]` and never depend on `deny_read_resolver.rs`'s output type, so
  excluding it does not affect what was vendored.
- `deny_read_state.rs`'s one CODEX_HOME-coupled dependency
  (`crate::setup::sandbox_dir`) is satisfied by a ONE-FUNCTION verbatim
  extraction into a local `setup.rs` (just `sandbox_dir(codex_home) -> PathBuf`,
  byte-identical to upstream); the rest of upstream's `setup.rs` (OS-account
  provisioning, `SandboxUsersFile`, elevated setup) is Wave 3 scope, ported
  into `hive-desktop-sandbox`'s `windows_elevated.rs`, not vendored here.
- `hide_users.rs`'s one dependency on `crate::logging::log_note` is satisfied
  by a minimal, Hive-authored (NOT vendored) `logging.rs` stand-in (writes to
  stderr) instead of upstream's real `logging.rs`, which pulls in
  `tracing-appender`, `chrono`, and the `codex-utils-string` crate for
  CODEX_HOME-scoped rotating log files -- none of that is load-bearing while
  `hide_users.rs` itself is not called from any Hive launch path yet.

### Newly authored (Hive-proprietary, ported from upstream's one policy seam)

- `hive-desktop-sandbox/src/windows_resolve.rs`: the port of
  `resolved_permissions.rs` to Hive-native types.
  `ResolvedWindowsSandboxPermissions::from_policy(&SandboxPolicy, cwd)` drops
  `codex_protocol` entirely and returns Hive types
  (`crate::policy::NetworkPolicy`, `Vec<PathBuf>`, a Hive `WindowsSandboxTokenMode`
  with a single `ElevatedSandboxUser` variant per the Step 3 Q1/Q3 decisions).
  Pure, no Win32 calls, unit-tested on Linux CI exactly like `windows_plan.rs`.
  Not called from `windows::launch` this wave.
- `codex-windows-sandbox/src/setup.rs` and `codex-windows-sandbox/src/logging.rs`:
  the two minimal stand-ins described above, not part of the verbatim vendor.

## `codex-windows-sandbox/` Step 3 Wave 2 (elevated-helper IPC mechanism)

Wave 2 vendors the elevated runner's named-pipe IPC mechanism into the same
inert `codex-windows-sandbox/` subcrate Wave 1 created. NOTHING from this wave
is wired into `windows::launch`. Same pinned commit as the rest of this file.

### What was vendored (verbatim, byte-for-byte except two documented import swaps)

- `elevated/mod.rs`, `elevated/runner_pipe.rs`, `elevated/runner_client.rs`
  (visibility bumped `pub(crate)` -> `pub` throughout, see `elevated/mod.rs`'s
  and `lib.rs`'s own comments: this crate's convention is fully `pub`
  mod-nested, not upstream's curated `pub(crate)` surface, and unused
  `pub(crate)` items fail `-D warnings` dead_code when this wave's vendor set
  gives them no caller).
- `elevated/ipc_framed.rs`: verbatim except swapping
  `codex_protocol::models::PermissionProfile` for
  `crate::permission_profile::PermissionProfile` and
  `codex_utils_absolute_path::AbsolutePathBuf` for
  `crate::absolute_path::AbsolutePathBuf` (both new local stand-ins, see
  below). One test, `spawn_request_serializes_permission_profile`, is excluded
  (not faked) because it asserts the real upstream `PermissionProfile`'s
  tagged-enum JSON shape, which the stand-in does not reproduce; see the
  file's own module doc and test-module comment.
- `helper_materialization.rs`: verbatim except two import-path edits
  (`crate::sandbox_bin_dir` -> `crate::setup::sandbox_bin_dir`,
  `crate::sandbox_dir` -> `crate::setup::sandbox_dir`), since this crate does
  not replicate upstream's flat crate-root re-export surface for `setup.rs`'s
  helpers (see `lib.rs`'s comment).
- `setup_error.rs`: verbatim except inlining `codex_utils_string::sanitize_metric_tag_value`
  (a ~20-line pure function) as a private fn instead of adding a new crate
  dependency for one function.
- `sandbox_users.rs`: relocated from upstream's
  `bin/setup_main/win/sandbox_users.rs` into this crate's library (top-level
  `src/`, not a `bin/`), TRIMMED to the OS-account/SID Win32 primitives only:
  `ensure_sandbox_users_group`, `resolve_sandbox_users_group_sid`,
  `ensure_sandbox_user`, `ensure_local_user`, `ensure_local_group`,
  `ensure_local_group_member`, `resolve_sid`, `well_known_sid_str`,
  `sid_bytes_from_string`, `lookup_account_name_for_sid`, `sid_bytes_to_psid`.
  See "Deliberately NOT vendored this wave" below for what was cut and why.
- `setup.rs` (the Wave 1 one-function stand-in) gains one more verbatim
  extraction, `sandbox_bin_dir`, needed by `helper_materialization.rs`.

### New, Hive-authored stand-ins (NOT vendored from upstream)

- `permission_profile.rs`: a minimal pure-data mirror of upstream
  `codex_protocol::models::PermissionProfile`, reproducing only the
  `read_only()` constructor `elevated/ipc_framed.rs`'s own test exercises.
  The real type is a much larger legacy-serde-compat tagged enum
  (`Managed`/`Disabled`/`External` variants, a hand-rolled `Deserialize` for
  old rollout files); interpreting it meaningfully requires
  `ResolvedWindowsSandboxPermissions::try_from_permission_profile*`
  (upstream's `resolved_permissions.rs`), which Wave 1 ported to Hive-native
  types instead of vendoring (Q1 decision), specifically to keep
  `codex_protocol` out of this tree. See "Deliberately NOT vendored this
  wave" below for the investigation that found `PermissionProfile` threaded
  far deeper into the elevated-helper modules than the blueprint's Wave 2
  module list assumed.
- `absolute_path.rs`: a minimal pure-data mirror of upstream
  `codex_utils_absolute_path::AbsolutePathBuf` (a real ~900-line crate with
  `schemars`/`ts-rs` TypeScript-binding dependencies and a thread-local
  base-path deserialization guard, none of which is load-bearing for an
  inert, unexercised struct field this wave).
- `identity.rs`: a one-struct (`SandboxCreds`) extraction from upstream's real
  `identity.rs`, the same treatment Wave 1 gave `setup.rs`. The rest of
  upstream's `identity.rs` (`sandbox_setup_is_complete`,
  `require_logon_sandbox_creds`, `refresh_logon_sandbox_creds`) takes or
  returns `&ResolvedWindowsSandboxPermissions` and is excluded for the same
  reason as `permission_profile.rs` above.

### Deliberately NOT vendored this wave (deviations from the mission's Wave 2 module list, found while fetching the actual upstream source)

Investigating the actual upstream source (not just the blueprint's module
table) found the `codex_protocol`/`resolved_permissions.rs` coupling is far
more pervasive across the "elevated helper" modules than Step 0.2's "one
seam" framing suggested. Concretely:

- **`spawn_prep.rs`**: every substantive function
  (`prepare_spawn_context_common`, `prepare_legacy_spawn_context`, etc.) takes
  `&PermissionProfile` and immediately calls
  `ResolvedWindowsSandboxPermissions::try_from_permission_profile_for_workspace_roots`.
  It also depends on `allow.rs` (also `codex_protocol`-coupled, never in
  scope) and `identity.rs`'s resolver-coupled functions. Not vendorable
  without also vendoring upstream's real `resolved_permissions.rs` (banned:
  no `codex_protocol` dependency) or duplicating Wave 1's Hive-native port a
  second time inside this Apache-2.0 crate (a maintenance/correctness
  hazard, not a thin adapter).
- **`wrapper.rs`** (+ `wrapper_tests.rs`): the CLI-argv wrapper threads
  `PermissionProfile`/`WindowsSandboxLevel` (`codex_protocol::config_types`)
  through its public entry points; same exclusion reason as `spawn_prep.rs`.
- **`elevated_impl.rs`**: its one substantive function,
  `run_windows_sandbox_capture_for_permission_profile`, calls
  `ResolvedWindowsSandboxPermissions::try_from_permission_profile_for_workspace_roots`
  directly; excluded whole-file (nearly the entire file is this one function
  plus its non-Windows stub).
- **`identity.rs`** (full file) and **`setup.rs`** (full file): both mix a
  few primitive path/marker helpers with resolver-coupled orchestration
  (`require_logon_sandbox_creds`, `refresh_logon_sandbox_creds`,
  `run_setup_refresh*`, `run_elevated_setup*`,
  `run_elevated_provisioning_setup`, `gather_write_roots_for_permissions`).
  Only the non-coupled pieces are extracted as stand-ins (see above); the
  rest belongs to whichever later wave ports the provisioning orchestration
  into `hive-desktop-sandbox`'s proprietary `windows_elevated.rs`, matching
  the blueprint's own original Wave 3 assignment for this exact content.
- **`bin/setup_main/` and `bin/command_runner/` (the two binaries)**: fetching
  their actual source found both `win.rs` files pull in scope well beyond
  Step 3: `bin/setup_main/win.rs` calls `install_wfp_filters` and declares
  `mod firewall;` (`bin/setup_main/win/firewall.rs` is genuine Windows
  Firewall/WFP management via COM `INetFwPolicy2` — Step 4 scope, explicitly
  out per the blueprint's own "Explicitly OUT of Step 3" list) and depends on
  a brand-new external crate, `codex-otel` (telemetry), not otherwise needed.
  `bin/command_runner/win.rs` depends on `ConptyInstance`/
  `spawn_conpty_process_as_user` (Step 5, ConPTY, explicitly out of scope),
  `process.rs` (deferred in Wave 1 for its own `desktop.rs` coupling), and
  `token_mode_for_permission_profile`/`WindowsSandboxTokenMode`
  (`resolved_permissions.rs` again). Both binaries are the fully-integrated
  END STATE of Steps 3+4+5 combined, not separable at the Step 3 boundary as
  written upstream. Vendoring them now would mean pulling in WFP, ConPTY, the
  resolver, and a new telemetry dependency all at once — the opposite of a
  staged, reviewable wave. Deferred as binaries; the one clean, self-contained
  primitive module living under `bin/setup_main/win/`
  (`sandbox_users.rs`) is salvaged and relocated into the library instead (see
  above). `bin/setup_main/win/read_acl_mutex.rs` and
  `bin/setup_main/win/setup_runtime_bin.rs` are individually clean too but are
  ONLY consumed by the excluded `win.rs`; no other Wave 2 file needs them, so
  they are excluded as orphans, not because they are themselves coupled.
- **`sandbox_users.rs`'s own `provision_sandbox_users`, `write_secrets`,
  `prepare_setup_marker`, `commit_setup_marker`, and `random_password`**:
  orchestration coupled to the excluded `win.rs`'s specific setup flow
  (`super::log_line`, a sibling fn on the excluded parent module) and,
  for the marker functions, a `chrono` dependency not otherwise needed. The
  remaining primitives get a local, timestamp-free `log_line` stand-in
  instead of upstream's real one (which itself needs `chrono`).

### Self-audit of newly vendored code (same bug classes CodeRabbit found in Wave 1)

Checked every newly vendored/relocated file for: raw-string/byte-string path
escaping bugs, Win32 HANDLE leaks on error paths, and unsynchronized file
read-modify-write.

- `runner_pipe.rs`'s named-pipe path (`format!(r"\\.\pipe\codex-runner-{nonce:x}")`)
  has the correct backslash count for the Win32 pipe namespace prefix
  (`\\.\pipe\`) when the raw string is decoded; unlike Wave 1's `acl.rs` NUL
  bug, this one is correct as vendored.
- `runner_pipe.rs::create_named_pipe` frees the security descriptor
  (`LocalFree(sd)`) unconditionally before checking `CreateNamedPipeW`'s
  return value, so the error path does not leak it.
- `runner_client.rs::spawn_runner_transport` closes `h_pipe_in`/`h_pipe_out`
  on the `CreateProcessWithLogonW` failure path, closes `pi.hThread`
  unconditionally right after the connect attempt (success or failure), and
  closes `pi.hProcess` on both the connect-failure and startup-failure paths
  (after `TerminateProcess`); the transport's own `File`s close their handles
  via `Drop` when `startup_result` fails and `transport` is dropped. No leak
  found on any traced path.
- `connect_pipe_with_timeout`'s `thread_handle` is closed unconditionally
  after the match, on every branch.
- No file-based read-modify-write was introduced this wave (no new code
  touches `cap.rs`'s already-documented unsynchronized `cap_sid` file, open
  risk #8 below, unchanged).

No new bugs found by this self-audit; nothing required a deviation beyond the
ones already documented above. The automated CodeRabbit/Greptile review pass
on PR #399 (below) found three more, after this self-audit had already
landed.

### Local deviations from upstream (CodeRabbit/Greptile findings, PR #399)

Three more small bug fixes on top of the byte-for-byte copy, found by the
automated review pass rather than the self-audit above. Same re-vendoring
note as Wave 1's equivalent section: re-copying these files verbatim from
upstream on a future pass should check whether upstream independently fixed
the same bugs, and if not, re-apply these fixes.

- `elevated/runner_client.rs::spawn_runner_transport`: the inbound server
  pipe (`h_pipe_in`) was created, then the outbound server pipe
  (`h_pipe_out`)'s creation used `?`, which returned without closing
  `h_pipe_in` on failure. Every failed launch attempt (for example, a second
  `create_named_pipe` call racing an already-in-use pipe name) leaked one
  server-pipe HANDLE. Fixed to explicitly `CloseHandle(h_pipe_in)` before
  returning the outbound-pipe error, matching the `CloseHandle`-on-cleanup
  idiom this same function already uses everywhere else (see the self-audit
  above).
- `sandbox_users.rs::ensure_local_group_member`: discarded every
  `NetLocalGroupAddMembers` result (`let _ = NetLocalGroupAddMembers(...)`),
  not only the expected "already a member" result. Access denied, a missing
  group, or an invalid account therefore made `ensure_sandbox_user` return
  `Ok(())` while the account was never actually added to
  `CodexSandboxUsers`, silently defeating the group-membership half of the
  sandbox-user provisioning contract. Fixed to check the status code against
  `NERR_Success` and `ERROR_MEMBER_IN_ALIAS` (1378; verified against the
  `windows-sys` 0.52 `Foundation` module this crate already depends on) only,
  returning a `SetupFailure` for any other code.
- `helper_materialization.rs::copy_helper_if_needed`: once a helper path was
  cached in the process-lifetime `HELPER_PATH_CACHE`, later calls returned it
  without checking the file still existed. Antivirus quarantine, cleanup, or
  a concurrent replacement removing the helper after it was cached would make
  every subsequent runner launch in the process keep using the missing path
  instead of recopying it. Fixed by gating the cache hit on `path.is_file()`;
  a miss now falls through to the existing copy-if-needed path below it
  (which already re-populates the cache once the copy succeeds again), so no
  other control flow changed.

Three further findings from the same review pass are genuine but were NOT
fixed this pass, deliberately: they fall inside latent hardening items an
independent security review of this same PR had already catalogued as
MUST-FIX-during-Integration-A (open risk #6 below), not new findings, and
fixing them piecemeal on still-inert code risks being redone or reshaped once
the transport is actually wired and lab-tested. Each gets one line here; open
risk #6 is the tracking entry.

- `helper_materialization.rs::legacy_lookup`'s fallback to the bare
  `codex-command-runner.exe` name (not an absolute path) lets
  `CreateProcessWithLogonW` fall back to executable search instead of
  failing closed when the packaged helper cannot be found or copied. Same
  bucket as the security review's "absolute-path guard" item.
- `elevated/ipc_framed.rs::read_frame` deserializes a `FramedMessage` without
  checking `msg.version` against `IPC_PROTOCOL_VERSION`, so a stale runner
  sending an older-versioned message with overlapping JSON fields would be
  treated as current. Same bucket as the security review's `ipc_framed`/nonce
  item.
- `identity.rs::SandboxCreds` derives `Debug` over its cleartext `password`
  field, so any future `{:?}` on the struct (log line, error context, panic)
  would leak it. No current call site formats `SandboxCreds` with `{:?}`
  (verified by grep), so this is latent, not live. Same underlying
  cleartext-logon-password concern the security review already flagged for
  `identity.rs`/`sandbox_users.rs`/`runner_client.rs` (the zeroize-on-drop
  item); bundling the Debug redaction with that pass avoids fixing the same
  struct's credential hygiene in two uncoordinated passes.

## `codex-windows-sandbox/` Step 3 Integration A1 (capture-path primitives + W2 security hardening)

Integration A1 adds the two remaining capture-path Win32 primitives Wave 1
deferred and applies the module-internal security must-fixes the PR #398/#399
review passes catalogued for "fix during Integration A" (open risk #6). Still
INERT: nothing added here is wired into `windows::launch`; the resolver-coupled
port (`spawn_prep`, `allow`, `elevated_impl` capture, the full `identity`/`setup`
provisioning, and the two binaries) and the launch wiring itself remain deferred
to Integration A2, which is lab-gated on `spike307-win` (blueprint L7 to L12).
Same pinned commit as the rest of this file.

### What was vendored (verbatim, byte-for-byte)

- `process.rs`: the `CreateProcessAsUserW` wrapper (`create_process_as_user`,
  `spawn_process_with_pipes`, `make_env_block`, `read_handle_loop`,
  `ConsoleMode`/`StdinMode`/`StderrMode`/`PipeSpawnHandles`). Wave 1 deferred it
  for its `crate::desktop::LaunchDesktop` and `crate::logging` coupling; both are
  now satisfied (see next). No `codex_protocol`, no `conpty`; vendored whole.
- `desktop.rs`: `LaunchDesktop` window-station/desktop handling. Vendored whole,
  including the `PrivateDesktop` private-desktop path, which stays DORMANT for
  A: the capture path always calls `LaunchDesktop::prepare(false, ..)` (the
  interactive desktop, no new isolation surface). The private-desktop hardening
  is a genuine Step 5 (ConPTY) feature, vendored-but-unused, not a lying stub.

`process.rs` calls `crate::logging::debug_log`; the Wave 1 `logging.rs` stand-in
gained a matching `debug_log` (SBX_DEBUG-gated stderr, no CODEX_HOME file
rotation), the same stand-in treatment `log_note` already had. `Cargo.toml`
gains the `Win32_System_Console`, `Win32_System_StationsAndDesktops`, and
`Win32_Graphics_Gdi` windows-sys features these two files need
(`CreateDesktopW` requires `Win32_Graphics_Gdi` in windows-sys 0.52).

### Local deviations from upstream (Integration A1 security must-fixes)

These edit files vendored in Waves 1/2, so they are no longer byte-identical to
upstream; the re-vendoring steps below note them. Each closes a specific W2
review finding (PR #398/#399) or open risk #6/#8 item. `zeroize = "1"` is added
to `Cargo.toml` for the password-scrubbing fixes.

- `elevated/ipc_framed.rs::read_frame` (W2 finding 5): now rejects a frame whose
  `version != IPC_PROTOCOL_VERSION` (fail closed, blueprint 3.3 "a mismatch must
  fail closed, never fall back"), at the single read choke point. `MAX_FRAME_LEN`
  is made `pub` so the parent-side pre-check below can reuse it. Test added.
- `elevated/runner_client.rs::wait_for_complete_frame` (W2 finding 1): pre-checks
  the peeked, attacker-influenceable declared frame length against
  `MAX_FRAME_LEN` before waiting for the rest of the frame, so an over-large
  length prefix fails closed immediately instead of spinning to the spawn-ready
  timeout.
- `elevated/runner_client.rs::spawn_runner_transport` (W2 finding 6): the
  cleartext password wide buffer (`password_w`) is `zeroize`d immediately after
  `CreateProcessWithLogonW` returns, so it does not linger in freed heap.
- `identity.rs::SandboxCreds` (W2 findings 3/6): `Debug` is now hand-written to
  redact the password (was a derive that would leak it via any `{:?}`), and the
  cleartext password is zeroized on drop.
- `sandbox_users.rs::ensure_local_user` (W2 finding 6): the password wide buffer
  (`pwd_w`) is `zeroize`d after the `NetUserAdd`/`NetUserSetInfo` calls complete
  (body wrapped in a closure so every return path scrubs it).
- `cap.rs` (open risk #8, intra-process half): `load_or_create_cap_sids`,
  `workspace_cap_sid_for_cwd`, and `writable_root_cap_sid_for_path` now hold a
  process-wide `Mutex` across the file read-modify-write and re-read under the
  lock, so two concurrent same-process callers cannot persist divergent SIDs or
  return a SID that does not match disk. A concurrency regression test is added.
  Cross-PROCESS serialization (the elevated setup binary racing the runner; a
  named OS mutex like upstream `setup_main`'s `read_acl_mutex`) is still open and
  belongs to the wired provisioning path (A2); open risk #8 stays open for that
  half.
- `absolute_path.rs` (W2 finding 4): the opaque `AbsolutePathBuf(PathBuf)`
  stand-in is now a validating newtype. `new`/`Deserialize` reject any path that
  is not an absolute Windows path or that contains a `..` traversal segment, so a
  crafted `SpawnRequest.workspace_roots` frame carrying `C:\ws\..\..\Windows`
  cannot reach the ACL grant path. The inner `PathBuf` is private, read via
  `as_path()`. Tests added. Hardened further in PR #400 below (embedded-NUL
  rejection) since it is Hive-authored, not vendored.

### Local deviations from upstream (Greptile findings, PR #400)

Two more fixes on top of the byte-for-byte `process.rs` vendor copy (see
"Integration A1" above), found by the automated Greptile review pass on PR
#400. Same re-vendoring note as the other deviation sections: re-copying
`process.rs` verbatim from upstream on a future pass should check whether
upstream independently fixed the same bugs, and if not, re-apply these fixes.

- `process.rs::make_env_block` (NUL-terminator bug): when `env` is empty, the
  function returned a single NUL, but `CreateProcessAsUserW` requires the
  environment block to end in two consecutive NULs (one ending the last
  string, one ending the block). A one-NUL block is malformed; the API can
  read past the end of the buffer looking for the second terminator. Fixed to
  push an extra NUL when the block would otherwise be empty, so it always
  ends in two. Two tests added (`make_env_block_empty_env_is_double_nul_terminated`,
  `make_env_block_nonempty_env_ends_in_double_nul`).
- `process.rs::create_process_as_user`'s no-explicit-stdio (`None`) branch
  (unrestricted child handle inheritance): this branch called
  `CreateProcessAsUserW` with `bInheritHandles = 1` via a plain `STARTUPINFOW`,
  with no `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` restricting which handles are
  inherited, unlike the explicit-stdio (`Some(..)`) branch a few lines above it
  in the same function, which already restricts inheritance to exactly the
  stdio handles it hands the child via `ProcThreadAttributeList::set_handle_list`.
  Without that restriction, any other inheritable handle open in the process at
  spawn time (pipe, file, event, IPC handle unrelated to stdio) is also
  silently inherited by the sandboxed child. Fixed by switching this branch to
  `STARTUPINFOEXW` + `EXTENDED_STARTUPINFO_PRESENT` and restricting the handle
  list to the three std handles `ensure_inheritable_stdio` already sets, the
  same pattern the `Some(..)` branch uses. Verified against the actual
  `codex-windows-sandbox` call graph: this `None` branch has no caller anywhere
  in this repo yet (`spawn_process_with_pipes`, the only caller of
  `create_process_as_user` in the vendored/authored tree, always passes
  `Some(..)`), so this is a preventive fix on still-inert code, not a behavior
  change to any live path.

### Still deferred after A1 (unchanged from Wave 2)

`deny_read_resolver.rs` (still `codex_protocol`/`codex_utils_absolute_path`
coupled AND still with no secret-path source to resolve, so `windows_resolve`'s
deny-read set stays legitimately empty), `spawn_prep.rs`, `allow.rs`,
`elevated_impl.rs` (capture), the full `identity.rs`/`setup.rs` provisioning
orchestration, and the two binaries (`setup_main`, `command_runner`). These are
the resolver-coupled port that consumes the thin Hive `windows_resolve`
`ResolvedWindowsSandboxPermissions` (a real rewrite, not a mechanical retarget,
against a resolver far thinner than upstream's) plus the launch wiring, and their
confinement correctness is only validatable in the A2 `spike307-win` lab (D-004),
so they are not hand-ported blind on inert code here. See open risk #6.

## Step 3 Integration A2 (launch wiring + provisioning + two Hive binaries)

Integration A2 lands the resolver-coupled port that A1 deferred, plus the
launch wiring and the two Hive binaries. It drops `codex_protocol` entirely
(Q1): the ports consume the Hive-native
`hive_desktop_sandbox::windows_resolve::ResolvedWindowsSandboxPermissions`, not
upstream's `resolved_permissions.rs`. Network stays refuse-until-WFP (Q2/D-005):
`windows::launch` keeps refusing both `NetworkPolicy` variants, and the elevated
compose is reachable only through the lab-only
`windows_elevated::spawn_confined_for_validation` entry point. ConPTY (Step 5)
and WFP/firewall (Integration B) are stubbed fail-closed, not vendored.

### Newly authored (Hive-proprietary, not Apache-2.0)

- `hive-desktop-sandbox/src/windows_elevated.rs`: the port of
  `allow.rs::compute_allow_paths_for_permissions` (`compute_allow_paths`,
  retargeted to the pre-resolved Hive resolver, six upstream cwd/env call sites
  collapsed to one per blueprint A.Q2), the `tty = false` CAPTURE half of
  `elevated_impl.rs::run_windows_sandbox_capture_for_permission_profile`
  (`run_windows_sandbox_capture`), the provisioning-readiness and
  credential-load logic of `identity.rs`/`setup.rs`
  (`sandbox_setup_is_complete`, `load_logon_sandbox_creds`,
  `provision_sandbox_account`), the token/ACL assembly of `spawn_prep.rs` plus
  the `command_runner/win.rs` `tty = false` branch (`run_command_runner`), and
  the pure `LaunchDecision`. It authors its own net-new Win32 in `windows-sys`
  0.52 (to interoperate natively with the subcrate's `HANDLE`/SID types): the
  LSA `SeInteractiveLogonRight` grant and the `seclogon` service check (the
  blueprint A.Q1 provisioning invariant that makes `CreateProcessWithLogonW`
  work for the hidden account), plus the child `WaitForSingleObject` /
  `GetExitCodeProcess`. `deny_read_resolver.rs` is still NOT ported: the Hive
  resolver's deny-read set is legitimately empty (no secret-path source yet), so
  there is nothing to resolve; `compute_allow_paths` propagates carve-outs
  already, for when a future `SandboxPolicy` extension adds them.
- `hive-desktop-sandbox/src/bin/hive-command-runner.rs`: the per-task runner
  binary (`tty = false` only; `tty = true` is refused fail-closed, ConPTY is
  Step 5). Port of the `bin/command_runner/win.rs` `tty = false` branch.
- `hive-desktop-sandbox/src/bin/hive-sandbox-provision.rs`: the elevated
  provisioning worker (port of `bin/setup_main` intent). It OMITS upstream
  `setup_main/win.rs`'s WFP + COM-firewall call sites (`install_wfp_filters`,
  `ensure_offline_proxy_allowlist`, `ensure_offline_outbound_block`); those are
  Integration B. It is deliberately NOT named `*-setup`/`*-install`: an
  unmanifested exe whose filename matches Windows installer-detection heuristics
  is auto-elevated by the OS, so the name avoids those trigger words to keep the
  correct default `asInvoker` behaviour without an embedded manifest.
- `hive-desktop-sandbox/src/windows.rs`: doc-only wiring pointing `launch` at
  the A2 elevated compose and the lab-only validation entry point; the two
  network-refusal guards are unchanged and are the `LaunchDecision` made real.

### Local deviations from upstream (Integration A2, subcrate)

Two now-wired-path items the A1 pass explicitly deferred to A2. Both are
`#[cfg(windows)]`-only, cross-checked by the `x86_64-pc-windows-gnu` CI step,
and lab-gated on `spike307-win`. Re-vendoring note: re-copying these two files
must re-apply these deviations (checking first whether upstream added an
equivalent).

- `cap.rs` (open risk #8, cross-process half): `load_or_create_cap_sids` and
  the two per-key accessors now also hold a named OS mutex
  (`CreateMutexW`/`WaitForSingleObject`/`ReleaseMutex`, keyed to a stable hash of
  the canonical `cap_sid` path) for the same critical section as the A1
  intra-process `CAP_SID_LOCK`. This closes the elevated-provisioning-binary vs
  runner cross-process race that A1 left open. Open risk #8 is now RESOLVED.
- `helper_materialization.rs` (blueprint security boundary #8, absolute-path
  guard): added `is_fully_qualified_launch_path` and routed the two fallback
  branches of `resolve_current_exe_for_launch`/`resolve_exe_for_launch` through
  it, so a resolution failure can never hand back a bare, cwd/PATH-resolvable
  runner name for elevated execution (a plant there is a direct escalation); the
  fallback is anchored under the trusted helper bin dir instead.

### PR #401 review round (CodeRabbit/Greptile findings, Integration A2 launch wiring)

An opus security review PASSED PR #401 MERGE-WITH-TRACKED-FOLLOWUP before this
round; the items below are the bot findings triaged against that disposition.
Each is either fixed (additive, does not alter the D-004 lab-validated
spawn/confinement path for the already-passing case) or explicitly deferred
with the reason a blind fix would risk invalidating that lab run.

**Fixed this round:**

- `cap.rs`'s cross-process mutex now fails CLOSED instead of open:
  `acquire_cross_process_cap_lock` returns `std::io::Result` instead of
  `Option`, propagating `CreateMutexW` failure and a `WAIT_FAILED` result from
  `WaitForSingleObject` as errors (previously both were silently swallowed,
  the callers proceeded under only the intra-process lock, and a second
  concurrent racer could overwrite the other's `cap_sid` entry). All three
  call sites (`load_or_create_cap_sids`, `workspace_cap_sid_for_cwd`,
  `writable_root_cap_sid_for_path`) now propagate the error with `?` and abort
  the read-modify-write on lock failure. Refines, not reopens, open risk #8
  below (still RESOLVED; this closes the fail-open edge of that fix).
- `helper_materialization.rs::resolve_helper_for_launch`'s failure branch
  still called `legacy_lookup` directly, which is exactly the bare-name gap
  item #6(a) below said would be "fixed with the A2 launch wiring" -- this IS
  that wiring, and the guard was missing. Now routes the legacy fallback
  through `is_fully_qualified_launch_path` (the same guard
  `resolve_exe_for_launch` already used) and anchors a non-qualified result
  under the trusted helper bin dir instead of handing `CreateProcessWithLogonW`
  a bare, cwd/PATH-resolvable name. Closes item #6(a).
- `windows_elevated.rs`'s elevated spawn path had no Job Object at all (unlike
  the base variant's Step 1 `windows.rs::create_job_object`), so an IPC write
  failure (the `SpawnReady` frame) or a parent disconnect after spawn orphaned
  the restricted child with nothing left to stop it. Added
  `confine_child_to_job`/`ChildJobGuard` in `spawn_and_stream`: assigns the
  child to a `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` job immediately after spawn,
  RAII-closed (and thus kill-on-close) on any early return, a no-op close once
  `stream_child` returns normally (the child has already exited by then). Adds
  the `Win32_System_JobObjects` windows-sys feature to
  `hive-desktop-sandbox/Cargo.toml`. Residual: the child is not
  `CREATE_SUSPENDED` (upstream `create_process_as_user` never suspends it, and
  patching that vendored primitive is out of scope here), so a narrow
  spawn-to-assign window without job containment remains; unchanged from
  before this fix and far smaller than the gap closed.
- `hive-sandbox-validate.rs`: added a fail-closed input guard rejecting
  `HIVE_SANDBOX_HOME`/`HIVE_VALIDATE_WS` values containing whitespace or
  `cmd.exe` metacharacters (`& | < > ^ % "`) before they reach the unescaped
  `cmd.exe /c` script (see that file's own comment on why proper escaping is
  not attempted there). Not a full escaping fix: this is lab-only tooling
  whose env vars are operator-supplied, not attacker input, so failing closed
  on a bad value fully closes the risk without solving general `cmd.exe`
  quoting. The existing lab-validated paths (no whitespace/metacharacters, per
  that file's own comment) pass the guard unchanged.

**Deferred this round (tracked, not silently ignored):**

- `desktop.rs::grant_winsta_desktop_access` (Greptile "Desktop Grant Outlives
  Runner"): the persistent `WINSTA_ALL_ACCESS`/`GENERIC_ALL`/`DESKTOP_ALL_ACCESS`
  grant to the shared sandbox account was never revoked, so a later process
  running as that account on the same station/desktop also inherited it. It was
  a known, non-blocking gap for Integration A (unreachable then: `windows::launch`
  refuses every network policy, so nothing reached this grant outside the
  lab-only validation entry), with a HARD GATE on Integration B: do not remove
  the network-refusal guards until the runner moves to a dedicated, non-shared
  private window station.
  RESOLVED by Step 3 Integration B1 (see the "Integration B1: private window
  station for UI isolation" deviation below): `grant_winsta_desktop_access` and
  its shared-`WinSta0` grant are removed. The runner now launches on a
  per-launch PRIVATE window station + desktop that no other process shares, so
  the outlives-runner class no longer exists. The `windows::launch`
  network-refusal guards remain in force (B1 adds UI isolation only; WFP egress
  is still Integration B / D-005), so the HARD GATE's own precondition (WFP
  landing) is untouched by this change.
- `windows_elevated.rs::stream_child`'s sequential stdout-then-stderr drain
  (CodeRabbit "Sequential stdout-then-stderr drain can deadlock", Greptile
  "Sequential Pipe Drain Deadlocks", both on the same code): draining stdout
  to EOF before touching stderr can deadlock if the child fills the stderr
  pipe buffer while stdout stays open. The correct fix, a concurrent
  two-thread drain with the frame writer behind a shared lock, changes this
  exact D-004 lab-validated spawn/stream path from single- to multi-threaded
  framing. Deferred rather than blind-rewritten; needs a fresh spike307-win
  run once implemented. Inline comment added at the call site so this is
  tracked, not silently missed.
- `windows_elevated.rs::stream_child`'s ignored `timeout_ms` (Greptile
  "Request Timeout Is Ignored"): the wait is always `INFINITE` and
  `timed_out` is always `false`. Currently dormant, not reachable: the sole
  `SpawnRequest` construction site (`run_windows_sandbox_capture`, this file)
  always sets `timeout_ms: None`. A correct fix needs a watchdog concurrent
  with the drains (coupled to the drain-deadlock deferral above, since a hang
  during drain must also be bounded), so it is tracked with that fix rather
  than half-implemented as a bare final-wait timeout that a stuck drain would
  never reach. Inline comment added at the call site.

### Integration B1: private window station for UI isolation (Hive deviation)

Upstream `desktop.rs` isolates the UI only as far as an optional private
DESKTOP under the shared `WinSta0` (`LaunchDesktop::prepare(use_private_desktop)`),
and Hive's Integration A shipped `grant_winsta_desktop_access`, which granted the
sandbox account access to the interactive user's shared `WinSta0` so the
user32-linking runner could attach at load. Both leave the sandboxed process on
the real user's window station, where it can shatter-attack, `SendInput` to, or
read the clipboard of the real desktop (clipboard and the atom table are
per-window-station, shared across every desktop on that station).

B1 closes that hole by adding a Hive-native `desktop.rs::PrivateWindowStation`
(no upstream equivalent) and removing `grant_winsta_desktop_access`:

- The parent creates a per-launch PRIVATE window station
  (`CreateWindowStationW`, `CWF_CREATE_ONLY`, a `HiveSandboxWinSta-<random u128>`
  name) and a `HiveSandboxDesktop` on it (`SetProcessWindowStation` to the new
  station so `CreateDesktopW` lands there, then the parent's own station is
  restored). The sandbox account SID is granted on both objects (reusing
  `merge_grant_on_window_object`) as insurance for a restrictive default DACL.
- `runner_client.rs::spawn_runner_transport` sets
  `STARTUPINFO.lpDesktop = "HiveSandboxWinSta-<..>\\HiveSandboxDesktop"` on the
  `CreateProcessWithLogonW` launch. The spike (spike307-win, 2026-07-20, cl.exe
  probe) proved `CreateProcessWithLogonW` (Secondary Logon) honours a
  parent-created private window station named in `lpDesktop`: the child ran as
  the sandbox SID on the private station and exited cleanly.
- Fail closed (D-005): if the private station cannot be created (e.g. a
  de-elevated caller in session 0 gets `ERROR_ACCESS_DENIED`) the launch errors
  and NEVER falls back to `WinSta0`. Assumes the caller runs in the interactive
  logged-in user's session, where window-station creation is permissive
  (documented in `PrivateWindowStation::create`).

Scope: B1 is UI isolation ONLY. The two `windows::launch` network-refusal guards
are untouched; both `NetworkPolicy::DenyAll` and `AllowHosts` still fail closed
until the WFP egress backend lands (Integration B / D-005). This code is
cross-compiled by CI (`x86_64-pc-windows-gnu`) but, like the rest of the Win32
compose, its behaviour is only lab-provable on `spike307-win`.

### Deliberately NOT done in A2 (fail-closed stubs, honest)

- WFP / firewall egress (Integration B): the two `windows::launch`
  network-refusal guards stay; no egress control is authored. `DenyAll` under a
  standard sandbox user can still open sockets, so refusing (not spawning) is
  the only D-005-safe behaviour until WFP lands.
- ConPTY (`tty = true`, Step 5): the command runner refuses a `tty = true`
  request with an `Error` frame, never a silent downgrade to pipes.
- App-side `ShellExecute "runas"` elevation trigger (the single UAC consent): it
  lives in the desktop shell (Tauri), not in this OS-mechanism crate. The
  provisioning binary is the elevated worker; `windows_elevated` only gates on
  `sandbox_setup_is_complete` and errors if unprovisioned.

### Why not `codex-rs/windows-sandbox-rs` for the Windows backend (historical, #395 wave; superseded in part by Step 3 above)

This section predates Step 3 and explains why the #395 wave (the base,
non-elevated Windows backend) did not vendor `windows-sandbox-rs` at all. Step
3 Wave 1 above now vendors a real subset of it. The reasoning below (WFP,
DPAPI, elevated helper, ConPTY, a dedicated setup binary are out of scope for
the BASE variant) still holds for why #395 itself stayed hand-authored; it is
Step 3, not #395, that draws on this upstream crate.

`codex-rs/windows-sandbox-rs` (`codex-windows-sandbox`) exists upstream and is
Apache-2.0, but it is a much larger, Codex-CLI-specific system: Windows
Filtering Platform (WFP) network filtering, DPAPI, an elevated low-privilege
helper process, ConPTY, a dedicated setup binary
(`codex-windows-sandbox-setup`) that provisions a sandbox OS user, ACL
resolution against Codex's own policy types, etc. Vendoring it would pull in
Codex's policy/config model and a Windows-user-provisioning flow this wave
does not need. Blueprint Step 4.2's Windows scope (restricted token +
directory ACL + Job Object, base non-elevated variant) is deliberately
smaller, so `hive-desktop-sandbox::windows`/`windows_plan` are newly authored
against plain Win32 contracts instead. `codex-windows-sandbox`'s WFP-based
egress filtering is the closest upstream precedent for blueprint Step 4.4's
Windows Firewall / egress-SSOT work and its elevated-helper-user pattern is
the closest precedent for the elevated variant mentioned below; both are
worth a follow-up vendoring pass when that step starts, but are out of scope
here.

## Newly authored (Hive-proprietary, not Apache-2.0)

Everything under `hive-desktop-sandbox/`: `policy.rs` (`SandboxPolicy`,
`NetworkPolicy`, the CBSE-mitigation invariant from security spike #307),
`linux.rs` (bwrap invocation + Landlock + seccomp-BPF), `windows.rs` (Win32
calls), `windows_plan.rs` (the pure, host-independent Windows enforcement
plan), and the public `lib.rs` (`launch`/`LaunchError`). `Cargo.toml` for this
crate deliberately does not inherit the workspace's Apache-2.0
`license.workspace = true` (see the crate's own `Cargo.toml` comment).

## Verification performed this pass

- `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`, and
  `cargo test` all pass on `x86_64-unknown-linux-gnu` (Docker `rust:1-bookworm`,
  toolchain pinned by `rust-toolchain.toml`; `libcap-dev`/`pkg-config`
  installed so `codex-bwrap`'s `build.rs` actually compiles vendored
  bubblewrap, not `CODEX_SKIP_BWRAP_BUILD`-skipped). 16 tests pass across the
  three crates (0 in `codex-bwrap`, 2 in `codex-process-hardening`, 14 in
  `hive-desktop-sandbox`).
- `cargo check`/`cargo clippy --all-targets` also pass cross-compiled to
  `x86_64-pc-windows-gnu`, so `windows.rs` at least type-checks against
  `windows` crate 0.58's real signatures (several Win32 call shapes needed
  fixing during this pass: `EXPLICIT_ACCESS_W.grfInheritance` needs
  `ACE_FLAGS`, not a bare `u32`; `SetEntriesInAclW`/`SetNamedSecurityInfoW`
  return `WIN32_ERROR` with an `.ok()` helper, not `windows::core::Result`
  directly; `LocalFree` takes `HLOCAL` by value, not `Option<HLOCAL>`;
  `CreateRestrictedToken` takes an out-pointer `*mut HANDLE` as its 6th
  argument rather than returning the handle).
- `windows_plan.rs`'s pure logic (the module explicitly designed to be
  meaningfully testable on Linux CI, see its module doc) had a real bug this
  pass fixed: `parent_dir` used `std::path::Path::parent()`, which parses
  `\` as an ordinary filename character on a non-Windows host, so
  `C:\Users\...\hooks`'s "parent" came back empty on Linux. Replaced with
  explicit `\`/`/`-aware string parsing so the enforced-default tests are
  actually meaningful on the Linux CI job, not silently vacuous.
- Not done: an actual behavioral run of `windows.rs` on Windows. See "Open
  risks" below.

### Security review follow-up (same PR, second pass)

- `linux.rs`'s Landlock ruleset now sets `CompatLevel::HardRequirement`
  (was the `landlock` crate's `BestEffort` default, which silently no-ops on
  a kernel without Landlock support instead of erroring). Linux is the
  demo-live confinement path, so a kernel that can't enforce the requested
  ruleset must fail loudly rather than let the sandboxed process run
  unconfined. Added `landlock_hard_requirement_never_silently_no_ops`,
  which behaviorally proves enforcement (write outside the writable roots
  fails, write inside succeeds) whenever the kernel does support Landlock,
  and accepts a loud error as the only other valid outcome.
- `windows_plan.rs`'s `parent_dir` had a second edge case: for a hook/config
  dir one level below a drive root (e.g. `C:\hooks`), it returned `"C:"`,
  which Win32 treats as "current directory on drive C", not the drive root;
  fixed to return `"C:\"` .
- `windows::launch` was disabled outright at that pass; it has since been
  wired to `CreateProcessAsUserW` (Open risks #1 and #4 below, now resolved).

## Open risks / follow-up (not this wave's scope)

1. **RESOLVED (Step 1, `c8c1434b` / #395): launch seam wired.** Confinement
   strength beyond the seam itself is explicitly PARTIAL and tracked
   separately below (items #3 and #6), not reopened here.
   `windows::launch` was disabled
   because `std::process::Command` cannot spawn under an alternate token. It
   now calls `CreateProcessAsUserW` directly under a distinct primary token,
   applies the directory ACL, creates the child `CREATE_SUSPENDED`, assigns it
   to the Job Object, then `ResumeThread`s (which also closes item #4). Any
   confinement step that fails returns an error instead of spawning
   unconfined, so the crate's honesty invariant holds. What Step 1 ACTUALLY
   enforces: the parent-directory deny-write ACL and the kill-on-close Job
   Object. What it does NOT (no overclaiming): (a) the token is an
   UNRESTRICTED duplicate (`CreateRestrictedToken` with flags `0` and empty
   disable/restrict/delete lists), so it does not reduce the child's
   privileges -- real restriction / Low IL / SID disabling is the Step 3
   sandbox-user variant (item #6); (b) network confinement -- `launch` refuses
   BOTH `DenyAll` (`NetworkConfinementNotImplemented`) and `AllowHosts`
   (`AllowHostsNotYetImplemented`) rather than run under an unenforced egress
   policy (item #3). CI now cross-compiles this file: the `rust-tests` job runs
   `cargo check` and `cargo clippy` for `x86_64-pc-windows-gnu -p
   hive-desktop-sandbox`, so the Win32 call shapes are type-checked against
   `windows`-crate 0.58 on every PR. MSVC compilation and any behavioral run
   are still lab-pending (item #2), so do not treat this as validated on a real
   Windows host yet.
2. **Windows backend is untested on real Windows.** `windows.rs` now
   type-checks and lints clean cross-compiled to `x86_64-pc-windows-gnu` (this
   cross-check runs in the `rust-tests` CI job on every PR, not just locally),
   but has never been compiled with the MSVC toolchain or run. Needs
   `cargo check --target x86_64-pc-windows-msvc` (CI does not do this; MSVC
   needs a Windows or cross-linker toolchain) plus a behavioral run in the lab
   (`win11vm`) before it is trusted, per the module's own doc comment.
3. **`NetworkPolicy::AllowHosts` enforcement -- Linux closed, Windows still
   codegen-only (blueprint Step 4.4, #308/#311).** `linux::launch` now
   always `--unshare-net`s (for `AllowHosts` too, not only `DenyAll`) and
   bind-mounts a real allowlist-enforcing proxy
   (`egress_proxy.rs`'s `AllowlistProxy`, a CONNECT-only forward proxy on a
   Unix socket) plus a tiny relay binary (`src/bin/hive-egress-shim.rs`,
   `shim.rs`) that bridges that socket to a loopback `HTTP_PROXY` inside the
   sandbox's own netns. Real, live-tested in this crate's own test suite
   (`egress_proxy.rs`, `shim.rs`): an unlisted host gets a 403 and is never
   dialed. Not exercised end-to-end with a real bwrap process in this
   repo's test suite (no test here spawns real bwrap at all, for either
   network variant -- that gap predates this pass); the proxy/relay layer
   itself is the actual policy-enforcement point and is real.
   `windows::launch` now runs for `DenyAll` (item 1 resolved), but still
   rejects `AllowHosts` with `LaunchError::AllowHostsNotYetImplemented` rather
   than launch under an unenforced network policy, so `AllowHosts` stays fully
   unenforced on Windows (WFP egress is a later step). `windows_plan.rs` also
   computes the Windows
   Firewall rule text for `AllowHosts` (`allow_hosts_firewall_script`,
   deny-outbound-by-default plus a per-host `netsh advfirewall` allow
   exception) as pure, unit-tested codegen -- explicitly not applied by
   this crate, since there is no live Windows launch path to apply it to
   yet. Do not treat the existence of that codegen as Windows enforcement:
   there is none until this rule text is actually wired to `netsh`/WFP and
   lab-verified.
4. **RESOLVED (Step 1).** The Job Object assignment race is closed: the child
   is created `CREATE_SUSPENDED`, `AssignProcessToJobObject` runs before the
   child executes a single instruction, and only then does `ResumeThread`
   release it. The returned `SandboxChild` owns the sole job handle via RAII,
   so a leaked or dropped handle cannot silently break kill-on-close. (Was: a
   `std::process::Command` child could only be assigned after `spawn()` had
   already let it run, and `Child` does not expose the primary thread handle.)
5. **AppArmor userns profile (`assets/apparmor/hive-bwrap-userns`) is
   untested in the lab.** Needed for Ubuntu 24.04+'s
   `kernel.apparmor_restrict_unprivileged_userns=1`, which otherwise blocks
   bubblewrap's `--unshare-user` for any process without an explicit `userns`
   grant. Written against the documented Ubuntu pattern but not loaded and
   exercised against a real restricted-userns box in this session.
6. **IN PROGRESS (Step 3): Windows restricted token is the base variant
   only.** No SIDs disabled, no privileges deleted beyond
   `CreateRestrictedToken`'s own defaults, and it restricts the *current*
   process's token rather than provisioning a dedicated low-privilege sandbox
   OS user. `codex-rs/windows-sandbox-rs`'s elevated-helper-user pattern (see
   above) is the precedent for that variant. Execution is
   `blueprint-step3-elevated-windows-sandbox.md` (project vault): Wave 1
   vendored the mechanism primitives (`codex-windows-sandbox/`) and ported
   the policy adapter (`windows_resolve.rs`). Wave 2 (this PR) vendors the
   elevated-helper IPC mechanism (`elevated/{ipc_framed,runner_pipe,
   runner_client}`, `helper_materialization.rs`, `setup_error.rs`, a trimmed
   `sandbox_users.rs`), all still inert and not wired into `windows::launch`.
   Wave 2 also found the `codex_protocol`/`resolved_permissions.rs` coupling
   is deeper than originally scoped: `spawn_prep.rs`, `wrapper.rs`,
   `elevated_impl.rs`, the full `identity.rs`, the full `setup.rs`, and both
   binaries (`setup_main`, `command_runner`, which additionally pull in WFP,
   ConPTY, and a new `codex-otel` telemetry dependency) are deferred to a
   later wave that ports them into `hive-desktop-sandbox`'s proprietary
   `windows_elevated.rs`, per this file's own "Deliberately NOT vendored this
   wave" section above. Remaining work (provisioning/identity port, the two
   binaries, and the actual launch-path wiring) stays open. Mark RESOLVED
   only when a later wave lab-validates the isolation matrix on
   `spike307-win` (assertions L7 to L12 in the blueprint).

   **Known issues, fix in Integration A (CodeRabbit/Greptile, PR #399):**
   (b) and (c) plus the wider security-review hardening were FIXED in
   Integration A1 (see this file's "Integration A1" section: `read_frame`
   version gate, `SandboxCreds` redacting `Debug` + zeroize-on-drop, password
   buffer zeroize in `runner_client`/`sandbox_users`, `runner_client` frame
   pre-check, `absolute_path` traversal guard, and the intra-process half of
   the `cap.rs` open-risk-#8 lock).
   (a) **RESOLVED (PR #401 review round, see "PR #401 review round" section
   above).** `helper_materialization.rs::legacy_lookup`'s bare-executable-name
   fallback let `CreateProcessWithLogonW` fall back to executable search
   instead of failing closed. The A2 launch wiring itself (this PR) called
   `legacy_lookup` from `resolve_helper_for_launch` without the absolute-path
   guard this note predicted it would need; Greptile caught the gap in review,
   and it is now closed by routing that fallback through
   `is_fully_qualified_launch_path`.
7. **RESOLVED (#350).** The Linux `pre_exec` post-fork allocator-deadlock
   hazard below was fixed by #350 (merged), which moved `linux::launch`'s
   `pre_exec` closure to an allocation-free path so no thread can deadlock on
   the allocator's lock immediately after `fork()`. Original hazard, kept for
   the regression record: `linux::launch`'s real `Command::spawn()` +
   `pre_exec` path was a post-fork allocator-deadlock hazard, discovered in
   the #308/#311 pass. `pre_exec`'s closure runs in a raw-`fork()`ed child
   (required so it can run arbitrary code before `exec`); `apply_landlock_ruleset`
   and `apply_seccomp_denylist` both allocated (`Vec`, `PathFd::new`, `format!`).
   Allocating in a forked child of a multithreaded parent risks the classic
   post-fork malloc deadlock (another thread can hold the allocator's lock
   at the instant of `fork()`, and the single surviving child thread then
   blocks on it forever) -- and because Rust's `pre_exec` machinery makes
   the *parent's* `spawn()` block on a pipe read until the child execs or
   reports an error, that hang propagated back to the caller too. Directly
   observed at the time: an end-to-end `launch()` test (removed, see
   `linux.rs`'s test module comment) hung `cargo test` for 15+ minutes in
   this crate's own (multithreaded) test binary. Pre-existing at the time: no
   test before that pass ever exercised the real spawn path, so nothing
   surfaced it earlier.
8. **RESOLVED (Integration A1: intra-process lock; Integration A2: cross-process
   mutex). Original issue (`cap.rs`, CodeRabbit finding, PR #398): the `cap_sid`
   file is an unsynchronized read-modify-write.** Integration A1 added a
   process-wide `Mutex` (`CAP_SID_LOCK`) that serializes the read-modify-write
   and re-reads under the lock in `load_or_create_cap_sids`,
   `workspace_cap_sid_for_cwd`, and `writable_root_cap_sid_for_path`, closing the
   intra-process race (a concurrency regression test proves 8 concurrent
   first-touches share one SID and persist exactly one entry). Integration A2
   added a named OS mutex (`CreateMutexW`, keyed to a stable hash of the
   canonical `cap_sid` path) held for the same critical section at all three
   sites, closing the CROSS-PROCESS race (the elevated provisioning binary
   racing the runner against the same sandbox home), as upstream `setup_main`
   does with its `read_acl_mutex`. Its behavioural proof (a real concurrent
   launch) is a `spike307-win` lab item. **Refined in the PR #401 review
   round** (see that section above): the mutex acquisition itself now fails
   CLOSED (propagates `CreateMutexW`/`WaitForSingleObject` failure as an error)
   instead of silently falling back to fail-open. Original detail retained below.
   `workspace_cap_sid_for_cwd` and `writable_root_cap_sid_for_path` both
   `load_or_create_cap_sids` (read + parse JSON), mutate the in-memory
   `CapSids`, then `persist_caps` (serialize + `fs::write`) the whole file
   back, with no file lock, mutex, or atomic replace between the read and
   the write. Two concurrent callers against the same `codex_home` can race:
   both read the same base state, each adds its own
   `workspace_by_cwd`/`writable_root_by_path` entry, and whichever
   `persist_caps` writes last silently drops the other's entry (and its
   already-applied ACL SID becomes orphaned). This is reachable in the
   intended production design, not merely theoretical: under the Step 3
   Q1/Q3 decision (one shared low-privilege sandbox OS user, not one per
   session), concurrent agent-engine sandbox launches on Windows share that
   one user's `codex_home`, hence one `cap_sid` file. Not guess-fixed this
   pass: `cap.rs` is still fully inert (nothing calls it from
   `windows::launch` yet, see item #6), and the right fix (`LockFileEx`
   around the read-modify-write, a named mutex, or an atomic
   rename-based compare-and-swap) needs a real concurrent-launch test on
   Windows to trust the chosen approach. Fix and lab-validate together with
   whichever of Wave 3 or Wave 4 first makes concurrent launches reachable
   (the same wave that wires the sandbox-user provisioning and the actual
   `cap.rs` launch-path call sites).

## Updating this vendor copy

1. Re-check the current commit/layout: `gh api repos/openai/codex/contents/codex-rs`.
2. Re-copy `codex-rs/bwrap/`, `codex-rs/process-hardening/`, and
   `codex-rs/vendor/bubblewrap/` verbatim (package names and `Cargo.toml`
   should not need renaming; they already match upstream).
3. Re-copy `codex-rs/windows-sandbox-rs/src/{token,cap,proc_thread_attr,acl,
   workspace_acl,deny_read_acl,deny_read_state,dpapi,hide_users,winutil,
   sandbox_utils,env,path_normalization}.rs` verbatim into
   `codex-windows-sandbox/src/`. Re-diff `resolved_permissions.rs` against
   `hive-desktop-sandbox/src/windows_resolve.rs` by hand (it is a port, not a
   verbatim copy) for any new upstream fields/accessors worth porting.
   Also re-copy `codex-rs/windows-sandbox-rs/src/{process,desktop}.rs` verbatim
   (Integration A1); `desktop.rs` is byte-for-byte upstream, `process.rs` is
   NOT (see the PR #400 deviations below). If `deny_read_resolver.rs`
   is ever vendored/ported (see "Still deferred after A1" above), re-check its
   dependency graph against `codex_protocol`/`codex_utils_absolute_path` again;
   upstream may have changed the coupling. NOTE the Integration A1 security
   deviations that make several Wave 1/2 files no longer byte-identical to
   upstream (`elevated/ipc_framed.rs`, `elevated/runner_client.rs`,
   `identity.rs`, `sandbox_users.rs`, `cap.rs`, `absolute_path.rs`), PLUS the
   PR #400 `process.rs` deviations (`make_env_block`'s empty-environment
   double-NUL terminator, `create_process_as_user`'s `None`-branch handle-list
   restriction); re-apply them the same way as the other documented
   deviations, checking first whether upstream fixed the same items.
3a. Re-copy `codex-rs/windows-sandbox-rs/src/{elevated/ipc_framed,
   elevated/runner_pipe,elevated/runner_client,elevated/mod,
   helper_materialization,setup_error}.rs` verbatim into
   `codex-windows-sandbox/src/` (Step 3 Wave 2), re-applying the same small
   deviations documented in this file's Wave 2 section (the
   `permission_profile`/`absolute_path` stand-in swap in `ipc_framed.rs`, the
   `crate::setup::X` import-path fix in `helper_materialization.rs`, the
   inlined `sanitize_metric_tag_value` in `setup_error.rs`). Re-copy
   `bin/setup_main/win/sandbox_users.rs` into `codex-windows-sandbox/src/sandbox_users.rs`,
   re-trimming to the OS-account primitives and re-applying the local
   `log_line` stand-in. Re-check whether `spawn_prep.rs`, `wrapper.rs`,
   `elevated_impl.rs`, `identity.rs`, `setup.rs`, or the two binaries have
   become separable from `resolved_permissions.rs`/WFP/ConPTY upstream; if
   so, that is the trigger to plan the next wave, not to vendor them here.
4. Re-copy the repo-root `LICENSE`/`NOTICE` if changed.
5. Re-run `cargo fmt --check && cargo clippy --all-targets -- -D warnings &&
   cargo test`, plus the `x86_64-pc-windows-gnu` cross-check (now also run by
   the `rust-tests` CI job, scoped to `-p hive-desktop-sandbox -p
   codex-windows-sandbox`), and the MSVC cross-check
   (`cargo check --target x86_64-pc-windows-msvc -p hive-desktop-sandbox -p
   codex-windows-sandbox`, lab or a Windows host only; CI still cannot link
   MSVC) before merging.
6. Update the commit SHA/date at the top of this file.
