//! Minimal, Hive-authored stand-in for upstream `logging.rs`
//! (`codex-rs/windows-sandbox-rs/src/logging.rs`). NOT vendored: upstream's
//! real version pulls in `tracing-appender`, `chrono`, and the
//! `codex-utils-string` crate to write rotating, CODEX_HOME-scoped log files.
//! None of that is wired to anything in this wave (`hide_users.rs` is
//! vendored but not yet called from any Hive launch path), so this stand-in
//! only needs to preserve `log_note`'s call shape for `hide_users.rs` to
//! compile, not its rotating-file persistence.
//!
//! Real logging (matching whatever Hive's own agent-engine/desktop logging
//! convention turns out to be, not upstream's CODEX_HOME file rotation) is
//! deferred to whichever later wave first calls `hide_users::hide_newly_created_users`
//! or `hide_users::hide_current_user_profile_dir` for real. See
//! `../VENDORING.md`.

use std::path::Path;

/// Writes `msg` to stderr. `base_dir` is accepted (matching upstream's
/// signature so `hide_users.rs` stays byte-for-byte) but unused: this
/// stand-in never writes to a log file.
pub fn log_note(msg: &str, _base_dir: Option<&Path>) {
    eprintln!("[codex-windows-sandbox] {msg}");
}
