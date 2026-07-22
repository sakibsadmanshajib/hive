//! Windows desktop sandbox backend: a distinct primary token, a directory-
//! level ACL, and a Job Object, all applied to a child spawned directly via
//! `CreateProcessAsUserW`.
//!
//! ## What Step 1 applies (and, just as importantly, what it does NOT)
//!
//! [`launch`] spawns `command` under the controls below and refuses to spawn
//! at all if any of them cannot be established (never a process under a
//! partial or absent confinement claim). This is the Step 1 launch seam, not
//! a finished sandbox:
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
//! Network confinement is NOT applied in Step 1: [`launch`] refuses BOTH
//! network policies (see the "Not implemented" list) rather than run a process
//! while claiming an egress control that is not in force.
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
//! Not implemented here (later steps of plan-codex-crossplatform-desktop.md):
//! - Network confinement of ANY kind. `launch` rejects
//!   [`NetworkPolicy::DenyAll`] with
//!   [`LaunchError::NetworkConfinementNotImplemented`] and
//!   [`NetworkPolicy::AllowHosts`] with
//!   [`LaunchError::AllowHostsNotYetImplemented`]. The Job Object carries no
//!   network limit and `windows_plan.rs`'s `netsh` codegen is never applied;
//!   the WFP egress backend is a dedicated later step (Step 4).
//! - Token privilege reduction / the low-privilege sandbox-user variant
//!   (Step 3): see control 1 above. Step 3 Integration A lands the elevated
//!   compose (the sandbox-account `CreateProcessWithLogonW` transport, the
//!   capability-restricted token derived in the runner, and the per-task ACL
//!   assembly) in [`crate::windows_elevated`]. That compose is NOT reached
//!   through this `launch` (which keeps refusing both network policies until
//!   the WFP egress backend lands in Integration B, per D-005): it is exercised
//!   only through the lab-only
//!   [`crate::windows_elevated::spawn_confined_for_validation`] entry point, so
//!   the filesystem / user / token / Job isolation matrix can be proven on
//!   `spike307-win` ahead of the composed network success path. The two
//!   network-refusal guards below are the pure-tested
//!   [`crate::windows_elevated::LaunchDecision`] made real.
//! - Environment scrubbing: `CreateProcessAsUserW` is called with
//!   `lpEnvironment = NULL`, so the child inherits the parent process's full
//!   environment (secrets and `*_API_KEY`-style values included). A scrubbed
//!   environment block lands with the Step 3 sandbox-user variant.
//! - Stdio/ConPTY wiring: the child is spawned with `bInheritHandles=FALSE`
//!   and default STARTUPINFOW, so its stdio is not bridged. The interactive
//!   terminal is a later step.

use crate::policy::NetworkPolicy;
use crate::windows_elevated::LaunchDecision;
use crate::windows_plan::{
    WindowsConfinementPlan, command_line_to_utf16, is_fully_qualified_program,
};
use crate::{LaunchError, SandboxPolicy};
use std::path::Path;
use windows::Win32::Foundation::{BOOL, CloseHandle, HANDLE, HLOCAL, LocalFree};
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
    AssignProcessToJobObject, CreateJobObjectW, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
    JOBOBJECT_BASIC_LIMIT_INFORMATION, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
    JobObjectExtendedLimitInformation, SetInformationJobObject,
};
use windows::Win32::System::Threading::{
    CREATE_SUSPENDED, CreateProcessAsUserW, GetCurrentProcess, OpenProcessToken,
    PROCESS_INFORMATION, ResumeThread, STARTUPINFOW, TerminateProcess,
};
use windows::core::{PCWSTR, PWSTR};

/// A running sandboxed child process.
///
/// Owns the sole handle to the kill-on-close Job Object the child belongs to,
/// plus the process handle. Dropping this value closes that last job handle,
/// which terminates the whole process tree (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`).
/// Keep it alive for as long as the sandboxed task should run.
///
/// This is deliberately NOT a `std::process::Child`: std cannot own a Job
/// Object handle, and a leaked or prematurely dropped job handle would
/// silently break kill-on-close. Owning both handles here is what makes the
/// sandbox lifetime the value's lifetime.
pub struct SandboxChild {
    process: HANDLE,
    job: HANDLE,
    pid: u32,
}

impl SandboxChild {
    /// The child's Windows process id.
    pub fn id(&self) -> u32 {
        self.pid
    }
}

impl Drop for SandboxChild {
    fn drop(&mut self) {
        // Closing the process handle only releases our reference; it does not
        // stop the child. Closing the last job handle is what terminates the
        // tree (kill-on-close), so the child never outlives this value.
        unsafe {
            let _ = CloseHandle(self.process);
            let _ = CloseHandle(self.job);
        }
    }
}

// SAFETY: `SandboxChild` exclusively owns `process` and `job` (transferred
// out of `HandleGuard` via `into_raw` in `launch` below, never aliased), and
// `pid` is a plain `u32`. Both handles are ordinary Win32 kernel-object
// handles -- a process handle and a Job Object handle -- neither of which is
// thread-affine: unlike a window or GDI handle, either can be waited on,
// queried, or closed from any thread, not only the one that created it.
// `windows::Win32::Foundation::HANDLE` is `!Send` only because it is a
// newtype over a raw pointer, which is more conservative than the actual
// Win32 contract for these two handle kinds. Moving a `SandboxChild` to
// another thread (as the desktop app's `spawn_with_timeout` does) is sound.
unsafe impl Send for SandboxChild {}

