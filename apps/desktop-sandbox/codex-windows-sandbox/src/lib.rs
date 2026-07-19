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
//! and the two modules deliberately NOT vendored this wave
//! (`deny_read_resolver.rs`'s glob-scan resolver and `process.rs`'s
//! `CreateProcessAsUserW` wrapper) and why.
//!
//! This crate is inert this wave (Step 3 Wave 1 of
//! `plan-codex-crossplatform-desktop.md`): nothing here is called from
//! `hive-desktop-sandbox`'s `launch` path yet. It exists so the vendored
//! primitives are vendored, compiled, and unit-tested on their own, ahead of
//! the elevated-helper and launch-wiring waves that will actually call them.
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
#[cfg(windows)]
pub mod cap;
#[cfg(windows)]
pub mod deny_read_acl;
#[cfg(windows)]
pub mod deny_read_state;
#[cfg(windows)]
pub mod dpapi;
#[cfg(windows)]
pub mod env;
#[cfg(windows)]
pub mod hide_users;
#[cfg(windows)]
pub mod path_normalization;
#[cfg(windows)]
pub mod proc_thread_attr;
#[cfg(windows)]
pub mod sandbox_utils;
#[cfg(windows)]
pub mod token;
#[cfg(windows)]
pub mod winutil;
#[cfg(windows)]
pub mod workspace_acl;

// Not vendored from upstream: minimal, Hive-authored stand-ins so the two
// verbatim modules above that need them (`deny_read_state::sync_persistent_deny_read_acls`
// needs `setup::sandbox_dir`; `hide_users` needs `logging::log_note`) compile
// without pulling in upstream's CODEX_HOME-coupled `setup.rs` or the
// `tracing-appender`/`codex-utils-string`-coupled `logging.rs`. See
// `../VENDORING.md` for what each stand-in does and does not do.
#[cfg(windows)]
mod logging;
#[cfg(windows)]
mod setup;
