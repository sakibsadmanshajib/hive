//! End-to-end regression guard for the desktop sandbox's post-fork
//! allocator-deadlock hazard (issue #342 item 1, VENDORING.md open risk #7).
//!
//! `hive_desktop_sandbox::launch` confines the child via a `pre_exec` closure
//! that runs after `fork()` and before `execve()`. That closure MUST be
//! async-signal-safe: allocating in a `fork()`ed child of a multithreaded
//! process can deadlock on the allocator lock, and std makes the parent's
//! `spawn()` block until the child execs or reports an error, so the whole
//! process hangs. This test drives `launch` all the way through that closure
//! from a multithreaded test harness and asserts it RETURNS (Ok or Err)
//! instead of hanging. If a future change reintroduces an allocation in the
//! closure, this test times out.
//!
//! It lives in its own integration binary on purpose: the crate's in-module
//! `landlock_hard_requirement_never_silently_no_ops` test calls
//! `restrict_self` on its own test-pool thread (a one-way confinement), and
//! reusing a Landlock-confined thread for the real filesystem I/O this test
//! needs would break it. A separate binary never runs `restrict_self`, so its
//! threads stay unconfined.

#![cfg(target_os = "linux")]

use hive_desktop_sandbox::{NetworkPolicy, SandboxPolicy, launch};
use std::path::PathBuf;

#[test]
fn launch_returns_without_hanging_on_alloc_free_pre_exec() {
    let workspace = tempfile::tempdir().expect("workspace tempdir");
    let hooks = tempfile::tempdir().expect("hooks tempdir");

    // Readonly `/` keeps the dynamic loader and /bin/true readable and
    // executable inside the ruleset; the workspace is the one writable root;
    // hooks is the always-ro-bound hook/config dir the policy requires.
    let policy = SandboxPolicy::build(
        vec![workspace.path().to_path_buf()],
        vec![PathBuf::from("/")],
        hooks.path().to_path_buf(),
        NetworkPolicy::DenyAll,
    )
    .expect("valid policy");

    // Point the bwrap lookup at /bin/true: the child applies Landlock +
    // seccomp in pre_exec, then execs a real always-present binary. No actual
    // bwrap needed -- this test is about the fork/pre_exec plumbing, not
    // bubblewrap.
    //
    // SAFETY: this binary holds a single test, so nothing else mutates or
    // reads the environment concurrently; `launch` reads HIVE_BWRAP_PATH
    // synchronously on this thread before the variable is restored.
    unsafe {
        std::env::set_var("HIVE_BWRAP_PATH", "/bin/true");
    }
    let result = launch(&policy, &["ignored-by-true".to_string()], workspace.path());
    unsafe {
        std::env::remove_var("HIVE_BWRAP_PATH");
    }

    match result {
        Ok(mut child) => {
            // The alloc-free closure ran in the forked child and the spawn
            // resolved. Reap it rather than leaking a zombie; its exit status
            // is not asserted (confinement and exec specifics vary by kernel
            // and environment). The guard is purely "launch returned".
            let _ = child.wait();
        }
        Err(_) => {
            // Acceptable: a kernel without Landlock v5 (CompatLevel::
            // HardRequirement) fails closed before the fork. Still no hang,
            // which is the whole point of this guard.
        }
    }
}
