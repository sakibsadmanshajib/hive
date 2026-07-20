use crate::logging;
use crate::token::get_current_token_for_restriction;
use crate::token::get_logon_sid_bytes;
use crate::winutil::format_last_error;
use crate::winutil::to_wide;
use anyhow::Result;
use rand::Rng;
use rand::SeedableRng;
use rand::rngs::SmallRng;
use std::ffi::c_void;
use std::path::Path;
use std::ptr;
use windows_sys::Win32::Foundation::CloseHandle;
use windows_sys::Win32::Foundation::ERROR_SUCCESS;
use windows_sys::Win32::Foundation::GetLastError;
use windows_sys::Win32::Foundation::HLOCAL;
use windows_sys::Win32::Foundation::LocalFree;
use windows_sys::Win32::Security::ACL;
use windows_sys::Win32::Security::Authorization::EXPLICIT_ACCESS_W;
use windows_sys::Win32::Security::Authorization::GRANT_ACCESS;
use windows_sys::Win32::Security::Authorization::GetSecurityInfo;
use windows_sys::Win32::Security::Authorization::SE_WINDOW_OBJECT;
use windows_sys::Win32::Security::Authorization::SetEntriesInAclW;
use windows_sys::Win32::Security::Authorization::SetSecurityInfo;
use windows_sys::Win32::Security::Authorization::TRUSTEE_IS_SID;
use windows_sys::Win32::Security::Authorization::TRUSTEE_IS_UNKNOWN;
use windows_sys::Win32::Security::Authorization::TRUSTEE_W;
use windows_sys::Win32::Security::DACL_SECURITY_INFORMATION;
use windows_sys::Win32::System::StationsAndDesktops::CloseDesktop;
use windows_sys::Win32::System::StationsAndDesktops::CreateDesktopW;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_CREATEMENU;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_CREATEWINDOW;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_DELETE;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_ENUMERATE;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_HOOKCONTROL;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_JOURNALPLAYBACK;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_JOURNALRECORD;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_READ_CONTROL;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_READOBJECTS;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_SWITCHDESKTOP;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_WRITE_DAC;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_WRITE_OWNER;
use windows_sys::Win32::System::StationsAndDesktops::DESKTOP_WRITEOBJECTS;
use windows_sys::Win32::System::StationsAndDesktops::GetProcessWindowStation;
use windows_sys::Win32::System::StationsAndDesktops::GetThreadDesktop;
use windows_sys::Win32::System::Threading::GetCurrentThreadId;

const DESKTOP_ALL_ACCESS: u32 = DESKTOP_READOBJECTS
    | DESKTOP_CREATEWINDOW
    | DESKTOP_CREATEMENU
    | DESKTOP_HOOKCONTROL
    | DESKTOP_JOURNALRECORD
    | DESKTOP_JOURNALPLAYBACK
    | DESKTOP_ENUMERATE
    | DESKTOP_WRITEOBJECTS
    | DESKTOP_SWITCHDESKTOP
    | DESKTOP_DELETE
    | DESKTOP_READ_CONTROL
    | DESKTOP_WRITE_DAC
    | DESKTOP_WRITE_OWNER;

pub struct LaunchDesktop {
    _private_desktop: Option<PrivateDesktop>,
    startup_name: Vec<u16>,
}

impl LaunchDesktop {
    pub fn prepare(use_private_desktop: bool, logs_base_dir: Option<&Path>) -> Result<Self> {
        if use_private_desktop {
            let private_desktop = PrivateDesktop::create(logs_base_dir)?;
            let startup_name = to_wide(format!("Winsta0\\{}", private_desktop.name));
            Ok(Self {
                _private_desktop: Some(private_desktop),
                startup_name,
            })
        } else {
            Ok(Self {
                _private_desktop: None,
                startup_name: to_wide("Winsta0\\Default"),
            })
        }
    }

    pub fn startup_info_desktop(&self) -> *mut u16 {
        self.startup_name.as_ptr() as *mut u16
    }
}

struct PrivateDesktop {
    handle: isize,
    name: String,
}

