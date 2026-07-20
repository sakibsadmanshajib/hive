use crate::logging;
use crate::token::get_current_token_for_restriction;
use crate::token::get_logon_sid_bytes;
use crate::winutil::to_wide;
use anyhow::Result;
use rand::RngCore;
use rand::rngs::OsRng;
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

/// Chooses the desktop a launched child attaches to. When `use_private_desktop`
/// is set, the child runs on a per-launch PRIVATE desktop on the interactive
/// `WinSta0` (its own desktop object, isolated from `WinSta0\Default`); otherwise
/// it stays on `WinSta0\Default`.
///
/// This mirrors upstream codex `desktop.rs`: a private DESKTOP under the shared
/// `WinSta0`, NOT a private window station. A least-privilege sandbox account
/// cannot create a securable window station (`CreateWindowStationW` returns
/// `ERROR_ACCESS_DENIED` for that token), and upstream never creates one; a
/// private desktop is the standard per-process UI isolation boundary. The known
/// limit: clipboard and the global atom table are per-window-station and stay
/// shared with the interactive user (a private station would isolate them, but
/// the sandbox token cannot create one).
///
/// Created RUNNER-side (inside the sandbox-account runner), so the logon SID the
/// private desktop is granted to is the runner's, which the restricted inner
/// child shares. See [`PrivateDesktop`].
pub struct LaunchDesktop {
    _private: Option<PrivateDesktop>,
    startup_name: Vec<u16>,
}

impl LaunchDesktop {
    pub fn prepare(use_private_desktop: bool, logs_base_dir: Option<&Path>) -> Result<Self> {
        if use_private_desktop {
            let private = PrivateDesktop::create().map_err(|e| {
                logging::debug_log(
                    &format!("private desktop create failed: {e}"),
                    logs_base_dir,
                );
                e
            })?;
            // Copy the "WinSta0\\<desktop>" name into our own buffer; the pointer
            // handed to STARTUPINFO.lpDesktop must outlive the spawn, and
            // `_private` keeps the desktop object itself alive.
            let startup_name = private.startup_name().to_vec();
            Ok(Self {
                _private: Some(private),
                startup_name,
            })
        } else {
            Ok(Self {
                _private: None,
                startup_name: to_wide("Winsta0\\Default"),
            })
        }
    }

    pub fn startup_info_desktop(&self) -> *mut u16 {
        self.startup_name.as_ptr() as *mut u16
    }
}

/// Grants the named account access to the CURRENT (shared `WinSta0`) window
/// station and desktop, so the sandbox-account RUNNER launched via
/// `CreateProcessWithLogonW` can complete user32 process-attach at load (without
/// it the runner dies with `STATUS_DLL_INIT_FAILED` 0xC0000142 before `main`).
///
/// The RUNNER stays on `WinSta0\Default`; the untrusted inner CHILD is moved to a
/// private DESKTOP on `WinSta0` (via [`LaunchDesktop`] / [`PrivateDesktop`]). The
/// child holds no handle to the default desktop, so it cannot enumerate the
/// default desktop's windows or inject input across the desktop boundary.
/// Clipboard and the atom table are per-window-station and stay shared with the
/// interactive user: the known limit of desktop-only isolation.
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

// ACE inheritance flags + generic-all mask (kept local; windows-sys spreads
// these across modules and the numeric values are stable).
const GENERIC_ALL_MASK: u32 = 0x1000_0000;
// WINSTA_ALL_ACCESS is not re-exported by windows-sys 0.52; value from the
// Win32 headers (winuser.h): the OR of all WINSTA_* rights.
const WINSTA_ALL_ACCESS: u32 = 0x0000_037F;
// Standard access rights (winnt.h) a window-station/desktop HANDLE needs to read
// and rewrite its OWN security descriptor: GetSecurityInfo requires READ_CONTROL
// and SetSecurityInfo requires WRITE_DAC. WINSTA_ALL_ACCESS (0x037F) does NOT
// include either, so CreateWindowStationW must request them or the sandbox-SID
// grant fails with ERROR_ACCESS_DENIED and, being fail-closed, aborts EVERY
// spawn (lab, MSVC, spike307-win). DESKTOP_ALL_ACCESS already folds these in
// (DESKTOP_READ_CONTROL = 0x0002_0000, DESKTOP_WRITE_DAC = 0x0004_0000); we OR
// them onto the desktop mask too so the requirement is explicit at both sites.
const READ_CONTROL: u32 = 0x0002_0000;
const WRITE_DAC: u32 = 0x0004_0000;
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

/// A per-launch PRIVATE desktop on the interactive `WinSta0`, created RUNNER-side
/// so the sandboxed inner child runs on its own desktop object, isolated from the
/// interactive user's `WinSta0\Default`. The child holds no handle to the default
/// desktop, so it cannot enumerate its windows, `SendInput` to it, or mount a
/// shatter-style cross-window attack across the desktop boundary.
///
/// This mirrors upstream codex `desktop.rs` (a private DESKTOP under the shared
/// `WinSta0`, NOT a private window station). A least-privilege sandbox account
/// cannot create a securable window station: `CreateWindowStationW` returns
/// `ERROR_ACCESS_DENIED` (GLE 5) for that token, and upstream never creates one.
/// `CreateDesktopW` on `WinSta0` only needs `WINSTA_CREATEDESKTOP`, which the
/// parent already granted the sandbox account via [`grant_winsta_desktop_access`].
/// The residual limit: clipboard and the global atom table are per-window-station
/// and remain shared with the interactive user.
///
/// The child is spawned with `STARTUPINFO.lpDesktop` set to this object's
/// [`PrivateDesktop::startup_name`] (`"WinSta0\\<desktop>"`). Access is granted to
/// the runner's logon SID, which the restricted inner-child token shares (see
/// [`PrivateDesktop::create`]).
///
/// The handle is held for the value's lifetime; keep it alive until the child has
/// been created. By then the child holds its OWN process reference to the desktop,
/// so it outlives this value even after the handle closes on drop.
pub struct PrivateDesktop {
    desktop: isize,
    /// `"WinSta0\\<desktop>"`, NUL-terminated UTF-16, for `STARTUPINFO.lpDesktop`.
    startup_name: Vec<u16>,
}

