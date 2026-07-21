//! Windows desktop sandbox backend.
//!
//! ## Integration B2 activation (read first)
//!
//! [`launch`] no longer spawns via the base caller-token seam below. It now
//! dispatches to the SID-fenced elevated compose
//! ([`crate::windows_elevated::run_windows_sandbox_capture`]): the child runs
//! AS the low-privilege sandbox account, and the two-layer egress fence, WFP
//! (persistent, installed at provision) plus the Windows Firewall (persistent
//! block-all plus a per-task loopback allow), is keyed on that account's SID.
//! `DenyAll` blocks all egress; `AllowHosts` routes through the loopback
//! [`crate::egress_proxy::AllowlistProxy`]. `launch` still applies the #307
//! deny-write directory ACL, still requires a fully qualified `command[0]`,
//! and now requires `HIVE_SANDBOX_HOME` (fail-closed: never launch unfenced).
//! Because the compose is capture-based, the returned [`SandboxChild`]
//! represents a COMPLETED confined run (a live/streaming child is a later
//! step). The base restricted-token / Job-Object primitives below are retained
//! (dead) as the documented base seam and to keep the CI cross-compile
//! exercising their Win32 call shapes.
//!
//! ## Base seam (superseded, retained): what it applied
//!
//! The base seam spawned `command` directly via `CreateProcessAsUserW` under
//! the controls below and refused to spawn if any could not be established. It
//! is no longer on the launch path:
//!
//! 1. Launch under a DISTINCT primary token ([`create_restricted_token`] +
//!    `CreateProcessAsUserW`). This wires the seam `std::process::Command`
//!    could never provide: std has no way to spawn under an alternate token,
//!    so a child spawned through it always inherited the caller's token.
//!    IMPORTANT, no overclaiming: the token handed to the child is currently
//!    an UNRESTRICTED duplicate (`CreateRestrictedToken` with flags `0` and
//!    empty disable/restrict/delete lists), so it does NOT yet reduce the
//!    child's privileges. It does not "confine the child"; today it only
//!    establishes the alternate-token launch path. Real restriction (Low
//!    integrity level, SID disabling, privilege deletion, or a dedicated
//!    low-privilege sandbox OS user) is deferred to the Step 3 sandbox-user
//!    variant.
//! 2. A directory-level deny-write ACL ([`apply_directory_acl`]) on the
//!    hook/config directory's PARENT (spike #307 condition 3). This is the
//!    load-bearing filesystem control Step 1 actually enforces; the Job Object
//!    is process containment only.
//! 3. A Job Object with kill-on-close ([`create_job_object`]). The child is
//!    created SUSPENDED, assigned to the Job Object, and only then resumed, so
//!    it is a member of the job before it runs a single instruction (no
//!    assignment race). The returned [`SandboxChild`] owns the sole job handle
//!    via RAII: dropping it closes that last handle and terminates the whole
//!    process tree.
//!
//! Network confinement (Integration B2, LIVE): `launch` enforces egress via the
//! SID-fenced compose described in the banner above, not via this base seam
//! (which could never carry a sandbox-SID fence, since its child ran under the
//! caller's SID). `DenyAll` blocks all egress; `AllowHosts` routes through the
//! loopback proxy.
//!
//! ## Verification status (read before trusting this file)
//!
//! This module is compiled only on Windows (`cfg(windows)`) and this
//! repository's CI and this session's development environment are both Linux.
//! CI now cross-compiles this file: the `rust-tests` job runs `cargo check`
//! and `cargo clippy` for `x86_64-pc-windows-gnu` against
//! `hive-desktop-sandbox`, so the Win32 call shapes are type-checked against
//! `windows`-crate 0.58's real signatures on every PR (before this, the
//! `cfg(windows)` gate meant CI never compiled this file at all). What CI
//! still does NOT do: build with the MSVC toolchain, or run anything. The
//! BEHAVIORAL confinement (the ACL denies the write, the job kills the tree)
//! only takes effect on a real Windows host: `CreateProcessAsUserW`,
//! `AssignProcessToJobObject`, and the deny-write ACE are UNVERIFIED here.
//! This path must still get a `cargo check --target x86_64-pc-windows-msvc`
//! and a behavioral run in the lab (`win11vm`, see VENDORING.md) before it is
//! trusted in production.
//!
//! The pure, always-tested part of this backend's design lives in
//! `windows_plan.rs` ([`crate::windows_plan::WindowsConfinementPlan`], plus
//! [`crate::windows_plan::command_line_to_utf16`], the argv quoting this
//! module hands to `CreateProcessAsUserW`, and
//! [`crate::windows_plan::is_fully_qualified_program`], the binary-planting
//! guard below), which run on every platform (including this crate's Linux CI
//! job). This module only applies that plan via Win32 calls.
//!
//! MANDATORY per security spike #307 (implementation condition 3): the
//! deny-write ACE goes on `plan.acl_deny_write_parent_dir` (the hook/config
//! directory's PARENT), not on the hook/config directory itself. A
//! file/dir-level ACL alone does not close the TOCTOU missing-file-create
//! class; only the parent-directory ACE does. [`apply_directory_acl`] is the
//! load-bearing control here, not the Job Object, which is process containment
//! only.
//!
//! Not implemented by the base seam (now handled by the compose or later steps):
//! - Network confinement: LIVE via the compose (see the banner above), no
//!   longer refused. `windows_plan.rs`'s `netsh` codegen stays unused (the WFP
//!   + firewall COM fence replaced it).
//! - Token privilege reduction / the low-privilege sandbox-user variant: owned
//!   by the elevated compose in [`crate::windows_elevated`] (the sandbox-account
//!   `CreateProcessWithLogonW` transport, the restricted sandbox token
//!   derived in the runner, and the per-task ACL assembly), which `launch` now
//!   dispatches to. The same compose is also exercised directly by the lab-only
//!   [`crate::windows_elevated::spawn_confined_for_validation`] on `spike307-win`
//!   (D-004).
//! - Environment scrubbing: `CreateProcessAsUserW` is called with
//!   `lpEnvironment = NULL`, so the child inherits the parent process's full
//!   environment (secrets and `*_API_KEY`-style values included). A scrubbed
//!   environment block lands with the Step 3 sandbox-user variant.
//! - Stdio/ConPTY wiring: the child is spawned with `bInheritHandles=FALSE`
//!   and default STARTUPINFOW, so its stdio is not bridged. The interactive
//!   terminal is a later step.

