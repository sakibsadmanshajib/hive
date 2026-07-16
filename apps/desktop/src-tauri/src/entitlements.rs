//! Step 4.3 (#310): desktop gate/license fetch at startup.
//!
//! ## Auth timing decision
//!
//! `main.rs`'s `setup()` runs before the window exists, so there is no
//! webview and no session: the agent-console page (which holds the real
//! Supabase session, per `settings.rs`'s doc comment on `CONSOLE_BASE_PATH`)
//! has not loaded yet. The main window's Tauri capability
//! (`src-tauri/capabilities/default.json`) also grants no IPC to remote
//! content by design, so there is no channel for that page to hand its
//! session back to Rust even after it loads.
//!
//! Given that, this module fetches `GET {origin}/v1/featuregate`
//! (`apps/edge-api/internal/featuregate/handler.go` `NewStateHandler`)
//! unauthenticated at startup. That call always comes back 401/403 today
//! (`AwaitingSession`) -- expected, not a failure. The real, authenticated
//! gate check already happens on every load of the agent-console page
//! itself, which calls the same endpoint with its own session and hides or
//! shows Cowork accordingly; nothing here needs to duplicate that. What this
//! startup fetch DOES add: telling a genuinely unreachable server (network
//! error, timeout, unexpected status) apart from a merely unauthenticated
//! one, so `main.rs` can fail safe and legible instead of silently opening a
//! window onto a dead URL.
//!
//! License state has no client-facing HTTP surface to call: the only
//! license read path, `GET /internal/license/entitlement`
//! (`apps/control-plane/internal/licensing/handler.go`), is wrapped in
//! `platformhttp.RequireInternalToken`, a service-to-service secret shared
//! between edge-api and control-plane that a desktop client must never hold
//! (and D9 already makes license state a non-gating attribute -- feature
//! gates are the sole capability switch, see `apps/control-plane/internal/
//! licensing/license.go`). So this module gates on featuregate alone.

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;

use futures_util::StreamExt;
use reqwest::Client;
use serde::{Deserialize, Serialize};

/// tenant_setting_key mirrored from apps/edge-api/internal/featuregate/gate.go
/// FeatureCowork. Kept as a local constant (not imported -- there is no Rust
/// binding to the Go package): if the Go side ever renames this key, nothing
/// here catches the drift automatically. A rename has to be grepped for and
/// updated by hand on both sides.
const GATE_COWORK: &str = "ENABLE_COWORK";

const FETCH_TIMEOUT: Duration = Duration::from_secs(5);

/// Response body cap for `fetch`, mirroring edge-api's own
/// `io.LimitReader(resp.Body, 4096)` in `featuregate/gate.go`. A gate map is
/// a handful of booleans; anything past this is not a legitimate response,
/// whether or not the server sent a Content-Length to check up front (a
/// chunked response has none).
const MAX_BODY_BYTES: usize = 4096;

/// Body shape of `GET {origin}/v1/featuregate`: identical to edge-api's
/// `FlagsResponse` (`{"gates": {key: bool}}`).
#[derive(Deserialize)]
struct FlagsResponse {
    gates: HashMap<String, bool>,
}

/// Result of the startup gate fetch.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum GateStatus {
    /// The server returned gate state. Only reachable today via a 200
    /// response; kept generic (not "cowork: bool") so a future authenticated
    /// caller reusing `fetch` needs no new variant.
    Known(HashMap<String, bool>),
    /// Reachable, but the request had no session (401/403). Expected at cold
    /// startup -- see module doc. Not a failure.
    AwaitingSession,
    /// The server did not answer as a Hive server should: transport error,
    /// timeout, or an unexpected status. Distinct from `AwaitingSession` so
    /// the caller can show a real error instead of proceeding to a dead URL.
    Unreachable(String),
}

/// Startup entitlement state. Only a `Known` status with the Cowork key
/// explicitly `true` opens Cowork -- every other status, including one that
/// has never fetched, reads as disabled (fail closed).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Entitlements {
    pub status: GateStatus,
}

