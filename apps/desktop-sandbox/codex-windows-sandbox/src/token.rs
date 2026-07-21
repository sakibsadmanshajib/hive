use crate::winutil::to_wide;
use anyhow::Result;
use anyhow::anyhow;
use std::ffi::c_void;
use windows_sys::Win32::Foundation::CloseHandle;
use windows_sys::Win32::Foundation::ERROR_SUCCESS;
use windows_sys::Win32::Foundation::GetLastError;
use windows_sys::Win32::Foundation::HANDLE;
use windows_sys::Win32::Foundation::HLOCAL;
use windows_sys::Win32::Foundation::LUID;
use windows_sys::Win32::Foundation::LocalFree;
use windows_sys::Win32::Security::AdjustTokenPrivileges;
use windows_sys::Win32::Security::Authorization::EXPLICIT_ACCESS_W;
use windows_sys::Win32::Security::Authorization::GRANT_ACCESS;
use windows_sys::Win32::Security::Authorization::SetEntriesInAclW;
use windows_sys::Win32::Security::Authorization::TRUSTEE_IS_SID;
use windows_sys::Win32::Security::Authorization::TRUSTEE_IS_UNKNOWN;
use windows_sys::Win32::Security::Authorization::TRUSTEE_W;
use windows_sys::Win32::Security::CopySid;
use windows_sys::Win32::Security::CreateRestrictedToken;
use windows_sys::Win32::Security::CreateWellKnownSid;
use windows_sys::Win32::Security::GetLengthSid;
use windows_sys::Win32::Security::GetTokenInformation;
use windows_sys::Win32::Security::LookupPrivilegeValueW;
use windows_sys::Win32::Security::SetTokenInformation;

use windows_sys::Win32::Security::ACL;
use windows_sys::Win32::Security::LUID_AND_ATTRIBUTES;
use windows_sys::Win32::Security::SID_AND_ATTRIBUTES;
use windows_sys::Win32::Security::TOKEN_ADJUST_DEFAULT;
use windows_sys::Win32::Security::TOKEN_ADJUST_PRIVILEGES;
use windows_sys::Win32::Security::TOKEN_ADJUST_SESSIONID;
use windows_sys::Win32::Security::TOKEN_ASSIGN_PRIMARY;
use windows_sys::Win32::Security::TOKEN_DUPLICATE;
use windows_sys::Win32::Security::TOKEN_PRIVILEGES;
use windows_sys::Win32::Security::TOKEN_QUERY;
use windows_sys::Win32::Security::TOKEN_USER;
use windows_sys::Win32::Security::TokenDefaultDacl;
use windows_sys::Win32::Security::TokenGroups;
use windows_sys::Win32::Security::TokenPrivileges;
use windows_sys::Win32::Security::TokenUser;
use windows_sys::Win32::System::Threading::GetCurrentProcess;

const LUA_TOKEN: u32 = 0x04;
const GENERIC_ALL: u32 = 0x1000_0000;
const WIN_WORLD_SID: i32 = 1;
const SE_GROUP_LOGON_ID: u32 = 0xC0000000;
/// `BUILTIN\Administrators`, disabled on the sandbox token.
const BUILTIN_ADMINISTRATORS_SID: &str = "S-1-5-32-544";
/// The only privilege the sandboxed child keeps. Everything else on the base
/// token is deleted (not merely disabled) by `CreateRestrictedToken`.
const KEPT_PRIVILEGE: &str = "SeChangeNotifyPrivilege";

#[repr(C)]
struct TokenDefaultDaclInfo {
    default_dacl: *mut ACL,
}

