// Prevents additional console window on Windows in release, DO NOT REMOVE!!
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

fn main() {
    app_core::run(
        tauri::generate_context!(),
        include_str!("../generated/browser-runtime.js"),
    )
}
