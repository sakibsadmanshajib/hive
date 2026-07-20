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
use windows_sys::Win32::System::StationsAndDesktops::CloseWindowStation;
use windows_sys::Win32::System::StationsAndDesktops::CreateDesktopW;
use windows_sys::Win32::System::StationsAndDesktops::CreateWindowStationW;
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
use windows_sys::Win32::System::StationsAndDesktops::SetProcessWindowStation;

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

/// `CWF_CREATE_ONLY` (winuser.h): make `CreateWindowStationW` FAIL rather than
/// open an existing station of the same name, so a name collision or a
/// pre-squatted station is a hard error instead of a silent attach to another
/// object. Not re-exported by windows-sys 0.52; value from the Win32 headers.
const CWF_CREATE_ONLY: u32 = 0x0000_0001;

/// A private window station plus a desktop on it, created by the parent so the
/// sandbox runner (and the child it spawns) launch in UI isolation from the
/// interactive user's `WinSta0`. Replaces the former shared-`WinSta0` grant.
///
/// The runner is launched with `STARTUPINFO.lpDesktop` set to
/// [`PrivateWindowStation::startup_info_desktop`] (`"<winsta>\\<desktop>"`),
/// which `CreateProcessWithLogonW` honours (validated on spike307-win,
/// 2026-07-20): the runner attaches to THIS station, so it holds no handle to
/// the real desktop and cannot shatter-attack it, `SendInput` to it, or read
/// its clipboard. That is the UI-isolation hole the old shared-station grant
/// left open.
///
/// Both handles are held for the value's lifetime. The caller keeps the value
/// alive until `CreateProcessWithLogonW` has returned; by then the runner is
/// attached and holds its OWN process reference to the station and desktop, so
/// they outlive this value even after its handles close on drop.
pub struct PrivateWindowStation {
    winsta: isize,
    desktop: isize,
    /// `"<winsta>\\<desktop>"`, NUL-terminated UTF-16, for `STARTUPINFO.lpDesktop`.
    startup_name: Vec<u16>,
}

