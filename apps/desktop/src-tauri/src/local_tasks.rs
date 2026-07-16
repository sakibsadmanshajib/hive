//! Desktop-local task execution (blueprint Step 4.4, issue #311, decision
//! D8: "desktop-LOCAL tasks stay OFF cloud sync by default").
//!
//! A local task never calls control-plane or edge-api: this module has no
//! HTTP client dependency at all (no `reqwest` import anywhere below), and
//! [`LocalTaskStore`]'s only collaborator is a [`SandboxLauncher`]. That is
//! the actual guarantee behind "a desktop-local task never appears in the
//! cloud task list" -- there is no code path connecting the two, not just
//! a runtime check that happens not to fire.
//!
//! Network policy for a local task's sandbox fails closed to
//! [`hive_desktop_sandbox::NetworkPolicy::DenyAll`] today
//! (`resolve_local_network_policy`): the `AllowHosts` variant needs the
//! caller's own effective egress policy from control-plane's SSOT
//! (#308/#319), and there is currently no channel for this process to
//! authenticate that fetch -- the same "no session handed back to Rust"
//! gap `entitlements.rs`'s module doc already documents for the
//! feature-gate fetch. Wiring that is follow-up work; until then, DenyAll
//! is the only value this function can honestly return.
//!
//! Execution itself is a placeholder today: `create_local_task` launches a
//! trivial confined command (`echo`) to prove the sandbox wiring end to
//! end, not a real agent run -- that needs the same kind of Engine seam
//! `apps/control-plane/internal/agenttask/SYNC_CONTRACT.md` documents
//! server-side, ported to a local runtime. No completion tracking either:
//! `status` reflects only whether the sandboxed process was spawned, not
//! whether it finished (see `SandboxLauncher::launch`'s doc comment).

use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc;
use std::sync::Mutex;
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use hive_desktop_sandbox::{NetworkPolicy, SandboxPolicy};
use serde::Serialize;

/// Placeholder confined command a local task actually runs today. See the
/// module doc's "Execution itself is a placeholder" paragraph.
const PLACEHOLDER_COMMAND: &[&str] = &["/bin/echo", "hive-local-task"];

/// `Started`, not `Running`: this only ever reflects whether the sandboxed
/// process was successfully spawned, never whether it is still executing
/// (see the module doc's "No completion tracking" note). The placeholder
/// command this crate launches today typically exits almost immediately,
/// so a status literally named "Running" would misrepresent a task that
/// finished seconds ago as still in progress.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum LocalTaskStatus {
    Started,
    Failed,
}

#[derive(Debug, Clone, Serialize)]
pub struct LocalTask {
    pub id: String,
    pub pack: String,
    pub instructions: String,
    pub status: LocalTaskStatus,
    pub created_at: String,
}

/// Narrow port `LocalTaskStore` launches through. Production wires
/// [`RealLauncher`] (a thin wrapper over `hive_desktop_sandbox::launch`);
/// tests wire a fake that records calls instead of spawning a real
/// process, so `create_local_task`'s behaviour is provable without a bwrap
/// binary present in the test environment (mirrors the
/// `StatusChecker`/`Engine` interface seam
/// `apps/control-plane/internal/agenttask` uses server-side).
pub trait SandboxLauncher: Send + Sync {
    /// Attempts to launch `command` confined by `policy`. `Ok(())` means the
    /// process was spawned (fire-and-forget: this trait does not report
    /// completion, only spawn success -- see the module doc's "No
    /// completion tracking" note).
    fn launch(&self, policy: &SandboxPolicy, command: &[String], cwd: &Path) -> Result<(), String>;
}

/// Timeout guard for the real OS-level spawn (VENDORING.md open risk #7,
/// `apps/desktop-sandbox`): `hive_desktop_sandbox::launch`'s `pre_exec`
/// closure allocates inside a raw-`fork()`ed child of this (multithreaded)
/// process, a known post-fork allocator-deadlock hazard. That hazard hung
/// `cargo test` for 15+ minutes when first exercised end to end (see that
/// crate's `linux.rs` test module comment) -- this is the same call, now on
/// a live path (every "Run a task locally" click). This is a containment
/// guard, not the fix: it stops one stuck spawn from hanging the whole
/// app, it does not make `pre_exec` alloc-free. The real fix (pre-allocate
/// before fork, or move off fork+pre_exec) is tracked as a follow-up, not
/// attempted here.
const SPAWN_TIMEOUT: Duration = Duration::from_secs(10);