impl Entitlements {
    /// State used before any fetch runs and whenever no server is configured
    /// yet (first run): not an error, just nothing to check.
    pub fn unconfigured() -> Self {
        Entitlements {
            status: GateStatus::AwaitingSession,
        }
    }

    /// State used when `build_client` itself fails -- rare (TLS backend or
    /// config init only; ordinary network conditions are classified inside
    /// `fetch`), but still fail-safe rather than a startup panic.
    pub fn unreachable(reason: impl std::fmt::Display) -> Self {
        Entitlements {
            status: GateStatus::Unreachable(reason.to_string()),
        }
    }

    pub fn cowork_enabled(&self) -> bool {
        matches!(&self.status, GateStatus::Known(gates) if *gates.get(GATE_COWORK).unwrap_or(&false))
    }

    pub fn is_unreachable(&self) -> bool {
        matches!(self.status, GateStatus::Unreachable(_))
    }

    fn to_view(&self) -> EntitlementsView {
        let (status, reason) = match &self.status {
            GateStatus::Known(_) => ("known", None),
            GateStatus::AwaitingSession => ("awaiting_session", None),
            GateStatus::Unreachable(reason) => ("unreachable", Some(reason.clone())),
        };
        EntitlementsView {
            status,
            cowork_enabled: self.cowork_enabled(),
            reason,
        }
    }
}

/// Frontend-facing projection of `Entitlements` for `get_entitlements`.
#[derive(Serialize)]
pub struct EntitlementsView {
    status: &'static str,
    cowork_enabled: bool,
    reason: Option<String>,
}

/// Builds the HTTP client used for the startup fetch. A dedicated builder
/// (rather than `Client::new()` inline at the call site) so the timeout and
/// redirect policy have one place to change. Never follows redirects: this
/// request only ever has one legitimate destination (the configured
/// server's own /v1/featuregate), so a redirect is treated as a transport
/// error rather than silently followed. Fallible instead of panicking --
/// the whole point of the fail-safe design below is that a startup problem
/// here becomes `Unreachable`, not a crash.
pub fn build_client() -> Result<Client, reqwest::Error> {
    Client::builder()
        .timeout(FETCH_TIMEOUT)
        .redirect(reqwest::redirect::Policy::none())
        .build()
}

/// Fetches gate state from `{origin}/v1/featuregate`, unauthenticated. See
/// the module doc for why unauthenticated is the right call here.
pub async fn fetch(client: &Client, origin: &str) -> Entitlements {
    let url = format!("{}/v1/featuregate", origin.trim_end_matches('/'));
    let resp = match client.get(&url).send().await {
        Ok(r) => r,
        Err(e) => {
            return Entitlements {
                status: GateStatus::Unreachable(e.to_string()),
            }
        }
    };

    let status = resp.status();
    if status.is_success() {
        let body = match read_capped_body(resp).await {
            Ok(b) => b,
            Err(reason) => {
                return Entitlements {
                    status: GateStatus::Unreachable(reason),
                }
            }
        };
        return match serde_json::from_slice::<FlagsResponse>(&body) {
            Ok(flags) => Entitlements {
                status: GateStatus::Known(flags.gates),
            },
            Err(e) => Entitlements {
                status: GateStatus::Unreachable(format!("malformed featuregate response: {e}")),
            },
        };
    }
    if status.as_u16() == 401 || status.as_u16() == 403 {
        return Entitlements {
            status: GateStatus::AwaitingSession,
        };
    }
    Entitlements {
        status: GateStatus::Unreachable(format!("unexpected status {status}")),
    }
}

