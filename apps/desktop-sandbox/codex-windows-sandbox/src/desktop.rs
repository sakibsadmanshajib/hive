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
use std::sync::Mutex;
use std::sync::OnceLock;
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
use windows_sys::Win32::System::StationsAndDesktops::GetThreadDesktop;
use windows_sys::Win32::System::StationsAndDesktops::SetProcessWindowStation;
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
/// is set, the child runs on a per-launch PRIVATE window station + desktop (full
/// UI isolation: a separate clipboard and global atom table, not just a separate
/// desktop under the shared `WinSta0`); otherwise it stays on the interactive
/// `Winsta0\Default`.
///
/// Created RUNNER-side (inside the sandbox-account runner), so the logon SID the
/// private objects are granted to is the runner's, which the restricted inner
/// child shares. See [`PrivateWindowStation`].
pub struct LaunchDesktop {
    _private: Option<PrivateWindowStation>,
    startup_name: Vec<u16>,
}

impl LaunchDesktop {
    pub fn prepare(use_private_desktop: bool, logs_base_dir: Option<&Path>) -> Result<Self> {
        if use_private_desktop {
            let private = PrivateWindowStation::create().map_err(|e| {
                logging::debug_log(
                    &format!("private window station create failed: {e}"),
                    logs_base_dir,
                );
                e
            })?;
            // Copy the "<winsta>\\<desktop>" name into our own buffer; the
            // pointer handed to STARTUPINFO.lpDesktop must outlive the spawn, and
            // `_private` keeps the objects themselves alive.
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
/// The RUNNER stays on `WinSta0`; only the untrusted inner CHILD is moved to a
/// private window station (via [`LaunchDesktop`] / [`PrivateWindowStation`]), so
/// this shared-station grant applies to the trusted runner only and is not a
/// UI-isolation relaxation for the sandboxed workload.
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

/// `CWF_CREATE_ONLY` (winuser.h): make `CreateWindowStationW` FAIL rather than
/// open an existing station of the same name, so a name collision or a
/// pre-squatted station is a hard error instead of a silent attach to another
/// object. Not re-exported by windows-sys 0.52; value from the Win32 headers.
const CWF_CREATE_ONLY: u32 = 0x0000_0001;

/// A per-launch PRIVATE window station plus a desktop on it, created RUNNER-side
/// so the sandboxed inner child runs in full UI isolation from the interactive
/// user's `WinSta0`: a separate clipboard and global atom table, not just a
/// separate desktop. The child holds no handle to the real desktop and cannot
/// shatter-attack it, `SendInput` to it, or read its clipboard.
///
/// The child is spawned with `STARTUPINFO.lpDesktop` set to this object's
/// [`PrivateWindowStation::startup_name`] (`"<winsta>\\<desktop>"`). Access is
/// granted to the runner's logon SID, which the restricted inner-child token
/// shares (see [`PrivateWindowStation::create`]).
///
/// Both handles are held for the value's lifetime; keep it alive until the child
/// has been created. By then the child holds its OWN process reference to the
/// station and desktop, so they outlive this value even after its handles close
/// on drop.
pub struct PrivateWindowStation {
    winsta: isize,
    desktop: isize,
    /// `"<winsta>\\<desktop>"`, NUL-terminated UTF-16, for `STARTUPINFO.lpDesktop`.
    startup_name: Vec<u16>,
}

impl PrivateWindowStation {
    /// Creates the private station + desktop and grants the CURRENT process's
    /// logon SID access to BOTH objects. Called RUNNER-side (the runner runs as
    /// the sandbox account), so the logon SID is the runner's; the restricted
    /// inner-child token derives from the runner's token and carries that same
    /// logon SID in its restricting-SID set, so this ONE grant lets the
    /// restricted child attach: it satisfies both halves of the
    /// `WRITE_RESTRICTED` access check (the logon SID is in the token's normal
    /// groups AND its restricting-SID set).
    ///
    /// Fail closed (D-005): any Win32 failure returns an error and NEVER falls
    /// back to the shared `WinSta0`. Assumes the caller runs in an interactive
    /// session (the runner is launched into one via seclogon), where
    /// window-station creation is permitted.
    pub fn create() -> Result<Self> {
        // OsRng (CSPRNG) for the station name: not cryptographically-weak
        // SmallRng, since this is a security-relevant object identifier.
        let mut token = [0u8; 16];
        OsRng.fill_bytes(&mut token);
        let winsta_name = format!("HiveSandboxWinSta-{:x}", u128::from_le_bytes(token));
        let desktop_name = "HiveSandboxDesktop";

        // SAFETY: standard CreateWindowStationW / SetProcessWindowStation /
        // CreateDesktopW sequence; see `create_private_winsta_desktop`. On
        // failure it restores the process window station and closes any created
        // handle before returning.
        let (winsta, desktop) =
            unsafe { create_private_winsta_desktop(&winsta_name, desktop_name)? };

        let station = Self {
            winsta,
            desktop,
            startup_name: to_wide(format!("{winsta_name}\\{desktop_name}")),
        };

        // On failure the returned `station` is dropped here, closing both handles.
        station.grant_access()?;
        Ok(station)
    }

    /// The `"<winsta>\\<desktop>"` name as NUL-terminated UTF-16, for the caller
    /// to copy into its own `STARTUPINFO.lpDesktop` buffer.
    pub(crate) fn startup_name(&self) -> &[u16] {
        &self.startup_name
    }

    /// Grants the current process's logon SID access to the private station and
    /// desktop, preserving every existing ACE (the creator keeps full control).
    /// See [`PrivateWindowStation::create`] for why the logon SID is the correct
    /// trustee for the restricted child.
    fn grant_access(&self) -> Result<()> {
        // SAFETY: the token helpers return this process's own token and its
        // logon SID; `psid` points into `logon_sid`, which outlives every grant
        // below. Each Win32 step inside `merge_grant_on_window_object` is checked.
        unsafe {
            let token = get_current_token_for_restriction()?;
            let mut logon_sid = get_logon_sid_bytes(token)?;
            CloseHandle(token);
            let psid = logon_sid.as_mut_ptr() as *mut c_void;
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
            merge_grant_on_window_object(self.winsta, &winsta_entries)?;
            let desktop_entries = [explicit_grant(psid, DESKTOP_ALL_ACCESS, NO_INHERITANCE)];
            merge_grant_on_window_object(self.desktop, &desktop_entries)?;
        }
        Ok(())
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

/// Serializes the process-global window-station switch inside
/// [`create_private_winsta_desktop`]. The hive sandbox is per-agent-task, so
/// concurrent launches are reachable; without this lock, one launch's
/// `SetProcessWindowStation` would corrupt another's `CreateDesktopW` placement.
/// Held across the ENTIRE switched window (from before the switch until after
/// the restore).
static STATION_SWITCH_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

/// Restores the process window station to `original` on drop, so a panic or an
/// early return between the switch and the explicit restore can never leave the
/// parent stranded on the private station (which would then make
/// `CloseWindowStation` fail on the current station). The happy path restores
/// explicitly via [`StationGuard::restore`] and checks the result; this `Drop`
/// is the last-resort fallback and disarms once the explicit restore succeeds.
struct StationGuard {
    original: isize,
    armed: bool,
}

impl StationGuard {
    /// Explicit, checked restore. Disarms the drop-time fallback ONLY on
    /// success, so a failed restore is retried when the guard drops. Returns the
    /// Win32 `BOOL` (0 means failure).
    ///
    /// # Safety
    /// `original` must be a valid window-station handle for this process.
    unsafe fn restore(&mut self) -> i32 {
        let ok = SetProcessWindowStation(self.original);
        if ok != 0 {
            self.armed = false;
        }
        ok
    }
}

impl Drop for StationGuard {
    fn drop(&mut self) {
        if self.armed {
            // SAFETY: `original` is the caller's valid station handle from
            // GetProcessWindowStation; making it current again needs no close.
            unsafe {
                let _ = SetProcessWindowStation(self.original);
            }
        }
    }
}

/// Creates `desktop_name` on the process's CURRENT window station. Split out of
/// [`create_private_winsta_desktop`] to keep that function under the size
/// guideline; must be called while the process is switched to the target
/// station.
///
/// # Safety
/// Calls raw Win32 `CreateDesktopW`; the returned handle must be closed by the
/// caller.
unsafe fn create_desktop_on_current_station(desktop_name: &str) -> Result<isize> {
    let desktop_name_wide = to_wide(desktop_name);
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

/// Creates a private window station and a desktop on it, restoring the caller's
/// original process window station before returning. Returns the `(winsta,
/// desktop)` handle pair on success.
///
/// `SetProcessWindowStation` is required because `CreateDesktopW` always creates
/// the desktop on the process's CURRENT window station; there is no
/// target-station parameter. The switch is reverted immediately after the
/// desktop is created so the parent keeps its own station, and the whole
/// switched window is serialized by [`STATION_SWITCH_LOCK`] so concurrent
/// per-task launches cannot corrupt each other's desktop placement. On any
/// failure the process window station is restored (via [`StationGuard`], which
/// also covers panics) BEFORE any private handle is closed, so
/// `CloseWindowStation` never runs on the current station.
///
/// # Safety
/// Calls raw Win32 window-station APIs. Must run in the interactive user's
/// session (see [`PrivateWindowStation::create`]); the returned handles must be
/// closed by the caller (they are, via [`PrivateWindowStation`]'s `Drop`).
unsafe fn create_private_winsta_desktop(
    winsta_name: &str,
    desktop_name: &str,
) -> Result<(isize, isize)> {
    // Serialize the whole process-global switch window (held until return).
    // Acquire the lock BEFORE reading `saved`: otherwise a concurrent launch
    // that has already switched the process to ITS private station could be
    // captured here as our `saved`, and on teardown we would restore the parent
    // to that other launch's (already closed) station. Reading `saved` under the
    // lock guarantees it is the real interactive station, not a transient one.
    let _switch_lock = STATION_SWITCH_LOCK
        .get_or_init(|| Mutex::new(()))
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);

    // Fail closed: without a valid saved station we cannot restore the parent
    // after the switch, so refuse BEFORE creating or switching anything.
    let saved = GetProcessWindowStation();
    if saved == 0 {
        return Err(anyhow::anyhow!(
            "GetProcessWindowStation failed: {}",
            GetLastError()
        ));
    }

    let winsta_name_wide = to_wide(winsta_name);
    let winsta = CreateWindowStationW(
        winsta_name_wide.as_ptr(),
        CWF_CREATE_ONLY,
        // READ_CONTROL | WRITE_DAC so the returned handle can GetSecurityInfo /
        // SetSecurityInfo when grant_access adds the sandbox SID ACE below.
        WINSTA_ALL_ACCESS | READ_CONTROL | WRITE_DAC,
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

    // Parent is now switched to `winsta`; `guard` restores `saved` on every exit
    // path (including panic) BEFORE any private handle is closed.
    let mut guard = StationGuard {
        original: saved,
        armed: true,
    };

    let desktop = create_desktop_on_current_station(desktop_name);
    // Explicit, checked restore; capture its error immediately, before any other
    // Win32 call clobbers the last-error.
    let restore_ok = guard.restore();
    let restore_err = if restore_ok == 0 { GetLastError() } else { 0 };

    match desktop {
        Ok(desktop) if restore_ok != 0 => Ok((winsta, desktop)),
        Ok(desktop) => {
            // Restore failed: the parent may still be on `winsta`, so closing it
            // could target the CURRENT station. Close only the desktop (never
            // current) and surface the error; `guard` (still armed) retries the
            // restore on drop. The winsta handle is intentionally leaked on this
            // near-impossible path rather than closed while possibly current.
            let _ = CloseDesktop(desktop);
            Err(anyhow::anyhow!(
                "SetProcessWindowStation(restore) failed: {restore_err}"
            ))
        }
        Err(e) => {
            // Desktop creation failed. If the restore succeeded, `winsta` is not
            // current and is safe to close; if it failed, leak it (guard retries).
            if restore_ok != 0 {
                let _ = CloseWindowStation(winsta);
            }
            Err(e)
        }
    }
}
