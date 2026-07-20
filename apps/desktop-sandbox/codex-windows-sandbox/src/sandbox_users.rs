//! OS-account create/enumerate/delete primitives for the elevated Windows sandbox helper.
//!
//! Relocated + trimmed from upstream `bin/setup_main/win/sandbox_users.rs`
//! (`codex-rs/windows-sandbox-rs/src/bin/setup_main/win/sandbox_users.rs`, commit
//! `a47c661…`, see `../VENDORING.md`) into this crate's library (`codex-windows-sandbox`)
//! rather than the excluded `setup_main` binary, since the mechanism itself (local
//! group/user creation, SID lookups) has no dependency on the excluded binary's WFP/ConPTY
//! surface. Two classes of upstream content are excluded, not vendored, here:
//!
//! 1. `provision_sandbox_users`, `write_secrets`, `prepare_setup_marker`, and
//!    `commit_setup_marker` (plus their private `SandboxUserRecord`/`SandboxUsersFile`/
//!    `SetupMarker` structs): these are orchestration coupled to the excluded `win.rs`'s
//!    specific setup flow, and additionally require a `chrono` dependency
//!    (`prepare_setup_marker`) not otherwise needed by this wave. `random_password` is excluded
//!    alongside them: its only upstream caller was `provision_sandbox_users`, so with that
//!    excluded it would be dead code under `-D warnings`; re-add together when a later wave
//!    ports the provisioning orchestration.
//! 2. Every remaining function's calls to upstream's `super::log_line` (a sibling function on
//!    `win.rs`, the excluded parent module) are replaced with a minimal local `log_line` below
//!    (verbatim primitive logic, no timestamp, since this module no longer has a `win.rs`
//!    parent to borrow the real upstream `log_line` — which itself needs `chrono` too — from).
//!
//! Import paths are module-qualified (`crate::setup_error::X`, `crate::winutil::X`) rather than
//! the flat `codex_windows_sandbox::X` paths upstream's binary crate used, since this file now
//! lives inside the crate that defines those items. Function bodies are otherwise byte-for-byte
//! upstream.

use anyhow::Result;
use std::ffi::OsStr;
use std::ffi::c_void;
use std::io::Write;
use windows_sys::Win32::Foundation::ERROR_INSUFFICIENT_BUFFER;
use windows_sys::Win32::Foundation::GetLastError;
use windows_sys::Win32::Foundation::LocalFree;
use windows_sys::Win32::NetworkManagement::NetManagement::LOCALGROUP_INFO_1;
use windows_sys::Win32::NetworkManagement::NetManagement::LOCALGROUP_MEMBERS_INFO_3;
use windows_sys::Win32::NetworkManagement::NetManagement::NERR_Success;
use windows_sys::Win32::NetworkManagement::NetManagement::NetLocalGroupAdd;
use windows_sys::Win32::NetworkManagement::NetManagement::NetLocalGroupAddMembers;
use windows_sys::Win32::NetworkManagement::NetManagement::NetUserAdd;
use windows_sys::Win32::NetworkManagement::NetManagement::NetUserSetInfo;
use windows_sys::Win32::NetworkManagement::NetManagement::UF_DONT_EXPIRE_PASSWD;
use windows_sys::Win32::NetworkManagement::NetManagement::UF_SCRIPT;
use windows_sys::Win32::NetworkManagement::NetManagement::USER_INFO_1;
use windows_sys::Win32::NetworkManagement::NetManagement::USER_INFO_1003;
use windows_sys::Win32::NetworkManagement::NetManagement::USER_PRIV_USER;
use windows_sys::Win32::Security::Authorization::ConvertStringSidToSidW;
use windows_sys::Win32::Security::CopySid;
use windows_sys::Win32::Security::GetLengthSid;
use windows_sys::Win32::Security::LookupAccountNameW;
use windows_sys::Win32::Security::LookupAccountSidW;
use windows_sys::Win32::Security::SID_NAME_USE;

use crate::setup_error::SetupErrorCode;
use crate::setup_error::SetupFailure;
use crate::winutil::string_from_sid_bytes;
use crate::winutil::to_wide;
use zeroize::Zeroize;

