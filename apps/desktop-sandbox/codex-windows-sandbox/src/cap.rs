use crate::path_normalization::canonical_path_key;
use crate::path_normalization::canonicalize_path;
use anyhow::Context;
use anyhow::Result;
use rand::RngCore;
use rand::SeedableRng;
use rand::rngs::SmallRng;
use serde::Deserialize;
use serde::Serialize;
use std::collections::HashMap;
use std::fs;
use std::path::Path;
use std::path::PathBuf;
use std::sync::Mutex;

/// Serializes the cap-SID file read-modify-write so two concurrent callers that
/// both miss a per-cwd or per-write-root key cannot persist divergent SIDs and
/// hand a caller a SID that does not match what landed on disk (Integration A1,
/// VENDORING open risk 8). Each mutating accessor re-reads the file under this
/// guard before inserting.
///
// ponytail: process-wide lock, coarse (serializes across every codex_home).
// Fine for the desktop's single shared sandbox user (Q3). Cross-PROCESS races
// (the elevated setup binary racing the runner) still need a named OS mutex,
// like upstream setup_main's read_acl_mutex; that belongs to the wired
// provisioning path and is deferred to A2. Per-codex_home locks if throughput
// ever matters.
static CAP_SID_LOCK: Mutex<()> = Mutex::new(());

// A2 (VENDORING open risk 8; documented deviation from the verbatim vendor):
// the intra-process CAP_SID_LOCK above serializes threads within one process,
// but the elevated provisioning binary and the per-task runner are SEPARATE
// processes that both read-modify-write this file. A named OS mutex, keyed to
// the cap_sid path, closes that cross-process race (the same discipline
// upstream setup_main uses with its read_acl_mutex). It is held for the SAME
// critical section as CAP_SID_LOCK and released on drop.
struct CrossProcessCapLock(windows_sys::Win32::Foundation::HANDLE);

impl Drop for CrossProcessCapLock {
    fn drop(&mut self) {
        // SAFETY: `self.0` is a mutex HANDLE we acquired below; release
        // ownership then close the handle. Both are no-ops on an invalid handle.
        unsafe {
            let _ = windows_sys::Win32::System::Threading::ReleaseMutex(self.0);
            let _ = windows_sys::Win32::Foundation::CloseHandle(self.0);
        }
    }
}

/// Acquires the cross-process cap-SID mutex, blocking until it is owned. Returns
/// `None` only if the mutex object could not be created (in which case the
/// intra-process lock still provides same-process safety); a WAIT_ABANDONED
/// result still confers ownership, which is the desired behaviour here.
fn acquire_cross_process_cap_lock(codex_home: &Path) -> Option<CrossProcessCapLock> {
    use std::hash::{Hash, Hasher};
    // Mutex names cannot contain '\' except the namespace prefix, so key the
    // name on a stable hash of the canonical cap_sid path rather than the path
    // itself. DefaultHasher (SipHash with fixed keys) is deterministic across
    // processes, so both racers derive the same name.
    let key = canonical_path_key(&cap_sid_file(codex_home));
    let mut hasher = std::collections::hash_map::DefaultHasher::new();
    key.hash(&mut hasher);
    let name = format!("Local\\hive_cap_sid_{:016x}", hasher.finish());
    let wide: Vec<u16> = name.encode_utf16().chain(std::iter::once(0)).collect();
    // SAFETY: standard CreateMutexW + WaitForSingleObject. `wide` is a valid
    // NUL-terminated UTF-16 name that outlives the call; the returned handle is
    // owned by the CrossProcessCapLock guard and closed on drop.
    unsafe {
        let h =
            windows_sys::Win32::System::Threading::CreateMutexW(std::ptr::null(), 0, wide.as_ptr());
        if h == 0 {
            return None;
        }
        let _ = windows_sys::Win32::System::Threading::WaitForSingleObject(h, u32::MAX);
        Some(CrossProcessCapLock(h))
    }
}

#[derive(Serialize, Deserialize, Clone, Debug)]
pub struct CapSids {
    pub workspace: String,
    pub readonly: String,
    /// Per-workspace capability SIDs keyed by canonicalized CWD string.
    ///
    /// This is used to isolate workspaces from other workspace sandbox writes and to
    /// apply per-workspace denies (e.g. protect `CWD/.codex`)
    /// without permanently affecting other workspaces.
    #[serde(default)]
    pub workspace_by_cwd: HashMap<String, String>,
    /// Per-write-root capability SIDs keyed by canonicalized write-root path.
    ///
    /// These are included in a workspace-write token only when the root is
    /// currently allowed, so stale ACLs from earlier extra roots do not expand
    /// later workspace sandboxes.
    #[serde(default)]
    pub writable_root_by_path: HashMap<String, String>,
}

