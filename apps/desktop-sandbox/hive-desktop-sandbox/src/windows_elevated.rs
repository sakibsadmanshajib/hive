//! Hive-typed elevated Windows sandbox compose (Step 3, Integration A core).
//!
//! This module is the Hive-proprietary port of the upstream elevated helper
//! surface (`openai/codex` `codex-rs/windows-sandbox-rs`, commit `a47c661…`,
//! Apache-2.0; see `../../VENDORING.md`). It ports, retargeted from
//! `codex_protocol` + `CODEX_HOME` to
//! [`crate::windows_resolve::ResolvedWindowsSandboxPermissions`] and a
//! caller-supplied Hive config directory (Q1 decision, drop `codex_protocol`):
//!
//! - `allow.rs::compute_allow_paths_for_permissions` -> [`compute_allow_paths`]
//!   (pure path selection for the per-task ACL grant set).
//! - `elevated_impl.rs::run_windows_sandbox_capture_for_permission_profile`
//!   (the `tty = false` CAPTURE half only) -> [`run_windows_sandbox_capture`].
//! - the token / env / cwd / ACL assembly of `spawn_prep.rs` -> the runner-side
//!   [`run_command_runner`] (token derivation + per-task ACL + child spawn).
//! - the provisioning-readiness and credential-load logic of `identity.rs` /
//!   `setup.rs` (W2 shipped stand-ins) -> [`sandbox_setup_is_complete`],
//!   [`load_logon_sandbox_creds`], [`provision_sandbox_account`].
//!
//! ## Elevation model (blueprint A.Q1, RESOLVED, locked)
//!
//! Nothing in the per-task launch path holds or needs
//! `SeAssignPrimaryTokenPrivilege`, and nothing prompts UAC. The non-elevated
//! Hive app launches the Hive command-runner AS the sandbox user with
//! `CreateProcessWithLogonW` (done inside the vendored
//! `runner_client::spawn_runner_transport`), passing the sandbox account, `.`
//! domain, and the DPAPI-unsealed password with `LOGON_WITH_PROFILE`. The
//! command-runner, now running as the low-privilege sandbox account, derives a
//! capability-restricted primary token FROM ITS OWN token
//! (`get_current_token_for_restriction` +
//! `create_*_token_with_caps_and_user_from`) and spawns the inner child with
//! `CreateProcessAsUserW` (via `spawn_process_with_pipes`). A token derived
//! from the caller's own token is assignable, so that call needs no privilege.
//! UAC prompts exactly once, at provisioning, when the setup binary runs
//! elevated to create the OS account.
//!
//! ## Network semantics (blueprint Q2 / D-005, locked)
//!
//! Step 3 does NOT enforce network egress (WFP is Integration B). The public
//! [`crate::launch`] therefore keeps REFUSING both `NetworkPolicy::DenyAll`
//! (`NetworkConfinementNotImplemented`) and `NetworkPolicy::AllowHosts`
//! (`AllowHostsNotYetImplemented`); see [`LaunchDecision`]. There is no
//! `launch()` code path that reports success while a child has unrestricted
//! network. The elevated compose ([`run_windows_sandbox_capture`]) is reachable
//! ONLY through the explicit, lab-only
//! [`spawn_confined_for_validation`] entry point, which is documented as NOT a
//! network-confined launch and exists solely so the `spike307-win` lab can
//! prove the filesystem / user / token / Job isolation matrix (exactly as #395
//! proved its primitives by lab replica, ahead of the composed success path
//! that lands with Integration B).
//!
//! ## Verification status (read before trusting this file)
//!
//! Every Win32 path here (`#[cfg(windows)]`) is cross-compiled by CI for
//! `x86_64-pc-windows-gnu` (type-checked against real `windows-sys` 0.52
//! signatures) but is NEVER executed off a real MSVC Windows host. All runtime
//! confinement assertions are lab-gated on `spike307-win` (D-004); see the
//! `LAB VALIDATION CHECKLIST` at the bottom of this file. The pure-logic parts
//! ([`LaunchDecision::for_policy`], [`compute_allow_paths`], the config-path
//! helpers) run on every platform, including this crate's Linux CI, and are
//! unit-tested there.

use crate::policy::NetworkPolicy;
use crate::windows_resolve::ResolvedWindowsSandboxPermissions;
use crate::{LaunchError, SandboxPolicy};
use std::collections::HashSet;
use std::path::{Path, PathBuf};

// ===========================================================================
// Pure, cross-platform logic (unit-tested on Linux CI)
// ===========================================================================

/// Captured output and exit status of a confined child run over the framed
/// pipe. Hive-native replacement for upstream's `windows_impl::CaptureResult`.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct CaptureResult {
    pub exit_code: i32,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    pub timed_out: bool,
}

/// The decision [`crate::launch`] reaches for a policy BEFORE any spawn.
///
/// Step 3 has no `Spawn` variant on purpose: [`NetworkPolicy`] has exactly two
/// variants and Step 3 refuses BOTH (WFP is Integration B; Q2/D-005). A future
/// `Spawn` variant lands with Integration B, when the refusal guards are
/// removed and the composed success path runs for the first time. Keeping this
/// as an explicit enum (rather than two inline `if` guards) makes the
/// refuse-vs-spawn decision independently unit-testable, and gives Integration
/// B one obvious place to add the success branch.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LaunchDecision {
    /// `NetworkPolicy::AllowHosts`: refuse with
    /// [`LaunchError::AllowHostsNotYetImplemented`] (needs the egress proxy +
    /// WFP, Integration B).
    RefuseAllowHosts,
    /// `NetworkPolicy::DenyAll`: refuse with
    /// [`LaunchError::NetworkConfinementNotImplemented`]. A `DenyAll` child
    /// under a standard sandbox user CAN still open sockets, so spawning it
    /// without WFP would be a SILENT fail-open (D-005, FORBIDDEN). Refuse until
    /// Integration B installs the WFP block.
    RefuseDenyAll,
}

impl LaunchDecision {
    /// Pure mapping from a policy's network posture to the Step 3 launch
    /// decision. Both variants refuse; see the type doc.
    pub fn for_policy(policy: &SandboxPolicy) -> Self {
        match policy.network() {
            NetworkPolicy::AllowHosts(_) => LaunchDecision::RefuseAllowHosts,
            NetworkPolicy::DenyAll => LaunchDecision::RefuseDenyAll,
        }
    }

    /// The [`LaunchError`] this decision produces from the public launch path.
    pub fn into_refusal(self) -> LaunchError {
        match self {
            LaunchDecision::RefuseAllowHosts => LaunchError::AllowHostsNotYetImplemented,
            LaunchDecision::RefuseDenyAll => LaunchError::NetworkConfinementNotImplemented,
        }
    }
}

/// The filesystem paths a per-task ACL grant should allow (writable) and deny
/// (read-only carve-outs inside a writable root). Hive port of
/// `allow.rs::AllowDenyPaths`.
#[derive(Debug, Default, PartialEq, Eq)]
pub struct AllowDenyPaths {
    pub allow: HashSet<PathBuf>,
    pub deny: HashSet<PathBuf>,
}

