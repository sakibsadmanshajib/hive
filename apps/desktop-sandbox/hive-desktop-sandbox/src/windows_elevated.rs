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
//! restricted primary token FROM ITS OWN token
//! (`get_current_token_for_restriction` +
//! `create_sandbox_restricted_token_from`) and spawns the inner child with
//! `CreateProcessAsUserW` (via `spawn_process_with_pipes`). A token derived
//! from the caller's own token is assignable, so that call needs no privilege.
//! UAC prompts exactly once, at provisioning, when the setup binary runs
//! elevated to create the OS account.
//!
//! ## Network semantics (blueprint Q2 / D-005, Integration B2 LIVE)
//!
//! Integration B2 makes egress enforcement LIVE and two-layer (D-011), all
//! keyed on the sandbox account SID (D-010):
//!   * PROVISION installs the persistent fence: the WFP per-protocol + core
//!     filters (loopback permit above SID block-all) and the firewall
//!     block-all-outbound rule.
//!   * PER TASK the compose ([`run_windows_sandbox_capture`]) installs the
//!     firewall loopback allow; `DenyAll` blocks all egress, `AllowHosts`
//!     routes through the loopback [`crate::egress_proxy::AllowlistProxy`]
//!     (injecting `HTTP(S)_PROXY` / `NO_PROXY` into the child env). A task-end
//!     RAII guard re-blocks loopback on every return path.
//!
//! The public [`crate::launch`] dispatches to this compose (it no longer
//! refuses either policy). Because the child runs UNDER the sandbox SID, the
//! SID-keyed fence applies to it (the whole point versus the base backend).
//! Fail-closed (D-005): a crash leaves the persistent block-all in force, and a
//! failed fence step aborts the launch rather than opening egress. Only one
//! fenced task runs at a time (single shared SID / loopback rule, D-003),
//! enforced by an [`ActiveTaskGuard`] CAS.
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

/// The spawn shape [`crate::launch`] reaches for a policy BEFORE any spawn.
///
/// Integration B2 activation flipped the two former `Refuse*` variants to
/// `Spawn*`: `launch` no longer refuses either network policy. It dispatches to
/// the SID-fenced elevated compose ([`run_windows_sandbox_capture`]), which, on
/// top of the persistent WFP + firewall block-all installed at provision,
/// installs the per-task loopback firewall allow and (for `AllowHosts`) starts
/// the loopback egress proxy and injects `HTTP(S)_PROXY`. Keeping this as an
/// explicit enum (rather than two inline `if`s) keeps the policy -> spawn-shape
/// mapping independently unit-testable.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum LaunchDecision {
    /// `NetworkPolicy::DenyAll`: spawn with all egress blocked, no proxy.
    SpawnDenyAll,
    /// `NetworkPolicy::AllowHosts`: spawn with the loopback egress proxy
    /// fronting exactly `hosts`.
    SpawnAllowHosts { hosts: Vec<String> },
}

impl LaunchDecision {
    /// Pure mapping from a policy's network posture to the launch spawn shape.
    pub fn for_policy(policy: &SandboxPolicy) -> Self {
        match policy.network() {
            NetworkPolicy::AllowHosts(hosts) => LaunchDecision::SpawnAllowHosts {
                hosts: hosts.clone(),
            },
            NetworkPolicy::DenyAll => LaunchDecision::SpawnDenyAll,
        }
    }
}

// -------- Single-active-task guard (pure helper; RAII lives in the compose) --

/// Attempts to acquire a single-active-task flag by CAS `false -> true`.
/// Returns `true` iff the flag was free (this caller now owns it). Its only
/// non-test caller is the Windows compose, and its only Linux use is the unit
/// test below, so it is gated `cfg(any(windows, test))`: present (and used) on
/// Windows and under test, absent on the plain native build where nothing
/// would call it (so it is never dead code).
///
/// Only one fenced sandbox task may run at a time because every task shares one
/// firewall loopback rule keyed on the single sandbox account SID (D-003); a
/// second concurrent launch would clobber the first task's per-task loopback
/// allow, so it is rejected instead (blocker a, fail-closed). This in-process
/// CAS is layered under a cross-process named mutex (see the compose) so two
/// separate processes cannot both enter the fence-config critical section.
#[cfg(any(windows, test))]
fn acquire_single_task_flag(flag: &std::sync::atomic::AtomicBool) -> bool {
    flag.compare_exchange(
        false,
        true,
        std::sync::atomic::Ordering::SeqCst,
        std::sync::atomic::Ordering::SeqCst,
    )
    .is_ok()
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

// ---- Sandbox-home DACL (decision D-013, Part A) ---------------------------

/// `NT AUTHORITY\SYSTEM`.
const LOCAL_SYSTEM_SID: &str = "S-1-5-18";
/// `BUILTIN\Administrators`.
const BUILTIN_ADMINISTRATORS_SID: &str = "S-1-5-32-544";

/// Broad group SIDs that must never be granted anything in the sandbox-home
/// DACL. The lab found `C:\hivesbx` carrying an INHERITED
/// `NT AUTHORITY\Authenticated Users:(I)(M)` grant, which is exactly why the
/// DACL is now protected (inheritance broken) rather than merely appended to.
/// Any of these as a trustee would re-open the hole the protection closes.
pub const FORBIDDEN_SANDBOX_HOME_TRUSTEES: [&str; 4] = [
    "S-1-5-11",     // Authenticated Users
    "S-1-5-32-545", // BUILTIN\Users
    "S-1-1-0",      // Everyone
    "S-1-5-4",      // INTERACTIVE
];

/// Access one trustee gets on the sandbox home. Both variants are inheritable
/// so the tree below the root (notably `.sandbox-bin`, which holds the
/// materialized command-runner image the sandbox account must be able to load)
/// is covered without a second pass.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SandboxHomeAccess {
    /// Full control. Provisioning, helper materialization, log writing, repair
    /// and uninstall all need it.
    FullControl,
    /// Read plus traverse/execute only. No write, no `DELETE`, and in
    /// particular no `FILE_DELETE_CHILD` on the root, so the holder cannot
    /// remove sibling entries such as the `secrets` directory.
    ReadExecute,
}

