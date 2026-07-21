//! Hive sandbox confined-spawn validation harness (Step 3 Integration A, D-004).
//!
//! Lab-only. Drives the lab-only
//! [`hive_desktop_sandbox::windows_elevated::spawn_confined_for_validation`]
//! entry point on `spike307-win` so the filesystem / user / token isolation
//! matrix can be PROVEN on a real MSVC Windows host (CI only cross-compiles
//! this crate for `x86_64-pc-windows-gnu`; it never runs the Win32 paths).
//!
//! It launches a probe command AS the low-privilege sandbox account under a
//! capability-restricted token and reports, with raw evidence (never a bare
//! "pass"):
//!   (a) the child token user SID (must equal the `hive_sandbox` account SID),
//!   (b) a write OUTSIDE the writable root is denied,
//!   (c) an inside-workspace write matches the token derivation (see below),
//!   (d) reading a seeded secret under the deny-read secrets dir is denied.
//!
//! Two token derivations are exercised, one per run, because the whole egress
//! fence rests on the sandbox account staying the token USER (never demoted to
//! deny-only) in EACH. `windows_elevated` sets `permission_profile.read_only =
//! !uses_write_capabilities()`, so the writable-root set selects the derivation:
//!   * DenyAll run  (HIVE_VALIDATE_NET unset/"deny"): empty writable_roots ->
//!     read-only token; an inside-workspace write must be DENIED.
//!   * AllowHosts run ("allow:host,..."): the workspace is a writable root ->
//!     workspace-write token; the inside write must SUCCEED.
//!
//! Assertions (a) and (d) must hold in BOTH derivations. For the DenyAll /
//! read-only run the operator must NOT pre-grant the sandbox account write on
//! the workspace (else (c) cannot observe the denial); a fresh `ws` suffices.
//!
//! This is a peer of `hive-sandbox-provision`: a `src/bin` target so it lands
//! in `target/<profile>/` next to `hive-command-runner.exe`, which is what the
//! helper-resolution sibling lookup (`helper_materialization::find_runner_exe`)
//! needs to locate the runner. Run AFTER `hive-sandbox-provision` has created
//! the account. It performs NO privileged provisioning itself.
//!
//! Configuration (environment):
//!   HIVE_SANDBOX_HOME  required, same sandbox home passed to provisioning.
//!   HIVE_VALIDATE_WS   optional writable-root dir; default `<home>\ws`. Must be
//!                      granted write to the sandbox account by the operator
//!                      (icacls) before running; it is the designated workspace.

