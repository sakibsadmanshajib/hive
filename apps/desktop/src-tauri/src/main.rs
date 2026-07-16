// Prevents an additional console window on Windows in release builds.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod entitlements;
mod settings;

use tauri::{Manager, WebviewUrl, WebviewWindowBuilder};

fn main() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![
            settings::get_server_url,
            settings::set_server_url,
            settings::reset_server_url,
        ])
        .setup(|app| {
            let handle = app.handle();
            let data_dir = handle.path().app_data_dir()?;
            let saved_console_url = settings::load(&data_dir);

            // See entitlements.rs: Step 4.3's fetch belongs right here.
            let _entitlements = entitlements::fetch_stub();

            let target = match settings::resolved_target_url(saved_console_url) {
                Some(url) => WebviewUrl::External(url),
                None => WebviewUrl::App("index.html".into()),
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