/// Port of `allow.rs::compute_allow_paths_for_permissions`, retargeted to the
/// Hive resolver.
///
/// Upstream iterates `permissions.writable_roots_for_cwd(cwd, env)` (cwd- and
/// env-parameterized) and, per writable root, canonicalizes the root into the
/// `allow` set and pushes each `read_only_subpath` into `deny`. The Hive
/// resolver is PRE-RESOLVED against a fixed cwd at
/// [`ResolvedWindowsSandboxPermissions::from_policy`] time, so the accessor is
/// the argument-free [`ResolvedWindowsSandboxPermissions::writable_roots`]; the
/// six upstream call sites collapse to this one, per blueprint A.Q2. Honest
/// difference recorded (D-005): `read_only_subpaths` is always empty from the
/// Hive resolver today (`SandboxPolicy` cannot express a nested read-only
/// carve-out, and `deny_read_resolver.rs`'s glob-scan carve-out selection is
/// deliberately not ported because there is nothing to resolve yet), so `deny`
/// is empty here rather than reproducing upstream's `.git`/`.codex`/`.agents`
/// deny logic, which lives in upstream's resolver, not in `allow.rs`. When a
/// future `SandboxPolicy` extension populates carve-outs, this function already
/// propagates them without a shape change.
///
/// Only paths that currently exist are added, matching upstream (an ACL grant
/// for a non-existent path is meaningless and would error at apply time).
pub fn compute_allow_paths(permissions: &ResolvedWindowsSandboxPermissions) -> AllowDenyPaths {
    let mut allow: HashSet<PathBuf> = HashSet::new();
    let mut deny: HashSet<PathBuf> = HashSet::new();

    for writable_root in permissions.writable_roots() {
        let canonical =
            dunce::canonicalize(&writable_root.root).unwrap_or_else(|_| writable_root.root.clone());
        if canonical.exists() {
            allow.insert(canonical);
        }
        for read_only_subpath in &writable_root.read_only_subpaths {
            if read_only_subpath.exists() {
                deny.insert(read_only_subpath.clone());
            }
        }
    }

    AllowDenyPaths { allow, deny }
}

// ---- Config-directory layout (pure; caller supplies the Hive sandbox home) --

/// Provisioning-readiness marker filename under the Hive sandbox home. Mirrors
/// upstream's setup-marker concept but with a Hive name and a Hive version.
const SETUP_MARKER_FILE: &str = "setup-marker.json";
/// Sealed sandbox-account credential file (DPAPI blob) under the sandbox home.
const SANDBOX_CREDS_FILE: &str = "sandbox-creds.dpapi";
/// Bump when the provisioning layout changes so a stale marker forces
/// re-provisioning rather than a broken reuse.
const SETUP_MARKER_VERSION: u32 = 1;

/// `<home>/setup-marker.json`.
pub fn setup_marker_path(sandbox_home: &Path) -> PathBuf {
    sandbox_home.join(SETUP_MARKER_FILE)
}

/// `<home>/secrets` — DPAPI-sealed credential directory. Its ACL MUST exclude
/// the sandbox account (lab item L5); provisioning applies that ACL.
pub fn secrets_dir(sandbox_home: &Path) -> PathBuf {
    sandbox_home.join("secrets")
}

/// `<home>/secrets/sandbox-creds.dpapi`.
pub fn sandbox_creds_path(sandbox_home: &Path) -> PathBuf {
    secrets_dir(sandbox_home).join(SANDBOX_CREDS_FILE)
}

/// Fixed local account name for the ONE shared low-privilege sandbox user
/// (Q3: one shared user for the desktop surface). Kept short and prefixed so
/// it is recognizable in `net user` output and unlikely to collide.
pub const SANDBOX_USERNAME: &str = "hive_sandbox";
/// Local group the sandbox account is the sole member of (least privilege).
pub const SANDBOX_GROUP: &str = "hive_sandbox_users";

/// `true` iff a valid, current provisioning marker exists. Port of
/// `identity.rs::sandbox_setup_is_complete`, retargeted off `CODEX_HOME`.
/// Pure filesystem read; safe on every platform (the account it gates is
/// Windows-only, but the readiness check itself is not).
pub fn sandbox_setup_is_complete(sandbox_home: &Path) -> bool {
    let Ok(bytes) = std::fs::read(setup_marker_path(sandbox_home)) else {
        return false;
    };
    match serde_json::from_slice::<SetupMarker>(&bytes) {
        Ok(marker) => marker.version == SETUP_MARKER_VERSION,
        Err(_) => false,
    }
}

/// On-disk provisioning-readiness marker.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
struct SetupMarker {
    version: u32,
    username: String,
}

// ===========================================================================
// Windows-only Win32 compose (cross-compiled for windows-gnu; lab-gated)
// ===========================================================================

#[cfg(windows)]
pub use windows_impl::{
    load_logon_sandbox_creds, provision_sandbox_account, run_command_runner,
    run_windows_sandbox_capture, runner_debug_log, spawn_confined_for_validation,
};

#[cfg(windows)]
mod windows_impl {
    use super::*;
    use codex_windows_sandbox::absolute_path::AbsolutePathBuf;
    use codex_windows_sandbox::identity::SandboxCreds;
    use codex_windows_sandbox::ipc_framed::{
        self, ErrorStage, FramedMessage, Message, OutputPayload, OutputStream, SpawnReady,
        SpawnRequest,
    };
    use codex_windows_sandbox::permission_profile::PermissionProfile;
    use codex_windows_sandbox::process::{
        ConsoleMode, PipeSpawnHandles, StderrMode, StdinMode, spawn_process_with_pipes,
    };
    use codex_windows_sandbox::runner_client::{retry_runner_spawn_once, spawn_runner_transport};
    use codex_windows_sandbox::token::{
        LocalSid, create_readonly_token_with_caps_and_user_from,
        create_workspace_write_token_with_caps_and_user_from, get_current_token_for_restriction,
    };
    use codex_windows_sandbox::{acl, cap, dpapi, hide_users, sandbox_users};
    use std::collections::HashMap;
    use std::ffi::c_void;
    use std::fs::File;
    use std::io::{Read, Write};

    use windows_sys::Win32::Foundation::{CloseHandle, GetLastError, HANDLE, WAIT_OBJECT_0};
    use windows_sys::Win32::Security::Authentication::Identity::{
        LSA_HANDLE, LSA_OBJECT_ATTRIBUTES, LSA_UNICODE_STRING, LsaAddAccountRights, LsaClose,
        LsaOpenPolicy, POLICY_CREATE_ACCOUNT, POLICY_LOOKUP_NAMES,
    };
    use windows_sys::Win32::System::JobObjects::{
        AssignProcessToJobObject, CreateJobObjectW, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
        JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JobObjectExtendedLimitInformation,
        SetInformationJobObject,
    };
    use windows_sys::Win32::System::Services::{
        CloseServiceHandle, OpenSCManagerW, OpenServiceW, QueryServiceStatus, SC_MANAGER_CONNECT,
        SERVICE_QUERY_STATUS, SERVICE_START, SERVICE_STATUS, StartServiceW,
    };
    use windows_sys::Win32::System::Threading::{
        GetExitCodeProcess, INFINITE, TerminateProcess, WaitForSingleObject,
    };

    // The runner-spawn transport wants a "codex_home"-shaped base directory for
    // its runner-exe resolution and log dir; Hive passes the sandbox home. The
    // name is kept as `codex_home` at the vendored call boundary only.
    type Result<T> = anyhow::Result<T>;

