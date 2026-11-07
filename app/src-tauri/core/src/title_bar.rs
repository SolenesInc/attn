use crate::browser_host::TrustedMainWebview;

#[tauri::command]
pub fn set_title_bar_overlay(
    _caller: TrustedMainWebview,
    window: tauri::Window,
) -> Result<(), String> {
    apply_overlay(&window)
}

#[cfg(target_os = "macos")]
fn apply_overlay(window: &tauri::Window) -> Result<(), String> {
    use objc2_app_kit::{NSWindow, NSWindowTitleVisibility};
    use tauri::TitleBarStyle;

    window
        .set_title_bar_style(TitleBarStyle::Overlay)
        .map_err(|error| format!("set title bar style: {error}"))?;
    let window_ptr = window
        .ns_window()
        .map_err(|error| format!("read the native window: {error}"))?;
    let native: &NSWindow = unsafe { &*window_ptr.cast() };
    native.setTitleVisibility(NSWindowTitleVisibility::Hidden);
    Ok(())
}

#[cfg(not(target_os = "macos"))]
fn apply_overlay(_window: &tauri::Window) -> Result<(), String> {
    Ok(())
}
