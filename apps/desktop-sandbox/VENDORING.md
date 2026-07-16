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

## Why not `codex-rs/windows-sandbox-rs` for the Windows backend

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

## Open risks / follow-up (not this wave's scope)

1. **Windows backend is untested on real Windows.** `windows.rs` now
   type-checks and lints clean cross-compiled to `x86_64-pc-windows-gnu`, but
   has never been compiled with the MSVC toolchain or run. Needs
   `cargo check --target x86_64-pc-windows-msvc` plus a behavioral run in the
   lab (`win11vm`) before it is trusted, per the module's own doc comment.
2. **`NetworkPolicy::AllowHosts` is not enforced.** Both `linux::launch` and
   `windows::launch` reject it outright (`LaunchError::AllowHostsNotYetImplemented`)
   rather than silently downgrading to allow-all or deny-all. The egress SSOT
   shape (#308/#319) round-trips through `SandboxPolicy` today; wiring actual
   enforcement (`--unshare-net` + a userspace proxy on Linux, WFP or a
   generated Firewall rule on Windows) is blueprint Step 4.4.
3. **Windows Job Object assignment race.** The sandboxed child is spawned via
   `std::process::Command` then assigned to the Job Object immediately after
   `spawn()`, not atomically via `CREATE_SUSPENDED` + `ResumeThread` (`Command`
   doesn't expose the primary thread handle needed to resume a suspended
   process). A future revision calling `CreateProcessW` directly can close
   this gap.
4. **AppArmor userns profile (`assets/apparmor/hive-bwrap-userns`) is
   untested in the lab.** Needed for Ubuntu 24.04+'s
   `kernel.apparmor_restrict_unprivileged_userns=1`, which otherwise blocks
   bubblewrap's `--unshare-user` for any process without an explicit `userns`
   grant. Written against the documented Ubuntu pattern but not loaded and
   exercised against a real restricted-userns box in this session.
5. **Windows restricted token is the base variant only.** No SIDs disabled,
   no privileges deleted beyond `CreateRestrictedToken`'s own defaults, and it
   restricts the *current* process's token rather than provisioning a
   dedicated low-privilege sandbox OS user. `codex-rs/windows-sandbox-rs`'s
   elevated-helper-user pattern (see above) is the precedent for that
   variant when it's prioritized.

## Updating this vendor copy

1. Re-check the current commit/layout: `gh api repos/openai/codex/contents/codex-rs`.
2. Re-copy `codex-rs/bwrap/`, `codex-rs/process-hardening/`, and
   `codex-rs/vendor/bubblewrap/` verbatim (package names and `Cargo.toml`
   should not need renaming; they already match upstream).
3. Re-copy the repo-root `LICENSE`/`NOTICE` if changed.
4. Re-run `cargo fmt --check && cargo clippy --all-targets -- -D warnings &&
   cargo test`, plus the `x86_64-pc-windows-gnu` cross-check, before merging.
5. Update the commit SHA/date at the top of this file.