    // -------------------------------------------------------------------
    // Credential seal / load (identity.rs port; DPAPI at rest)
    // -------------------------------------------------------------------

    /// Loads the sandbox-account logon credentials, DPAPI-unsealing the
    /// password. Port of `identity.rs::require_logon_sandbox_creds`'s load half
    /// (the resolver-driven ACL-refresh half is Integration-B/refresh scope and
    /// is deliberately not reproduced: Step 3 provisions ACLs once, at
    /// provisioning, per A.Q1). Fail-closed (D-005): any missing marker, missing
    /// blob, or unseal failure returns an error and never a blank/guessed
    /// credential.
    pub fn load_logon_sandbox_creds(sandbox_home: &Path) -> Result<SandboxCreds> {
        if !sandbox_setup_is_complete(sandbox_home) {
            anyhow::bail!("sandbox account is not provisioned (no valid setup marker)");
        }
        let blob = std::fs::read(sandbox_creds_path(sandbox_home))
            .map_err(|e| anyhow::anyhow!("read sealed sandbox creds: {e}"))?;
        let cleartext = dpapi::unprotect(&blob)
            .map_err(|e| anyhow::anyhow!("DPAPI unseal of sandbox creds failed: {e}"))?;
        let password = String::from_utf8(cleartext)
            .map_err(|_| anyhow::anyhow!("sealed sandbox password was not valid UTF-8"))?;
        Ok(SandboxCreds {
            username: SANDBOX_USERNAME.to_string(),
            password,
        })
    }

    /// Generates a strong random password for the sandbox account. Uses the
    /// vendored crate's `rand` (CSPRNG `OsRng`-seeded) via a printable-ASCII
    /// alphabet broad enough to satisfy the local-account complexity policy.
    fn generate_password() -> String {
        use rand::RngCore;
        use rand::rngs::OsRng;
        // 32 bytes of CSPRNG entropy, mapped to a 64-char complexity-satisfying
        // set (upper, lower, digit, symbol) so `NetUserAdd` never rejects it.
        const ALPHABET: &[u8] =
            b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()-_=+";
        let mut raw = [0u8; 32];
        OsRng.fill_bytes(&mut raw);
        let mut pw: String = raw
            .iter()
            .map(|b| ALPHABET[(*b as usize) % ALPHABET.len()] as char)
            .collect();
        // Guarantee at least one of each class regardless of the random draw,
        // so complexity policy is satisfied deterministically (fail-closed:
        // never emit a password the account-create step would reject).
        pw.push_str("Aa1!");
        pw
    }

    // -------------------------------------------------------------------
    // Provisioning (identity.rs / setup.rs port; WFP + firewall OMITTED)
    // -------------------------------------------------------------------

    /// One-time, ELEVATED provisioning of the shared low-privilege sandbox
    /// account. Port of the provisioning half of `identity.rs` /`setup.rs`
    /// (`run_elevated_provisioning_setup`), retargeted to the Hive sandbox home
    /// and with the WFP/firewall call sites OMITTED (Integration B): this
    /// authors NO network egress control.
    ///
    /// Steps (each fail-closed; a failure aborts and does NOT leave a
    /// half-created account usable):
    /// 1. create the least-privilege local group and account (`sandbox_users`);
    /// 2. grant the interactive-logon right and ensure the Secondary Logon
    ///    service is enabled, so `CreateProcessWithLogonW` can use the account
    ///    (blueprint A.Q1 provisioning invariant, lab item L1/L4);
    /// 3. DPAPI-seal the CSPRNG password into a secrets dir whose ACL excludes
    ///    the sandbox account (lab item L5);
    /// 4. hide the account and its profile dir from the login screen (L4);
    /// 5. write the readiness marker so subsequent launches do not re-elevate.
    ///
    /// MUST be run elevated (Administrator). Callers reach it through the setup
    /// binary, which re-launches itself elevated via `ShellExecute "runas"` for
    /// exactly one UAC consent (L1).
    pub fn provision_sandbox_account(sandbox_home: &Path, log: &mut dyn Write) -> Result<()> {
        std::fs::create_dir_all(sandbox_home)
            .map_err(|e| anyhow::anyhow!("create sandbox home: {e}"))?;
        let secrets = secrets_dir(sandbox_home);
        std::fs::create_dir_all(&secrets)
            .map_err(|e| anyhow::anyhow!("create secrets dir: {e}"))?;

        // Zeroizing<String> rather than a plain String: this cleartext
        // password would otherwise linger in freed heap after this function
        // returns. Matches the zeroize-on-drop handling `SandboxCreds`
        // (codex-windows-sandbox::identity) and `runner_client` already give
        // this same class of secret.
        let password = zeroize::Zeroizing::new(generate_password());

        // 1. Least-privilege group + account.
        sandbox_users::ensure_local_group(SANDBOX_GROUP, "Hive sandbox low-privilege users", log)?;
        sandbox_users::ensure_local_user(SANDBOX_USERNAME, &password, log)?;
        sandbox_users::ensure_local_group_member(SANDBOX_GROUP, SANDBOX_USERNAME)?;

        // 2. Logon prerequisites for CreateProcessWithLogonW (A.Q1 invariant).
        grant_interactive_logon_right(SANDBOX_USERNAME)?;
        ensure_seclogon_enabled()?;

        // 3. Seal the password; then ACL the secrets dir to EXCLUDE the sandbox
        //    account so a compromised child cannot read its own credential.
        let blob = dpapi::protect(password.as_bytes())
            .map_err(|e| anyhow::anyhow!("DPAPI seal of sandbox password failed: {e}"))?;
        std::fs::write(sandbox_creds_path(sandbox_home), &blob)
            .map_err(|e| anyhow::anyhow!("write sealed sandbox creds: {e}"))?;
        deny_sandbox_account_read(&secrets)?;

        // 4. Hide the account + its profile dir from the login screen.
        hide_users::hide_newly_created_users(&[SANDBOX_USERNAME.to_string()], sandbox_home);

        // 5. Readiness marker (last, so a partial provisioning is never
        //    reported complete).
        let marker = SetupMarker {
            version: SETUP_MARKER_VERSION,
            username: SANDBOX_USERNAME.to_string(),
        };
        let bytes = serde_json::to_vec_pretty(&marker)
            .map_err(|e| anyhow::anyhow!("serialize setup marker: {e}"))?;
        std::fs::write(setup_marker_path(sandbox_home), bytes)
            .map_err(|e| anyhow::anyhow!("write setup marker: {e}"))?;
        let _ = writeln!(log, "provisioning complete for {SANDBOX_USERNAME}");
        Ok(())
    }