/// Sets a permissive default DACL so sandboxed processes can create pipes/IPC objects
/// without hitting ACCESS_DENIED when PowerShell builds pipelines.
unsafe fn set_default_dacl(h_token: HANDLE, sids: &[*mut c_void]) -> Result<()> {
    if sids.is_empty() {
        return Ok(());
    }
    let entries: Vec<EXPLICIT_ACCESS_W> = sids
        .iter()
        .map(|sid| EXPLICIT_ACCESS_W {
            grfAccessPermissions: GENERIC_ALL,
            grfAccessMode: GRANT_ACCESS,
            grfInheritance: 0,
            Trustee: TRUSTEE_W {
                pMultipleTrustee: std::ptr::null_mut(),
                MultipleTrusteeOperation: 0,
                TrusteeForm: TRUSTEE_IS_SID,
                TrusteeType: TRUSTEE_IS_UNKNOWN,
                ptstrName: *sid as *mut u16,
            },
        })
        .collect();
    let mut p_new_dacl: *mut ACL = std::ptr::null_mut();
    let res = SetEntriesInAclW(
        entries.len() as u32,
        entries.as_ptr(),
        std::ptr::null_mut(),
        &mut p_new_dacl,
    );
    if res != ERROR_SUCCESS {
        return Err(anyhow!("SetEntriesInAclW failed: {res}"));
    }
    let mut info = TokenDefaultDaclInfo {
        default_dacl: p_new_dacl,
    };
    let ok = SetTokenInformation(
        h_token,
        TokenDefaultDacl,
        &mut info as *mut _ as *mut c_void,
        std::mem::size_of::<TokenDefaultDaclInfo>() as u32,
    );
    if ok == 0 {
        let err = GetLastError();
        if !p_new_dacl.is_null() {
            LocalFree(p_new_dacl as HLOCAL);
        }
        return Err(anyhow!(
            "SetTokenInformation(TokenDefaultDacl) failed: {err}",
        ));
    }
    if !p_new_dacl.is_null() {
        LocalFree(p_new_dacl as HLOCAL);
    }
    Ok(())
}

pub unsafe fn world_sid() -> Result<Vec<u8>> {
    let mut size: u32 = 0;
    CreateWellKnownSid(
        WIN_WORLD_SID,
        std::ptr::null_mut(),
        std::ptr::null_mut(),
        &mut size,
    );
    let mut buf: Vec<u8> = vec![0u8; size as usize];
    let ok = CreateWellKnownSid(
        WIN_WORLD_SID,
        std::ptr::null_mut(),
        buf.as_mut_ptr() as *mut c_void,
        &mut size,
    );
    if ok == 0 {
        return Err(anyhow!("CreateWellKnownSid failed: {}", GetLastError()));
    }
    Ok(buf)
}

/// # Safety
/// Caller is responsible for freeing the returned SID with `LocalFree`.
pub unsafe fn convert_string_sid_to_sid(s: &str) -> Option<*mut c_void> {
    #[link(name = "advapi32")]
    unsafe extern "system" {
        fn ConvertStringSidToSidW(StringSid: *const u16, Sid: *mut *mut c_void) -> i32;
    }
    let mut psid: *mut c_void = std::ptr::null_mut();
    let ok = unsafe { ConvertStringSidToSidW(to_wide(s).as_ptr(), &mut psid) };
    if ok != 0 { Some(psid) } else { None }
}

/// Owns a SID allocated by `ConvertStringSidToSidW` and releases it with `LocalFree`.
pub struct LocalSid {
    psid: *mut c_void,
}

impl LocalSid {
    pub fn from_string(sid: &str) -> Result<Self> {
        let psid = unsafe { convert_string_sid_to_sid(sid) }
            .ok_or_else(|| anyhow!("invalid SID string: {sid}"))?;
        Ok(Self { psid })
    }

    pub fn as_ptr(&self) -> *mut c_void {
        self.psid
    }
}

impl Drop for LocalSid {
    fn drop(&mut self) {
        if !self.psid.is_null() {
            unsafe {
                LocalFree(self.psid as HLOCAL);
            }
        }
    }
}

/// # Safety
/// Caller must close the returned token handle.
pub unsafe fn get_current_token_for_restriction() -> Result<HANDLE> {
    let desired = TOKEN_DUPLICATE
        | TOKEN_QUERY
        | TOKEN_ASSIGN_PRIMARY
        | TOKEN_ADJUST_DEFAULT
        | TOKEN_ADJUST_SESSIONID
        | TOKEN_ADJUST_PRIVILEGES;
    let mut h: HANDLE = 0;
    #[link(name = "advapi32")]
    unsafe extern "system" {
        fn OpenProcessToken(
            ProcessHandle: HANDLE,
            DesiredAccess: u32,
            TokenHandle: *mut HANDLE,
        ) -> i32;
    }
    let ok = unsafe { OpenProcessToken(GetCurrentProcess(), desired, &mut h) };
    if ok == 0 {
        return Err(anyhow!("OpenProcessToken failed: {}", GetLastError()));
    }
    Ok(h)
}