pub fn cap_sid_file(codex_home: &Path) -> PathBuf {
    codex_home.join("cap_sid")
}

fn make_random_cap_sid_string() -> String {
    let mut rng = SmallRng::from_entropy();
    let a = rng.next_u32();
    let b = rng.next_u32();
    let c = rng.next_u32();
    let d = rng.next_u32();
    format!("S-1-5-21-{a}-{b}-{c}-{d}")
}

fn persist_caps(path: &Path, caps: &CapSids) -> Result<()> {
    if let Some(dir) = path.parent() {
        fs::create_dir_all(dir).with_context(|| format!("create cap sid dir {}", dir.display()))?;
    }
    let json = serde_json::to_string(caps)?;
    fs::write(path, json).with_context(|| format!("write cap sid file {}", path.display()))?;
    Ok(())
}

pub fn load_or_create_cap_sids(codex_home: &Path) -> Result<CapSids> {
    let _guard = CAP_SID_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    // Hold the cross-process mutex for the same critical section (A2, see above).
    let _xproc = acquire_cross_process_cap_lock(codex_home);
    load_or_create_cap_sids_locked(&cap_sid_file(codex_home))
}

/// Read-or-create the cap-SID file. Callers MUST hold [`CAP_SID_LOCK`]; the
/// public [`load_or_create_cap_sids`] and the per-key accessors acquire it
/// before calling here so the whole read-modify-write is atomic within the
/// process.
fn load_or_create_cap_sids_locked(path: &Path) -> Result<CapSids> {
    if path.exists() {
        let txt = fs::read_to_string(path)
            .with_context(|| format!("read cap sid file {}", path.display()))?;
        let t = txt.trim();
        if t.starts_with('{') && t.ends_with('}') {
            if let Ok(obj) = serde_json::from_str::<CapSids>(t) {
                return Ok(obj);
            }
        } else if !t.is_empty() {
            let caps = CapSids {
                workspace: t.to_string(),
                readonly: make_random_cap_sid_string(),
                workspace_by_cwd: HashMap::new(),
                writable_root_by_path: HashMap::new(),
            };
            persist_caps(path, &caps)?;
            return Ok(caps);
        }
    }
    let caps = CapSids {
        workspace: make_random_cap_sid_string(),
        readonly: make_random_cap_sid_string(),
        workspace_by_cwd: HashMap::new(),
        writable_root_by_path: HashMap::new(),
    };
    persist_caps(path, &caps)?;
    Ok(caps)
}

/// Returns the workspace-specific capability SID for `cwd`, creating and persisting it if missing.
pub fn workspace_cap_sid_for_cwd(codex_home: &Path, cwd: &Path) -> Result<String> {
    let path = cap_sid_file(codex_home);
    let _guard = CAP_SID_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    // Hold the cross-process mutex for the same critical section (A2, see above).
    let _xproc = acquire_cross_process_cap_lock(codex_home);
    let mut caps = load_or_create_cap_sids_locked(&path)?;
    let key = canonical_path_key(cwd);
    if let Some(sid) = caps.workspace_by_cwd.get(&key) {
        return Ok(sid.clone());
    }
    let sid = make_random_cap_sid_string();
    caps.workspace_by_cwd.insert(key, sid.clone());
    persist_caps(&path, &caps)?;
    Ok(sid)
}

/// Returns the capability SID for an additional writable root, creating and persisting it if missing.
pub fn writable_root_cap_sid_for_path(codex_home: &Path, root: &Path) -> Result<String> {
    let path = cap_sid_file(codex_home);
    let _guard = CAP_SID_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    // Hold the cross-process mutex for the same critical section (A2, see above).
    let _xproc = acquire_cross_process_cap_lock(codex_home);
    let mut caps = load_or_create_cap_sids_locked(&path)?;
    let key = canonical_path_key(root);
    if let Some(sid) = caps.writable_root_by_path.get(&key) {
        return Ok(sid.clone());
    }
    let sid = make_random_cap_sid_string();
    caps.writable_root_by_path.insert(key, sid.clone());
    persist_caps(&path, &caps)?;
    Ok(sid)
}

pub fn workspace_write_cap_sid_for_root(
    codex_home: &Path,
    cwd: &Path,
    root: &Path,
) -> Result<String> {
    if canonical_path_key(root) == canonical_path_key(cwd) {
        workspace_cap_sid_for_cwd(codex_home, cwd)
    } else {
        writable_root_cap_sid_for_path(codex_home, root)
    }
}