    /// Applies a deny-read ACE for the sandbox account SID on `path`, so the
    /// sealed credential is not readable by the child. Uses the vendored
    /// `acl`/`sandbox_users` SID resolution.
    fn deny_sandbox_account_read(path: &Path) -> Result<()> {
        let sid_bytes = sandbox_users::resolve_sid(SANDBOX_USERNAME)?;
        let psid = sandbox_users::sid_bytes_to_psid(&sid_bytes)?;
        // SAFETY: `psid` was just built from a valid resolved SID byte buffer by
        // the vendored helper; `add_deny_read_ace` reads it and applies a
        // deny-read ACE to `path`. The buffer outlives the call.
        let newly_applied = unsafe { acl::add_deny_read_ace(path, psid) }
            .map_err(|e| anyhow::anyhow!("deny-read ACE on secrets dir: {e}"))?;
        // `add_deny_read_ace` returns `false` in TWO cases: the deny-read ACE is
        // already present (an idempotent re-run, which must NOT fail), and — the
        // vendored acl layer maps a failed `SetEntriesInAclW`/
        // `SetNamedSecurityInfoW` to `Ok(false)` — when the ACE could not be
        // applied at all. Treating every `false` as fatal is what broke
        // re-running provisioning ("deny-read ACE was not applied"). Fail closed
        // (D-005): only accept a non-add once we have confirmed the deny is
        // actually in force on disk; otherwise bail.
        if !newly_applied {
            // SAFETY: `psid` is the same live SID pointer built from
            // `sandbox_users::sid_bytes_to_psid` used just above for
            // `add_deny_read_ace`; it is still valid here.
            let present = unsafe { acl::path_has_read_deny(path, psid) }
                .map_err(|e| anyhow::anyhow!("verify deny-read ACE on secrets dir: {e}"))?;
            if !present {
                anyhow::bail!("deny-read ACE was not applied to the secrets directory");
            }
        }
        Ok(())
    }

    /// Grants `SeInteractiveLogonRight` to the account via the LSA policy API,
    /// so the hidden sandbox account can be used by `CreateProcessWithLogonW`
    /// (which performs an interactive-style logon). `hide_users` hides the
    /// account from the login screen but must not strip this right; granting it
    /// here is the counterpart (blueprint A.Q1 provisioning invariant).
    fn grant_interactive_logon_right(username: &str) -> Result<()> {
        // SAFETY: standard LsaOpenPolicy / LsaAddAccountRights sequence. All
        // pointers point at locals that outlive the calls; every fallible step
        // is checked and the policy handle is closed on every path.
        unsafe {
            let mut attrs: LSA_OBJECT_ATTRIBUTES = std::mem::zeroed();
            attrs.Length = std::mem::size_of::<LSA_OBJECT_ATTRIBUTES>() as u32;
            let mut policy: LSA_HANDLE = 0;
            let status = LsaOpenPolicy(
                std::ptr::null_mut(),
                &attrs,
                (POLICY_CREATE_ACCOUNT | POLICY_LOOKUP_NAMES) as u32,
                &mut policy,
            );
            if status != 0 {
                anyhow::bail!("LsaOpenPolicy failed: NTSTATUS 0x{status:08x}");
            }

            let sid_bytes = sandbox_users::resolve_sid(username)?;
            let psid = sandbox_users::sid_bytes_to_psid(&sid_bytes)?;

            let mut right: Vec<u16> = "SeInteractiveLogonRight".encode_utf16().collect();
            let right_us = LSA_UNICODE_STRING {
                Length: (right.len() * 2) as u16,
                MaximumLength: (right.len() * 2) as u16,
                Buffer: right.as_mut_ptr(),
            };
            let add_status = LsaAddAccountRights(policy, psid, &right_us, 1);
            let _ = LsaClose(policy);
            if add_status != 0 {
                anyhow::bail!("LsaAddAccountRights failed: NTSTATUS 0x{add_status:08x}");
            }
        }
        Ok(())
    }

    /// Ensures the Secondary Logon service (`seclogon`) is running, since
    /// `CreateProcessWithLogonW` depends on it (blueprint A.Q1). Fail-closed: a
    /// service that cannot be started aborts provisioning rather than leaving a
    /// non-functional account.
    fn ensure_seclogon_enabled() -> Result<()> {
        // SAFETY: OpenSCManager / OpenService / QueryServiceStatus / StartService
        // sequence; every handle is closed and every fallible step checked.
        unsafe {
            let scm = OpenSCManagerW(std::ptr::null(), std::ptr::null(), SC_MANAGER_CONNECT);
            if scm == 0 {
                anyhow::bail!("OpenSCManagerW failed: {}", GetLastError());
            }
            let name: Vec<u16> = "seclogon\0".encode_utf16().collect();
            let svc = OpenServiceW(scm, name.as_ptr(), SERVICE_QUERY_STATUS | SERVICE_START);
            if svc == 0 {
                let err = GetLastError();
                CloseServiceHandle(scm);
                anyhow::bail!("OpenServiceW(seclogon) failed: {err}");
            }
            let mut status: SERVICE_STATUS = std::mem::zeroed();
            // Already running is success; otherwise try to start it.
            if QueryServiceStatus(svc, &mut status) != 0
                && status.dwCurrentState == windows_sys::Win32::System::Services::SERVICE_RUNNING
            {
                CloseServiceHandle(svc);
                CloseServiceHandle(scm);
                return Ok(());
            }
            let started = StartServiceW(svc, 0, std::ptr::null());
            CloseServiceHandle(svc);
            CloseServiceHandle(scm);
            if started == 0 {
                let err = GetLastError();
                // ERROR_SERVICE_ALREADY_RUNNING (1056) is benign.
                if err != 1056 {
                    anyhow::bail!("StartServiceW(seclogon) failed: {err}");
                }
            }
        }
        Ok(())
    }

    // -------------------------------------------------------------------
    // Parent-side capture compose (elevated_impl.rs port; tty = false only)
    // -------------------------------------------------------------------

    /// Builds the capability-SID list for the ACL grants and the `SpawnRequest`
    /// permission profile from the Hive resolver. Port of the cap-selection
    /// block of `elevated_impl.rs::run_windows_sandbox_capture_for_permission_profile`.
    fn cap_sids_for(
        sandbox_home: &Path,
        cwd: &Path,
        permissions: &ResolvedWindowsSandboxPermissions,
    ) -> Result<Vec<String>> {
        let caps = cap::load_or_create_cap_sids(sandbox_home)?;
        if permissions.uses_write_capabilities() {
            let cap_sids = permissions
                .writable_roots()
                .iter()
                .map(|root| cap::workspace_write_cap_sid_for_root(sandbox_home, cwd, &root.root))
                .collect::<Result<Vec<_>>>()?;
            if cap_sids.is_empty() {
                anyhow::bail!("workspace-write sandbox has no writable-root capability SIDs");
            }
            Ok(cap_sids)
        } else {
            Ok(vec![caps.readonly])
        }
    }

