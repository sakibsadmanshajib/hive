//! Minimal, verbatim ONE-STRUCT subset of upstream `identity.rs`
//! (`codex-rs/windows-sandbox-rs/src/identity.rs`, commit `a47c661…`, see
//! `../VENDORING.md`), extracted so `elevated/runner_client.rs` and `elevated/runner_pipe.rs`'s
//! consumers (`retry_runner_spawn_once`, `spawn_runner_transport`) compile without pulling in
//! the rest of upstream's `identity.rs`: `sandbox_setup_is_complete`, `require_logon_sandbox_creds`,
//! and `refresh_logon_sandbox_creds` all take or return `&ResolvedWindowsSandboxPermissions`
//! (upstream's `resolved_permissions.rs`, deliberately not vendored this wave, see
//! `permission_profile.rs`'s module doc and `VENDORING.md`'s "Deliberately NOT vendored this
//! wave" section), so the rest of `identity.rs` is excluded, not vendored here.
//!
//! `SandboxCreds` itself is copied byte-for-byte from upstream; only the surrounding file
//! (credential loading, DPAPI unseal, setup-marker readiness checks) is trimmed away.

use zeroize::Zeroize;

/// Decoded sandbox-account logon credentials (username + cleartext password).
///
/// Integration A1 hardening (W2 review findings 3/6): `Debug` is hand-written
/// to redact the password so it never lands in a log line or panic message,
/// and the cleartext password is zeroized on drop so it does not linger in
/// freed heap. `Clone` is retained (the retry path clones creds); each clone
/// zeroizes its own copy on drop.
#[derive(Clone)]
pub struct SandboxCreds {
    pub username: String,
    pub password: String,
}

impl std::fmt::Debug for SandboxCreds {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("SandboxCreds")
            .field("username", &self.username)
            .field("password", &"<redacted>")
            .finish()
    }
}

impl Drop for SandboxCreds {
    fn drop(&mut self) {
        self.password.zeroize();
    }
}
