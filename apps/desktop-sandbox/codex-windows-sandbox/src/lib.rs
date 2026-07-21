// Vendored files below are byte-for-byte upstream (see VENDORING.md); do not
// hand-edit them to satisfy lints, since that would defeat the point of
// verbatim vendoring (every future re-vendoring pass would need to re-apply
// the same patches by hand). Two lint classes fire purely from an edition
// mismatch between upstream's authoring edition and this workspace's
// `edition = "2024"`, not from any bug in the vendored code, and are allowed
// here instead:
// - `unsafe_op_in_unsafe_fn`: edition 2024 requires an explicit `unsafe {}`
//   block for every unsafe call even inside an `unsafe fn`'s own body (edition
//   2021 and earlier treat the whole body as an implicit unsafe context).
//   Upstream code predates this requirement; 135 call sites would need a
//   wrapping `unsafe {}` added to satisfy it under this workspace's edition.
// - `clippy::missing_safety_doc`: upstream documents `# Safety` on some but
//   not all `pub unsafe fn`s (this repo's own `rules/rust/coding-style.md`
//   requires a `// SAFETY:` comment on every `unsafe` block; upstream's own
//   convention is close but not total coverage).
// Verified via `cargo clippy --target x86_64-pc-windows-gnu -p
// codex-windows-sandbox -- -D warnings` (see VENDORING.md): every failure
// without these two allows is one of these two lints, none are a deeper
// clippy correctness lint.
#![allow(unsafe_op_in_unsafe_fn)]
#![allow(clippy::missing_safety_doc)]

//! Verbatim-vendored Windows sandbox mechanism primitives from
//! `openai/codex`'s `codex-rs/windows-sandbox-rs` crate (Apache-2.0). See
//! `../VENDORING.md` for the pinned commit, the full file-by-file provenance,
//! and the modules deliberately NOT vendored (`deny_read_resolver.rs`'s
//! glob-scan resolver, `spawn_prep.rs`, `allow.rs`, `wrapper.rs`,
//! `elevated_impl.rs`, the full `identity.rs` and `setup.rs`, and the
//! `setup_main`/`command_runner` binaries) and why. Integration A1 added the
//! `process.rs` (`CreateProcessAsUserW`) and `desktop.rs` capture-path
//! primitives that Wave 1 had deferred, and applied the W2 review security
//! must-fixes (see `../VENDORING.md`'s "Integration A1" section).
//!
//! This crate is inert (Step 3 Waves 1-2 + Integration A1 of `plan-codex-crossplatform-desktop.md`):
//! nothing here is called from `hive-desktop-sandbox`'s `launch` path yet. It
//! exists so the vendored primitives and elevated-helper IPC mechanism are
//! vendored, compiled, and unit-tested on their own, ahead of the
//! provisioning-port and launch-wiring waves that will actually call them.
//!
//! Every module below is real Win32 FFI (via `windows-sys`) with no
//! `#[cfg(windows)]` gate of its own (matching upstream, which is a
//! Windows-only crate). The gate is applied HERE, once, per module
//! declaration, so this crate compiles to an empty, valid crate on
//! non-Windows hosts (this repository's CI and dev environment are Linux) and
//! is exercised for real only by the dedicated
//! `cargo check --target x86_64-pc-windows-gnu -p codex-windows-sandbox` CI
//! step (see `../../.github/workflows/ci.yml`), mirroring how
//! `hive-desktop-sandbox`'s own `windows.rs` is gated and cross-checked.

#[cfg(windows)]
pub mod acl;
// `cap.rs` (upstream's synthetic random capability SIDs) was REMOVED by
// decision D-013. Those SIDs were only ever supplied to `CreateRestrictedToken`
// as restricting SIDs, and the restricting-SID array is now NULL (see
// `token.rs`), so a cap SID would no longer appear on the child token at all
// and any ACL grant addressed to one would be silently dead. Per-task write
// grants now target the dedicated sandbox account SID directly, matching the
// Anthropic Sandbox Runtime model (dedicated account plus per-task ACEs, no
// capability SIDs).
#[cfg(windows)]
pub mod deny_read_acl;
#[cfg(windows)]
pub mod deny_read_state;
#[cfg(windows)]
pub mod desktop;
#[cfg(windows)]
pub mod dpapi;
#[cfg(windows)]
pub mod env;
#[cfg(windows)]
pub mod helper_materialization;
#[cfg(windows)]
pub mod hide_users;
#[cfg(windows)]
pub mod identity;
#[cfg(windows)]
pub mod path_normalization;
#[cfg(windows)]
pub mod proc_thread_attr;
#[cfg(windows)]
pub mod process;
#[cfg(windows)]
pub mod sandbox_users;
#[cfg(windows)]
pub mod sandbox_utils;
#[cfg(windows)]
pub mod setup_error;
#[cfg(windows)]
pub mod token;
// Hive-owned WFP egress fence (Step 4 / Integration B2). Not upstream-verbatim:
// Hive GUIDs + names in `wfp`, and `wfp_setup` inverts upstream's
// log-and-continue to fail-closed. See `../VENDORING.md`.
#[cfg(windows)]
pub mod wfp;
#[cfg(windows)]
pub mod wfp_setup;
#[cfg(windows)]
pub mod winutil;
#[cfg(windows)]
pub mod workspace_acl;

// Elevated-helper IPC mechanism (Step 3 Wave 2). Upstream's own real lib.rs
// declares `elevated` as a private module and re-exports its three
// sub-modules flat at the crate root (`pub(crate) use elevated::ipc_framed;`
// etc.); vendored files below reference them via that same flat `crate::X`
// path, so the re-exports are reproduced here rather than switching those
// files to `crate::elevated::X` paths. Local deviation from upstream: `pub`
// rather than `pub(crate)`, matching this crate's own "everything pub,
// mod-nested" convention (every other module in this file is `pub mod`) so
// the re-export is exempt from `unused_imports` even though nothing in this
// wave's vendor set calls into `runner_client` yet (its only upstream callers,
// `elevated_impl.rs`/`wrapper.rs`/`command_runner/win.rs`, are excluded this
// wave; see `../VENDORING.md`).
#[cfg(windows)]
mod elevated;
#[cfg(windows)]
pub use elevated::ipc_framed;
#[cfg(windows)]
pub use elevated::runner_client;
#[cfg(windows)]
pub use elevated::runner_pipe;

// Not vendored from upstream: minimal, Hive-authored stand-ins so vendored
// modules that need them compile without pulling in upstream's
// CODEX_HOME-coupled `setup.rs`/`identity.rs`, the
// `tracing-appender`/`codex-utils-string`-coupled `logging.rs`, or a
// `codex_protocol`/`codex_utils_absolute_path` dependency. See
// `../VENDORING.md` for what each stand-in does and does not do.
#[cfg(windows)]
pub mod absolute_path;
#[cfg(windows)]
mod logging;
#[cfg(windows)]
pub mod permission_profile;
#[cfg(windows)]
mod setup;