/// Copies every group SID on `h` that carries `SE_GROUP_LOGON_ID`.
///
/// A token normally carries exactly one logon SID, but the API models groups as
/// a list and nothing guarantees uniqueness, so this returns all of them: the
/// sandbox token builder must disable every logon SID it finds, not just the
/// first one.
unsafe fn scan_token_groups_for_logon(h: HANDLE) -> Vec<Vec<u8>> {
    let mut out: Vec<Vec<u8>> = Vec::new();
    let mut needed: u32 = 0;
    GetTokenInformation(h, TokenGroups, std::ptr::null_mut(), 0, &mut needed);
    if needed == 0 {
        return out;
    }
    let mut buf: Vec<u8> = vec![0u8; needed as usize];
    let ok = GetTokenInformation(
        h,
        TokenGroups,
        buf.as_mut_ptr() as *mut c_void,
        needed,
        &mut needed,
    );
    if ok == 0 || (needed as usize) < std::mem::size_of::<u32>() {
        return out;
    }
    let group_count = std::ptr::read_unaligned(buf.as_ptr() as *const u32) as usize;
    // TOKEN_GROUPS layout is: DWORD GroupCount; SID_AND_ATTRIBUTES Groups[];
    // On 64-bit, Groups is aligned to pointer alignment after 4-byte GroupCount.
    let after_count = unsafe { buf.as_ptr().add(std::mem::size_of::<u32>()) } as usize;
    let align = std::mem::align_of::<SID_AND_ATTRIBUTES>();
    let aligned = (after_count + (align - 1)) & !(align - 1);
    let groups_ptr = aligned as *const SID_AND_ATTRIBUTES;
    for i in 0..group_count {
        let entry: SID_AND_ATTRIBUTES = std::ptr::read_unaligned(groups_ptr.add(i));
        if (entry.Attributes & SE_GROUP_LOGON_ID) != SE_GROUP_LOGON_ID {
            continue;
        }
        let sid = entry.Sid;
        let sid_len = GetLengthSid(sid);
        if sid_len == 0 {
            continue;
        }
        let mut copied = vec![0u8; sid_len as usize];
        if CopySid(sid_len, copied.as_mut_ptr() as *mut c_void, sid) == 0 {
            continue;
        }
        out.push(copied);
    }
    out
}

pub unsafe fn get_logon_sid_bytes(h_token: HANDLE) -> Result<Vec<u8>> {
    if let Some(v) = scan_token_groups_for_logon(h_token).into_iter().next() {
        return Ok(v);
    }

    #[repr(C)]
    struct TOKEN_LINKED_TOKEN {
        linked_token: HANDLE,
    }
    const TOKEN_LINKED_TOKEN_CLASS: i32 = 19; // TokenLinkedToken
    let mut ln_needed: u32 = 0;
    GetTokenInformation(
        h_token,
        TOKEN_LINKED_TOKEN_CLASS,
        std::ptr::null_mut(),
        0,
        &mut ln_needed,
    );
    if ln_needed >= std::mem::size_of::<TOKEN_LINKED_TOKEN>() as u32 {
        let mut ln_buf: Vec<u8> = vec![0u8; ln_needed as usize];
        let ok = GetTokenInformation(
            h_token,
            TOKEN_LINKED_TOKEN_CLASS,
            ln_buf.as_mut_ptr() as *mut c_void,
            ln_needed,
            &mut ln_needed,
        );
        if ok != 0 {
            let lt: TOKEN_LINKED_TOKEN =
                std::ptr::read_unaligned(ln_buf.as_ptr() as *const TOKEN_LINKED_TOKEN);
            if lt.linked_token != 0 {
                let res = scan_token_groups_for_logon(lt.linked_token);
                CloseHandle(lt.linked_token);
                if let Some(v) = res.into_iter().next() {
                    return Ok(v);
                }
            }
        }
    }

    Err(anyhow!("Logon SID not present on token"))
}