impl PrivateDesktop {
    /// Creates the private desktop on the process's CURRENT window station (the
    /// interactive `WinSta0`) and grants the CURRENT process's logon SID access to
    /// it. Called RUNNER-side (the runner runs as the sandbox account), so the
    /// logon SID is the runner's; the restricted inner-child token derives from
    /// the runner's token and carries that same logon SID in its restricting-SID
    /// set, so this ONE grant lets the restricted child attach: the logon SID
    /// satisfies both halves of the `WRITE_RESTRICTED` access check (it is in the
    /// token's normal groups AND its restricting-SID set).
    ///
    /// No process-wide window-station switch is performed (`CreateDesktopW`
    /// targets the current station directly), so there is no station-switch lock
    /// or `StationGuard` to restore.
    ///
    /// Fail closed (D-005): any Win32 failure returns an error and NEVER falls
    /// back to `WinSta0\Default`.
    pub fn create() -> Result<Self> {
        // OsRng (CSPRNG) for the desktop name: a security-relevant object
        // identifier, so not cryptographically-weak SmallRng.
        let mut token = [0u8; 16];
        OsRng.fill_bytes(&mut token);
        let desktop_name = format!("HiveSandboxDesktop-{:x}", u128::from_le_bytes(token));

        // SAFETY: creates a desktop on the current (`WinSta0`) window station; the
        // returned handle is closed by this value's `Drop`.
        let desktop = unsafe { create_desktop_on_current_station(&desktop_name)? };

        let this = Self {
            desktop,
            // The interactive station is `WinSta0` (same convention as the
            // `Winsta0\Default` fallback above; Win32 station names are
            // case-insensitive).
            startup_name: to_wide(format!("Winsta0\\{desktop_name}")),
        };

        // On failure the returned `this` is dropped here, closing the handle.
        this.grant_access()?;
        Ok(this)
    }

    /// The `"WinSta0\\<desktop>"` name as NUL-terminated UTF-16, for the caller to
    /// copy into its own `STARTUPINFO.lpDesktop` buffer.
    pub(crate) fn startup_name(&self) -> &[u16] {
        &self.startup_name
    }

    /// Grants the current process's logon SID access to the private desktop,
    /// preserving every existing ACE (the creator keeps full control). See
    /// [`PrivateDesktop::create`] for why the logon SID is the correct trustee for
    /// the restricted child.
    fn grant_access(&self) -> Result<()> {
        // SAFETY: the token helpers return this process's own token and its logon
        // SID; `psid` points into `logon_sid`, which outlives the grant below.
        // Each Win32 step inside `merge_grant_on_window_object` is checked.
        unsafe {
            let token = get_current_token_for_restriction()?;
            // Close the token on both the success and error paths of
            // get_logon_sid_bytes: propagating its error with `?` before
            // CloseHandle would leak the handle.
            let logon_sid_result = get_logon_sid_bytes(token);
            CloseHandle(token);
            let mut logon_sid = logon_sid_result?;
            let psid = logon_sid.as_mut_ptr() as *mut c_void;
            let desktop_entries = [explicit_grant(psid, DESKTOP_ALL_ACCESS, NO_INHERITANCE)];
            merge_grant_on_window_object(self.desktop, &desktop_entries)?;
        }
        Ok(())
    }
}

impl Drop for PrivateDesktop {
    fn drop(&mut self) {
        // SAFETY: `desktop` is a handle from CreateDesktopW; closing it once is
        // safe.
        unsafe {
            if self.desktop != 0 {
                let _ = CloseDesktop(self.desktop);
            }
        }
    }
}

/// Creates `desktop_name` on the process's CURRENT window station (the
/// interactive `WinSta0` for the sandbox-account runner). No station switch is
/// performed: `CreateDesktopW` targets the current station directly.
///
/// # Safety
/// Calls raw Win32 `CreateDesktopW`; the returned handle must be closed by the
/// caller.
unsafe fn create_desktop_on_current_station(desktop_name: &str) -> Result<isize> {
    let desktop_name_wide = to_wide(desktop_name);
    // CreateDesktopW has no CWF_CREATE_ONLY equivalent: a duplicate name opens
    // the existing desktop rather than erroring. Accepted because desktop_name
    // carries a 128-bit OsRng suffix, so a collision is cryptographically
    // negligible.
    let desktop = CreateDesktopW(
        desktop_name_wide.as_ptr(),
        ptr::null(),
        ptr::null(),
        0,
        // READ_CONTROL | WRITE_DAC so the handle can GetSecurityInfo /
        // SetSecurityInfo for the sandbox SID grant. DESKTOP_ALL_ACCESS already
        // includes both (DESKTOP_READ_CONTROL / DESKTOP_WRITE_DAC); ORed again
        // here so the requirement is explicit and not dependent on that alias.
        DESKTOP_ALL_ACCESS | READ_CONTROL | WRITE_DAC,
        ptr::null(),
    );
    if desktop == 0 {
        return Err(anyhow::anyhow!("CreateDesktopW failed: {}", GetLastError()));
    }
    Ok(desktop)
}