impl PrivateDesktop {
    fn create(logs_base_dir: Option<&Path>) -> Result<Self> {
        let mut rng = SmallRng::from_entropy();
        let name = format!("CodexSandboxDesktop-{:x}", rng.r#gen::<u128>());
        let name_wide = to_wide(&name);
        let handle = unsafe {
            CreateDesktopW(
                name_wide.as_ptr(),
                ptr::null(),
                ptr::null_mut(),
                0,
                DESKTOP_ALL_ACCESS,
                ptr::null_mut(),
            )
        };
        if handle == 0 {
            let err = unsafe { GetLastError() } as i32;
            logging::debug_log(
                &format!(
                    "CreateDesktopW failed for {name}: {} ({})",
                    err,
                    format_last_error(err),
                ),
                logs_base_dir,
            );
            return Err(anyhow::anyhow!("CreateDesktopW failed: {err}"));
        }

        unsafe {
            if let Err(err) = grant_desktop_access(handle, logs_base_dir) {
                let _ = CloseDesktop(handle);
                return Err(err);
            }
        }

        Ok(Self { handle, name })
    }
}

unsafe fn grant_desktop_access(handle: isize, logs_base_dir: Option<&Path>) -> Result<()> {
    let token = get_current_token_for_restriction()?;
    let mut logon_sid = get_logon_sid_bytes(token)?;
    CloseHandle(token);

    let entries = [EXPLICIT_ACCESS_W {
        grfAccessPermissions: DESKTOP_ALL_ACCESS,
        grfAccessMode: GRANT_ACCESS,
        grfInheritance: 0,
        Trustee: TRUSTEE_W {
            pMultipleTrustee: ptr::null_mut(),
            MultipleTrusteeOperation: 0,
            TrusteeForm: TRUSTEE_IS_SID,
            TrusteeType: TRUSTEE_IS_UNKNOWN,
            ptstrName: logon_sid.as_mut_ptr() as *mut c_void as *mut u16,
        },
    }];

    let mut updated_dacl = ptr::null_mut();
    let set_entries_code = SetEntriesInAclW(
        entries.len() as u32,
        entries.as_ptr(),
        ptr::null_mut(),
        &mut updated_dacl,
    );
    if set_entries_code != ERROR_SUCCESS {
        logging::debug_log(
            &format!("SetEntriesInAclW failed for private desktop: {set_entries_code}"),
            logs_base_dir,
        );
        return Err(anyhow::anyhow!(
            "SetEntriesInAclW failed for private desktop: {set_entries_code}"
        ));
    }

    let set_security_code = SetSecurityInfo(
        handle,
        SE_WINDOW_OBJECT,
        DACL_SECURITY_INFORMATION,
        ptr::null_mut(),
        ptr::null_mut(),
        updated_dacl,
        ptr::null_mut(),
    );
    if !updated_dacl.is_null() {
        LocalFree(updated_dacl as HLOCAL);
    }
    if set_security_code != ERROR_SUCCESS {
        logging::debug_log(
            &format!("SetSecurityInfo failed for private desktop: {set_security_code}"),
            logs_base_dir,
        );
        return Err(anyhow::anyhow!(
            "SetSecurityInfo failed for private desktop: {set_security_code}"
        ));
    }

    Ok(())
}

impl Drop for PrivateDesktop {
    fn drop(&mut self) {
        unsafe {
            if self.handle != 0 {
                let _ = CloseDesktop(self.handle);
            }
        }
    }
}

// ACE inheritance flags + generic-all mask (kept local; windows-sys spreads
// these across modules and the numeric values are stable).
const GENERIC_ALL_MASK: u32 = 0x1000_0000;
// WINSTA_ALL_ACCESS is not re-exported by windows-sys 0.52; value from the
// Win32 headers (winuser.h): the OR of all WINSTA_* rights.
const WINSTA_ALL_ACCESS: u32 = 0x0000_037F;
const CONTAINER_INHERIT_ACE: u32 = 0x2;
const OBJECT_INHERIT_ACE: u32 = 0x1;
const INHERIT_ONLY_ACE: u32 = 0x8;
const NO_INHERITANCE: u32 = 0x0;

fn explicit_grant(psid: *mut c_void, access: u32, inheritance: u32) -> EXPLICIT_ACCESS_W {
    EXPLICIT_ACCESS_W {
        grfAccessPermissions: access,
        grfAccessMode: GRANT_ACCESS,
        grfInheritance: inheritance,
        Trustee: TRUSTEE_W {
            pMultipleTrustee: ptr::null_mut(),
            MultipleTrusteeOperation: 0,
            TrusteeForm: TRUSTEE_IS_SID,
            TrusteeType: TRUSTEE_IS_UNKNOWN,
            ptstrName: psid as *mut u16,
        },
    }
}

/// Merges `entries` (allow ACEs) into the existing DACL of a window-station or
/// desktop object, preserving every existing ACE. The interactive user's and
/// SYSTEM's access are never removed; we only ADD the sandbox account.
unsafe fn merge_grant_on_window_object(handle: isize, entries: &[EXPLICIT_ACCESS_W]) -> Result<()> {
    let mut p_dacl: *mut ACL = ptr::null_mut();
    let mut p_sd: *mut c_void = ptr::null_mut();
    let code = GetSecurityInfo(
        handle,
        SE_WINDOW_OBJECT,
        DACL_SECURITY_INFORMATION,
        ptr::null_mut(),
        ptr::null_mut(),
        &mut p_dacl,
        ptr::null_mut(),
        &mut p_sd,
    );
    if code != ERROR_SUCCESS {
        return Err(anyhow::anyhow!(
            "GetSecurityInfo(window object) failed: {code}"
        ));
    }
    let mut new_dacl: *mut ACL = ptr::null_mut();
    let set_entries = SetEntriesInAclW(
        entries.len() as u32,
        entries.as_ptr(),
        p_dacl,
        &mut new_dacl,
    );
    if set_entries != ERROR_SUCCESS {
        if !p_sd.is_null() {
            LocalFree(p_sd as HLOCAL);
        }
        return Err(anyhow::anyhow!(
            "SetEntriesInAclW(window object) failed: {set_entries}"
        ));
    }
    let set_security = SetSecurityInfo(
        handle,
        SE_WINDOW_OBJECT,
        DACL_SECURITY_INFORMATION,
        ptr::null_mut(),
        ptr::null_mut(),
        new_dacl,
        ptr::null_mut(),
    );
    if !new_dacl.is_null() {
        LocalFree(new_dacl as HLOCAL);
    }
    if !p_sd.is_null() {
        LocalFree(p_sd as HLOCAL);
    }
    if set_security != ERROR_SUCCESS {
        return Err(anyhow::anyhow!(
            "SetSecurityInfo(window object) failed: {set_security}"
        ));
    }
    Ok(())
}

/// Grants the named account the window-station and desktop access a process
/// launched AS that account needs to INITIALIZE.
///
/// The command-runner links `user32.dll`; its process-attach initialization
/// connects to the caller's window station and desktop. When the runner is
/// launched as the low-privilege sandbox account via `CreateProcessWithLogonW`,
/// that account has no access to the caller's station/desktop by default, so
/// the connect fails and the runner dies at load with `STATUS_DLL_INIT_FAILED`
/// (0xC0000142) BEFORE `main` runs (no output, no crash event). Granting the
/// sandbox SID access to the CURRENT window station + desktop (whatever the
/// caller is on: the interactive `WinSta0` for the desktop app, a service
/// station for a headless/CI host) fixes it. The launch leaves
/// `STARTUPINFO.lpDesktop` NULL so the runner inherits this same, now-accessible
/// station/desktop; there is deliberately no hardcoded station name.
///
/// Scope (D-005): this is a launch PREREQUISITE, not a relaxation of any control
/// Step 3 enforces. Step 3's confinement is filesystem + sandbox user + a
/// capability-restricted token + Job Object; UI/desktop isolation (a dedicated
/// private window station for the runner) is a separate follow-up hardening and
/// is intentionally not attempted here.
///
/// KNOWN GAP, dispositioned and NOT re-fixed here (Greptile finding "Desktop
/// Grant Outlives Runner", PR #401 review round; opus security review PASSED
/// PR #401 MERGE-WITH-TRACKED-FOLLOWUP on this exact basis): the grant is
/// persistent (`WINSTA_ALL_ACCESS` / inheritable `GENERIC_ALL` / `DESKTOP_ALL_ACCESS`)
/// and never revoked, so any later process running as the shared sandbox
/// account on this station/desktop also inherits it. Unreachable in the
/// product path today because `windows::launch` refuses every network
/// policy (`DenyAll` and `AllowHosts` both fail closed; see VENDORING.md
/// "Open risks" #3), so nothing reaches this grant outside the lab-only
/// `spawn_confined_for_validation` entry.
///
/// HARD GATE ON INTEGRATION B: do not remove or loosen the network-refusal
/// guards in `windows::launch` until the runner is switched to a dedicated,
/// non-shared private window station (revoking this shared-station/desktop
/// grant) instead of the caller's own interactive station. WFP egress
/// enforcement landing without that switch would make this gap reachable in
/// production. Track resolution together, not WFP alone.
pub fn grant_winsta_desktop_access(sandbox_username: &str) -> Result<()> {
    let sid_bytes = crate::sandbox_users::resolve_sid(sandbox_username)?;
    let psid = crate::sandbox_users::sid_bytes_to_psid(&sid_bytes)?;
    // SAFETY: `psid` is a live SID from ConvertStringSidToSidW (freed below);
    // the window-station/desktop handles are process/thread pseudo-handles that
    // need no close. Every fallible Win32 step is checked.
    let result = unsafe {
        let winsta = GetProcessWindowStation();
        if winsta == 0 {
            LocalFree(psid as HLOCAL);
            return Err(anyhow::anyhow!(
                "GetProcessWindowStation failed: {}",
                GetLastError()
            ));
        }
        // KB165194: a window station needs two ACEs for a launched-as user — an
        // inherit-only generic ACE so child desktops inherit access, plus the
        // station-specific access on the station object itself.
        let winsta_entries = [
            explicit_grant(
                psid,
                GENERIC_ALL_MASK,
                CONTAINER_INHERIT_ACE | INHERIT_ONLY_ACE | OBJECT_INHERIT_ACE,
            ),
            explicit_grant(psid, WINSTA_ALL_ACCESS, NO_INHERITANCE),
        ];
        merge_grant_on_window_object(winsta, &winsta_entries).and_then(|()| {
            let desktop = GetThreadDesktop(GetCurrentThreadId());
            if desktop == 0 {
                return Err(anyhow::anyhow!(
                    "GetThreadDesktop failed: {}",
                    GetLastError()
                ));
            }
            let desktop_entries = [explicit_grant(psid, DESKTOP_ALL_ACCESS, NO_INHERITANCE)];
            merge_grant_on_window_object(desktop, &desktop_entries)
        })
    };
    unsafe {
        LocalFree(psid as HLOCAL);
    }
    result
}