    /// Port of the `tty = false` CAPTURE half of
    /// `elevated_impl.rs::run_windows_sandbox_capture_for_permission_profile`,
    /// retargeted to the Hive resolver and sandbox home. Spawns the Hive
    /// command-runner AS the sandbox user (via the vendored transport, which
    /// uses `CreateProcessWithLogonW`), sends a `tty = false` `SpawnRequest`,
    /// and drives the frame loop to capture stdout/stderr/exit.
    ///
    /// This is the confinement mechanism; it is NOT a network-confined launch.
    /// It is invoked only through [`spawn_confined_for_validation`].
    pub fn run_windows_sandbox_capture(
        sandbox_home: &Path,
        policy: &SandboxPolicy,
        command: &[String],
        cwd: &Path,
        env: &HashMap<String, String>,
    ) -> Result<CaptureResult> {
        let permissions = ResolvedWindowsSandboxPermissions::from_policy(policy, cwd)
            .map_err(|e| anyhow::anyhow!("resolve policy: {e}"))?;

        let cap_sids = cap_sids_for(sandbox_home, cwd, &permissions)?;
        let workspace_roots: Vec<AbsolutePathBuf> = permissions
            .writable_roots()
            .iter()
            .map(|r| AbsolutePathBuf::new(r.root.clone()))
            .collect::<std::result::Result<Vec<_>, _>>()
            .map_err(|e| anyhow::anyhow!("workspace root not absolute: {e}"))?;

        // Per-task allow-write grants, applied HERE in the elevated parent
        // rather than in the runner. The runner executes as the low-privilege
        // sandbox account, which does not own the workspace root and has no
        // WRITE_DAC on it, so its SetNamedSecurityInfoW was denied and no ACE
        // landed -- the (c) "write inside the writable root" failure. This
        // parent owns the root (it created it) and can rewrite its DACL. On each
        // writable root we grant:
        //   * the sandbox-account SID -- the identity present in BOTH the
        //     WRITE_RESTRICTED token's normal groups AND its restricting-SID set
        //     (the token adds the sandbox user as an extra restricting SID), so
        //     a write passes both of the token's access checks. A capability SID
        //     alone is only in the restricting set, so the normal-groups check
        //     still denied the write; and
        //   * each per-workspace capability SID -- the restricting-side gate the
        //     token carries, kept so the isolation design stays populated.
        // Fail-closed (D-005): add_allow_ace now returns Err (and verifies the
        // ACE persisted) so a grant that cannot be applied aborts the launch
        // rather than proceeding with a workspace the confined child cannot
        // write.
        if permissions.uses_write_capabilities() {
            let sandbox_sid_str = codex_windows_sandbox::winutil::string_from_sid_bytes(
                &sandbox_users::resolve_sid(SANDBOX_USERNAME)?,
            )
            .map_err(anyhow::Error::msg)?;
            let sandbox_local = LocalSid::from_string(&sandbox_sid_str)?;
            let cap_local_sids: Vec<LocalSid> = cap_sids
                .iter()
                .map(|s| LocalSid::from_string(s))
                .collect::<Result<Vec<_>>>()?;
            for root in &workspace_roots {
                let root_path = root.as_path();
                // SAFETY: sandbox_local / cap_local_sids own live SID pointers
                // that outlive this loop; add_allow_ace reads them and rewrites
                // the DACL of the validated absolute root path.
                unsafe { acl::add_allow_ace(root_path, sandbox_local.as_ptr()) }.map_err(|e| {
                    anyhow::anyhow!(
                        "grant sandbox-account write on {}: {e}",
                        root_path.display()
                    )
                })?;
                for cap in &cap_local_sids {
                    unsafe { acl::add_allow_ace(root_path, cap.as_ptr()) }.map_err(|e| {
                        anyhow::anyhow!("grant capability write on {}: {e}", root_path.display())
                    })?;
                }
            }
        }

        let spawn_request = SpawnRequest {
            command: command.to_vec(),
            cwd: cwd.to_path_buf(),
            env: env.clone(),
            permission_profile: PermissionProfile {
                read_only: !permissions.uses_write_capabilities(),
            },
            workspace_roots,
            codex_home: sandbox_home.to_path_buf(),
            real_codex_home: sandbox_home.to_path_buf(),
            cap_sids,
            timeout_ms: None,
            tty: false,
            stdin_open: false,
            // UI isolation (Step 3 B1): the runner moves the restricted inner
            // child onto a private window station + desktop (full clipboard/atom
            // isolation), granting its own logon SID so the child can attach.
            use_private_desktop: true,
        };

        let sandbox_creds = load_logon_sandbox_creds(sandbox_home)?;
        let logs_base_dir = sandbox_home.join(".sandbox");
        let _ = std::fs::create_dir_all(&logs_base_dir);

        let transport = retry_runner_spawn_once(
            sandbox_creds,
            &spawn_request.command,
            |creds| {
                spawn_runner_transport(
                    sandbox_home,
                    cwd,
                    &creds,
                    Some(logs_base_dir.as_path()),
                    spawn_request.clone(),
                )
            },
            // Step 3 does not re-derive creds; a stale-cred failure is a real
            // provisioning error (fail-closed), so the refresh closure just
            // reloads the sealed creds rather than silently re-provisioning.
            || load_logon_sandbox_creds(sandbox_home),
        )?;

        let (pipe_write, mut pipe_read) = transport.into_files();
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        let result = loop {
            let msg = match ipc_framed::read_frame(&mut pipe_read) {
                Ok(Some(msg)) => msg,
                Ok(None) => break Err(anyhow::anyhow!("runner pipe closed before exit")),
                Err(err) => break Err(err),
            };
            match msg.message {
                Message::SpawnReady { .. } => {}
                Message::Output { payload } => match ipc_framed::decode_bytes(&payload.data_b64) {
                    Ok(bytes) => match payload.stream {
                        OutputStream::Stdout => stdout.extend_from_slice(&bytes),
                        OutputStream::Stderr => stderr.extend_from_slice(&bytes),
                    },
                    Err(err) => break Err(err),
                },
                Message::Exit { payload } => break Ok((payload.exit_code, payload.timed_out)),
                Message::Error { payload } => {
                    break Err(anyhow::anyhow!("runner error: {}", payload.message));
                }
                other => {
                    break Err(anyhow::anyhow!("unexpected runner message: {other:?}"));
                }
            }
        };
        drop(pipe_write);
        let (exit_code, timed_out) = result?;
        Ok(CaptureResult {
            exit_code,
            stdout,
            stderr,
            timed_out,
        })
    }

    /// Lab-only confinement-validation entry point. NOT a network-confined
    /// launch (Q2/D-005): it runs the elevated compose so the `spike307-win`
    /// lab can prove the filesystem / user / token / Job isolation matrix
    /// directly, exactly as #395 proved its primitives by lab replica ahead of
    /// the composed success path. The public [`crate::launch`] never calls this;
    /// it keeps refusing both network policies until Integration B.
    pub fn spawn_confined_for_validation(
        sandbox_home: &Path,
        policy: &SandboxPolicy,
        command: &[String],
        cwd: &Path,
        env: &HashMap<String, String>,
    ) -> std::result::Result<CaptureResult, LaunchError> {
        if !sandbox_setup_is_complete(sandbox_home) {
            return Err(LaunchError::Confinement(
                "sandbox account not provisioned; run the Hive sandbox setup binary first"
                    .to_string(),
            ));
        }
        run_windows_sandbox_capture(sandbox_home, policy, command, cwd, env)
            .map_err(|e| LaunchError::Confinement(format!("confined validation run: {e}")))
    }

    // -------------------------------------------------------------------
    // Runner-side (spawn_prep.rs + command_runner/win.rs port; tty = false)
    // -------------------------------------------------------------------