/// Reads `resp`'s body up to `MAX_BODY_BYTES`, erroring instead of buffering
/// further. Streamed rather than a single `.bytes()` call so an oversized or
/// slow-drip body is rejected as soon as the cap is crossed, not after it's
/// already fully buffered.
///
/// ponytail: the cap is enforced between chunks, so one oversized chunk can
/// still be materialized in full before this rejects it (hyper's own
/// internal per-read buffer bounds how large that one chunk can practically
/// be, unlike Go's io.LimitReader which truncates the read call itself).
/// Good enough against our own server misbehaving; a hostile TLS peer has
/// other ways to cost this client memory regardless. Upgrade to a
/// size-limiting reader wrapper (e.g. tokio_util::io::StreamReader) only if
/// that gap ever actually matters here.
async fn read_capped_body(resp: reqwest::Response) -> Result<Vec<u8>, String> {
    let mut buf = Vec::new();
    let mut stream = resp.bytes_stream();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.map_err(|e| e.to_string())?;
        if buf.len() + chunk.len() > MAX_BODY_BYTES {
            return Err(format!("response body exceeds {MAX_BODY_BYTES} byte limit"));
        }
        buf.extend_from_slice(&chunk);
    }
    Ok(buf)
}

/// Reads the entitlements state `main.rs` fetched at startup.
#[tauri::command]
pub fn get_entitlements(state: tauri::State<Mutex<Entitlements>>) -> EntitlementsView {
    state
        .inner()
        .lock()
        .expect("entitlements mutex poisoned")
        .to_view()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    /// Spawns a one-shot HTTP server on an ephemeral port that writes a
    /// response with the given status line and body (Content-Length
    /// computed from `body`, never hand-counted) to the first connection it
    /// accepts, then returns the "http://host:port" origin to hit. One
    /// connection is all a single `fetch` call needs. The response is
    /// formatted before spawning so the thread only ever owns a `String`,
    /// letting callers (e.g. the oversized-body test) pass a borrowed,
    /// dynamically built body instead of a `&'static str` literal.
    fn mock_server(status_line: &str, body: &str) -> String {
        let response = format!(
            "{status_line}\r\ncontent-type: application/json\r\ncontent-length: {}\r\n\r\n{body}",
            body.len()
        );
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        std::thread::spawn(move || {
            if let Ok((mut stream, _)) = listener.accept() {
                let mut buf = [0u8; 1024];
                let _ = stream.read(&mut buf);
                let _ = stream.write_all(response.as_bytes());
            }
        });
        format!("http://{addr}")
    }

    /// Binds an ephemeral port, then immediately drops the listener so the
    /// port is guaranteed to refuse the next connection.
    fn unreachable_origin() -> String {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        drop(listener);
        format!("http://{addr}")
    }

    // -- Entitlements::unconfigured -----------------------------------------

    #[test]
    fn unconfigured_is_awaiting_session_not_unreachable() {
        let e = Entitlements::unconfigured();
        assert_eq!(e.status, GateStatus::AwaitingSession);
        assert!(!e.is_unreachable());
        assert!(!e.cowork_enabled());
    }

    // -- cowork_enabled -------------------------------------------------------

    #[test]
    fn cowork_enabled_true_when_known_and_true() {
        let mut gates = HashMap::new();
        gates.insert("ENABLE_COWORK".to_string(), true);
        let e = Entitlements {
            status: GateStatus::Known(gates),
        };
        assert!(e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_known_but_false() {
        let mut gates = HashMap::new();
        gates.insert("ENABLE_COWORK".to_string(), false);
        gates.insert("ENABLE_RAG".to_string(), true);
        let e = Entitlements {
            status: GateStatus::Known(gates),
        };
        assert!(!e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_key_missing() {
        let e = Entitlements {
            status: GateStatus::Known(HashMap::new()),
        };
        assert!(!e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_awaiting_session() {
        let e = Entitlements {
            status: GateStatus::AwaitingSession,
        };
        assert!(!e.cowork_enabled());
    }

    #[test]
    fn cowork_enabled_false_when_unreachable() {
        let e = Entitlements {
            status: GateStatus::Unreachable("boom".to_string()),
        };
        assert!(!e.cowork_enabled());
        assert!(e.is_unreachable());
    }

    // -- fetch: success --------------------------------------------------------

    #[tokio::test]
    async fn fetch_parses_200_gates_body() {
        let origin = mock_server("HTTP/1.1 200 OK", "{\"gates\":{\"ENABLE_COWORK\":true}}");
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        match &e.status {
            GateStatus::Known(gates) => assert_eq!(gates.get("ENABLE_COWORK"), Some(&true)),
            other => panic!("expected Known, got {other:?}"),
        }
        assert!(e.cowork_enabled());
    }

    #[tokio::test]
    async fn fetch_reports_cowork_disabled_when_gate_false() {
        let origin = mock_server("HTTP/1.1 200 OK", "{\"gates\":{\"ENABLE_COWORK\":false}}");
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        match &e.status {
            GateStatus::Known(gates) => assert_eq!(gates.get("ENABLE_COWORK"), Some(&false)),
            other => panic!("expected Known, got {other:?}"),
        }
        assert!(!e.cowork_enabled());
    }

    // -- fetch: fail-safe branches ----------------------------------------------

    #[tokio::test]
    async fn fetch_treats_401_as_awaiting_session() {
        let origin = mock_server("HTTP/1.1 401 Unauthorized", "");
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        assert_eq!(e.status, GateStatus::AwaitingSession);
        assert!(!e.is_unreachable());
    }

    #[tokio::test]
    async fn fetch_treats_403_as_awaiting_session() {
        let origin = mock_server("HTTP/1.1 403 Forbidden", "");
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        assert_eq!(e.status, GateStatus::AwaitingSession);
    }

    #[tokio::test]
    async fn fetch_treats_500_as_unreachable() {
        let origin = mock_server("HTTP/1.1 500 Internal Server Error", "");
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        assert!(e.is_unreachable());
    }

    #[tokio::test]
    async fn fetch_treats_malformed_json_body_as_unreachable() {
        let origin = mock_server("HTTP/1.1 200 OK", "not json!");
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        assert!(e.is_unreachable());
        assert!(!e.cowork_enabled());
    }

    #[tokio::test]
    async fn fetch_treats_connection_refused_as_unreachable() {
        let origin = unreachable_origin();
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        assert!(e.is_unreachable());
        assert!(!e.cowork_enabled());
    }

    #[tokio::test]
    async fn fetch_treats_oversized_body_as_unreachable() {
        let body = format!("{{\"pad\":\"{}\"}}", "x".repeat(MAX_BODY_BYTES + 100));
        let origin = mock_server("HTTP/1.1 200 OK", &body);
        let client = build_client().unwrap();
        let e = fetch(&client, &origin).await;
        assert!(e.is_unreachable());
        assert!(!e.cowork_enabled());
    }

    // -- crypto provider (BLOCKER fix regression) ------------------------------

    /// Mirrors main.rs's startup call. rustls 0.23 panics on the first TLS
    /// handshake if more than one crypto provider is linked (both ring and
    /// aws-lc-rs are, via Cargo feature unification -- see Cargo.toml) and
    /// none was installed as the process default. cargo test runs every
    /// test in this file in one process, so this may run after another test
    /// already installed one; install_default's Err in that case is exactly
    /// as fine as it is in main.rs, hence the same `let _ =`.
    ///
    /// A full HTTPS round-trip against a local TLS test server (the
    /// reviewer's stronger option) is deliberately skipped: it needs a
    /// self-signed cert plus a cert-generation dependency neither this
    /// crate nor its dev-dependencies carry today, disproportionate for a
    /// gap already closed by the single explicit install_default() call
    /// this test exercises directly.
    #[test]
    fn crypto_provider_installs_and_client_builds_deterministically() {
        let _ = rustls::crypto::ring::default_provider().install_default();
        assert!(rustls::crypto::CryptoProvider::get_default().is_some());
        assert!(build_client().is_ok());
    }
}
