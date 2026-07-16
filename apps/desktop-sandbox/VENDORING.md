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
- `windows::launch` is now disabled outright; see Open risks #1.

## Open risks / follow-up (not this wave's scope)

1. **Windows sandbox `launch` is disabled (security review on PR #335).**
   `create_restricted_token` builds a restricted token, but nothing applies
   it to the spawned process: `std::process::Command` has no
   `CreateProcessAsUserW`/`CreateProcessWithTokenW` equivalent, so a child
   spawned through it always inherits the *caller's* full token. Shipping
   that silently would mean this crate's own docs describe a confinement
   layer that doesn't exist. `windows::launch` therefore always returns
   `LaunchError::Confinement` before spawning anything, rather than
   launching a process under a false confinement claim. The fix is to call
   `CreateProcessAsUserW` (or `CreateProcessWithTokenW`) directly with
   `restricted_token` instead of going through `std::process::Command`,
   which would also let it fix item 3 below (`CREATE_SUSPENDED` +
   `ResumeThread`) in the same pass, since both need the raw `CreateProcessW`
   call. Do not re-enable the launch path without this wired and a
   behavioral lab run (item 2).
2. **Windows backend is untested on real Windows.** `windows.rs` now
   type-checks and lints clean cross-compiled to `x86_64-pc-windows-gnu`, but
   has never been compiled with the MSVC toolchain or run. Needs
   `cargo check --target x86_64-pc-windows-msvc` plus a behavioral run in the
   lab (`win11vm`) before it is trusted, per the module's own doc comment.
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
   `windows::launch` is unchanged: it still refuses to run at all,
   regardless of network policy (item 1), so `AllowHosts` stays fully
   unenforced there. `windows_plan.rs` now also computes the Windows
   Firewall rule text for `AllowHosts` (`allow_hosts_firewall_script`,
   deny-outbound-by-default plus a per-host `netsh advfirewall` allow
   exception) as pure, unit-tested codegen -- explicitly not applied by
   this crate, since there is no live Windows launch path to apply it to
   yet. Do not treat the existence of that codegen as Windows enforcement:
   there is none until item 1 is closed and this rule text is actually
   wired to `netsh`/WFP and lab-verified.
4. **Windows Job Object assignment race.** Dormant while `launch` is disabled
   (item 1), but still true of the written-but-uncalled code: the sandboxed
   child would be assigned to the Job Object immediately after `spawn()`, not
   atomically via `CREATE_SUSPENDED` + `ResumeThread` (`Command` doesn't
   expose the primary thread handle needed to resume a suspended process).
   Closed by the same `CreateProcessW`-direct rewrite as item 1.
5. **AppArmor userns profile (`assets/apparmor/hive-bwrap-userns`) is
   untested in the lab.** Needed for Ubuntu 24.04+'s
   `kernel.apparmor_restrict_unprivileged_userns=1`, which otherwise blocks
   bubblewrap's `--unshare-user` for any process without an explicit `userns`
   grant. Written against the documented Ubuntu pattern but not loaded and
   exercised against a real restricted-userns box in this session.
6. **Windows restricted token is the base variant only.** No SIDs disabled,
   no privileges deleted beyond `CreateRestrictedToken`'s own defaults, and it
   restricts the *current* process's token rather than provisioning a
   dedicated low-privilege sandbox OS user. `codex-rs/windows-sandbox-rs`'s
   elevated-helper-user pattern (see above) is the precedent for that
   variant when it's prioritized. Moot until item 1 is wired.
7. **`linux::launch`'s real `Command::spawn()` + `pre_exec` path is a
   post-fork allocator-deadlock hazard, discovered this pass (#308/#311).**
   `pre_exec`'s closure runs in a raw-`fork()`ed child (required so it can
   run arbitrary code before `exec`); `apply_landlock_ruleset` and
   `apply_seccomp_denylist` both allocate (`Vec`, `PathFd::new`, `format!`).
   Allocating in a forked child of a multithreaded parent risks the classic
   post-fork malloc deadlock (another thread can hold the allocator's lock
   at the instant of `fork()`, and the single surviving child thread then
   blocks on it forever) -- and because Rust's `pre_exec` machinery makes
   the *parent's* `spawn()` block on a pipe read until the child execs or
   reports an error, that hang propagates back to the caller too. Directly
   observed this pass: an end-to-end `launch()` test (removed, see
   `linux.rs`'s test module comment) hung `cargo test` for 15+ minutes in
   this crate's own (multithreaded) test binary. Pre-existing: no test
   before this pass ever exercised the real spawn path, so nothing
   surfaced it earlier; it is not new in this pass, only newly visible.
   Fix needs either an alloc-free `pre_exec` closure or moving off
   fork+pre_exec toward a `posix_spawn`-style API -- out of scope here.

## Updating this vendor copy

1. Re-check the current commit/layout: `gh api repos/openai/codex/contents/codex-rs`.
2. Re-copy `codex-rs/bwrap/`, `codex-rs/process-hardening/`, and
   `codex-rs/vendor/bubblewrap/` verbatim (package names and `Cargo.toml`
   should not need renaming; they already match upstream).
3. Re-copy the repo-root `LICENSE`/`NOTICE` if changed.
4. Re-run `cargo fmt --check && cargo clippy --all-targets -- -D warnings &&
   cargo test`, plus the `x86_64-pc-windows-gnu` cross-check, before merging.
5. Update the commit SHA/date at the top of this file.
