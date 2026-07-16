// Prevents an additional console window on Windows in release builds.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod entitlements;
mod settings;

use std::sync::Mutex;

use entitlements::Entitlements;
use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

fn main() {
    // Cargo's feature unification links both the ring and aws-lc-rs crypto
    // provider crates transitively (see Cargo.toml). rustls 0.23 panics on
    // the first TLS handshake if more than one provider is linked and none
    // was installed as the process default, so pick one explicitly before
    // any HTTP client (entitlements::build_client) is ever constructed.
    // Err means something else already installed a default first -- fine,
    // some provider is installed either way.
    let _ = rustls::crypto::ring::default_provider().install_default();

    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            settings::get_server_url,
            settings::set_server_url,
            settings::reset_server_url,
            entitlements::get_entitlements,
        ])
        .setup(|app| {
            let handle = app.handle();
            let data_dir = handle.path().app_data_dir()?;
            let saved_console_url = settings::load(&data_dir);

            // Step 4.3 (#310): startup gate fetch. Unauthenticated by design
            // -- see entitlements.rs module doc for the auth-timing decision
            // and why there is no license fetch here.
            let entitlements = match saved_console_url.as_deref().map(settings::origin) {
                Some(origin) => match entitlements::build_client() {
                    Ok(client) => {
                        let origin = origin.to_string();
                        tauri::async_runtime::block_on(async move {
                            entitlements::fetch(&client, &origin).await
                        })
                    }
                    Err(e) => Entitlements::unreachable(e),
                },
                None => Entitlements::unconfigured(),
            };
            let unreachable = entitlements.is_unreachable();
            app.manage(Mutex::new(entitlements));

            let target = if unreachable {
                // Fail safe and legible (#310): never navigate to a server
                // just confirmed unreachable. The local launcher page reads
                // get_entitlements on load and shows the error inline
                // instead (see src/main.ts).
                WebviewUrl::App("index.html".into())
            } else {
                match settings::resolved_target_url(saved_console_url) {
                    Some(url) => WebviewUrl::External(url),
                    None => WebviewUrl::App("index.html".into()),
                }
            };

            WebviewWindowBuilder::new(app, "main", target)
                .title("Hive")
                .inner_size(1200.0, 800.0)
                .build()?;

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running Hive desktop shell");
}
