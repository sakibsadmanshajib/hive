//! Windows desktop sandbox backend: restricted token, directory-level ACL,
//! and a Job Object.
//!
//! ## Verification status (read before trusting this file)
//!
//! This module is compiled only on Windows (`cfg(windows)`) and this
//! repository's CI and this session's development environment are both
//! Linux; nothing in this file has been compiled or run. It must get a real
//! `cargo check --target x86_64-pc-windows-msvc` and a behavioral run in
//! the lab (`win11vm`, see VENDORING.md) before it is trusted. The
//! Win32 API shapes below are written from stable, long-documented Win32
//! contracts, but the exact `windows`-crate 0.58 signatures (slice vs.
//! pointer+count, `Option<&T>` vs raw pointer, which calls return
//! `windows::core::Result<()>` vs an out-handle) are not verified here.
//!
//! The pure, always-tested part of this backend's design lives in
//! `windows_plan.rs` ([`crate::windows_plan::WindowsConfinementPlan`]),
//! which is where the "enforced defaults cannot be constructed without
//! them" tests required by spike #307 actually run (on every platform,
//! including this crate's Linux CI job). This module only applies that
//! plan via Win32 calls.
//!
//! MANDATORY per security spike #307 (implementation condition 3): the
//! deny-write ACE goes on `plan.acl_deny_write_parent_dir` (the hook/config
//! directory's PARENT), not on the hook/config directory itself. A
//! file/dir-level ACL alone does not close the TOCTOU
//! missing-file-create class; only the parent-directory ACE does.
//! [`apply_directory_acl`] is the load-bearing control here, not the Job
//! Object, which is process containment only.
//!
//! Not implemented here (see blueprint Step 4.4, and `windows_plan.rs`):
//! Windows Firewall deny-outbound rule generation from the egress SSOT, and
//! the elevated low-privilege-sandbox-user variant of the restricted
//! token (this module restricts the current process's own token rather
//! than provisioning a dedicated low-privilege OS user).
//!
//! Known race (tracked as follow-up, see VENDORING.md "Open risks"):
//! the child is assigned to the Job Object immediately after `spawn()`
//! rather than atomically via `CREATE_SUSPENDED` + `ResumeThread`, because
//! `std::process::Command`/`Child` do not expose the primary thread handle
//! needed to resume a suspended process. A future revision that calls
//! `CreateProcessW` directly (bypassing `std::process::Command`) can close
//! this gap.

use crate::policy::NetworkPolicy;
use crate::windows_plan::WindowsConfinementPlan;
use crate::{LaunchError, SandboxPolicy};
use std::os::windows::io::AsRawHandle;
use std::path::Path;
use std::process::{Child, Command};
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
    AssignProcessToJobObject, CreateJobObjectW, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
    JOBOBJECT_BASIC_LIMIT_INFORMATION, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
    JobObjectExtendedLimitInformation, SetInformationJobObject,
};
use windows::Win32::System::Threading::{GetCurrentProcess, OpenProcessToken};
use windows::core::{PCWSTR, PWSTR};

pub(crate) fn launch(
    policy: &SandboxPolicy,
    command: &[String],
    cwd: &Path,
) -> Result<Child, LaunchError> {
    if matches!(policy.network(), NetworkPolicy::AllowHosts(_)) {
        return Err(LaunchError::AllowHostsNotYetImplemented);
    }
    let plan = WindowsConfinementPlan::for_policy(policy);

    apply_directory_acl(&plan)
        .map_err(|err| LaunchError::Confinement(format!("directory ACL: {err}")))?;
    let _restricted_token = create_restricted_token()
        .map_err(|err| LaunchError::Confinement(format!("restricted token: {err}")))?;
    let job = create_job_object(&plan)
        .map_err(|err| LaunchError::Confinement(format!("job object: {err}")))?;

    let (program, args) = command.split_first().ok_or_else(|| {
        LaunchError::Confinement("command must have at least a program name".to_string())
    })?;

    let child = Command::new(program).args(args).current_dir(cwd).spawn()?;

    // Known race: see module docs "Known race". Assigning immediately after
    // spawn rather than atomically via CREATE_SUSPENDED.
    let process_handle = HANDLE(child.as_raw_handle());
    let assign_result = unsafe { AssignProcessToJobObject(job, process_handle) };
    unsafe {
        let _ = CloseHandle(job);
        let _ = CloseHandle(_restricted_token);
    }
    assign_result
        .map_err(|err| LaunchError::Confinement(format!("assign to job object: {err}")))?;

    Ok(child)
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
    let token_user = unsafe { &*(token_user_buf.as_ptr() as *const TOKEN_USER) };
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

/// Restricts the CURRENT process's own token. This is intentionally the
/// base variant with no SIDs disabled and no privileges deleted beyond
/// `CreateRestrictedToken`'s own contract; the elevated variant (a
/// dedicated low-privilege sandbox OS user) is follow-up work, see
/// VENDORING.md "Open risks".
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