#[cfg(windows)]
fn main() -> std::process::ExitCode {
    use std::collections::HashMap;
    use std::path::PathBuf;

    let sandbox_home = match std::env::var_os("HIVE_SANDBOX_HOME") {
        Some(v) => PathBuf::from(v),
        None => {
            eprintln!("HIVE_SANDBOX_HOME must be set");
            return std::process::ExitCode::from(2);
        }
    };
    let ws = std::env::var_os("HIVE_VALIDATE_WS")
        .map(PathBuf::from)
        .unwrap_or_else(|| sandbox_home.join("ws"));

    // Greptile finding, PR #401: `sandbox_home`/`ws` flow unescaped into a
    // `cmd.exe /c` script below (see the comment on `script` for why proper
    // escaping is not attempted). This tool is lab-only and its env vars are
    // operator-supplied, not attacker input, but fail closed rather than
    // silently building a script whose structure a stray metacharacter can
    // rewrite. Reject up front instead of trying to escape.
    for (label, p) in [
        ("HIVE_SANDBOX_HOME", &sandbox_home),
        ("HIVE_VALIDATE_WS", &ws),
    ] {
        let s = p.to_string_lossy();
        if s.chars()
            .any(|c| c.is_whitespace() || "&|<>^%\"".contains(c))
        {
            eprintln!(
                "{label} must not contain whitespace or cmd.exe metacharacters (& | < > ^ % \"): {}",
                p.display()
            );
            return std::process::ExitCode::from(2);
        }
    }

    // Network policy under test (Integration B2 matrix, plan §5 rows):
    //   unset / "deny"       -> NetworkPolicy::DenyAll (rows 1-4 and row 7: all
    //                           egress and DNS blocked, only the loopback proxy
    //                           port reachable and it has no proxy behind it;
    //                           the probes below run in this branch too, so the
    //                           block is proven live rather than config-only);
    //   "allow:host1,host2"  -> NetworkPolicy::AllowHosts([..]) (rows 5-6: the
    //                           allowed host is reachable via the proxy, a
    //                           non-allowed host and any direct connect are not).
    let net_spec = std::env::var("HIVE_VALIDATE_NET").unwrap_or_else(|_| "deny".to_string());
    let allow_hosts: Vec<String> = match net_spec.strip_prefix("allow:") {
        Some(rest) => rest
            .split(',')
            .map(|h| h.trim().to_string())
            .filter(|h| !h.is_empty())
            .collect(),
        None => Vec::new(),
    };
    // Same cmd.exe metacharacter guard as the paths above: allowed hosts flow
    // into the probe script, so reject anything that could rewrite it (extend
    // the guard to host strings too, not just paths).
    for host in &allow_hosts {
        if host
            .chars()
            .any(|c| c.is_whitespace() || "&|<>^%\"/".contains(c))
        {
            eprintln!(
                "HIVE_VALIDATE_NET host must not contain whitespace or cmd.exe metacharacters: {host}"
            );
            return std::process::ExitCode::from(2);
        }
    }
    let network = if allow_hosts.is_empty() {
        hive_desktop_sandbox::NetworkPolicy::DenyAll
    } else {
        hive_desktop_sandbox::NetworkPolicy::AllowHosts(allow_hosts.clone())
    };
    // Token derivation follows the network run (see the module doc): the DenyAll
    // run exercises the read-only derivation (empty writable_roots), the
    // AllowHosts run the workspace-write derivation (workspace is writable).
    let workspace_write = !allow_hosts.is_empty();

    if let Err(e) = std::fs::create_dir_all(&ws) {
        eprintln!("create workspace {}: {e}", ws.display());
        return std::process::ExitCode::FAILURE;
    }

    // Expected sandbox account SID, resolved independently of the child so the
    // (a) assertion compares two independently-obtained values.
    let expected_sid = match codex_windows_sandbox::sandbox_users::resolve_sid(
        hive_desktop_sandbox::windows_elevated::SANDBOX_USERNAME,
    ) {
        Ok(bytes) => match codex_windows_sandbox::winutil::string_from_sid_bytes(&bytes) {
            Ok(s) => s,
            Err(e) => {
                eprintln!("sandbox SID to string: {e}");
                return std::process::ExitCode::FAILURE;
            }
        },
        Err(e) => {
            eprintln!("resolve sandbox SID: {e:#}");
            return std::process::ExitCode::FAILURE;
        }
    };

    // Paths the probe touches. `outside` is the sandbox_home root: created by
    // provisioning as an administrator, the least-privilege sandbox account has
    // no write there, so a write must be denied. `canary` is a plaintext secret
    // seeded (by this parent, as admin) INSIDE the deny-read secrets dir, so its
    // marker inheriting the deny-read ACE means the confined child must not be
    // able to read it back.
    let inside = ws.join("inside.txt");
    let outside = sandbox_home.join("outside_probe.txt");
    let secrets = hive_desktop_sandbox::windows_elevated::secrets_dir(&sandbox_home);
    let canary = secrets.join("canary.txt");
    const CANARY_MARKER: &str = "HIVE_CANARY_DO_NOT_LEAK";
    let _ = std::fs::remove_file(&inside);
    let _ = std::fs::remove_file(&outside);
    if let Err(e) = std::fs::write(&canary, CANARY_MARKER) {
        eprintln!("seed canary {}: {e}", canary.display());
        return std::process::ExitCode::FAILURE;
    }

    // Probe batch. Paths under the sandbox home contain no spaces, so no inner
    // quoting is used: quoting the whole script arg and re-quoting inner paths
    // double-escapes through the argv -> command-line -> cmd re-parse and
    // produces a malformed path. Commands are sequenced with `&`; each step
    // prints a marker so stdout is self-describing. `echo`/`type` are cmd
    // built-ins (no PATH needed); `whoami` resolves via the minimal PATH below.
    let mut script = format!(
        "whoami /user & echo ===INSIDE=== & echo probe>{inside} & \
         echo ===OUTSIDE=== & echo probe>{outside} & \
         echo ===SECRET=== & type {canary}",
        inside = inside.display(),
        outside = outside.display(),
        canary = canary.display(),
    );
    // Network matrix probes (plan §5 rows 1-7). Emitted for BOTH runs so the
    // DenyAll row is LIVE-probed rather than only config-verified: an AllowHosts
    // run proves the allowlist decision (rows 5-6), a DenyAll run proves that
    // every probe is blocked, including the host that WOULD be the allowed one
    // under an AllowHosts run (row 7). Marker names are identical across the two
    // runs so the operator parses both the same way; only the EXPECTATIONS
    // invert, and the legend printed below states which run is which so a
    // blocked allowed-host probe under DenyAll is not misread as a row 5 failure.
    // curl uses `--ssl-no-revoke` for the schannel CRL gotcha on the lab host;
    // each step prints a marker and an OK/FAIL/BLOCKED tag so stdout is
    // self-describing. The harness (and operator) read the raw stdout and derive
    // per-row verdicts; nothing here is executed by this build (compile-only,
    // D-004).
    let probe_host = allow_hosts
        .first()
        .map(String::as_str)
        // DenyAll has no allowed host, so probe a real, normally reachable host
        // purely as a target to prove it is blocked. Reachability is the point:
        // an unroutable name would prove nothing about the fence.
        .unwrap_or("api.github.com");
    script.push_str(&format!(
        " & echo ===NET_ALLOWED=== & curl -sS --ssl-no-revoke -o NUL https://{host}/ && echo NET_ALLOWED_OK || echo NET_ALLOWED_FAIL \
         & echo ===NET_DENIED=== & curl -sS --ssl-no-revoke -o NUL https://example.org/ && echo NET_DENIED_REACHED || echo NET_DENIED_BLOCKED \
         & echo ===NET_DIRECT=== & curl -sS --ssl-no-revoke --noproxy * -o NUL https://{host}/ && echo NET_DIRECT_REACHED || echo NET_DIRECT_BLOCKED \
         & echo ===NET_DNS=== & nslookup {host} \
         & echo ===CERTSTORE=== & certutil -user -store My \
         & echo ===WHOAMI=== & whoami /groups",
        host = probe_host,
    ));
    let command = vec![
        r"C:\Windows\System32\cmd.exe".to_string(),
        "/c".to_string(),
        script,
    ];

    // Empty for the read-only derivation, [ws] for workspace-write. This is the
    // single lever that flips windows_elevated's `read_only` token choice.
    let writable_roots = if workspace_write {
        vec![ws.clone()]
    } else {
        vec![]
    };
    let policy = match hive_desktop_sandbox::SandboxPolicy::build(
        writable_roots,
        vec![],
        sandbox_home.join("hooks"),
        network,
    ) {
        Ok(p) => p,
        Err(e) => {
            eprintln!("build policy: {e}");
            return std::process::ExitCode::FAILURE;
        }
    };

    // Minimal, scrubbed environment: enough for cmd/whoami to function
    // (SystemRoot + a System32 PATH), nothing sensitive. Confinement comes from
    // the token + ACLs, not from env starvation.
    //
    // This is the HARNESS environment, built here deliberately and kept separate
    // from the product's own environment construction (`windows.rs::launch`,
    // which derives the child env from the parent process). Changing one does
    // not change the other.
    //
    // The profile variables below are load-bearing, not decoration. curl on the
    // lab host uses schannel, and schannel acquires CLIENT credentials out of
    // the per-user crypto store under the user profile. With no profile
    // variables set it fails at `AcquireCredentialsHandle` with
    // `SEC_E_NO_CREDENTIALS (0x8009030E)` before a single byte reaches the wire,
    // which looks like an egress failure and is not one (this is exactly what
    // made matrix row 5 fail while the proxy allowlist decision was working).
    //   USERPROFILE   -> profile root schannel resolves the crypto store from
    //   APPDATA       -> roaming crypto key containers
    //   LOCALAPPDATA  -> local crypto key containers and per-user caches
    //   TEMP / TMP    -> scratch schannel and curl need while building the
    //                    credential handle (unset TEMP alone can fail the same way)
    // They point at the SANDBOX ACCOUNT's profile, never the launching user's:
    // the sandbox account has no access to another user's profile directory.
    // No deny-read policy path is touched here; the deny-read ACEs apply only to
    // explicit policy paths, not to the crypto store.
    let mut env: HashMap<String, String> = HashMap::new();
    env.insert("SystemRoot".to_string(), r"C:\Windows".to_string());
    env.insert(
        "PATH".to_string(),
        r"C:\Windows\System32;C:\Windows".to_string(),
    );
    // Derive the profiles root from this process's own USERPROFILE parent
    // instead of hardcoding `C:\Users`, so a relocated or non-C: profiles root
    // still resolves; the account name itself comes from the same constant the
    // SID lookup above uses, so the two can never drift apart.
    let profiles_root = std::env::var_os("USERPROFILE")
        .map(PathBuf::from)
        .and_then(|p| p.parent().map(|parent| parent.to_path_buf()))
        .unwrap_or_else(|| PathBuf::from(r"C:\Users"));
    let sandbox_profile =
        profiles_root.join(hive_desktop_sandbox::windows_elevated::SANDBOX_USERNAME);
    let local_appdata = sandbox_profile.join("AppData").join("Local");
    let sandbox_temp = local_appdata.join("Temp");
    env.insert(
        "USERPROFILE".to_string(),
        sandbox_profile.display().to_string(),
    );
    env.insert(
        "APPDATA".to_string(),
        sandbox_profile
            .join("AppData")
            .join("Roaming")
            .display()
            .to_string(),
    );
    env.insert(
        "LOCALAPPDATA".to_string(),
        local_appdata.display().to_string(),
    );
    env.insert("TEMP".to_string(), sandbox_temp.display().to_string());
    env.insert("TMP".to_string(), sandbox_temp.display().to_string());

    println!("== confined-spawn validation ==");
    println!("sandbox_home = {}", sandbox_home.display());
    println!("workspace    = {}", ws.display());
    println!("network      = {net_spec}");
    println!("child profile = {}", sandbox_profile.display());
    println!("expected sandbox SID = {expected_sid}");
    // Network expectations, stated per run so the identical NET_* markers are
    // never misread across the two runs.
    if allow_hosts.is_empty() {
        println!(
            "net expectations (DenyAll, row 7): ALL probes must be blocked -- \
             NET_ALLOWED_FAIL (probe host {probe_host} is NOT allowed in this run, \
             a block here is CORRECT and is not a row 5 failure), NET_DENIED_BLOCKED, \
             NET_DIRECT_BLOCKED, and NET_DNS must not resolve"
        );
    } else {
        println!(
            "net expectations (AllowHosts, rows 5-6): NET_ALLOWED_OK for {probe_host} via the \
             proxy, NET_DENIED_BLOCKED, NET_DIRECT_BLOCKED"
        );
    }

    let result = hive_desktop_sandbox::windows_elevated::spawn_confined_for_validation(
        &sandbox_home,
        &policy,
        &command,
        &ws,
        &env,
    );
    let capture = match result {
        Ok(c) => c,
        Err(e) => {
            // The IPC-failure blocker surfaces here as a confinement error
            // (e.g. the 15s pipe-connect timeout). Print it as the evidence.
            eprintln!("spawn_confined_for_validation FAILED: {e}");
            return std::process::ExitCode::FAILURE;
        }
    };

    let stdout = String::from_utf8_lossy(&capture.stdout);
    let stderr = String::from_utf8_lossy(&capture.stderr);
    println!(
        "-- child exit_code = {} timed_out = {} --",
        capture.exit_code, capture.timed_out
    );
    println!("-- child stdout --\n{stdout}");
    println!("-- child stderr --\n{stderr}");

    // Independent, parent-side filesystem evidence (does not trust child stdout).
    let inside_exists = inside.is_file();
    let outside_exists = outside.is_file();
    println!("-- parent-side filesystem evidence --");
    println!(
        "inside file exists (expect {}): {inside_exists}",
        workspace_write
    );
    println!("outside file exists (expect false): {outside_exists}");

    // Derived verdicts from the raw evidence above. Printed for convenience;
    // the raw evidence is authoritative.
    let sid_ok = stdout.to_lowercase().contains(&expected_sid.to_lowercase());
    // Inside-workspace write: allowed under the workspace-write derivation,
    // DENIED under the read-only derivation. Assertion (a) and (d) below are
    // checked identically in both runs (the invariant the fence depends on).
    let inside_ok = if workspace_write {
        inside_exists
    } else {
        !inside_exists
    };
    let outside_denied = !outside_exists;
    let secret_denied = !stdout.contains(CANARY_MARKER);
    println!("-- derived verdicts --");
    println!("(a) child SID == sandbox SID : {sid_ok}");
    println!("(b) outside write denied     : {outside_denied}");
    println!(
        "(c) inside write {} : {inside_ok}",
        if workspace_write {
            "allowed"
        } else {
            "denied "
        }
    );
    println!("(d) secret read denied       : {secret_denied}");

    if sid_ok && inside_ok && outside_denied && secret_denied {
        println!("VALIDATION: ALL ASSERTIONS PASS");
        std::process::ExitCode::SUCCESS
    } else {
        println!("VALIDATION: ONE OR MORE ASSERTIONS FAILED (see evidence above)");
        std::process::ExitCode::FAILURE
    }
}

/// Non-Windows: the confined spawn is Windows-only. Fail closed (D-005).
#[cfg(not(windows))]
fn main() -> std::process::ExitCode {
    eprintln!("hive-sandbox-validate is only functional on Windows");
    std::process::ExitCode::FAILURE
}
