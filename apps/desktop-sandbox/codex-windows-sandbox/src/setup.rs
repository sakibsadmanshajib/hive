//! Minimal, verbatim ONE-FUNCTION subset of upstream `setup.rs`
//! (`codex-rs/windows-sandbox-rs/src/setup.rs`, commit `a47c661…`, see
//! `../VENDORING.md`), extracted so `deny_read_state.rs`'s
//! `sync_persistent_deny_read_acls` (which needs a base directory to persist
//! its state file under) compiles without pulling in the rest of upstream's
//! `setup.rs`: OS-account provisioning, `SandboxUsersFile`,
//! `run_elevated_provisioning_setup`, and the setup-marker machinery, all of
//! which are CODEX_HOME-coupled and belong to the Step 3 elevated-helper wave
//! (ported into `hive-desktop-sandbox`'s `windows_elevated.rs`, not vendored
//! here), per the module list in `../VENDORING.md`.
//!
//! `sandbox_dir` itself is copied byte-for-byte from upstream; only the
//! surrounding file (which upstream calls `codex_home`, Codex's config
//! directory) is trimmed to this one function.

use std::path::Path;
use std::path::PathBuf;

pub fn sandbox_dir(codex_home: &Path) -> PathBuf {
    codex_home.join(".sandbox")
}