/// Copies the token user SID out of `h_token`.
///
/// # Safety
/// `h_token` must be a valid token handle opened with `TOKEN_QUERY`.
pub unsafe fn get_user_sid_bytes(h_token: HANDLE) -> Result<Vec<u8>> {
    let mut needed: u32 = 0;
    GetTokenInformation(h_token, TokenUser, std::ptr::null_mut(), 0, &mut needed);
    if needed == 0 {
        return Err(anyhow!("TokenUser size query returned 0"));
    }
    let mut user_buf: Vec<u8> = vec![0u8; needed as usize];
    let ok = GetTokenInformation(
        h_token,
        TokenUser,
        user_buf.as_mut_ptr() as *mut c_void,
        needed,
        &mut needed,
    );
    if ok == 0 || (needed as usize) < std::mem::size_of::<TOKEN_USER>() {
        return Err(anyhow!(
            "GetTokenInformation(TokenUser) failed: {}",
            GetLastError()
        ));
    }
    let token_user: TOKEN_USER = std::ptr::read_unaligned(user_buf.as_ptr() as *const TOKEN_USER);
    let sid_len = GetLengthSid(token_user.User.Sid);
    if sid_len == 0 {
        return Err(anyhow!(
            "GetLengthSid(TokenUser) failed: {}",
            GetLastError()
        ));
    }
    let mut user_sid_bytes = vec![0u8; sid_len as usize];
    if CopySid(
        sid_len,
        user_sid_bytes.as_mut_ptr() as *mut c_void,
        token_user.User.Sid,
    ) == 0
    {
        return Err(anyhow!("CopySid(TokenUser) failed: {}", GetLastError()));
    }
    Ok(user_sid_bytes)
}

unsafe fn lookup_privilege_luid(name: &str) -> Result<LUID> {
    let mut luid = LUID {
        LowPart: 0,
        HighPart: 0,
    };
    let ok = LookupPrivilegeValueW(std::ptr::null(), to_wide(name).as_ptr(), &mut luid);
    if ok == 0 {
        return Err(anyhow!("LookupPrivilegeValueW failed: {}", GetLastError()));
    }
    Ok(luid)
}

fn luid_eq(a: &LUID, b: &LUID) -> bool {
    a.LowPart == b.LowPart && a.HighPart == b.HighPart
}

/// Pure selection step of the `PrivilegesToDelete` array: every privilege the
/// base token carries EXCEPT `keep`. This replaces `DISABLE_MAX_PRIVILEGE`,
/// which merely disables privileges wholesale and is silently a no-op for
/// anything Windows re-adds; deleting by name is explicit and auditable.
fn select_privileges_to_delete(
    all: &[LUID_AND_ATTRIBUTES],
    keep: &LUID,
) -> Vec<LUID_AND_ATTRIBUTES> {
    all.iter()
        .filter(|entry| !luid_eq(&entry.Luid, keep))
        .copied()
        .collect()
}

/// Reads the full `TOKEN_PRIVILEGES` array off `h_token`.
unsafe fn token_privileges(h_token: HANDLE) -> Result<Vec<LUID_AND_ATTRIBUTES>> {
    let mut needed: u32 = 0;
    GetTokenInformation(
        h_token,
        TokenPrivileges,
        std::ptr::null_mut(),
        0,
        &mut needed,
    );
    if needed == 0 {
        return Err(anyhow!(
            "GetTokenInformation(TokenPrivileges) size query returned 0: {}",
            GetLastError()
        ));
    }
    let mut buf: Vec<u8> = vec![0u8; needed as usize];
    let ok = GetTokenInformation(
        h_token,
        TokenPrivileges,
        buf.as_mut_ptr() as *mut c_void,
        needed,
        &mut needed,
    );
    if ok == 0 || (needed as usize) < std::mem::size_of::<u32>() {
        return Err(anyhow!(
            "GetTokenInformation(TokenPrivileges) failed: {}",
            GetLastError()
        ));
    }
    // TOKEN_PRIVILEGES layout: DWORD PrivilegeCount; LUID_AND_ATTRIBUTES Privileges[].
    let count = std::ptr::read_unaligned(buf.as_ptr() as *const u32) as usize;
    let after_count = unsafe { buf.as_ptr().add(std::mem::size_of::<u32>()) } as usize;
    let align = std::mem::align_of::<LUID_AND_ATTRIBUTES>();
    let aligned = (after_count + (align - 1)) & !(align - 1);
    let privileges_ptr = aligned as *const LUID_AND_ATTRIBUTES;
    let mut out = Vec::with_capacity(count);
    for i in 0..count {
        out.push(std::ptr::read_unaligned(privileges_ptr.add(i)));
    }
    Ok(out)
}

