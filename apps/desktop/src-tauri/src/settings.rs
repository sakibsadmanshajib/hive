//! First-run server URL configuration: validate, normalize, persist to a
//! JSON file under the app's data directory, and load it back on startup so
//! `main.rs` can decide which URL the main window opens.

use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use tauri::Manager;
use url::Url;

const SETTINGS_FILE: &str = "settings.json";
const CONSOLE_BASE_PATH: &str = "/agent-workspace";

#[derive(Serialize, Deserialize)]
struct StoredSettings {
    /// Full URL to the deployment's agent-console, already normalized
    /// (scheme + host + optional port + the fixed console base path).
    console_url: String,
}

/// Validates a user-entered server address and returns the full,
/// normalized agent-console URL to load in the webview.
///
/// Only http/https with a host are accepted; any path, query, or fragment
/// the user typed is discarded in favour of the console's fixed base path
/// (`apps/agent-console`'s `next.config.ts` bakes in `/agent-workspace`).
pub fn validate_and_normalize(input: &str) -> Result<String, String> {
    let trimmed = input.trim();
    if trimmed.is_empty() {
        return Err("Server URL is required.".to_string());
    }

    let parsed = Url::parse(trimmed)
        .map_err(|_| "Enter a valid URL, e.g. https://hive.example.com".to_string())?;

    match parsed.scheme() {
        "http" | "https" => {}
        other => {
            return Err(format!(
                "Unsupported URL scheme \"{other}\". Use http or https."
            ))
        }
    }

    let host = parsed
        .host_str()
        .filter(|h| !h.is_empty())
        .ok_or_else(|| "Server URL must include a host.".to_string())?;

    let mut normalized = format!("{}://{}", parsed.scheme(), host);
    if let Some(port) = parsed.port() {
        normalized.push_str(&format!(":{port}"));
    }
    normalized.push_str(CONSOLE_BASE_PATH);

    Ok(normalized)
}

fn settings_path(data_dir: &Path) -> PathBuf {
    data_dir.join(SETTINGS_FILE)
}

/// Reads the persisted console URL, if any. Any I/O or parse error is
/// treated as "not configured yet" rather than a hard failure, since the
/// only recovery is showing the settings page again.
pub fn load(data_dir: &Path) -> Option<String> {
    let bytes = std::fs::read(settings_path(data_dir)).ok()?;
    let parsed: StoredSettings = serde_json::from_slice(&bytes).ok()?;
    Some(parsed.console_url)
}

/// Resolves the saved console URL string into an actual `Url` for
/// `main.rs` to open, falling back to "unconfigured" (`None`) if the
/// string itself doesn't parse (e.g. valid JSON but a hand-edited or
/// otherwise corrupt URL value) rather than propagating a startup error.
pub fn resolved_target_url(saved: Option<String>) -> Option<Url> {
    saved.and_then(|s| Url::parse(&s).ok())
}

/// Returns the deployment origin (scheme + host + optional port) the stored
/// console URL was built from, by stripping the fixed console base path this
/// module always appends. Used by `entitlements.rs` to target edge-api's
/// `/v1/featuregate` on the same origin as the console, without re-parsing
/// or re-deriving the URL a second way.
pub fn origin(console_url: &str) -> &str {
    console_url
        .strip_suffix(CONSOLE_BASE_PATH)
        .unwrap_or(console_url)
}

pub fn save(data_dir: &Path, normalized_url: &str) -> std::io::Result<()> {
    std::fs::create_dir_all(data_dir)?;
    let payload = StoredSettings {
        console_url: normalized_url.to_string(),
    };
    std::fs::write(settings_path(data_dir), serde_json::to_vec_pretty(&payload)?)
}

pub fn remove(data_dir: &Path) -> std::io::Result<()> {
    match std::fs::remove_file(settings_path(data_dir)) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e),
    }
}

#[tauri::command]
pub fn get_server_url(app: tauri::AppHandle) -> Option<String> {
    let dir = app.path().app_data_dir().ok()?;
    load(&dir)
}

#[tauri::command]
pub fn set_server_url(app: tauri::AppHandle, url: String) -> Result<String, String> {
    let normalized = validate_and_normalize(&url)?;
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cannot resolve app data directory: {e}"))?;
    save(&dir, &normalized).map_err(|e| format!("cannot persist settings: {e}"))?;
    Ok(normalized)
}