impl PrivateWindowStation {
    /// Creates the private station + desktop and grants `sandbox_username`
    /// access to BOTH objects. The grant is cheap insurance for a restrictive
    /// default DACL; the spike showed the child attaches via the default DACL
    /// even without it, but a hardened host may not, so it is applied anyway.
    ///
    /// Fail closed (D-005): any Win32 failure returns an error and NEVER falls
    /// back to the shared `WinSta0`. Assumes the caller runs in the interactive
    /// logged-in user's session (the Hive desktop app), where window-station
    /// creation is permitted; a de-elevated caller in session 0 gets
    /// `ERROR_ACCESS_DENIED` from `CreateWindowStationW`, surfaced here as an
    /// error rather than a downgrade.
    pub fn create(sandbox_username: &str) -> Result<Self> {
        let mut rng = SmallRng::from_entropy();
        let winsta_name = format!("HiveSandboxWinSta-{:x}", rng.r#gen::<u128>());
        let desktop_name = "HiveSandboxDesktop";

        // SAFETY: standard CreateWindowStationW / SetProcessWindowStation /
        // CreateDesktopW sequence. The saved station handle from
        // GetProcessWindowStation needs no close; the created winsta/desktop
        // handles are owned by the returned value (closed on drop). Every
        // fallible step is checked and, on failure, the process window station
        // is restored and any created handle closed before returning.
        let (winsta, desktop) =
            unsafe { create_private_winsta_desktop(&winsta_name, desktop_name)? };

        let station = Self {
            winsta,
            desktop,
            startup_name: to_wide(format!("{winsta_name}\\{desktop_name}")),
        };

        // Grant the sandbox account access to both objects. On failure the
        // returned `station` is dropped here, closing both handles.
        station.grant_access(sandbox_username)?;
        Ok(station)
    }

    /// `"<winsta>\\<desktop>"` as a `*mut u16` for `STARTUPINFO.lpDesktop`. The
    /// backing buffer lives as long as `self`; keep `self` alive across the
    /// `CreateProcessWithLogonW` call that reads this pointer.
    pub fn startup_info_desktop(&self) -> *mut u16 {
        self.startup_name.as_ptr() as *mut u16
    }

    /// Adds allow ACEs for the sandbox account SID on the private station and
    /// desktop, preserving every existing ACE (the creator keeps full control).
    fn grant_access(&self, sandbox_username: &str) -> Result<()> {
        let sid_bytes = crate::sandbox_users::resolve_sid(sandbox_username)?;
        let psid = crate::sandbox_users::sid_bytes_to_psid(&sid_bytes)?;
        // SAFETY: `psid` is a live SID (freed below); `self.winsta`/`self.desktop`
        // are the private objects created in `create`. Each Win32 step inside
        // `merge_grant_on_window_object` is checked.
        let result = unsafe {
            // KB165194: a window station needs two ACEs for a launched-as user —
            // an inherit-only generic ACE so child desktops inherit access, plus
            // the station-specific access on the station object itself.
            let winsta_entries = [
                explicit_grant(
                    psid,
                    GENERIC_ALL_MASK,
                    CONTAINER_INHERIT_ACE | INHERIT_ONLY_ACE | OBJECT_INHERIT_ACE,
                ),
                explicit_grant(psid, WINSTA_ALL_ACCESS, NO_INHERITANCE),
            ];
            merge_grant_on_window_object(self.winsta, &winsta_entries).and_then(|()| {
                let desktop_entries = [explicit_grant(psid, DESKTOP_ALL_ACCESS, NO_INHERITANCE)];
                merge_grant_on_window_object(self.desktop, &desktop_entries)
            })
        };
        unsafe {
            LocalFree(psid as HLOCAL);
        }
        result
    }
}

impl Drop for PrivateWindowStation {
    fn drop(&mut self) {
        // SAFETY: `desktop`/`winsta` are handles from CreateDesktopW /
        // CreateWindowStationW; closing each once is safe. Close the desktop
        // before the station it lives on.
        unsafe {
            if self.desktop != 0 {
                let _ = CloseDesktop(self.desktop);
            }
            if self.winsta != 0 {
                let _ = CloseWindowStation(self.winsta);
            }
        }
    }
}

/// Creates a private window station and a desktop on it, restoring the caller's
/// original process window station before returning. Returns the `(winsta,
/// desktop)` handle pair on success.
///
/// `SetProcessWindowStation` is required because `CreateDesktopW` always creates
/// the desktop on the process's CURRENT window station; there is no
/// target-station parameter. The switch is reverted immediately after the
/// desktop is created so the parent process keeps its own station. On any
/// failure the process window station is restored and every partially created
/// handle is closed (fail closed, no leak).
///
/// The station switch is process-global for its brief window, so callers must
/// spawn runners SERIALLY (the current runner-spawn path does): two concurrent
/// spawns could observe each other's temporary station. A per-launch
/// `CreateDesktopW`-on-a-separately-opened-station path (no process switch) is
/// the upgrade if concurrent per-task spawns are ever needed.
///
/// # Safety
/// Calls raw Win32 window-station APIs. Must run in the interactive user's
/// session (see [`PrivateWindowStation::create`]); the returned handles must be
/// closed by the caller (they are, via [`PrivateWindowStation`]'s `Drop`).
unsafe fn create_private_winsta_desktop(
    winsta_name: &str,
    desktop_name: &str,
) -> Result<(isize, isize)> {
    let saved = GetProcessWindowStation();
    let winsta_name_wide = to_wide(winsta_name);
    let winsta = CreateWindowStationW(
        winsta_name_wide.as_ptr(),
        CWF_CREATE_ONLY,
        WINSTA_ALL_ACCESS,
        ptr::null(),
    );
    if winsta == 0 {
        return Err(anyhow::anyhow!(
            "CreateWindowStationW failed: {}",
            GetLastError()
        ));
    }
    if SetProcessWindowStation(winsta) == 0 {
        let err = GetLastError();
        let _ = CloseWindowStation(winsta);
        return Err(anyhow::anyhow!(
            "SetProcessWindowStation(private) failed: {err}"
        ));
    }
    let desktop_name_wide = to_wide(desktop_name);
    let desktop = CreateDesktopW(
        desktop_name_wide.as_ptr(),
        ptr::null(),
        ptr::null(),
        0,
        DESKTOP_ALL_ACCESS,
        ptr::null(),
    );
    let desktop_err = GetLastError();
    // Restore the parent's own window station regardless of the desktop result,
    // so the parent process is never left stranded on the private station.
    if saved != 0 && SetProcessWindowStation(saved) == 0 {
        let restore_err = GetLastError();
        if desktop != 0 {
            let _ = CloseDesktop(desktop);
        }
        let _ = CloseWindowStation(winsta);
        return Err(anyhow::anyhow!(
            "SetProcessWindowStation(restore) failed: {restore_err}"
        ));
    }
    if desktop == 0 {
        let _ = CloseWindowStation(winsta);
        return Err(anyhow::anyhow!("CreateDesktopW failed: {desktop_err}"));
    }
    Ok((winsta, desktop))
}
