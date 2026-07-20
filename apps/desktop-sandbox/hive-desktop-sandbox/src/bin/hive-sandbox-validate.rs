//! Hive sandbox confined-spawn validation harness (Step 3 Integration A, D-004).
//!
//! Lab-only. Drives the lab-only
//! [`hive_desktop_sandbox::windows_elevated::spawn_confined_for_validation`]
//! entry point on `spike307-win` so the filesystem / user / token isolation
//! matrix can be PROVEN on a real MSVC Windows host (CI only cross-compiles
//! this crate for `x86_64-pc-windows-gnu`; it never runs the Win32 paths).
//!
//! It launches a probe command AS the low-privilege sandbox account under a
//! workspace-write capability-restricted token and reports, with raw evidence
//! (never a bare "pass"):
//!   (a) the child token user SID (must equal the `hive_sandbox` account SID),
//!   (b) a write OUTSIDE the writable root is denied,
//!   (c) a write INSIDE the writable root succeeds,
//!   (d) reading a seeded secret under the deny-read secrets dir is denied.
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
    //   unset / "deny"       -> NetworkPolicy::DenyAll (rows 1-4: all egress
    //                           and DNS blocked, only the loopback proxy port
    //                           reachable and it has no proxy behind it);
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
    // Network matrix probes (plan §5 rows 1-6) for an AllowHosts run. curl uses
    // `--ssl-no-revoke` for the schannel CRL gotcha on the lab host; each step
    // prints a marker and an OK/FAIL/BLOCKED tag so stdout is self-describing.
    // The harness (and operator) read the raw stdout and derive per-row
    // verdicts; nothing here is executed by this build (compile-only, D-004).
    if let Some(host) = allow_hosts.first() {
        script.push_str(&format!(
            " & echo ===NET_ALLOWED=== & curl -sS --ssl-no-revoke -o NUL https://{host}/ && echo NET_ALLOWED_OK || echo NET_ALLOWED_FAIL \
             & echo ===NET_DENIED=== & curl -sS --ssl-no-revoke -o NUL https://example.org/ && echo NET_DENIED_REACHED || echo NET_DENIED_BLOCKED \
             & echo ===NET_DIRECT=== & curl -sS --ssl-no-revoke --noproxy * -o NUL https://{host}/ && echo NET_DIRECT_REACHED || echo NET_DIRECT_BLOCKED \
             & echo ===NET_DNS=== & nslookup {host}",
            host = host,
        ));
    }
    let command = vec![
        r"C:\Windows\System32\cmd.exe".to_string(),
        "/c".to_string(),
        script,
    ];

    let policy = match hive_desktop_sandbox::SandboxPolicy::build(
        vec![ws.clone()],
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
    let mut env: HashMap<String, String> = HashMap::new();
    env.insert("SystemRoot".to_string(), r"C:\Windows".to_string());
    env.insert(
        "PATH".to_string(),
        r"C:\Windows\System32;C:\Windows".to_string(),
    );
    println!("== confined-spawn validation ==");
    println!("sandbox_home = {}", sandbox_home.display());
    println!("workspace    = {}", ws.display());
    println!("network      = {net_spec}");
    println!("expected sandbox SID = {expected_sid}");

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
    println!("inside file exists (expect true):  {inside_exists}");
    println!("outside file exists (expect false): {outside_exists}");

    // Derived verdicts from the raw evidence above. Printed for convenience;
    // the raw evidence is authoritative.
    let sid_ok = stdout.to_lowercase().contains(&expected_sid.to_lowercase());
    let inside_ok = inside_exists;
    let outside_denied = !outside_exists;
    let secret_denied = !stdout.contains(CANARY_MARKER);
    println!("-- derived verdicts --");
    println!("(a) child SID == sandbox SID : {sid_ok}");
    println!("(b) outside write denied     : {outside_denied}");
    println!("(c) inside write ok          : {inside_ok}");
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