use crate::windows_plan::{WindowsConfinementPlan, is_fully_qualified_program};
use crate::{LaunchError, SandboxPolicy};
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use windows::Win32::Foundation::{CloseHandle, HANDLE, HLOCAL, LocalFree};
use windows::Win32::Security::Authorization::{
    BuildTrusteeWithSidW, DENY_ACCESS, EXPLICIT_ACCESS_W, SE_FILE_OBJECT, SetEntriesInAclW,
    SetNamedSecurityInfoW, TRUSTEE_W,
};
use windows::Win32::Security::{
    ACE_FLAGS, CREATE_RESTRICTED_TOKEN_FLAGS, CreateRestrictedToken, DACL_SECURITY_INFORMATION,
    GetTokenInformation, OBJECT_INHERIT_ACE, PROTECTED_DACL_SECURITY_INFORMATION, PSID,
    SUB_CONTAINERS_AND_OBJECTS_INHERIT, TOKEN_ALL_ACCESS, TOKEN_USER, TokenUser,
};
use windows::Win32::Storage::FileSystem::FILE_GENERIC_WRITE;
use windows::Win32::System::JobObjects::{
    CreateJobObjectW, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, JOBOBJECT_BASIC_LIMIT_INFORMATION,
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION, JobObjectExtendedLimitInformation,
    SetInformationJobObject,
};
use windows::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};
use windows::core::{PCWSTR, PWSTR};

/// A sandboxed child.
///
/// Integration B2: `launch` dispatches to the capture-based elevated compose,
/// so the value it returns represents a COMPLETED confined run: `process` and
/// `job` are `None` and `exit_code` carries the captured status. The handle
/// fields stay `Option<HANDLE>` (rather than being removed) so a future
/// live/streaming launch can populate them and keep the kill-on-close Drop
/// contract below: closing the last job handle terminates the process tree
/// (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`). This is deliberately NOT a
/// `std::process::Child`: std cannot own a Job Object handle.
pub struct SandboxChild {
    process: Option<HANDLE>,
    job: Option<HANDLE>,
    pid: u32,
    exit_code: Option<i32>,
}

impl SandboxChild {
    /// The child's Windows process id (0 for a completed capture run, which
    /// owns no live process handle).
    pub fn id(&self) -> u32 {
        self.pid
    }

    /// The captured exit code when this value represents a COMPLETED confined
    /// run (the Integration B2 capture-based launch). `None` for a value that
    /// owns a live process/job handle.
    pub fn exit_code(&self) -> Option<i32> {
        self.exit_code
    }
}

impl Drop for SandboxChild {
    fn drop(&mut self) {
        // Close handles only when this value owns live ones (never for a
        // completed capture run). Closing the last job handle is what
        // terminates the tree (kill-on-close), so a live child never outlives
        // this value.
        unsafe {
            if let Some(process) = self.process {
                let _ = CloseHandle(process);
            }
            if let Some(job) = self.job {
                let _ = CloseHandle(job);
            }
        }
    }
}