pub const SANDBOX_USERS_GROUP: &str = "CodexSandboxUsers";
const SANDBOX_USERS_GROUP_COMMENT: &str = "Codex sandbox internal group (managed)";
const SID_ADMINISTRATORS: &str = "S-1-5-32-544";
const SID_USERS: &str = "S-1-5-32-545";
const SID_AUTHENTICATED_USERS: &str = "S-1-5-11";
const SID_EVERYONE: &str = "S-1-1-0";
const SID_SYSTEM: &str = "S-1-5-18";

/// Local stand-in for upstream `win.rs::log_line` (see this file's module doc): writes one
/// line without a timestamp, avoiding a `chrono` dependency for a log helper this wave has no
/// caller for beyond the primitives below.
fn log_line(log: &mut dyn Write, msg: &str) -> Result<()> {
    writeln!(log, "{msg}").map_err(|err| {
        anyhow::Error::new(SetupFailure::new(
            SetupErrorCode::HelperLogFailed,
            format!("failed to write setup log line: {err}"),
        ))
    })?;
    Ok(())
}

pub fn ensure_sandbox_users_group(log: &mut dyn Write) -> Result<()> {
    ensure_local_group(SANDBOX_USERS_GROUP, SANDBOX_USERS_GROUP_COMMENT, log)
}

pub fn resolve_sandbox_users_group_sid() -> Result<Vec<u8>> {
    resolve_sid(SANDBOX_USERS_GROUP)
}

pub fn ensure_sandbox_user(username: &str, password: &str, log: &mut dyn Write) -> Result<()> {
    ensure_local_user(username, password, log)?;
    ensure_local_group_member(SANDBOX_USERS_GROUP, username)?;
    Ok(())
}

pub fn ensure_local_user(name: &str, password: &str, log: &mut dyn Write) -> Result<()> {
    let name_w = to_wide(OsStr::new(name));
    // Cleartext password wide buffer for NetUserAdd / NetUserSetInfo. Zeroized
    // after the account API calls complete (Integration A1, W2 review finding 6)
    // so it does not linger in freed heap; the raw pointers into it are only
    // dereferenced inside the `unsafe` block below.
    let mut pwd_w = to_wide(OsStr::new(password));
    let result = (|| {
        unsafe {
            let info = USER_INFO_1 {
                usri1_name: name_w.as_ptr() as *mut u16,
                usri1_password: pwd_w.as_ptr() as *mut u16,
                usri1_password_age: 0,
                usri1_priv: USER_PRIV_USER,
                usri1_home_dir: std::ptr::null_mut(),
                usri1_comment: std::ptr::null_mut(),
                usri1_flags: UF_SCRIPT | UF_DONT_EXPIRE_PASSWD,
                usri1_script_path: std::ptr::null_mut(),
            };
            let status = NetUserAdd(
                std::ptr::null(),
                1,
                &info as *const _ as *mut u8,
                std::ptr::null_mut(),
            );
            if status != NERR_Success {
                // Try update password via level 1003.
                let pw_info = USER_INFO_1003 {
                    usri1003_password: pwd_w.as_ptr() as *mut u16,
                };
                let upd = NetUserSetInfo(
                    std::ptr::null(),
                    name_w.as_ptr(),
                    1003,
                    &pw_info as *const _ as *mut u8,
                    std::ptr::null_mut(),
                );
                if upd != NERR_Success {
                    log_line(log, &format!("NetUserSetInfo failed for {name} code {upd}"))?;
                    return Err(anyhow::Error::new(SetupFailure::new(
                        SetupErrorCode::HelperUserCreateOrUpdateFailed,
                        format!("failed to create/update user {name}, code {status}/{upd}"),
                    )));
                }
            }

            // Ensure the principal is a regular local user account.
            if let Ok(group_name) = lookup_account_name_for_sid(SID_USERS) {
                let group = to_wide(OsStr::new(&group_name));
                let member = LOCALGROUP_MEMBERS_INFO_3 {
                    lgrmi3_domainandname: name_w.as_ptr() as *mut u16,
                };
                let _ = NetLocalGroupAddMembers(
                    std::ptr::null(),
                    group.as_ptr(),
                    3,
                    &member as *const _ as *mut u8,
                    1,
                );
            } else {
                log_line(
                    log,
                    "LookupAccountSidW failed for Users SID; skipping Users group membership",
                )?;
            }
        }
        Ok(())
    })();
    pwd_w.zeroize();
    result
}

