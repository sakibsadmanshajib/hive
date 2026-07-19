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
   `blueprint-step3-elevated-windows-sandbox.md` (project vault): Wave 1 (this
   PR) vendors the mechanism primitives (`codex-windows-sandbox/`) and ports
   the policy adapter (`windows_resolve.rs`), both inert and not yet wired
   into `windows::launch`. Waves 2 to 4 (elevated helper and provisioning,
   then the actual launch-path wiring) remain open. Mark RESOLVED only when
   Wave 4 lab-validates the isolation matrix on `spike307-win` (assertions L7
   to L12 in the blueprint).
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
   verbatim copy) for any new upstream fields/accessors worth porting. If
   `process.rs` or `deny_read_resolver.rs` are ever vendored/ported (see
   "Deliberately NOT vendored this wave" above), re-check their dependency
   graph against `desktop.rs`/`logging.rs`/`codex_protocol` again; upstream
   may have changed the coupling.
4. Re-copy the repo-root `LICENSE`/`NOTICE` if changed.
5. Re-run `cargo fmt --check && cargo clippy --all-targets -- -D warnings &&
   cargo test`, plus the `x86_64-pc-windows-gnu` cross-check (now also run by
   the `rust-tests` CI job, scoped to `-p hive-desktop-sandbox -p
   codex-windows-sandbox`), and the MSVC cross-check
   (`cargo check --target x86_64-pc-windows-msvc -p hive-desktop-sandbox -p
   codex-windows-sandbox`, lab or a Windows host only; CI still cannot link
   MSVC) before merging.
6. Update the commit SHA/date at the top of this file.