    /// Append-only diagnostic sink for the command-runner startup path.
    ///
    /// The runner is launched by `CreateProcessWithLogonW` with
    /// `CREATE_NO_WINDOW` and no redirected stdio, so anything it writes to
    /// stderr is discarded: a failure BEFORE the IPC handshake (for example a
    /// pipe-open error) otherwise leaves no trace at all, which is exactly the
    /// blind spot that made the "runner exits before connecting" blocker
    /// undiagnosable. This writes timestamped lines to
    /// `<temp>/hive-command-runner.log` in the runner account's OWN temp
    /// directory (readable by an administrator for lab inspection).
    ///
    /// Gated on `HIVE_RUNNER_DEBUG=1` so it is inert in normal operation and
    /// leaves the release path untouched. It NEVER logs the sandbox password,
    /// credential bytes, or environment values (D-005 honest logging); only
    /// non-sensitive shape (counts, flags, pipe names, error kinds).
    pub fn runner_debug_log(msg: &str) {
        if std::env::var("HIVE_RUNNER_DEBUG").ok().as_deref() != Some("1") {
            return;
        }
        // Deterministic sink override (HIVE_RUNNER_DEBUG_DIR) so the log lands
        // somewhere the sandbox account can write and an administrator can read,
        // independent of how CreateProcessWithLogonW resolves the runner's TEMP
        // (with a NULL environment block the runner inherits the caller's env,
        // which makes the plain temp dir ambiguous). Falls back to the process
        // temp dir when unset.
        let dir = std::env::var_os("HIVE_RUNNER_DEBUG_DIR")
            .map(std::path::PathBuf::from)
            .unwrap_or_else(std::env::temp_dir);
        let path = dir.join("hive-command-runner.log");
        if let Ok(mut f) = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)
        {
            let now = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.as_millis())
                .unwrap_or(0);
            let pid = std::process::id();
            let _ = writeln!(f, "[{now}][pid {pid}] {msg}");
        }
    }

    /// Runs the Hive command-runner protocol on the two named pipes. This is
    /// the process that `CreateProcessWithLogonW` starts AS the sandbox account;
    /// it derives a capability-restricted primary token FROM ITS OWN token and
    /// spawns the inner child under it (A.Q1). Port of the `tty = false` branch
    /// of `bin/command_runner/win.rs` (upstream lines 339-360) plus the
    /// token/ACL assembly of `spawn_prep.rs`.
    ///
    /// ConPTY stub site 1 (blueprint R.1): a `tty = true` request is rejected
    /// fail-closed (an `Error` frame), never silently downgraded; ConPTY is
    /// Step 5.
    pub fn run_command_runner(pipe_in: &str, pipe_out: &str) -> Result<()> {
        runner_debug_log(&format!(
            "run_command_runner entry: pipe_in={pipe_in} pipe_out={pipe_out}"
        ));
        // The runner is the pipe CLIENT: it opens the parent's server pipes as
        // files. `pipe_in` was created by the parent with PIPE_ACCESS_OUTBOUND
        // (parent writes, runner reads); `pipe_out` PIPE_ACCESS_INBOUND (runner
        // writes, parent reads). Opening a named pipe as a std file is the
        // documented client path and avoids a bespoke CreateFileW FFI.
        // The two server pipes are HALF-DUPLEX, so the client (this runner) must
        // open each with the single matching direction: `pipe_in` was created
        // PIPE_ACCESS_OUTBOUND (server -> client), so the client opens it
        // READ-only; `pipe_out` was created PIPE_ACCESS_INBOUND (client ->
        // server), so the client opens it WRITE-only. Opening either with the
        // opposite access added (read+write) is rejected by the half-duplex
        // pipe with ERROR_ACCESS_DENIED, which previously aborted the runner at
        // the first pipe open. Match the direction exactly.
        runner_debug_log("opening inbound pipe (pipe_in, read)");
        let mut reader = File::options().read(true).open(pipe_in).map_err(|e| {
            runner_debug_log(&format!("open inbound pipe FAILED: {e}"));
            anyhow::anyhow!("open inbound pipe {pipe_in}: {e}")
        })?;
        runner_debug_log("inbound pipe opened; opening outbound pipe (pipe_out, write)");
        let mut writer = File::options().write(true).open(pipe_out).map_err(|e| {
            runner_debug_log(&format!("open outbound pipe FAILED: {e}"));
            anyhow::anyhow!("open outbound pipe {pipe_out}: {e}")
        })?;
        runner_debug_log("both pipes opened; reading spawn request frame");

        let request = match ipc_framed::read_frame(&mut reader) {
            Ok(Some(FramedMessage {
                message: Message::SpawnRequest { payload },
                ..
            })) => *payload,
            Ok(_) => {
                send_error(
                    &mut writer,
                    ErrorStage::ReadSpawnRequest,
                    "expected SpawnRequest",
                );
                anyhow::bail!("first frame was not a SpawnRequest");
            }
            Err(e) => {
                send_error(&mut writer, ErrorStage::ReadSpawnRequest, &format!("{e}"));
                return Err(e);
            }
        };

        // Non-sensitive shape only: NO env values, NO command args, NO creds.
        runner_debug_log(&format!(
            "spawn request received: program={:?} argc={} tty={} read_only={} cap_sids={} workspace_roots={} env_keys={}",
            request
                .command
                .first()
                .map(String::as_str)
                .unwrap_or("<none>"),
            request.command.len(),
            request.tty,
            request.permission_profile.read_only,
            request.cap_sids.len(),
            request.workspace_roots.len(),
            request.env.len(),
        ));

        // ConPTY stub site 1: fail closed, never downgrade to pipes silently.
        if request.tty {
            runner_debug_log("rejecting tty=true (ConPTY is Step 5)");
            send_error(
                &mut writer,
                ErrorStage::SpawnChild,
                "ConPTY sessions (tty=true) are Step 5, not implemented",
            );
            anyhow::bail!("tty=true requested but ConPTY is not implemented (Step 5)");
        }

        match spawn_and_stream(&request, &mut writer) {
            Ok(()) => Ok(()),
            Err(e) => {
                send_error(&mut writer, ErrorStage::SpawnChild, &format!("{e}"));
                Err(e)
            }
        }
    }

    fn send_error(writer: &mut File, stage: ErrorStage, message: &str) {
        let _ = ipc_framed::write_frame(
            &mut *writer,
            &FramedMessage {
                version: ipc_framed::IPC_PROTOCOL_VERSION,
                message: Message::Error {
                    payload: ipc_framed::ErrorPayload {
                        message: message.to_string(),
                        stage,
                        windows_error_code: None,
                    },
                },
            },
        );
    }

    /// Derives the capability-restricted token from the runner's own token,
    /// applies the per-task ACL grants, spawns the child via
    /// `spawn_process_with_pipes`, and streams Output/Exit frames.
    fn spawn_and_stream(request: &SpawnRequest, writer: &mut File) -> Result<()> {
        // Convert the capability SID strings into LocalSid pointers.
        let cap_sids: Vec<LocalSid> = request
            .cap_sids
            .iter()
            .map(|s| LocalSid::from_string(s))
            .collect::<Result<Vec<_>>>()?;
        let cap_ptrs: Vec<*mut c_void> = cap_sids.iter().map(|s| s.as_ptr()).collect();

        // The per-task allow-write ACEs are applied by the ELEVATED parent
        // (run_windows_sandbox_capture) BEFORE this runner is launched, because
        // this runner runs as the low-privilege sandbox account and has no
        // WRITE_DAC on the workspace root (a runner-side SetNamedSecurityInfoW
        // was denied, so the ACE never landed -- the (c) write-inside failure).
        // The cap SIDs below are still what the restricted token carries.
        runner_debug_log(&format!(
            "per-task allow ACEs applied parent-side; deriving token with {} cap sid(s)",
            cap_ptrs.len()
        ));

        // SAFETY: get_current_token_for_restriction returns the runner's own
        // primary token; the *_and_user_from builders derive a capability-
        // restricted token whose token user is this (sandbox) account. Both
        // handles are closed below. Because the derived token comes from the
        // caller's OWN token it is assignable, so the CreateProcessAsUserW
        // inside spawn_process_with_pipes needs no privilege (A.Q1).
        runner_debug_log(&format!(
            "deriving capability-restricted token from own token (read_only={})",
            request.permission_profile.read_only
        ));
        let token = unsafe {
            let base = get_current_token_for_restriction()?;
            let derived = if request.permission_profile.read_only {
                create_readonly_token_with_caps_and_user_from(base, &cap_ptrs)
            } else {
                create_workspace_write_token_with_caps_and_user_from(base, &cap_ptrs)
            };
            CloseHandle(base);
            derived?
        };
        runner_debug_log("token derived; spawning inner child via CreateProcessAsUserW");

        let spawn_result = spawn_process_with_pipes(
            token,
            &request.command,
            &request.cwd,
            &request.env,
            StdinMode::Closed,
            StderrMode::Separate,
            ConsoleMode::NoWindow,
            request.use_private_desktop,
            None,
        );
        // SAFETY: `token` is a valid handle from the token builder above; close
        // it once the spawn has consumed it (the child holds its own copy).
        unsafe {
            CloseHandle(token);
        }
        let handles = spawn_result.map_err(|e| {
            runner_debug_log(&format!("spawn_process_with_pipes FAILED: {e}"));
            e
        })?;
        // Greptile finding, PR #401: nothing previously controlled this child
        // once spawned, so an IPC write failure or a parent disconnect below
        // orphaned it with no controller left to stop it. Assign it to a
        // kill-on-close Job Object now so any early return in `stream_child`
        // (the SpawnReady write, a drain error, or the wait) terminates the
        // child instead of leaving it running unsupervised. If job setup
        // itself fails, terminate the child directly rather than propagate
        // the error with an unconfined child still alive.
        let job_guard = match confine_child_to_job(handles.process.hProcess) {
            Ok(guard) => guard,
            Err(e) => {
                runner_debug_log(&format!(
                    "confine_child_to_job FAILED: {e}; terminating unconfined child"
                ));
                // SAFETY: `handles.process.hProcess` is the live handle from
                // `spawn_process_with_pipes` above; terminating it here (job
                // setup failed) is the fail-closed choice over leaving it
                // running with no containment at all.
                unsafe {
                    let _ = TerminateProcess(handles.process.hProcess, 1);
                }
                return Err(e);
            }
        };
        runner_debug_log(&format!(
            "inner child spawned (pid={}); confined to kill-on-close job; acking SpawnReady and streaming",
            handles.process.dwProcessId
        ));

        let result = stream_child(handles, writer);
        // By the time `stream_child` returns `Ok`, the child has already
        // exited (it waits on the process handle before returning), so
        // dropping the job here is a no-op in that case; on `Err` it
        // terminates whatever is still running.
        drop(job_guard);
        result
    }

    /// RAII guard that terminates the sandboxed child if dropped before the
    /// child has already exited on its own: closing the last handle to a job
    /// created with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` terminates every
    /// process still in it. Mirrors the base (non-elevated) variant's Step 1
    /// containment (`windows.rs::create_job_object`), reused here for the
    /// elevated path via `windows-sys` to interoperate with this module's
    /// `HANDLE` type (see the module's own `windows-sys` vs `windows` crate
    /// note at the top of this file).
    struct ChildJobGuard(HANDLE);

    impl Drop for ChildJobGuard {
        fn drop(&mut self) {
            // SAFETY: `self.0` is a job handle created by `CreateJobObjectW`
            // in `confine_child_to_job`; closing it is safe at any point and,
            // with KILL_ON_JOB_CLOSE set, is exactly the desired cleanup.
            unsafe {
                let _ = CloseHandle(self.0);
            }
        }
    }

    /// Creates a job object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` and
    /// assigns `process` to it immediately after spawn. Unlike the base
    /// variant's Step 1 sequence, the child here is not `CREATE_SUSPENDED`
    /// (upstream `create_process_as_user` never suspends it, and changing
    /// that vendored primitive is out of scope here), so there is a narrow
    /// window between spawn and this call in which the child runs outside
    /// any job; that window is unchanged from before this fix. What this
    /// closes is the much larger gap after it: previously nothing controlled
    /// the child for the rest of its life, so an IPC failure or parent
    /// disconnect at any later point orphaned it (Greptile finding, PR #401).
    fn confine_child_to_job(process: HANDLE) -> Result<ChildJobGuard> {
        // SAFETY: standard CreateJobObjectW / SetInformationJobObject /
        // AssignProcessToJobObject sequence. `process` is the live handle
        // from `spawn_process_with_pipes`, still owned and closed by its
        // existing caller; the job handle is owned by the returned guard and
        // closed on drop.
        unsafe {
            let job = CreateJobObjectW(std::ptr::null(), std::ptr::null());
            if job == 0 {
                anyhow::bail!("CreateJobObjectW failed: {}", GetLastError());
            }
            let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
            limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            if SetInformationJobObject(
                job,
                JobObjectExtendedLimitInformation,
                &limits as *const _ as *const c_void,
                std::mem::size_of_val(&limits) as u32,
            ) == 0
            {
                let err = GetLastError();
                CloseHandle(job);
                anyhow::bail!("SetInformationJobObject failed: {err}");
            }
            if AssignProcessToJobObject(job, process) == 0 {
                let err = GetLastError();
                CloseHandle(job);
                anyhow::bail!("AssignProcessToJobObject failed: {err}");
            }
            Ok(ChildJobGuard(job))
        }
    }

    /// Streams the child's stdout/stderr as Output frames and its exit as an
    /// Exit frame, after acking with SpawnReady.
    fn stream_child(handles: PipeSpawnHandles, writer: &mut File) -> Result<()> {
        let process = handles.process.hProcess;
        let pid = handles.process.dwProcessId;

        ipc_framed::write_frame(
            &mut *writer,
            &FramedMessage {
                version: ipc_framed::IPC_PROTOCOL_VERSION,
                message: Message::SpawnReady {
                    payload: SpawnReady { process_id: pid },
                },
            },
        )?;

        // DEFERRED, not fixed here (CodeRabbit + Greptile findings, PR #401
        // review round; VENDORING.md tracks both): draining stdout to EOF
        // before touching stderr can deadlock if the child fills the stderr
        // pipe buffer while stdout stays open (the child blocks on stderr,
        // this runner blocks waiting for stdout EOF, neither ever proceeds).
        // The correct fix is a concurrent two-thread drain (one per stream)
        // with the frame `writer` behind a shared lock, which changes this
        // exact lab-validated (D-004) spawn/stream path from single- to
        // multi-threaded framing and needs re-validation on spike307-win
        // before landing, not a blind rewrite. Tracked, not silently ignored.
        drain_stream(handles.stdout_read, OutputStream::Stdout, &mut *writer)?;
        if let Some(err_read) = handles.stderr_read {
            drain_stream(err_read, OutputStream::Stderr, &mut *writer)?;
        }

        // DEFERRED, not fixed here (Greptile finding, PR #401 review round;
        // VENDORING.md tracks it): `SpawnRequest::timeout_ms` is plumbed
        // through the protocol but never read here, so the wait below is
        // always `INFINITE` and `timed_out` below is always `false`. Today
        // this is dormant, not reachable: the sole call site that builds a
        // `SpawnRequest` (see `run_windows_sandbox_capture` in this file)
        // always sets `timeout_ms: None`. Enforcing a real deadline correctly
        // needs a watchdog concurrent with the drains above (the same
        // restructuring as the drain-deadlock deferral just above, since a
        // hang during drain must also be bounded, not only the final wait),
        // so it is tracked together with that fix rather than half-done here.
        //
        // SAFETY: `process` is the child's process handle from PROCESS_INFORMATION;
        // wait for it to exit, then read its exit code.
        let exit_code = unsafe {
            if WaitForSingleObject(process, INFINITE) != WAIT_OBJECT_0 {
                anyhow::bail!("WaitForSingleObject on child failed: {}", GetLastError());
            }
            let mut code: u32 = 0;
            if GetExitCodeProcess(process, &mut code) == 0 {
                anyhow::bail!("GetExitCodeProcess failed: {}", GetLastError());
            }
            code as i32
        };
        runner_debug_log(&format!(
            "child pid={pid} exited code={exit_code}; sending Exit frame"
        ));

        ipc_framed::write_frame(
            &mut *writer,
            &FramedMessage {
                version: ipc_framed::IPC_PROTOCOL_VERSION,
                message: Message::Exit {
                    payload: ipc_framed::ExitPayload {
                        exit_code,
                        timed_out: false,
                    },
                },
            },
        )?;
        Ok(())
    }

    /// Reads a child pipe handle to EOF, emitting Output frames.
    fn drain_stream(handle: HANDLE, stream: OutputStream, writer: &mut File) -> Result<()> {
        // Wrap the raw read handle as a File so we get std buffered reads; the
        // handle ownership transfers here and is closed on drop.
        use std::os::windows::io::FromRawHandle;
        // SAFETY: `handle` is a live read end of an anonymous pipe from
        // spawn_process_with_pipes; wrapping it in a File takes ownership and
        // closes it on drop. isize -> *mut c_void is the documented raw-handle
        // representation on Windows.
        let mut file = unsafe { File::from_raw_handle(handle as *mut _) };
        let mut buf = [0u8; 8192];
        loop {
            match file.read(&mut buf) {
                Ok(0) => break,
                Ok(n) => {
                    ipc_framed::write_frame(
                        &mut *writer,
                        &FramedMessage {
                            version: ipc_framed::IPC_PROTOCOL_VERSION,
                            message: Message::Output {
                                payload: OutputPayload {
                                    data_b64: ipc_framed::encode_bytes(&buf[..n]),
                                    stream,
                                },
                            },
                        },
                    )?;
                }
                Err(ref e) if e.kind() == std::io::ErrorKind::BrokenPipe => break,
                Err(e) => return Err(anyhow::anyhow!("read child {stream:?}: {e}")),
            }
        }
        Ok(())
    }
}