pub fn ensure_local_group(name: &str, comment: &str, log: &mut dyn Write) -> Result<()> {
    const ERROR_ALIAS_EXISTS: u32 = 1379;
    const NERR_GROUP_EXISTS: u32 = 2223;

    let name_w = to_wide(OsStr::new(name));
    let comment_w = to_wide(OsStr::new(comment));
    unsafe {
        let info = LOCALGROUP_INFO_1 {
            lgrpi1_name: name_w.as_ptr() as *mut u16,
            lgrpi1_comment: comment_w.as_ptr() as *mut u16,
        };
        let mut parm_err: u32 = 0;
        let status = NetLocalGroupAdd(
            std::ptr::null(),
            1,
            &info as *const _ as *mut u8,
            &mut parm_err as *mut _,
        );
        if status != NERR_Success && status != ERROR_ALIAS_EXISTS && status != NERR_GROUP_EXISTS {
            log_line(
                log,
                &format!("NetLocalGroupAdd failed for {name} code {status} parm_err={parm_err}"),
            )?;
            return Err(anyhow::Error::new(SetupFailure::new(
                SetupErrorCode::HelperUsersGroupCreateFailed,
                format!("failed to create local group {name}, code {status}"),
            )));
        }
    }
    Ok(())
}

pub fn ensure_local_group_member(group_name: &str, member_name: &str) -> Result<()> {
    // If the member is already in the group, NetLocalGroupAddMembers returns
    // ERROR_MEMBER_IN_ALIAS; treat that (and NERR_Success) as success. Any other code
    // (access denied, missing group, invalid account, ...) must fail instead of being
    // silently discarded, otherwise this reports success while the account never actually
    // joined the group (CodeRabbit/Greptile finding, PR #399, see ../VENDORING.md).
    const ERROR_MEMBER_IN_ALIAS: u32 = 1378;

    let group_w = to_wide(OsStr::new(group_name));
    let member_w = to_wide(OsStr::new(member_name));
    let status = unsafe {
        let member = LOCALGROUP_MEMBERS_INFO_3 {
            lgrmi3_domainandname: member_w.as_ptr() as *mut u16,
        };
        NetLocalGroupAddMembers(
            std::ptr::null(),
            group_w.as_ptr(),
            3,
            &member as *const _ as *mut u8,
            1,
        )
    };
    if status != NERR_Success && status != ERROR_MEMBER_IN_ALIAS {
        return Err(anyhow::Error::new(SetupFailure::new(
            SetupErrorCode::HelperUserProvisionFailed,
            format!("failed to add {member_name} to group {group_name}, code {status}"),
        )));
    }
    Ok(())
}

pub fn resolve_sid(name: &str) -> Result<Vec<u8>> {
    if let Some(sid_str) = well_known_sid_str(name) {
        return sid_bytes_from_string(sid_str);
    }
    let name_w = to_wide(OsStr::new(name));
    let mut sid_buffer = vec![0u8; 68];
    let mut sid_len: u32 = sid_buffer.len() as u32;
    let mut domain: Vec<u16> = Vec::new();
    let mut domain_len: u32 = 0;
    let mut use_type: SID_NAME_USE = 0;
    loop {
        let ok = unsafe {
            LookupAccountNameW(
                std::ptr::null(),
                name_w.as_ptr(),
                sid_buffer.as_mut_ptr() as *mut c_void,
                &mut sid_len,
                domain.as_mut_ptr(),
                &mut domain_len,
                &mut use_type,
            )
        };
        if ok != 0 {
            sid_buffer.truncate(sid_len as usize);
            return Ok(sid_buffer);
        }
        let err = unsafe { GetLastError() };
        if err == ERROR_INSUFFICIENT_BUFFER {
            sid_buffer.resize(sid_len as usize, 0);
            domain.resize(domain_len as usize, 0);
            continue;
        }
        return Err(anyhow::anyhow!(
            "LookupAccountNameW failed for {name}: {err}"
        ));
    }
}