unsafe fn enable_single_privilege(h_token: HANDLE, name: &str) -> Result<()> {
    let luid = lookup_privilege_luid(name)?;
    let mut tp: TOKEN_PRIVILEGES = std::mem::zeroed();
    tp.PrivilegeCount = 1;
    tp.Privileges[0].Luid = luid;
    tp.Privileges[0].Attributes = 0x00000002; // SE_PRIVILEGE_ENABLED
    let ok2 = AdjustTokenPrivileges(
        h_token,
        0,
        &tp,
        0,
        std::ptr::null_mut(),
        std::ptr::null_mut(),
    );
    if ok2 == 0 {
        return Err(anyhow!("AdjustTokenPrivileges failed: {}", GetLastError()));
    }
    let err = GetLastError();
    if err != 0 {
        return Err(anyhow!("AdjustTokenPrivileges error {err}"));
    }
    Ok(())
}

/// Builds the primary token the sandboxed child runs under.
///
/// # Why there is NO restricting-SID array (decision D-013)
///
/// `RestrictingSids` is deliberately NULL. Schannel client credentials are LSA
/// objects acquired over SSPI RPC into `lsass`. ANY `RestrictingSids` array on
/// the caller's token breaks that path, and there is no ACL fix available
/// because `lsass`'s internal client context is not an object anyone can grant
/// access to. Symptom when it is present:
/// `curl: (35) schannel: AcquireCredentialsHandle failed: SEC_E_NO_CREDENTIALS`,
/// so no schannel-based HTTPS client (curl, WinHTTP, .NET default TLS) can
/// complete a TLS handshake inside the sandbox.
///
/// Precedent: Anthropic's Sandbox Runtime reaches the same conclusion in
/// `src/token.rs` ("No RestrictingSids array, that breaks Schannel/LSA RPC")
/// and passes `None`. See https://github.com/anthropic-experimental/sandbox-runtime
///
/// The shape this file previously used was copied from OpenAI Codex
/// (`codex-rs/windows-sandbox-rs`), which carries the same defect and the same
/// open bug: https://github.com/openai/codex/issues/17459
///
/// An earlier attempt to keep `WRITE_RESTRICTED` and merely widen the
/// restricting set with interactive group SIDs was tried in the lab and
/// reverted: it did not fix credential acquisition and it broke write
/// containment. See VENDORING.md, "TRIED AND REVERTED".
///
/// ## What is given up
///
/// `WRITE_RESTRICTED` gave every write-type access check a SECOND pass against
/// the restricting set. That second check is gone. Containment for writes now
/// rests entirely on the dedicated low-privilege sandbox account plus NTFS
/// ACEs, so any location that is world-writable or writable by ordinary user
/// groups (`C:\Windows\Temp`, `%TEMP%`, the account's own profile, any share
/// granting `Authenticated Users` Modify) IS writable by the sandbox account.
/// The read-only permission profile is likewise no longer a token-level
/// property: it is now expressed purely by NOT granting the sandbox account an
/// allow-write ACE on the workspace root. The sandbox home is given an explicit
/// protected ACL at provisioning time (`hive_desktop_sandbox`'s
/// `sandbox_home_dacl_entries` / `apply_sandbox_home_acl`), which is the
/// enabling prerequisite for this change; arbitrary task workspaces outside
/// that tree are NOT hardened by it.
///
/// ## What still protects the host
///
/// * a dedicated, hidden, least-privilege local account (not the user's own),
/// * the kill-on-close Job Object with no breakaway,
/// * the private desktop,
/// * the WFP plus firewall egress fence, keyed on the sandbox account SID,
/// * the deny-read ACEs (the sealed credential directory),
/// * NO privileges beyond `SeChangeNotifyPrivilege`, all others deleted,
/// * `BUILTIN\Administrators` and every logon SID disabled on the token,
/// * `LUA_TOKEN`, which keeps the token at Medium integrity.
///
/// Returns the new token plus its resolved user SID (string form), so a
/// caller can report which account the child actually runs as WITHOUT
/// shelling `whoami /user` into the confined child itself. `whoami /user`
/// resolves the SID to a name via `LookupAccountSid` (LSA RPC), and D-013's
/// lab session found that call blocks indefinitely under this exact token
/// (differential-tested: same account, same fence, unrestricted token ->
/// 403ms; restricted token -> 10-20+ min, never returned). `GetTokenInformation(TokenUser)`,
/// used below, reads the SID bytes straight off the token object with no RPC
/// involved, so it cannot hit the same hang. See VENDORING.md, "known
/// limitation: LSA name lookups under the D-013 token" for the record.
///
/// # Safety
/// Caller must close the returned token handle; `base_token` must be a valid
/// primary token (the runner's own token).
pub unsafe fn create_sandbox_restricted_token_from(base_token: HANDLE) -> Result<(HANDLE, String)> {
    // SidsToDisable: Administrators plus every logon SID on the token. Disabled
    // SIDs stay present for DENY evaluation but grant nothing.
    let admins = LocalSid::from_string(BUILTIN_ADMINISTRATORS_SID)?;
    let mut logon_sid_bytes = scan_token_groups_for_logon(base_token);
    let mut sids_to_disable: Vec<SID_AND_ATTRIBUTES> =
        Vec::with_capacity(1 + logon_sid_bytes.len());
    sids_to_disable.push(SID_AND_ATTRIBUTES {
        Sid: admins.as_ptr(),
        Attributes: 0,
    });
    for bytes in logon_sid_bytes.iter_mut() {
        sids_to_disable.push(SID_AND_ATTRIBUTES {
            Sid: bytes.as_mut_ptr() as *mut c_void,
            Attributes: 0,
        });
    }

    // PrivilegesToDelete: everything except SeChangeNotifyPrivilege.
    let keep = lookup_privilege_luid(KEPT_PRIVILEGE)?;
    let privileges_to_delete = select_privileges_to_delete(&token_privileges(base_token)?, &keep);

    let mut new_token: HANDLE = 0;
    let ok = CreateRestrictedToken(
        base_token,
        LUA_TOKEN,
        sids_to_disable.len() as u32,
        sids_to_disable.as_ptr(),
        privileges_to_delete.len() as u32,
        if privileges_to_delete.is_empty() {
            std::ptr::null()
        } else {
            privileges_to_delete.as_ptr()
        },
        // RestrictingSids: NULL. See the doc comment above; this is the point.
        0,
        std::ptr::null(),
        &mut new_token,
    );
    if ok == 0 {
        return Err(anyhow!("CreateRestrictedToken failed: {}", GetLastError()));
    }

    // Permissive default DACL so the child can create its own pipes and IPC
    // objects (PowerShell pipelines hit ACCESS_DENIED without it). Addressed to
    // the token user (the sandbox account) and Everyone; the logon SID is no
    // longer usable here because it is disabled above.
    let mut user_sid_bytes = get_user_sid_bytes(base_token)?;
    let psid_user = user_sid_bytes.as_mut_ptr() as *mut c_void;
    let mut everyone = world_sid()?;
    let psid_everyone = everyone.as_mut_ptr() as *mut c_void;
    if let Err(e) = set_default_dacl(new_token, &[psid_user, psid_everyone]) {
        CloseHandle(new_token);
        return Err(e);
    }

    if let Err(e) = enable_single_privilege(new_token, KEPT_PRIVILEGE) {
        CloseHandle(new_token);
        return Err(e);
    }
    // Same bytes already resolved above for the default-DACL trustee; stringify
    // for the caller rather than repeat the GetTokenInformation(TokenUser) call.
    let sid_string = crate::winutil::string_from_sid_bytes(&user_sid_bytes)
        .map_err(|e| anyhow!("stringify sandbox token SID: {e}"))?;
    Ok((new_token, sid_string))
}