pub fn workspace_write_root_contains_path(root: &Path, path: &Path) -> bool {
    canonicalize_path(path).starts_with(canonicalize_path(root))
}

pub fn workspace_write_root_overlaps_path(root: &Path, path: &Path) -> bool {
    workspace_write_root_contains_path(root, path) || workspace_write_root_contains_path(path, root)
}

pub fn workspace_write_root_specificity(root: &Path) -> usize {
    canonicalize_path(root).components().count()
}

#[cfg(test)]
mod tests {
    use super::load_or_create_cap_sids;
    use super::workspace_cap_sid_for_cwd;
    use super::workspace_write_cap_sid_for_root;
    use super::writable_root_cap_sid_for_path;
    use pretty_assertions::assert_eq;
    use std::path::PathBuf;

    #[test]
    fn equivalent_cwd_spellings_share_workspace_sid_key() {
        let temp = tempfile::tempdir().expect("tempdir");
        let codex_home = temp.path().join("codex-home");
        std::fs::create_dir_all(&codex_home).expect("create codex home");

        let workspace = temp.path().join("WorkspaceRoot");
        std::fs::create_dir_all(&workspace).expect("create workspace root");

        let canonical = dunce::canonicalize(&workspace).expect("canonical workspace root");
        let alt_spelling = PathBuf::from(
            canonical
                .to_string_lossy()
                .replace('\\', "/")
                .to_ascii_uppercase(),
        );

        let first_sid =
            workspace_cap_sid_for_cwd(&codex_home, canonical.as_path()).expect("first sid");
        let second_sid =
            workspace_cap_sid_for_cwd(&codex_home, alt_spelling.as_path()).expect("second sid");

        assert_eq!(first_sid, second_sid);

        let caps = load_or_create_cap_sids(&codex_home).expect("load caps");
        assert_eq!(caps.workspace_by_cwd.len(), 1);
    }

    #[test]
    fn write_roots_get_path_scoped_sids() {
        let temp = tempfile::tempdir().expect("tempdir");
        let codex_home = temp.path().join("codex-home");
        std::fs::create_dir_all(&codex_home).expect("create codex home");

        let workspace = temp.path().join("workspace");
        let extra_root = temp.path().join("extra-root");
        std::fs::create_dir_all(&workspace).expect("create workspace");
        std::fs::create_dir_all(&extra_root).expect("create extra root");

        let workspace_sid = workspace_write_cap_sid_for_root(&codex_home, &workspace, &workspace)
            .expect("workspace sid");
        let extra_sid = workspace_write_cap_sid_for_root(&codex_home, &workspace, &extra_root)
            .expect("extra root sid");

        assert_ne!(workspace_sid, extra_sid);
        assert_eq!(
            extra_sid,
            writable_root_cap_sid_for_path(&codex_home, &extra_root).expect("extra root sid again")
        );

        let caps = load_or_create_cap_sids(&codex_home).expect("load caps");
        assert_eq!(caps.workspace_by_cwd.len(), 1);
        assert_eq!(caps.writable_root_by_path.len(), 1);
    }

    #[test]
    fn concurrent_first_touch_of_same_cwd_yields_one_stable_sid() {
        use std::sync::Arc;
        use std::thread;

        let temp = tempfile::tempdir().expect("tempdir");
        let codex_home = Arc::new(temp.path().join("codex-home"));
        std::fs::create_dir_all(codex_home.as_path()).expect("create codex home");
        let cwd = Arc::new(temp.path().join("workspace"));
        std::fs::create_dir_all(cwd.as_path()).expect("create workspace");

        let handles: Vec<_> = (0..8)
            .map(|_| {
                let codex_home = Arc::clone(&codex_home);
                let cwd = Arc::clone(&cwd);
                thread::spawn(move || workspace_cap_sid_for_cwd(&codex_home, &cwd).expect("sid"))
            })
            .collect();
        let sids: Vec<String> = handles
            .into_iter()
            .map(|handle| handle.join().expect("join"))
            .collect();

        let first = &sids[0];
        assert!(
            sids.iter().all(|sid| sid == first),
            "all concurrent callers must observe the same workspace SID: {sids:?}"
        );
        let caps = load_or_create_cap_sids(&codex_home).expect("load caps");
        assert_eq!(
            caps.workspace_by_cwd.len(),
            1,
            "exactly one workspace SID must be persisted after a concurrent first touch"
        );
    }
}