fn well_known_sid_str(name: &str) -> Option<&'static str> {
    match name {
        "Administrators" => Some(SID_ADMINISTRATORS),
        "Users" => Some(SID_USERS),
        "Authenticated Users" => Some(SID_AUTHENTICATED_USERS),
        "Everyone" => Some(SID_EVERYONE),
        "SYSTEM" => Some(SID_SYSTEM),
        _ => None,
    }
}

fn sid_bytes_from_string(sid_str: &str) -> Result<Vec<u8>> {
    let sid_w = to_wide(OsStr::new(sid_str));
    let mut psid: *mut c_void = std::ptr::null_mut();
    if unsafe { ConvertStringSidToSidW(sid_w.as_ptr(), &mut psid) } == 0 {
        return Err(anyhow::anyhow!(
            "ConvertStringSidToSidW failed for {sid_str}: {}",
            unsafe { GetLastError() }
        ));
    }
    let sid_len = unsafe { GetLengthSid(psid) };
    if sid_len == 0 {
        unsafe {
            LocalFree(psid as _);
        }
        return Err(anyhow::anyhow!("GetLengthSid failed for {sid_str}"));
    }
    let mut out = vec![0u8; sid_len as usize];
    let ok = unsafe { CopySid(sid_len, out.as_mut_ptr() as *mut c_void, psid) };
    unsafe {
        LocalFree(psid as _);
    }
    if ok == 0 {
        return Err(anyhow::anyhow!("CopySid failed for {sid_str}"));
    }
    Ok(out)
}

fn lookup_account_name_for_sid(sid_str: &str) -> Result<String> {
    let sid_w = to_wide(OsStr::new(sid_str));
    let mut psid: *mut c_void = std::ptr::null_mut();
    if unsafe { ConvertStringSidToSidW(sid_w.as_ptr(), &mut psid) } == 0 {
        return Err(anyhow::anyhow!(
            "ConvertStringSidToSidW failed for {sid_str}: {}",
            unsafe { GetLastError() }
        ));
    }
    let mut name_len: u32 = 0;
    let mut domain_len: u32 = 0;
    let mut use_type: SID_NAME_USE = 0;
    let ok = unsafe {
        LookupAccountSidW(
            std::ptr::null(),
            psid,
            std::ptr::null_mut(),
            &mut name_len,
            std::ptr::null_mut(),
            &mut domain_len,
            &mut use_type,
        )
    };
    if ok == 0 {
        let err = unsafe { GetLastError() };
        if err != ERROR_INSUFFICIENT_BUFFER {
            unsafe {
                LocalFree(psid as _);
            }
            return Err(anyhow::anyhow!(
                "LookupAccountSidW preflight failed for {sid_str}: {err}"
            ));
        }
    }
    let mut name_buf: Vec<u16> = vec![0u16; name_len as usize];
    let mut domain_buf: Vec<u16> = vec![0u16; domain_len as usize];
    let ok = unsafe {
        LookupAccountSidW(
            std::ptr::null(),
            psid,
            name_buf.as_mut_ptr(),
            &mut name_len,
            domain_buf.as_mut_ptr(),
            &mut domain_len,
            &mut use_type,
        )
    };
    unsafe {
        LocalFree(psid as _);
    }
    if ok == 0 {
        return Err(anyhow::anyhow!(
            "LookupAccountSidW failed for {sid_str}: {}",
            unsafe { GetLastError() }
        ));
    }
    let name = String::from_utf16_lossy(&name_buf);
    Ok(name.trim_end_matches('\0').to_string())
}

pub fn sid_bytes_to_psid(sid: &[u8]) -> Result<*mut c_void> {
    let sid_str = string_from_sid_bytes(sid).map_err(anyhow::Error::msg)?;
    let sid_w = to_wide(OsStr::new(&sid_str));
    let mut psid: *mut c_void = std::ptr::null_mut();
    if unsafe { ConvertStringSidToSidW(sid_w.as_ptr(), &mut psid) } == 0 {
        return Err(anyhow::anyhow!(
            "ConvertStringSidToSidW failed: {}",
            unsafe { GetLastError() }
        ));
    }
    Ok(psid)
}