// ===========================================================================
// Non-Windows honest fail-closed stubs (D-005), mirroring elevated_impl's
// stub: the compose only exists on Windows; on other targets every entry point
// returns an error rather than pretending to confine.
// ===========================================================================

#[cfg(not(windows))]
mod non_windows_stub {
    use super::*;
    use std::collections::HashMap;

    /// See the Windows impl. Non-Windows honest fail-closed stub.
    pub fn spawn_confined_for_validation(
        _sandbox_home: &Path,
        _policy: &SandboxPolicy,
        _command: &[String],
        _cwd: &Path,
        _env: &HashMap<String, String>,
    ) -> Result<CaptureResult, LaunchError> {
        Err(LaunchError::Confinement(
            "the Windows elevated sandbox is only available on Windows".to_string(),
        ))
    }
}

#[cfg(not(windows))]
pub use non_windows_stub::spawn_confined_for_validation;

#[cfg(test)]
mod tests {
    use super::*;
    use crate::policy::SandboxPolicy;
    use std::path::PathBuf;

    fn policy(network: NetworkPolicy) -> SandboxPolicy {
        SandboxPolicy::build(vec![], vec![], PathBuf::from(r"C:\hive\hooks"), network)
            .expect("valid policy")
    }

    #[test]
    fn deny_all_refuses_with_network_confinement_error() {
        let decision = LaunchDecision::for_policy(&policy(NetworkPolicy::DenyAll));
        assert_eq!(decision, LaunchDecision::RefuseDenyAll);
        assert!(matches!(
            decision.into_refusal(),
            LaunchError::NetworkConfinementNotImplemented
        ));
    }