/// RAII wrapper closing a Win32 handle on drop, so every fallible step below
/// its acquisition cleans the handle up without an explicit `CloseHandle` on
/// each error path. Ownership is transferred out with [`HandleGuard::into_raw`]
/// only once the handle has reached a value (`SandboxChild`) that will close
/// it later.
///
/// Retained (dead) with the base-seam primitives below: see
/// [`create_restricted_token`] for the supersession rationale.
#[allow(dead_code)]
struct HandleGuard(HANDLE);

#[allow(dead_code)]
impl HandleGuard {
    /// Consumes the guard, returning the raw handle WITHOUT closing it. The
    /// caller becomes responsible for closing it.
    fn into_raw(self) -> HANDLE {
        let handle = self.0;
        std::mem::forget(self);
        handle
    }
}

impl Drop for HandleGuard {
    fn drop(&mut self) {
        if !self.0.is_invalid() {
            unsafe {
                let _ = CloseHandle(self.0);
            }
        }
    }
}

pub(crate) fn launch(
    policy: &SandboxPolicy,
    command: &[String],
    cwd: &Path,
) -> Result<SandboxChild, LaunchError> {
    if command.is_empty() {
        return Err(LaunchError::Confinement(
            "empty command: nothing to launch".to_string(),
        ));
    }
    if !is_fully_qualified_program(&command[0]) {
        // The module search consults the child's CURRENT DIRECTORY before PATH,
        // so with an attacker-controlled cwd a bare program name is a
        // binary-planting vector. Require a fully qualified absolute path.
        return Err(LaunchError::Confinement(format!(
            "command[0] must be a fully qualified absolute path to avoid \
             binary planting from the working directory, got: {}",
            command[0]
        )));
    }

    let plan = WindowsConfinementPlan::for_policy(policy);

    // Filesystem control (#307): the load-bearing deny-write ACE on the
    // hook/config parent dir. Kept from the base seam; must succeed before we
    // dispatch to the fenced compose.
    apply_directory_acl(&plan)
        .map_err(|e| LaunchError::Confinement(format!("directory ACL: {e}")))?;

    // Integration B2: dispatch to the SID-fenced elevated compose. This is the
    // only D-005-safe LIVE path: the base caller-token seam below cannot carry
    // the sandbox-SID egress fence (the child would run under the caller's SID,
    // which the WFP/firewall rules do not target), so it is superseded here.
    // Fail-closed: never launch unfenced, so the sandbox home MUST be set.
    let sandbox_home = std::env::var_os("HIVE_SANDBOX_HOME")
        .map(PathBuf::from)
        .ok_or_else(|| {
            LaunchError::Confinement(
                "HIVE_SANDBOX_HOME must be set for the confined Windows sandbox launch".to_string(),
            )
        })?;

    // The child env is derived from the parent's; the compose adds the proxy
    // vars for an AllowHosts policy before spawning the runner.
    let env: HashMap<String, String> = std::env::vars().collect();

    let capture = crate::windows_elevated::run_windows_sandbox_capture(
        &sandbox_home,
        policy,
        command,
        cwd,
        &env,
    )
    .map_err(|e| LaunchError::Confinement(format!("confined launch: {e}")))?;

    // The compose is capture-based (blocking), so the returned SandboxChild
    // represents a COMPLETED confined run: no live process/job handle to own,
    // just the captured exit code. Drop is a no-op. A live/streaming child is
    // a later step (it would populate the handle fields).
    Ok(SandboxChild {
        process: None,
        job: None,
        pid: 0,
        exit_code: Some(capture.exit_code),
    })
}