/// Something `spawn_with_timeout` can kill if the underlying spawn call
/// resolves after the caller already gave up on it. Generic so tests can
/// exercise the timeout path without a real OS process.
pub trait Killable {
    fn kill_best_effort(&mut self);
}

impl Killable for std::process::Child {
    fn kill_best_effort(&mut self) {
        let _ = self.kill();
    }
}

/// Runs `spawn` (the actual OS-level launch attempt) on a dedicated thread
/// and waits up to `timeout`. On timeout, returns an error immediately
/// instead of blocking the caller -- a second, detached thread keeps
/// waiting for `spawn` to finish and kills whatever it eventually produces,
/// since the caller has already given up on it and nothing else will.
fn spawn_with_timeout<K, F>(timeout: Duration, spawn: F) -> Result<(), String>
where
    K: Killable + Send + 'static,
    F: FnOnce() -> Result<K, String> + Send + 'static,
{
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(spawn());
    });

    match rx.recv_timeout(timeout) {
        Ok(Ok(_child)) => Ok(()),
        Ok(Err(e)) => Err(e),
        Err(_) => {
            thread::spawn(move || {
                if let Ok(Ok(mut child)) = rx.recv() {
                    child.kill_best_effort();
                }
            });
            Err("sandbox launch timed out".to_string())
        }
    }
}

pub struct RealLauncher;

impl SandboxLauncher for RealLauncher {
    fn launch(&self, policy: &SandboxPolicy, command: &[String], cwd: &Path) -> Result<(), String> {
        let policy = policy.clone();
        let command = command.to_vec();
        let cwd = cwd.to_path_buf();
        spawn_with_timeout(SPAWN_TIMEOUT, move || {
            hive_desktop_sandbox::launch(&policy, &command, &cwd).map_err(|e| e.to_string())
        })
    }
}

/// Always `DenyAll` today. See the module doc's network-policy paragraph
/// for why: this is the fail-closed seam a future authenticated fetch of
/// the caller's own effective egress policy (#308/#319) replaces.
fn resolve_local_network_policy() -> NetworkPolicy {
    NetworkPolicy::DenyAll
}

pub struct LocalTaskStore {
    launcher: Box<dyn SandboxLauncher>,
    hook_config_dir: PathBuf,
    tasks: Mutex<Vec<LocalTask>>,
    next_id: AtomicU64,
}

impl LocalTaskStore {
    pub fn new(launcher: Box<dyn SandboxLauncher>, hook_config_dir: PathBuf) -> Self {
        Self {
            launcher,
            hook_config_dir,
            tasks: Mutex::new(Vec::new()),
            next_id: AtomicU64::new(1),
        }
    }

    /// PID plus an in-memory counter alone can repeat across an app
    /// restart (PIDs get reused by the OS, and the counter always restarts
    /// at 1), which would make a stale, never-cleaned-up workspace
    /// directory from a previous run (see the module doc's leaked-resource
    /// notes elsewhere in this crate) collide with a new task's id.
    /// Folding in a nanosecond timestamp makes that collision practically
    /// impossible; `local_workspace_dir` also refuses to reuse an existing
    /// directory outright as a second, independent guard.
    pub fn next_task_id(&self) -> String {
        let n = self.next_id.fetch_add(1, Ordering::SeqCst);
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or(0);
        format!("local-{}-{nanos}-{n}", std::process::id())
    }

