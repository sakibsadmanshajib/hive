//! Build script for the desktop-sandbox crate.
//!
//! Its only job today: delay-load `user32.dll` for the `hive-command-runner`
//! binary on the MSVC target, as defense-in-depth around the runner's user32
//! desktop attach.
//!
//! Why user32 attach matters: `hive-command-runner` statically imports
//! `user32.dll` (the private desktop calls: `CreateDesktopW` builds the child's
//! per-launch private desktop). The Windows loader runs user32's process-attach
//! `DllMain` BEFORE `main()`, and that attach connects the process to a window
//! station + desktop. For the low-privilege sandbox account launched via
//! `CreateProcessWithLogonW`, that connect only succeeds once the account has
//! been granted access to `WinSta0` and `WinSta0\Default`. The actual fix for
//! the desktop-attach failure is `desktop::grant_winsta_desktop_access` granting
//! the sandbox account SID on the REAL `WinSta0\Default` desktop, NOT this
//! delay-load.
//!
//! What the delay-load buys, given that grant is in place: it defers user32's
//! load (and its DllMain attach) until the runner's FIRST user32 call. By then
//! `main()` has run, the IPC handshake has completed, and the runner is on
//! `WinSta0` where `grant_winsta_desktop_access` has granted it access, so the
//! now-deferred attach succeeds when the runner first calls `CreateDesktopW` to
//! build the child's private desktop. It is retained as cheap insurance: keeping
//! the linkage means a future regression in the grant surfaces as a diagnosable
//! first-call `0xC06D007E` rather than an uncatchable `STATUS_DLL_INIT_FAILED`
//! (0xC0000142) before `main`. Lab-proven on spike307-win: before the grant fix,
//! a marker on the first line of `main` never fired.
//!
//! MSVC only: `/DELAYLOAD` and `delayimp.lib` are MSVC-linker features, and the
//! product ships the MSVC target. The `x86_64-pc-windows-gnu` cross-compile that
//! CI type-checks uses a different toolchain and is intentionally left untouched
//! (it never runs, so its loader behaviour does not matter).
fn main() {
    println!("cargo:rerun-if-changed=build.rs");

    let target_os = std::env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
    let target_env = std::env::var("CARGO_CFG_TARGET_ENV").unwrap_or_default();
    if target_os == "windows" && target_env == "msvc" {
        // Scoped to the runner bin only; other bins/libs are unaffected.
        println!("cargo:rustc-link-arg-bin=hive-command-runner=/DELAYLOAD:user32.dll");
        // The delay-load thunks call __delayLoadHelper2 from delayimp.lib.
        println!("cargo:rustc-link-arg-bin=hive-command-runner=delayimp.lib");
    }
}