/// Sets a protected, deny-write ACE for the current user on
/// `plan.acl_deny_write_parent_dir`, object-inherit + container-inherit so
/// it covers files created under the directory after the ACE is set
/// (closing the TOCTOU missing-file-create class from spike #307 row 13).
fn apply_directory_acl(plan: &WindowsConfinementPlan) -> windows::core::Result<()> {
    let dir_wide = to_wide_path(&plan.acl_deny_write_parent_dir);

    let mut process_token = HANDLE::default();
    unsafe {
        OpenProcessToken(
            GetCurrentProcess(),
            windows::Win32::Security::TOKEN_QUERY,
            &mut process_token,
        )?;
    }

    let mut token_user_buf = vec![0u8; 512];
    let mut returned_len: u32 = 0;
    let get_info_result = unsafe {
        GetTokenInformation(
            process_token,
            TokenUser,
            Some(token_user_buf.as_mut_ptr() as *mut _),
            token_user_buf.len() as u32,
            &mut returned_len,
        )
    };
    unsafe {
        let _ = CloseHandle(process_token);
    }
    get_info_result?;
    // SAFETY: `GetTokenInformation` (checked by `get_info_result?` above) wrote
    // a `TOKEN_USER` into `token_user_buf`, but the buffer is a `Vec<u8>`
    // (alignment 1) while `TOKEN_USER` requires 8-byte alignment, so taking a
    // `&*(.. as *const TOKEN_USER)` reference would be undefined behaviour.
    // Read an aligned copy out with `read_unaligned` instead. The `User.Sid`
    // pointer inside the copy still points into `token_user_buf`, which
    // outlives every use of `sid` below (through `SetEntriesInAclW`), so the
    // SID bytes it references stay valid.
    let token_user =
        unsafe { std::ptr::read_unaligned(token_user_buf.as_ptr() as *const TOKEN_USER) };
    let sid: PSID = token_user.User.Sid;

    let mut trustee = TRUSTEE_W::default();
    unsafe {
        BuildTrusteeWithSidW(&mut trustee, sid);
    }

    let explicit_access = EXPLICIT_ACCESS_W {
        grfAccessPermissions: FILE_GENERIC_WRITE.0,
        grfAccessMode: DENY_ACCESS,
        grfInheritance: ACE_FLAGS(OBJECT_INHERIT_ACE.0 | SUB_CONTAINERS_AND_OBJECTS_INHERIT.0),
        Trustee: trustee,
    };

    let mut new_acl = std::ptr::null_mut();
    unsafe { SetEntriesInAclW(Some(&[explicit_access]), None, &mut new_acl) }.ok()?;

    let result = unsafe {
        SetNamedSecurityInfoW(
            PWSTR(dir_wide.as_ptr() as *mut u16),
            SE_FILE_OBJECT,
            DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
            None,
            None,
            Some(new_acl as *const _),
            None,
        )
    };

    unsafe {
        let _ = LocalFree(HLOCAL(new_acl as *mut _));
    }

    result.ok()
}

/// Duplicates the CURRENT process's own token via `CreateRestrictedToken`
/// with flags `0` and empty disable-SID / restrict-SID / delete-privilege
/// lists. Despite the API name, that exact combination yields a token with the
/// SAME privileges and groups as the source: it is an UNRESTRICTED duplicate,
/// NOT a privilege-reduced token. Its only Step 1 job is to give
/// `CreateProcessAsUserW` a distinct primary token to spawn under (the launch
/// seam). Real restriction -- Low integrity level, SID disabling, privilege
/// deletion, or a dedicated low-privilege sandbox OS user -- is deferred to
/// the Step 3 sandbox-user variant; see VENDORING.md "Open risks" #1 and #6.
/// Do not read this as confining the child.
///
/// Retained (dead) after Integration B2: `launch` no longer spawns via the
/// base caller-token seam (it dispatches to the elevated SID-fenced compose,
/// the only D-005-safe live path). Kept as the documented base primitive and
/// to keep the CI cross-compile exercising these Win32 call shapes.
#[allow(dead_code)]
fn create_restricted_token() -> windows::core::Result<HANDLE> {
    let mut process_token = HANDLE::default();
    unsafe {
        OpenProcessToken(GetCurrentProcess(), TOKEN_ALL_ACCESS, &mut process_token)?;
    }

    let mut restricted_token = HANDLE::default();
    let result = unsafe {
        CreateRestrictedToken(
            process_token,
            CREATE_RESTRICTED_TOKEN_FLAGS(0),
            None,
            None,
            None,
            &mut restricted_token,
        )
    };

    unsafe {
        let _ = CloseHandle(process_token);
    }

    result?;
    Ok(restricted_token)
}

/// Creates a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` set
/// unconditionally: process containment only, not a substitute for the
/// directory ACL above (spike #307: "zero filesystem-CBSE protection by
/// itself").
///
/// Retained (dead) after Integration B2 for the same reason as
/// [`create_restricted_token`]: the elevated compose owns the live child's job
/// containment now (`windows_elevated::confine_child_to_job`).
#[allow(dead_code)]
fn create_job_object(plan: &WindowsConfinementPlan) -> windows::core::Result<HANDLE> {
    debug_assert!(
        plan.job_object_kill_on_close,
        "plan must always request kill-on-close"
    );

    let job = unsafe { CreateJobObjectW(None, PCWSTR::null())? };

    let limits = JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
        BasicLimitInformation: JOBOBJECT_BASIC_LIMIT_INFORMATION {
            LimitFlags: JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
            ..Default::default()
        },
        ..Default::default()
    };

    unsafe {
        SetInformationJobObject(
            job,
            JobObjectExtendedLimitInformation,
            &limits as *const _ as *const _,
            std::mem::size_of::<JOBOBJECT_EXTENDED_LIMIT_INFORMATION>() as u32,
        )?;
    }

    Ok(job)
}

fn to_wide_path(path: &Path) -> Vec<u16> {
    use std::os::windows::ffi::OsStrExt;
    path.as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect()
}