    #[test]
    fn allow_hosts_refuses_with_allow_hosts_error() {
        let decision =
            LaunchDecision::for_policy(&policy(NetworkPolicy::AllowHosts(vec!["h".into()])));
        assert_eq!(decision, LaunchDecision::RefuseAllowHosts);
        assert!(matches!(
            decision.into_refusal(),
            LaunchError::AllowHostsNotYetImplemented
        ));
    }

    #[test]
    fn compute_allow_paths_includes_existing_writable_roots_and_no_deny() {
        let tmp = tempfile::tempdir().expect("tempdir");
        let ws = tmp.path().join("workspace");
        std::fs::create_dir_all(&ws).expect("mkdir");
        // A writable root under an absolute cwd. On Linux CI the tempdir path is
        // absolute in host terms; from_policy uses windows-absolute parsing, so
        // build the resolver directly to keep this test host-agnostic.
        let policy = SandboxPolicy::build(
            vec![ws.clone()],
            vec![],
            tmp.path().join("hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("policy");
        // Resolve against a windows-absolute cwd string (the roots themselves
        // are what compute_allow_paths canonicalizes; cwd only gates from_policy).
        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");
        let paths = compute_allow_paths(&resolved);
        let canonical_ws = dunce::canonicalize(&ws).unwrap_or(ws);
        assert!(
            paths.allow.contains(&canonical_ws),
            "expected the existing writable root in the allow set: {paths:?}"
        );
        assert!(
            paths.deny.is_empty(),
            "the Hive resolver produces no read-only carve-outs yet, so deny must be empty"
        );
    }

    #[test]
    fn compute_allow_paths_skips_nonexistent_writable_root() {
        let policy = SandboxPolicy::build(
            vec![PathBuf::from(r"C:\does\not\exist\anywhere")],
            vec![],
            PathBuf::from(r"C:\hive\hooks"),
            NetworkPolicy::DenyAll,
        )
        .expect("policy");
        let resolved =
            ResolvedWindowsSandboxPermissions::from_policy(&policy, Path::new(r"C:\workspace"))
                .expect("resolves");
        let paths = compute_allow_paths(&resolved);
        assert!(
            paths.allow.is_empty(),
            "a non-existent writable root must not be added to the allow set"
        );
    }

    #[test]
    fn setup_is_incomplete_without_marker() {
        let tmp = tempfile::tempdir().expect("tempdir");
        assert!(!sandbox_setup_is_complete(tmp.path()));
    }
}