#[cfg(test)]
mod tests {
    use super::*;
    use pretty_assertions::assert_eq;

    fn luid(low: u32, high: i32) -> LUID {
        LUID {
            LowPart: low,
            HighPart: high,
        }
    }

    fn entry(low: u32, high: i32) -> LUID_AND_ATTRIBUTES {
        LUID_AND_ATTRIBUTES {
            Luid: luid(low, high),
            Attributes: 0,
        }
    }

    #[test]
    fn every_privilege_except_the_kept_one_is_deleted() {
        let keep = luid(23, 0);
        let all = [entry(19, 0), entry(23, 0), entry(25, 0)];

        let deleted = select_privileges_to_delete(&all, &keep);

        assert_eq!(deleted.len(), 2);
        assert!(deleted.iter().all(|e| !luid_eq(&e.Luid, &keep)));
    }

    #[test]
    fn high_part_participates_in_luid_identity() {
        // A LUID differing only in HighPart is a DIFFERENT privilege and must
        // still be deleted; comparing LowPart alone would silently keep it.
        let keep = luid(23, 0);
        let all = [entry(23, 1)];

        assert_eq!(select_privileges_to_delete(&all, &keep).len(), 1);
    }

    #[test]
    fn a_token_carrying_only_the_kept_privilege_deletes_nothing() {
        let keep = luid(23, 0);

        assert!(select_privileges_to_delete(&[entry(23, 0)], &keep).is_empty());
    }
}