    /// `id` is generated by the caller (via [`Self::next_task_id`]) up
    /// front, before this call, because the caller (`create_local_task`)
    /// needs the same id to name the task's workspace directory. Generating
    /// it here instead would mint a second, different id for the same
    /// task.
    fn create(
        &self,
        id: String,
        pack: String,
        instructions: String,
        workspace: &Path,
    ) -> LocalTask {
        let policy = SandboxPolicy::build(
            vec![workspace.to_path_buf()],
            vec![],
            self.hook_config_dir.clone(),
            resolve_local_network_policy(),
        );

        let status = match policy {
            Ok(policy) => {
                let command: Vec<String> =
                    PLACEHOLDER_COMMAND.iter().map(|s| s.to_string()).collect();
                match self.launcher.launch(&policy, &command, workspace) {
                    Ok(()) => LocalTaskStatus::Started,
                    Err(_) => LocalTaskStatus::Failed,
                }
            }
            Err(_) => LocalTaskStatus::Failed,
        };

        let task = LocalTask {
            id,
            pack,
            instructions,
            status,
            created_at: now_rfc3339_ish(),
        };
        self.tasks
            .lock()
            .expect("local task store mutex poisoned")
            .push(task.clone());
        task
    }

    fn list(&self) -> Vec<LocalTask> {
        self.tasks
            .lock()
            .expect("local task store mutex poisoned")
            .clone()
    }
}

/// Not a real RFC3339 formatter (no chrono/time dependency added for one
/// timestamp field) -- seconds since the Unix epoch, which is sortable and
/// unambiguous, is enough for a local-only, non-synced task list.
fn now_rfc3339_ish() -> String {
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    secs.to_string()
}

#[tauri::command]
pub fn create_local_task(
    state: tauri::State<LocalTaskStore>,
    app: tauri::AppHandle,
    pack: String,
    instructions: String,
) -> Result<LocalTask, String> {
    let id = state.next_task_id();
    let workspace = local_workspace_dir(&app, &id)?;
    Ok(state.create(id, pack, instructions, &workspace))
}

#[tauri::command]
pub fn list_local_tasks(state: tauri::State<LocalTaskStore>) -> Vec<LocalTask> {
    state.list()
}