#[tauri::command]
pub fn reset_server_url(app: tauri::AppHandle) -> Result<(), String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("cannot resolve app data directory: {e}"))?;
    remove(&dir).map_err(|e| format!("cannot remove settings: {e}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    // -- validate_and_normalize -------------------------------------------

    #[test]
    fn rejects_empty_input() {
        assert!(validate_and_normalize("").is_err());
    }

    #[test]
    fn rejects_whitespace_only_input() {
        assert!(validate_and_normalize("   ").is_err());
    }

    #[test]
    fn rejects_missing_scheme() {
        // Url::parse requires an absolute URL; a bare host has no scheme.
        assert!(validate_and_normalize("hive.example.com").is_err());
    }

    #[test]
    fn rejects_javascript_scheme() {
        assert!(validate_and_normalize("javascript:alert(1)").is_err());
    }

    #[test]
    fn rejects_ftp_scheme() {
        assert!(validate_and_normalize("ftp://hive.example.com").is_err());
    }

    #[test]
    fn rejects_url_without_host() {
        // WHATWG URL parsing (which the `url` crate implements) throws
        // outright for a special scheme (http/https) with no authority,
        // rather than yielding an empty host.
        assert!(validate_and_normalize("https://").is_err());
    }

    #[test]
    fn accepts_https_and_appends_base_path() {
        let out = validate_and_normalize("https://hive.example.com").unwrap();
        assert_eq!(out, "https://hive.example.com/agent-workspace");
    }

    #[test]
    fn accepts_http_for_local_dev() {
        let out = validate_and_normalize("http://localhost:8090").unwrap();
        assert_eq!(out, "http://localhost:8090/agent-workspace");
    }

    #[test]
    fn strips_user_provided_path_query_and_fragment() {
        let out =
            validate_and_normalize("https://hive.example.com/some/path?x=1#frag").unwrap();
        assert_eq!(out, "https://hive.example.com/agent-workspace");
    }

    #[test]
    fn trims_surrounding_whitespace() {
        let out = validate_and_normalize("  https://hive.example.com  ").unwrap();
        assert_eq!(out, "https://hive.example.com/agent-workspace");
    }

    #[test]
    fn preserves_explicit_port() {
        let out = validate_and_normalize("https://hive.example.com:8443/").unwrap();
        assert_eq!(out, "https://hive.example.com:8443/agent-workspace");
    }

    // -- load / save / remove ----------------------------------------------

    fn temp_dir(label: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "hive-desktop-test-{label}-{}-{}",
            std::process::id(),
            label.len()
        ));
        let _ = std::fs::remove_dir_all(&dir);
        dir
    }

    // -- resolved_target_url ------------------------------------------------

    #[test]
    fn resolved_target_url_none_when_unconfigured() {
        assert_eq!(resolved_target_url(None), None);
    }

    #[test]
    fn resolved_target_url_falls_back_on_corrupt_saved_string() {
        // Valid JSON, invalid URL value -- e.g. a hand-edited settings file.
        assert_eq!(resolved_target_url(Some("not a url".to_string())), None);
    }

    #[test]
    fn resolved_target_url_parses_valid_saved_string() {
        let url = resolved_target_url(Some(
            "https://hive.example.com/agent-workspace".to_string(),
        ))
        .unwrap();
        assert_eq!(url.as_str(), "https://hive.example.com/agent-workspace");
    }

    #[test]
    fn load_returns_none_when_never_saved() {
        let dir = temp_dir("never-saved");
        assert_eq!(load(&dir), None);
    }

    #[test]
    fn save_then_load_round_trips() {
        let dir = temp_dir("round-trip");
        save(&dir, "https://hive.example.com/agent-workspace").unwrap();
        assert_eq!(
            load(&dir),
            Some("https://hive.example.com/agent-workspace".to_string())
        );
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn remove_clears_saved_settings() {
        let dir = temp_dir("remove");
        save(&dir, "https://hive.example.com/agent-workspace").unwrap();
        remove(&dir).unwrap();
        assert_eq!(load(&dir), None);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn remove_is_idempotent_when_nothing_saved() {
        let dir = temp_dir("remove-idempotent");
        assert!(remove(&dir).is_ok());
    }

    // -- origin ---------------------------------------------------------------

    #[test]
    fn origin_strips_console_base_path() {
        assert_eq!(
            origin("https://hive.example.com/agent-workspace"),
            "https://hive.example.com"
        );
    }

    #[test]
    fn origin_preserves_port() {
        assert_eq!(
            origin("https://hive.example.com:8443/agent-workspace"),
            "https://hive.example.com:8443"
        );
    }

    #[test]
    fn origin_falls_back_to_input_when_suffix_absent() {
        // Defensive: a hand-edited or otherwise corrupt stored value that
        // doesn't end in the expected base path is returned unchanged rather
        // than mangled.
        assert_eq!(origin("not a url"), "not a url");
    }

    #[test]
    fn load_treats_corrupt_file_as_unconfigured() {
        let dir = temp_dir("corrupt");
        std::fs::create_dir_all(&dir).unwrap();
        std::fs::write(settings_path(&dir), b"not json").unwrap();
        assert_eq!(load(&dir), None);
        std::fs::remove_dir_all(&dir).ok();
    }
}