/// One ACE of the explicit sandbox-home DACL.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SandboxHomeAce {
    pub sid: String,
    pub access: SandboxHomeAccess,
}

/// Builds the explicit, protected DACL for the sandbox home (decision D-013,
/// Part A).
///
/// Removing the restricting-SID set from the child token (see
/// `codex_windows_sandbox::token::create_sandbox_restricted_token_from`) removes
/// the blanket second write check that was the only thing suppressing the
/// inherited `Authenticated Users` Modify grant on this tree. NTFS ACEs are now
/// the entire write defense here, so the root's DACL is replaced wholesale and
/// marked protected (inheritance broken, inherited ACEs NOT copied in) rather
/// than being appended to.
///
/// The sandbox account gets read plus execute only: it must be able to load the
/// materialized runner image out of `.sandbox-bin`, and nothing more. It gets no
/// write anywhere under the sandbox home; the sealed-credential `secrets`
/// directory additionally keeps its explicit deny-read ACE, which provisioning
/// applies after this DACL and which beats the inherited allow-read from here.
///
/// Fails closed on a caller that would widen the DACL: a broad group trustee,
/// an empty SID, or a sandbox account SID equal to the provisioning user's (the
/// latter would silently promote the sandbox account to full control).
pub fn sandbox_home_dacl_entries(
    provisioning_user_sid: &str,
    sandbox_account_sid: &str,
) -> Result<Vec<SandboxHomeAce>, String> {
    if provisioning_user_sid.is_empty() || sandbox_account_sid.is_empty() {
        return Err(
            "sandbox-home DACL needs a non-empty provisioning user and sandbox account SID"
                .to_string(),
        );
    }
    if provisioning_user_sid.eq_ignore_ascii_case(sandbox_account_sid) {
        return Err(format!(
            "sandbox account SID {sandbox_account_sid} is also the provisioning user SID; \
             that would grant the sandbox account full control of its own home"
        ));
    }
    for sid in [provisioning_user_sid, sandbox_account_sid] {
        if FORBIDDEN_SANDBOX_HOME_TRUSTEES
            .iter()
            .any(|forbidden| forbidden.eq_ignore_ascii_case(sid))
        {
            return Err(format!(
                "{sid} is a broad group SID and must never be a sandbox-home DACL trustee"
            ));
        }
    }

    Ok(vec![
        SandboxHomeAce {
            sid: LOCAL_SYSTEM_SID.to_string(),
            access: SandboxHomeAccess::FullControl,
        },
        SandboxHomeAce {
            sid: BUILTIN_ADMINISTRATORS_SID.to_string(),
            access: SandboxHomeAccess::FullControl,
        },
        // The desktop app itself runs NON-elevated as this user and owns the
        // tree: it materializes the runner helper, seals and reads the
        // credential, and writes the egress-fence log. Without it, protecting
        // the DACL would lock the product out of its own sandbox home.
        SandboxHomeAce {
            sid: provisioning_user_sid.to_string(),
            access: SandboxHomeAccess::FullControl,
        },
        SandboxHomeAce {
            sid: sandbox_account_sid.to_string(),
            access: SandboxHomeAccess::ReadExecute,
        },
    ])
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
    use crate::windows_firewall;
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
        LocalSid, create_sandbox_restricted_token_from, get_current_token_for_restriction,
        get_user_sid_bytes,
    };
    use codex_windows_sandbox::{acl, dpapi, hide_users, sandbox_users};
    use std::collections::HashMap;
    use std::ffi::c_void;
    use std::fs::File;
    use std::io::{Read, Write};
    use std::sync::Mutex;
    use std::sync::atomic::AtomicBool;

    use windows_sys::Win32::Foundation::{
        CloseHandle, GetLastError, HANDLE, WAIT_ABANDONED, WAIT_OBJECT_0, WAIT_TIMEOUT,
    };
    use windows_sys::Win32::Foundation::{ERROR_SUCCESS, HLOCAL, LocalFree};
    use windows_sys::Win32::Security::Authentication::Identity::{
        LSA_HANDLE, LSA_OBJECT_ATTRIBUTES, LSA_UNICODE_STRING, LsaAddAccountRights, LsaClose,
        LsaOpenPolicy, POLICY_CREATE_ACCOUNT, POLICY_LOOKUP_NAMES,
    };
    use windows_sys::Win32::Security::Authorization::{
        EXPLICIT_ACCESS_W, SetEntriesInAclW, SetNamedSecurityInfoW, TRUSTEE_IS_SID,
        TRUSTEE_IS_UNKNOWN, TRUSTEE_W,
    };
    use windows_sys::Win32::Security::{
        ACL, DACL_SECURITY_INFORMATION, PROTECTED_DACL_SECURITY_INFORMATION,
    };
    use windows_sys::Win32::Storage::FileSystem::{
        DELETE, FILE_ALL_ACCESS, FILE_APPEND_DATA, FILE_DELETE_CHILD, FILE_GENERIC_EXECUTE,
        FILE_GENERIC_READ, FILE_WRITE_ATTRIBUTES, FILE_WRITE_DATA, FILE_WRITE_EA,
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
        CreateMutexW, GetExitCodeProcess, INFINITE, ReleaseMutex, TerminateProcess,
        WaitForSingleObject,
    };

    // The runner-spawn transport wants a "codex_home"-shaped base directory for
    // its runner-exe resolution and log dir; Hive passes the sandbox home. The
    // name is kept as `codex_home` at the vendored call boundary only.
    type Result<T> = anyhow::Result<T>;

    // -------------------------------------------------------------------
    // Per-task egress fence guards (Integration B2 activation)
    // -------------------------------------------------------------------

    /// Process-wide single-active-task flag. Only one fenced sandbox task may
    /// run at a time because all tasks share one firewall loopback rule keyed
    /// on the single sandbox account SID (D-003).
    static SANDBOX_TASK_ACTIVE: AtomicBool = AtomicBool::new(false);

    /// RAII holder of the [`SANDBOX_TASK_ACTIVE`] flag; releases on drop.
    struct ActiveTaskGuard;

    impl ActiveTaskGuard {
        /// Acquires the single-active-task flag, or `None` if another fenced
        /// task is already active (blocker a: reject rather than clobber the
        /// shared loopback rule).
        fn acquire() -> Option<Self> {
            if acquire_single_task_flag(&SANDBOX_TASK_ACTIVE) {
                Some(ActiveTaskGuard)
            } else {
                None
            }
        }
    }

    impl Drop for ActiveTaskGuard {
        fn drop(&mut self) {
            SANDBOX_TASK_ACTIVE.store(false, std::sync::atomic::Ordering::SeqCst);
        }
    }

    /// Machine-global cross-process lock for the egress-fence critical section
    /// (F2). The in-process [`SANDBOX_TASK_ACTIVE`] CAS only guards one process;
    /// this named mutex additionally rejects a SECOND PROCESS (the validate bin,
    /// or a second app instance) that would otherwise reconfigure the single
    /// OS-global firewall loopback rule concurrently. Held for the task duration
    /// by [`EgressFenceGuard`], layered UNDER the CAS.
    struct CrossProcessFenceLock(HANDLE);

    impl CrossProcessFenceLock {
        /// Acquires the machine-global egress-fence mutex without blocking.
        /// `Ok(Some)` = acquired. `Ok(None)` = another process holds it, so the
        /// caller rejects the launch fail-closed. `WAIT_ABANDONED` (a crashed
        /// prior holder) counts as acquired, since Windows auto-releases an
        /// abandoned mutex.
        fn acquire() -> Result<Option<Self>> {
            // ponytail: the Global\ namespace needs SeCreateGlobalPrivilege; the
            // elevated provisioner has it. If a non-elevated launcher cannot
            // create it, CreateMutexW fails and we reject fail-closed. Upgrade
            // path: fall back to a Local\ (per-session) name if same-session
            // isolation is ever sufficient.
            let name: Vec<u16> = "Global\\HiveSandboxEgressFence\0".encode_utf16().collect();
            // SAFETY: CreateMutexW with a null security-attributes pointer, a
            // non-owning initial flag, and a NUL-terminated UTF-16 name we own
            // for the call; it returns a handle this value takes ownership of.
            let handle = unsafe { CreateMutexW(std::ptr::null(), 0, name.as_ptr()) };
            if handle == 0 {
                anyhow::bail!(
                    "CreateMutexW(Global\\HiveSandboxEgressFence) failed: {} (needs SeCreateGlobalPrivilege)",
                    unsafe { GetLastError() }
                );
            }
            // SAFETY: `handle` is the mutex just created; wait with a zero
            // timeout so a contended lock returns WAIT_TIMEOUT immediately.
            let wait = unsafe { WaitForSingleObject(handle, 0) };
            match wait {
                WAIT_OBJECT_0 | WAIT_ABANDONED => Ok(Some(Self(handle))),
                WAIT_TIMEOUT => {
                    // SAFETY: `handle` is owned; close it since we did not acquire.
                    unsafe {
                        CloseHandle(handle);
                    }
                    Ok(None)
                }
                other => {
                    // SAFETY: `handle` is owned; close it before erroring.
                    unsafe {
                        CloseHandle(handle);
                    }
                    anyhow::bail!(
                        "WaitForSingleObject on the egress-fence mutex failed: 0x{other:08x}"
                    )
                }
            }
        }
    }

    impl Drop for CrossProcessFenceLock {
        fn drop(&mut self) {
            // SAFETY: `self.0` is the mutex handle this value acquired; release
            // ownership then close the handle. Both are safe on an owned handle.
            unsafe {
                let _ = ReleaseMutex(self.0);
                let _ = CloseHandle(self.0);
            }
        }
    }

    /// Per-task egress-fence teardown guard. On drop it re-blocks loopback for
    /// the sandbox SID (removing the per-task proxy allow) and releases the
    /// single-active-task flag, so EVERY return path from the compose (normal
    /// return or early error) restores the fail-closed baseline: proxy gone,
    /// loopback re-blocked, persistent block-all still up (blocker c: PROVISION
    /// owns the persistent fence, TASK-END owns this per-task teardown).
    /// Best-effort: a teardown error is logged but cannot re-open egress, since
    /// the persistent provision-time block-all stays in force regardless.
    struct EgressFenceGuard {
        sid: String,
        fence_log_path: PathBuf,
        // Dropped after the teardown below (which runs in Drop::drop, before any
        // field drops): releases the single-active flag, then the cross-process
        // mutex, so the loopback re-block happens while this process still owns
        // the fence.
        _active: ActiveTaskGuard,
        _xproc: CrossProcessFenceLock,
    }

    impl Drop for EgressFenceGuard {
        fn drop(&mut self) {
            let mut log: Box<dyn Write> = match std::fs::OpenOptions::new()
                .create(true)
                .append(true)
                .open(&self.fence_log_path)
            {
                Ok(file) => Box::new(file),
                Err(_) => Box::new(std::io::sink()),
            };
            if let Err(err) =
                windows_firewall::teardown_offline_proxy_allowlist(&self.sid, &mut *log)
            {
                let _ = writeln!(
                    log,
                    "egress fence teardown FAILED (persistent block-all still enforced, egress stays closed): {err}"
                );
            }
        }
    }

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
    // Provisioning (identity.rs / setup.rs port; WFP + firewall INSTALLED)
    // -------------------------------------------------------------------

    /// One-time, ELEVATED provisioning of the shared low-privilege sandbox
    /// account. Port of the provisioning half of `identity.rs` /`setup.rs`
    /// (`run_elevated_provisioning_setup`), retargeted to the Hive sandbox home.
    /// Integration B2: it now INSTALLS the persistent, SID-keyed egress fence
    /// (WFP per-protocol + core filters and the firewall block-all-outbound)
    /// before the readiness marker, so the fence is up before the first task.
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
    ///    then install the persistent egress fence (WFP + firewall block-all),
    ///    SID-keyed, fail-closed (Integration B2);
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

        // 1b. Explicit protected DACL on the sandbox home (decision D-013,
        //     Part A). Applied as soon as the account exists (its SID is a
        //     trustee) and BEFORE the secrets deny-read below, so that deny is
        //     layered on top of this DACL rather than wiped by it. Fail-closed:
        //     `?` aborts long before the readiness marker is written.
        apply_sandbox_home_acl(sandbox_home, log)?;

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

        // 4b. Persistent egress fence (WFP + firewall block-all), SID-keyed,
        //     fail-closed. Installed here so the fence is up before the first
        //     task ever launches; `?` aborts provisioning (and the marker below
        //     is never written) if either half fails, so a partially-fenced
        //     account is never reported provisioned (D-005).
        // WFP per-protocol + core filters (persistent, keyed on the account
        // name -> SID).
        codex_windows_sandbox::wfp_setup::install_wfp_filters(SANDBOX_USERNAME, |line| {
            let _ = writeln!(log, "{line}");
        })?;
        // Firewall block-all outbound (non-loopback), SID-keyed.
        let fence_sid = codex_windows_sandbox::winutil::string_from_sid_bytes(
            &sandbox_users::resolve_sid(SANDBOX_USERNAME)?,
        )
        .map_err(anyhow::Error::msg)?;
        crate::windows_firewall::ensure_offline_outbound_block(&fence_sid, log)?;

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

    /// Win32 access mask for one [`SandboxHomeAccess`].
    fn sandbox_home_access_mask(access: SandboxHomeAccess) -> u32 {
        match access {
            SandboxHomeAccess::FullControl => FILE_ALL_ACCESS,
            // Deliberately NOT FILE_ALL_ACCESS and deliberately without DELETE
            // or FILE_DELETE_CHILD: the sandbox account may read and traverse
            // the tree (it must load the runner image out of `.sandbox-bin`)
            // but may neither modify an entry nor unlink a sibling.
            SandboxHomeAccess::ReadExecute => FILE_GENERIC_READ | FILE_GENERIC_EXECUTE,
        }
    }

    /// Inherit to both child containers and child objects, so one ACE on the
    /// root covers the whole sandbox-home tree.
    const CONTAINER_AND_OBJECT_INHERIT: u32 = 0x2 | 0x1;

    /// Write-implying access bits ONLY. `FILE_GENERIC_WRITE` cannot be used for
    /// a "does this trustee have write" probe because it shares `SYNCHRONIZE`
    /// and `READ_CONTROL` with `FILE_GENERIC_READ`, so an any-bit match against
    /// it reports true for a pure read grant.
    const WRITE_IMPLYING_BITS: u32 = FILE_WRITE_DATA
        | FILE_APPEND_DATA
        | FILE_WRITE_EA
        | FILE_WRITE_ATTRIBUTES
        | DELETE
        | FILE_DELETE_CHILD;

    /// Replaces the sandbox home's DACL with the explicit, PROTECTED DACL from
    /// [`sandbox_home_dacl_entries`] (decision D-013, Part A), then verifies
    /// fail-closed that the result actually denies write to the sandbox account
    /// and to every broad group trustee.
    ///
    /// Protecting the DACL is the whole point: passing
    /// `PROTECTED_DACL_SECURITY_INFORMATION` breaks inheritance from the parent
    /// directory, and passing a NULL old-ACL to `SetEntriesInAclW` builds the
    /// new DACL from these entries alone, so the inherited
    /// `Authenticated Users:(I)(M)` grant the lab found on `C:\hivesbx` is not
    /// copied forward.
    fn apply_sandbox_home_acl(sandbox_home: &Path, log: &mut dyn Write) -> Result<()> {
        let sandbox_sid = codex_windows_sandbox::winutil::string_from_sid_bytes(
            &sandbox_users::resolve_sid(SANDBOX_USERNAME)?,
        )
        .map_err(anyhow::Error::msg)?;

        // The provisioning process runs elevated AS the desktop app's own user;
        // that user owns this tree and must keep full control (see
        // `sandbox_home_dacl_entries`).
        // SAFETY: `get_current_token_for_restriction` returns a token handle
        // opened with TOKEN_QUERY; `get_user_sid_bytes` copies its user SID out
        // and the handle is closed on both paths.
        let user_sid_bytes = unsafe {
            let token = get_current_token_for_restriction()?;
            let bytes = get_user_sid_bytes(token);
            CloseHandle(token);
            bytes?
        };
        let provisioning_user_sid =
            codex_windows_sandbox::winutil::string_from_sid_bytes(&user_sid_bytes)
                .map_err(anyhow::Error::msg)?;

        let aces = sandbox_home_dacl_entries(&provisioning_user_sid, &sandbox_sid)
            .map_err(|e| anyhow::anyhow!("build sandbox-home DACL: {e}"))?;
        let trustee_sids: Vec<LocalSid> = aces
            .iter()
            .map(|ace| LocalSid::from_string(&ace.sid))
            .collect::<Result<Vec<_>>>()?;
        let explicit: Vec<EXPLICIT_ACCESS_W> = aces
            .iter()
            .zip(trustee_sids.iter())
            .map(|(ace, sid)| EXPLICIT_ACCESS_W {
                grfAccessPermissions: sandbox_home_access_mask(ace.access),
                grfAccessMode: 2, // SET_ACCESS
                grfInheritance: CONTAINER_AND_OBJECT_INHERIT,
                Trustee: TRUSTEE_W {
                    pMultipleTrustee: std::ptr::null_mut(),
                    MultipleTrusteeOperation: 0,
                    TrusteeForm: TRUSTEE_IS_SID,
                    TrusteeType: TRUSTEE_IS_UNKNOWN,
                    ptstrName: sid.as_ptr() as *mut u16,
                },
            })
            .collect();

        // SAFETY: `explicit` borrows SID pointers owned by `trustee_sids`,
        // which outlives this block. A NULL old-ACL makes `SetEntriesInAclW`
        // build a fresh DACL from these entries alone; the resulting ACL is
        // freed on every path.
        unsafe {
            let mut new_dacl: *mut ACL = std::ptr::null_mut();
            let code = SetEntriesInAclW(
                explicit.len() as u32,
                explicit.as_ptr(),
                std::ptr::null_mut(),
                &mut new_dacl,
            );
            if code != ERROR_SUCCESS {
                anyhow::bail!("SetEntriesInAclW for sandbox-home DACL failed: {code}");
            }
            let mut wide = codex_windows_sandbox::winutil::to_wide(sandbox_home);
            let code = SetNamedSecurityInfoW(
                wide.as_mut_ptr(),
                1, // SE_FILE_OBJECT
                DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                new_dacl,
                std::ptr::null_mut(),
            );
            if !new_dacl.is_null() {
                LocalFree(new_dacl as HLOCAL);
            }
            if code != ERROR_SUCCESS {
                anyhow::bail!("SetNamedSecurityInfoW for sandbox-home DACL failed: {code}");
            }
        }

        // Fail-closed verification (D-005): re-read the DACL and prove the
        // properties this whole change depends on, rather than trusting the
        // Win32 return code. A silent no-op here would leave the sandbox with
        // NEITHER the restricting-SID write check NOR an ACL write check.
        let sandbox_local = LocalSid::from_string(&sandbox_sid)?;
        if acl::path_mask_allows(
            sandbox_home,
            &[sandbox_local.as_ptr()],
            WRITE_IMPLYING_BITS,
            false,
        )? {
            anyhow::bail!(
                "sandbox account still has write access to {} after applying the protected DACL",
                sandbox_home.display()
            );
        }
        if !acl::path_mask_allows(
            sandbox_home,
            &[sandbox_local.as_ptr()],
            FILE_GENERIC_READ,
            false,
        )? {
            anyhow::bail!(
                "sandbox account lost read access to {}; it could not load the runner helper",
                sandbox_home.display()
            );
        }
        for forbidden in FORBIDDEN_SANDBOX_HOME_TRUSTEES {
            let sid = LocalSid::from_string(forbidden)?;
            if acl::path_mask_allows(sandbox_home, &[sid.as_ptr()], WRITE_IMPLYING_BITS, false)? {
                anyhow::bail!(
                    "{forbidden} still has write access to {} after protecting the DACL",
                    sandbox_home.display()
                );
            }
        }

        let _ = writeln!(
            log,
            "sandbox-home DACL protected on {} ({} explicit ACEs, sandbox account read+execute only)",
            sandbox_home.display(),
            aces.len()
        );
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

    /// Port of the `tty = false` CAPTURE half of
    /// `elevated_impl.rs::run_windows_sandbox_capture_for_permission_profile`,
    /// retargeted to the Hive resolver and sandbox home. Spawns the Hive
    /// command-runner AS the sandbox user (via the vendored transport, which
    /// uses `CreateProcessWithLogonW`), sends a `tty = false` `SpawnRequest`,
    /// and drives the frame loop to capture stdout/stderr/exit.
    ///
    /// Integration B2: this is now the LIVE network-fenced path. Before
    /// building the `SpawnRequest` it installs the per-task egress fence (the
    /// firewall loopback allow, plus the loopback proxy + `HTTP(S)_PROXY` env
    /// for `AllowHosts`) on top of the persistent WFP + block-all fence from
    /// provision, all keyed on the sandbox SID, and arms an RAII teardown that
    /// re-blocks loopback on every return path. It is reached both through the
    /// public [`crate::launch`] and the lab-only
    /// [`spawn_confined_for_validation`].
    pub fn run_windows_sandbox_capture(
        sandbox_home: &Path,
        policy: &SandboxPolicy,
        command: &[String],
        cwd: &Path,
        env: &HashMap<String, String>,
    ) -> Result<CaptureResult> {
        // ---- Per-task egress fence (Integration B2 activation) ----
        // Single active task (D-003): reject a concurrent fenced launch rather
        // than clobber the shared SID-keyed loopback rule (blocker a).
        let active = ActiveTaskGuard::acquire().ok_or_else(|| {
            anyhow::anyhow!(
                "another sandboxed task is already active; concurrent fenced tasks are not supported (single shared sandbox account, D-003)"
            )
        })?;

        // Cross-process gate (F2): the in-process CAS above only guards this
        // process. Two separate processes (the app plus the validate bin, or two
        // app instances) would each pass their own CAS and then clobber the one
        // OS-global named firewall rule. A machine-global named mutex, layered
        // UNDER the CAS, rejects the second PROCESS fail-closed.
        let xproc = match CrossProcessFenceLock::acquire()? {
            Some(lock) => lock,
            None => anyhow::bail!(
                "another process already holds the egress-fence mutex; concurrent fenced tasks across processes are not supported (blocker a / F2, fail-closed)"
            ),
        };

        // Sandbox account SID: the firewall fence + loopback allow are keyed on
        // it (D-010), the same SID the WFP filters are keyed on at provision.
        let sid = codex_windows_sandbox::winutil::string_from_sid_bytes(
            &sandbox_users::resolve_sid(SANDBOX_USERNAME)?,
        )
        .map_err(anyhow::Error::msg)?;

        let logs_base_dir = sandbox_home.join(".sandbox");
        std::fs::create_dir_all(&logs_base_dir)
            .map_err(|e| anyhow::anyhow!("create sandbox log dir: {e}"))?;
        let fence_log_path = logs_base_dir.join("egress-fence.log");
        let mut fence_log = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&fence_log_path)
            .map_err(|e| anyhow::anyhow!("open egress fence log: {e}"))?;

        // Mutable child env; AllowHosts injects the proxy vars into this clone.
        let mut env = env.clone();

        // Arm the teardown guard BEFORE touching any firewall rule (F4a). If a
        // fence call below fails partway (a mid-narrowing error) or any later
        // step returns early, the guard's Drop still re-blocks loopback (the
        // fail-closed direction) and releases the single-active flag plus the
        // cross-process mutex, on every return path (blocker c).
        let _fence_guard = EgressFenceGuard {
            sid: sid.clone(),
            fence_log_path: fence_log_path.clone(),
            _active: active,
            _xproc: xproc,
        };

        match policy.network() {
            NetworkPolicy::DenyAll => {
                // Block all loopback (no proxy exception). The persistent WFP +
                // firewall block-all (provision) already denies non-loopback.
                windows_firewall::ensure_offline_proxy_allowlist(&sid, &[], false, &mut fence_log)?;
            }
            NetworkPolicy::AllowHosts(hosts) => {
                let proxy = crate::egress_proxy::AllowlistProxy::spawn(hosts.clone())
                    .map_err(|e| anyhow::anyhow!("start loopback egress proxy: {e}"))?;
                let port = proxy.port();
                // Allow loopback ONLY to the bound proxy port; block the rest.
                windows_firewall::ensure_offline_proxy_allowlist(
                    &sid,
                    &[port],
                    false,
                    &mut fence_log,
                )?;
                env.insert("HTTP_PROXY".to_string(), format!("http://127.0.0.1:{port}"));
                env.insert(
                    "HTTPS_PROXY".to_string(),
                    format!("http://127.0.0.1:{port}"),
                );
                env.insert("NO_PROXY".to_string(), "127.0.0.1,localhost".to_string());
                // Task-lifetime: the RAII teardown below re-blocks loopback at
                // task end, after which the leaked proxy reaches nothing.
                proxy.leak_for_process_lifetime();
            }
        }

        let permissions = ResolvedWindowsSandboxPermissions::from_policy(policy, cwd)
            .map_err(|e| anyhow::anyhow!("resolve policy: {e}"))?;

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
        // parent owns the root (it created it) and can rewrite its DACL.
        //
        // Decision D-013: the grant targets the dedicated sandbox account SID
        // and nothing else. The per-workspace capability SIDs that used to be
        // granted alongside it are gone: they only ever had effect as
        // restricting SIDs on the child token, and that array is now NULL (see
        // `codex_windows_sandbox::token::create_sandbox_restricted_token_from`),
        // so an ACE addressed to one would match no SID on the token and be
        // silently dead. This ACE is therefore the ONLY thing separating a
        // workspace-write task from a read-only one: a read-only task adds no
        // grant here, and the token itself no longer distinguishes the two.
        //
        // Fail-closed (D-005): add_allow_ace returns Err (and verifies the ACE
        // persisted) so a grant that cannot be applied aborts the launch rather
        // than proceeding with a workspace the confined child cannot write.
        if permissions.grants_write() {
            let sandbox_sid_str = codex_windows_sandbox::winutil::string_from_sid_bytes(
                &sandbox_users::resolve_sid(SANDBOX_USERNAME)?,
            )
            .map_err(anyhow::Error::msg)?;
            let sandbox_local = LocalSid::from_string(&sandbox_sid_str)?;
            for root in &workspace_roots {
                let root_path = root.as_path();
                // SAFETY: `sandbox_local` owns a live SID pointer that outlives
                // this loop; add_allow_ace reads it and rewrites the DACL of the
                // validated absolute root path.
                unsafe { acl::add_allow_ace(root_path, sandbox_local.as_ptr()) }.map_err(|e| {
                    anyhow::anyhow!(
                        "grant sandbox-account write on {}: {e}",
                        root_path.display()
                    )
                })?;
            }
        }

        let spawn_request = SpawnRequest {
            command: command.to_vec(),
            cwd: cwd.to_path_buf(),
            env: env.clone(),
            permission_profile: PermissionProfile {
                read_only: !permissions.grants_write(),
            },
            workspace_roots,
            codex_home: sandbox_home.to_path_buf(),
            real_codex_home: sandbox_home.to_path_buf(),
            timeout_ms: None,
            tty: false,
            stdin_open: false,
            // UI isolation (Step 3 B1): the runner moves the restricted inner
            // child onto a private DESKTOP on the interactive WinSta0
            // (upstream/Chromium baseline), granting its own logon SID so the
            // child can attach. Desktop-level UI isolation only: clipboard and
            // the global atom table are per-window-station and stay shared with
            // the interactive user (accepted tradeoff, no station-level
            // isolation). A hostile child's desktop escape is contained only
            // once the deferred Low-integrity / SID-disable seam lands.
            use_private_desktop: true,
        };

        let sandbox_creds = load_logon_sandbox_creds(sandbox_home)?;
        // `logs_base_dir` was created above (it also holds the egress-fence log).

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

    /// Lab-only confinement-validation entry point. Integration B2: it now runs
    /// the LIVE network-fenced compose ([`run_windows_sandbox_capture`]), so the
    /// `spike307-win` lab drives the full matrix, filesystem / user / token /
    /// Job isolation AND the SID-keyed egress fence, for whatever policy it is
    /// given (the validate bin exercises both `DenyAll` and `AllowHosts`). The
    /// public [`crate::launch`] runs the same compose directly.
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
    /// it derives a restricted primary token FROM ITS OWN token and
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
            "spawn request received: program={:?} argc={} tty={} read_only={} workspace_roots={} env_keys={}",
            request
                .command
                .first()
                .map(String::as_str)
                .unwrap_or("<none>"),
            request.command.len(),
            request.tty,
            request.permission_profile.read_only,
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

    /// Derives the restricted sandbox token from the runner's own token,
    /// applies the per-task ACL grants, spawns the child via
    /// `spawn_process_with_pipes`, and streams Output/Exit frames.
    fn spawn_and_stream(request: &SpawnRequest, writer: &mut File) -> Result<()> {
        // The per-task allow-write ACEs are applied by the ELEVATED parent
        // (run_windows_sandbox_capture) BEFORE this runner is launched, because
        // this runner runs as the low-privilege sandbox account and has no
        // WRITE_DAC on the workspace root (a runner-side SetNamedSecurityInfoW
        // was denied, so the ACE never landed -- the (c) write-inside failure).
        //
        // Decision D-013: the token is now the same in both permission
        // profiles. `read_only` no longer selects a token shape, because the
        // restricting-SID array (the only thing that ever differed) is gone;
        // read-only is expressed by the parent simply not adding the workspace
        // allow-write ACE. It is logged here purely as launch context.
        runner_debug_log(&format!(
            "per-task allow ACEs applied parent-side; deriving restricted token (read_only={})",
            request.permission_profile.read_only
        ));

        // SAFETY: get_current_token_for_restriction returns the runner's own
        // primary token; create_sandbox_restricted_token_from derives the
        // sandbox token from it. Both handles are closed below. Because the
        // derived token comes from the caller's OWN token it is assignable, so
        // the CreateProcessAsUserW inside spawn_process_with_pipes needs no
        // privilege (A.Q1).
        let (token, child_sid) = unsafe {
            let base = get_current_token_for_restriction()?;
            let derived = create_sandbox_restricted_token_from(base);
            CloseHandle(base);
            derived?
        };
        runner_debug_log(&format!(
            "token derived (child SID {child_sid}); spawning inner child via CreateProcessAsUserW"
        ));

        // Report the derived SID as an Output frame BEFORE spawning the
        // confined child, reusing the existing stdout-capture path rather than
        // adding a new IPC message type. This is decision D-013's option 1:
        // the validator's SID assertion no longer needs the confined child to
        // run `whoami /user` itself, which was found (2026-07-21 lab session)
        // to block indefinitely under this token -- `LookupAccountSid` is an
        // LSA RPC call, and `GetTokenInformation(TokenUser)` (used to derive
        // `child_sid` above) is not, so it cannot hit the same hang. See
        // VENDORING.md for the differential evidence and the standing
        // limitation this leaves for any sandboxed code that resolves a SID to
        // a name the same way `whoami` does.
        ipc_framed::write_frame(
            &mut *writer,
            &FramedMessage {
                version: ipc_framed::IPC_PROTOCOL_VERSION,
                message: Message::Output {
                    payload: OutputPayload {
                        data_b64: ipc_framed::encode_bytes(
                            format!("HIVE_SANDBOX_CHILD_SID={child_sid}\n").as_bytes(),
                        ),
                        stream: OutputStream::Stdout,
                    },
                },
            },
        )?;

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

        // FIXED (CodeRabbit "Sequential stdout-then-stderr drain can
        // deadlock" / Greptile "Sequential Pipe Drain Deadlocks", VENDORING.md
        // tracked both): draining stdout to EOF before touching stderr could
        // deadlock if the child filled the stderr pipe buffer while stdout
        // stayed open (the child blocks writing stderr, this runner blocks
        // waiting for stdout EOF, neither proceeds). Both streams now drain
        // concurrently, one OS thread per stream, sharing the frame `writer`
        // behind a `Mutex` so Output frames from either stream never
        // interleave mid-write. `std::thread::scope` lets both threads borrow
        // `writer`/the pipe handles without needing `'static`/`Arc`, and
        // guarantees every thread spawned here is joined before this function
        // can return. A panic mid-drain on either stream (e.g. a
        // poisoned-mutex `.lock().unwrap()`) converts to a fail-closed `Err`
        // via `.join()`, not only on stdout.
        let writer_lock = Mutex::new(writer);
        let stdout_handle = handles.stdout_read;
        let stderr_handle = handles.stderr_read;

        let exit_code = std::thread::scope(|scope| -> Result<i32> {
            let stdout_thread =
                scope.spawn(|| drain_stream(stdout_handle, OutputStream::Stdout, &writer_lock));
            let stderr_thread = scope.spawn(|| match stderr_handle {
                Some(h) => drain_stream(h, OutputStream::Stderr, &writer_lock),
                None => Ok(()),
            });
            let stdout_result = stdout_thread
                .join()
                .map_err(|_| anyhow::anyhow!("stdout drain thread panicked"))?;
            let stderr_result = stderr_thread
                .join()
                .map_err(|_| anyhow::anyhow!("stderr drain thread panicked"))?;
            stdout_result.and(stderr_result)?;

            // SAFETY: `process` is the child's process handle from
            // PROCESS_INFORMATION; wait for it to exit, then read its exit
            // code.
            unsafe {
                if WaitForSingleObject(process, INFINITE) != WAIT_OBJECT_0 {
                    anyhow::bail!("WaitForSingleObject on child failed: {}", GetLastError());
                }
                let mut code: u32 = 0;
                if GetExitCodeProcess(process, &mut code) == 0 {
                    anyhow::bail!("GetExitCodeProcess failed: {}", GetLastError());
                }
                Ok(code as i32)
            }
        })?;

        runner_debug_log(&format!(
            "child pid={pid} exited code={exit_code}; sending Exit frame"
        ));

        // Both drain threads have joined (the `thread::scope` call above does
        // not return until they have), so the mutex is uncontended here;
        // reclaim the plain `&mut File` for the final write.
        let writer = writer_lock.into_inner().unwrap();
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

    /// Reads a child pipe handle to EOF, emitting Output frames. `writer` is
    /// shared with the sibling stream's drain thread (see `stream_child`); the
    /// lock is held only around each frame write, not the blocking read.
    fn drain_stream(handle: HANDLE, stream: OutputStream, writer: &Mutex<&mut File>) -> Result<()> {
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
                    let frame = FramedMessage {
                        version: ipc_framed::IPC_PROTOCOL_VERSION,
                        message: Message::Output {
                            payload: OutputPayload {
                                data_b64: ipc_framed::encode_bytes(&buf[..n]),
                                stream,
                            },
                        },
                    };
                    let mut guard = writer.lock().unwrap();
                    ipc_framed::write_frame(&mut **guard, &frame)?;
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
    fn deny_all_maps_to_spawn_deny_all() {
        let decision = LaunchDecision::for_policy(&policy(NetworkPolicy::DenyAll));
        assert_eq!(decision, LaunchDecision::SpawnDenyAll);
    }

    #[test]
    fn allow_hosts_maps_to_spawn_allow_hosts_preserving_hosts() {
        let decision =
            LaunchDecision::for_policy(&policy(NetworkPolicy::AllowHosts(vec!["h".into()])));
        assert_eq!(
            decision,
            LaunchDecision::SpawnAllowHosts {
                hosts: vec!["h".into()]
            }
        );
    }

    #[test]
    fn single_task_flag_admits_one_then_rejects_until_released() {
        use std::sync::atomic::{AtomicBool, Ordering};
        // Blocker a: the CAS admits exactly one holder at a time.
        let flag = AtomicBool::new(false);
        assert!(
            acquire_single_task_flag(&flag),
            "first acquire must succeed"
        );
        assert!(
            !acquire_single_task_flag(&flag),
            "second acquire must be rejected while the first is held"
        );
        flag.store(false, Ordering::SeqCst);
        assert!(
            acquire_single_task_flag(&flag),
            "after release a fresh acquire must succeed again"
        );
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

    // ---- Sandbox-home DACL (decision D-013, Part A) -----------------------

    const USER_SID: &str = "S-1-5-21-111-222-333-1001";
    const SANDBOX_SID: &str = "S-1-5-21-111-222-333-1002";

    #[test]
    fn sandbox_home_dacl_grants_the_sandbox_account_read_but_never_write() {
        let aces = sandbox_home_dacl_entries(USER_SID, SANDBOX_SID).expect("entries");

        let sandbox: Vec<&SandboxHomeAce> =
            aces.iter().filter(|ace| ace.sid == SANDBOX_SID).collect();
        assert_eq!(sandbox.len(), 1, "sandbox account must appear exactly once");
        assert_eq!(sandbox[0].access, SandboxHomeAccess::ReadExecute);
    }

    #[test]
    fn sandbox_home_dacl_grants_full_control_only_to_system_admins_and_the_owner() {
        let aces = sandbox_home_dacl_entries(USER_SID, SANDBOX_SID).expect("entries");

        let full: Vec<&str> = aces
            .iter()
            .filter(|ace| ace.access == SandboxHomeAccess::FullControl)
            .map(|ace| ace.sid.as_str())
            .collect();
        assert_eq!(full, vec!["S-1-5-18", "S-1-5-32-544", USER_SID]);
    }

    #[test]
    fn sandbox_home_dacl_never_names_a_broad_group_trustee() {
        let aces = sandbox_home_dacl_entries(USER_SID, SANDBOX_SID).expect("entries");

        for forbidden in FORBIDDEN_SANDBOX_HOME_TRUSTEES {
            assert!(
                !aces.iter().any(|ace| ace.sid == forbidden),
                "{forbidden} must never be a sandbox-home trustee"
            );
        }
    }

    #[test]
    fn sandbox_home_dacl_rejects_a_broad_group_sid_as_input() {
        for forbidden in FORBIDDEN_SANDBOX_HOME_TRUSTEES {
            assert!(
                sandbox_home_dacl_entries(USER_SID, forbidden).is_err(),
                "{forbidden} must be rejected as the sandbox account SID"
            );
            assert!(
                sandbox_home_dacl_entries(forbidden, SANDBOX_SID).is_err(),
                "{forbidden} must be rejected as the provisioning user SID"
            );
        }
    }

    #[test]
    fn sandbox_home_dacl_rejects_a_sandbox_account_equal_to_the_provisioning_user() {
        // Otherwise the sandbox account inherits the owner's full-control ACE
        // and the whole protection is silently void.
        assert!(sandbox_home_dacl_entries(USER_SID, USER_SID).is_err());
        assert!(
            sandbox_home_dacl_entries(USER_SID, &USER_SID.to_ascii_lowercase()).is_err(),
            "SID comparison must be case-insensitive"
        );
    }

    #[test]
    fn sandbox_home_dacl_rejects_empty_sids() {
        assert!(sandbox_home_dacl_entries("", SANDBOX_SID).is_err());
        assert!(sandbox_home_dacl_entries(USER_SID, "").is_err());
    }
}