fn local_workspace_dir(app: &tauri::AppHandle, task_id: &str) -> Result<PathBuf, String> {
    use tauri::Manager;
    let tasks_root = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cannot resolve app data directory: {e}"))?
        .join("local-tasks");
    std::fs::create_dir_all(&tasks_root)
        .map_err(|e| format!("cannot create local-tasks directory: {e}"))?;

    let base = tasks_root.join(task_id);
    // Strict create_dir (not create_dir_all) for the per-task leaf: a
    // second guard, alongside next_task_id's nanosecond component, against
    // ever silently reusing another task's workspace and exposing its
    // leftover files to a new one.
    std::fs::create_dir(&base).map_err(|e| {
        if e.kind() == std::io::ErrorKind::AlreadyExists {
            format!("task workspace {task_id} already exists")
        } else {
            format!("cannot create task workspace: {e}")
        }
    })?;
    Ok(base)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::AtomicUsize;
    use std::sync::Arc;

    struct FakeLauncher {
        calls: Arc<AtomicUsize>,
        fail: bool,
    }

    impl SandboxLauncher for FakeLauncher {
        fn launch(
            &self,
            _policy: &SandboxPolicy,
            _command: &[String],
            _cwd: &Path,
        ) -> Result<(), String> {
            self.calls.fetch_add(1, Ordering::SeqCst);
            if self.fail {
                Err("fake launch failure".to_string())
            } else {
                Ok(())
            }
        }
    }

    fn temp_dir(label: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "hive-local-tasks-test-{label}-{}-{}",
            std::process::id(),
            label.len()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }

    #[test]
    fn create_returns_started_when_launch_succeeds() {
        let calls = Arc::new(AtomicUsize::new(0));
        let store = LocalTaskStore::new(
            Box::new(FakeLauncher {
                calls: Arc::clone(&calls),
                fail: false,
            }),
            temp_dir("hooks-ok"),
        );

        let workspace = temp_dir("workspace-ok");
        let id = store.next_task_id();
        let task = store.create(
            id,
            "coding-pack".to_string(),
            "do the thing".to_string(),
            &workspace,
        );

        assert_eq!(task.status, LocalTaskStatus::Started);
        assert_eq!(task.pack, "coding-pack");
        assert_eq!(task.instructions, "do the thing");
        assert_eq!(calls.load(Ordering::SeqCst), 1);
    }

    #[test]
    fn create_returns_failed_when_launch_errors() {
        let calls = Arc::new(AtomicUsize::new(0));
        let store = LocalTaskStore::new(
            Box::new(FakeLauncher {
                calls: Arc::clone(&calls),
                fail: true,
            }),
            temp_dir("hooks-fail"),
        );

        let workspace = temp_dir("workspace-fail");
        let id = store.next_task_id();
        let task = store.create(id, "coding-pack".to_string(), String::new(), &workspace);

        assert_eq!(task.status, LocalTaskStatus::Failed);
        assert_eq!(calls.load(Ordering::SeqCst), 1);
    }

    #[test]
    fn list_returns_every_created_task_in_creation_order() {
        let calls = Arc::new(AtomicUsize::new(0));
        let store = LocalTaskStore::new(
            Box::new(FakeLauncher {
                calls: Arc::clone(&calls),
                fail: false,
            }),
            temp_dir("hooks-list"),
        );
        let workspace = temp_dir("workspace-list");

        let id1 = store.next_task_id();
        store.create(
            id1,
            "coding-pack".to_string(),
            "first".to_string(),
            &workspace,
        );
        let id2 = store.next_task_id();
        store.create(
            id2,
            "knowledge-work-pack".to_string(),
            "second".to_string(),
            &workspace,
        );

        let tasks = store.list();
        assert_eq!(tasks.len(), 2);
        assert_eq!(tasks[0].instructions, "first");
        assert_eq!(tasks[1].instructions, "second");
        assert_ne!(tasks[0].id, tasks[1].id, "each task must get a distinct id");
    }

    #[test]
    fn list_is_empty_before_any_task_is_created() {
        let store = LocalTaskStore::new(
            Box::new(FakeLauncher {
                calls: Arc::new(AtomicUsize::new(0)),
                fail: false,
            }),
            temp_dir("hooks-empty"),
        );
        assert!(store.list().is_empty());
    }

    #[test]
    fn resolve_local_network_policy_fails_closed_to_deny_all() {
        // Hard requirement (see module doc): until an authenticated
        // per-user egress-policy fetch is wired, this must never resolve
        // to AllowHosts.
        assert_eq!(resolve_local_network_policy(), NetworkPolicy::DenyAll);
    }

    struct FakeChild {
        killed: Arc<std::sync::atomic::AtomicBool>,
    }

    impl Killable for FakeChild {
        fn kill_best_effort(&mut self) {
            self.killed.store(true, Ordering::SeqCst);
        }
    }

    #[test]
    fn spawn_with_timeout_returns_promptly_when_spawn_is_fast() {
        let result: Result<(), String> = spawn_with_timeout(Duration::from_secs(10), || {
            Ok(FakeChild {
                killed: Arc::new(std::sync::atomic::AtomicBool::new(false)),
            })
        });
        assert_eq!(result, Ok(()));
    }

    #[test]
    fn spawn_with_timeout_propagates_a_fast_error() {
        let result: Result<(), String> =
            spawn_with_timeout::<FakeChild, _>(Duration::from_secs(10), || Err("boom".to_string()));
        assert_eq!(result, Err("boom".to_string()));
    }

    #[test]
    fn spawn_with_timeout_returns_error_without_waiting_for_a_hung_spawn() {
        let killed = Arc::new(std::sync::atomic::AtomicBool::new(false));
        let killed_for_spawn = Arc::clone(&killed);
        let started = std::time::Instant::now();

        let result: Result<(), String> = spawn_with_timeout(Duration::from_millis(50), move || {
            // Simulates the real launch()/pre_exec hang this guard exists
            // for: a spawn attempt that never returns within the timeout.
            thread::sleep(Duration::from_secs(2));
            Ok(FakeChild {
                killed: killed_for_spawn,
            })
        });

        assert_eq!(result, Err("sandbox launch timed out".to_string()));
        assert!(
            started.elapsed() < Duration::from_secs(1),
            "must return promptly on timeout, not block until the hung spawn resolves"
        );

        // The hung "spawn" finishes ~2s in; give the detached watcher thread
        // time to receive it and kill it, since the caller already gave up.
        thread::sleep(Duration::from_millis(2200));
        assert!(
            killed.load(Ordering::SeqCst),
            "a spawn result that arrives after the caller gave up must be killed"
        );
    }
}
