//! Minimal, verbatim FEW-FUNCTION subset of upstream `setup.rs`
//! (`codex-rs/windows-sandbox-rs/src/setup.rs`, commit `a47c661…`, see
//! `../VENDORING.md`), extracted so a handful of vendored mechanism modules
//! compile without pulling in the rest of upstream's `setup.rs`: OS-account
//! provisioning, `SandboxUsersFile`, `run_elevated_provisioning_setup`,
//! `run_setup_refresh*`, and the setup-marker machinery all call
//! `ResolvedWindowsSandboxPermissions::try_from_permission_profile*`
//! (upstream's `resolved_permissions.rs`, deliberately not vendored, see
//! `permission_profile.rs`'s module doc), so the rest of `setup.rs` is
//! excluded, belonging to a later wave that ports the resolver-coupled
//! orchestration into `hive-desktop-sandbox`'s proprietary code.
//!
//! Each function below is copied byte-for-byte from upstream; only the
//! surrounding file (which upstream calls `codex_home`, Codex's config
//! directory) is trimmed to these few path-helper functions:
//! - `sandbox_dir`: needed by `deny_read_state.rs` (Wave 1) and
//!   `helper_materialization.rs`/`sandbox_users.rs` (Wave 2).
//! - `sandbox_bin_dir`: needed by `helper_materialization.rs` (Wave 2).
//!
//! `sandbox_secrets_dir` is NOT included: its only upstream caller,
//! `write_secrets`, is excluded (see `sandbox_users.rs`'s module doc), and an
//! unused `pub fn` inside this private module still fails `-D warnings`
//! dead_code (module privacy, not just item privacy, gates reachability).
//! Add it back together with whichever wave ports `write_secrets`.

use std::path::Path;
use std::path::PathBuf;

pub fn sandbox_dir(codex_home: &Path) -> PathBuf {
    codex_home.join(".sandbox")
}

pub fn sandbox_bin_dir(codex_home: &Path) -> PathBuf {
    codex_home.join(".sandbox-bin")
}