/// RAII wrapper closing a Win32 handle on drop, so every fallible step below
/// its acquisition cleans the handle up without an explicit `CloseHandle` on
/// each error path. Ownership is transferred out with [`HandleGuard::into_raw`]
/// only once the handle has reached a value (`SandboxChild`) that will close
/// it later.
struct HandleGuard(HANDLE);

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
    if matches!(
        policy.network(),
        NetworkPolicy::AllowHosts(_) | NetworkPolicy::DenyAll
    ) {
        // Network confinement (WFP) is not applied on Windows yet (Step 4).
        // Both network policies refuse rather than launch with an unenforced
        // egress control. `LaunchDecision` is the single, unit-tested source
        // of that refusal decision (see its type doc in
        // `windows_elevated.rs`); this module no longer hand-duplicates the
        // guard logic inline. Kept as an `if` (not a `match`) so rustc's
        // dead-code analysis does not treat it as exhaustive and eliminate
        // the confinement seam below, which must stay compiled and reachable
        // for the CI cross-check until Step 4 removes this rejection.
        return Err(LaunchDecision::for_policy(policy).into_refusal());
    }
    if command.is_empty() {
        return Err(LaunchError::Confinement(
            "empty command: nothing to launch".to_string(),
        ));
    }
    if !is_fully_qualified_program(&command[0]) {
        // `CreateProcessAsUserW` is called with `lpApplicationName = NULL`, so
        // Windows parses the program name out of the command line and runs its
        // module search -- which consults the child's CURRENT DIRECTORY before
        // PATH. With an attacker-controlled cwd that is a binary-planting
        // vector (a malicious `notepad.exe` dropped in cwd would win over the
        // real one). Require a fully qualified absolute path for command[0] so
        // the module search never consults cwd.
        return Err(LaunchError::Confinement(format!(
            "command[0] must be a fully qualified absolute path to avoid \
             binary planting from the working directory, got: {}",
            command[0]
        )));
    }

    let plan = WindowsConfinementPlan::for_policy(policy);

    // 1. Filesystem confinement: the load-bearing deny-write ACE on the
    //    hook/config parent dir (spike #307). Must succeed before we spawn.
    apply_directory_acl(&plan)
        .map_err(|e| LaunchError::Confinement(format!("directory ACL: {e}")))?;

    // 2. Restricted primary token for the child.
    let token = HandleGuard(
        create_restricted_token()
            .map_err(|e| LaunchError::Confinement(format!("restricted token: {e}")))?,
    );

    // 3. Job Object with kill-on-close.
    let job = HandleGuard(
        create_job_object(&plan)
            .map_err(|e| LaunchError::Confinement(format!("job object: {e}")))?,
    );

    // 4. Spawn SUSPENDED under the restricted token so we can join the Job
    //    Object before the child runs (closes the assignment race).
    let mut command_line = command_line_to_utf16(command);
    let cwd_wide = to_wide_path(cwd);
    let cwd_ptr = if cwd.as_os_str().is_empty() {
        PCWSTR::null()
    } else {
        PCWSTR(cwd_wide.as_ptr())
    };
    let startup_info = STARTUPINFOW {
        cb: std::mem::size_of::<STARTUPINFOW>() as u32,
        ..Default::default()
    };
    let mut process_info = PROCESS_INFORMATION::default();

    unsafe {
        CreateProcessAsUserW(
            token.0,
            PCWSTR::null(),
            PWSTR(command_line.as_mut_ptr()),
            None,
            None,
            BOOL(0),
            CREATE_SUSPENDED,
            // lpEnvironment = NULL: the child inherits the parent's full
            // environment. Environment scrubbing (dropping secrets/API keys)
            // lands with the Step 3 sandbox-user variant; see the module doc.
            None,
            cwd_ptr,
            &startup_info,
            &mut process_info,
        )
    }
    .map_err(|e| LaunchError::Confinement(format!("CreateProcessAsUserW: {e}")))?;

    let process = HandleGuard(process_info.hProcess);
    let thread = HandleGuard(process_info.hThread);

    // 5. Join the Job Object BEFORE resuming. The child has not run yet, so
    //    membership is established atomically with respect to its execution.
    if let Err(e) = unsafe { AssignProcessToJobObject(job.0, process.0) } {
        // The child exists but is suspended and unconfined-by-job; kill it
        // rather than leak a suspended process or resume it outside the job.
        unsafe {
            let _ = TerminateProcess(process.0, 1);
        }
        return Err(LaunchError::Confinement(format!(
            "AssignProcessToJobObject: {e}"
        )));
    }

    // 6. Release the child to run.
    if unsafe { ResumeThread(thread.0) } == u32::MAX {
        let err = windows::core::Error::from_win32();
        unsafe {
            let _ = TerminateProcess(process.0, 1);
        }
        return Err(LaunchError::Confinement(format!("ResumeThread: {err}")));
    }

    // Thread and restricted-token handles are no longer needed; their guards
    // close them here. Process and job handles transfer to SandboxChild, whose
    // Drop closes them (closing the last job handle triggers kill-on-close).
    drop(thread);
    drop(token);
    Ok(SandboxChild {
        process: process.into_raw(),
        job: job.into_raw(),
        pid: process_info.dwProcessId,
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
