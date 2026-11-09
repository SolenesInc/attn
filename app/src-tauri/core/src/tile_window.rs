use serde::{Deserialize, Serialize};
use std::sync::Mutex;
use tauri::webview::{NewWindowFeatures, NewWindowResponse};
use tauri::{
    AppHandle, LogicalPosition, LogicalSize, Manager, Monitor, Url, WebviewUrl, WebviewWindow,
    WebviewWindowBuilder, Window, WindowEvent, Wry,
};

const LABEL_PREFIX: &str = "tile-window-";
const MIN_WIDTH: f64 = 320.0;
const MIN_HEIGHT: f64 = 200.0;
const VISIBLE_GRIP: f64 = 40.0;

#[derive(Debug, Clone, Copy, PartialEq, Deserialize, Serialize)]
pub struct Frame {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}

struct Opening {
    label: String,
    frame: Frame,
}

static NEXT_OPENING: Mutex<Option<Opening>> = Mutex::new(None);
static TITLE_BAR_HEIGHT: std::sync::OnceLock<f64> = std::sync::OnceLock::new();

#[cfg(target_os = "macos")]
pub fn measure_title_bar() {
    use objc2::MainThreadMarker;
    use objc2_app_kit::{NSWindow, NSWindowStyleMask};
    use objc2_foundation::{NSPoint, NSRect, NSSize};
    let Some(mtm) = MainThreadMarker::new() else {
        return;
    };
    let content = NSRect::new(NSPoint::new(0.0, 0.0), NSSize::new(400.0, 300.0));
    let style = NSWindowStyleMask::Titled
        | NSWindowStyleMask::Closable
        | NSWindowStyleMask::Miniaturizable
        | NSWindowStyleMask::Resizable;
    let frame = NSWindow::frameRectForContentRect_styleMask(content, style, mtm);
    let _ = TITLE_BAR_HEIGHT.set(frame.size.height - content.size.height);
}

#[derive(Debug, Clone, Copy, Deserialize)]
pub struct ContentRect {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

#[tauri::command]
pub fn tile_window_frame_over(app: AppHandle, rect: ContentRect) -> Result<Frame, String> {
    let main = app
        .get_window("main")
        .ok_or_else(|| "main window is not open".to_string())?;
    let origin = webview_rect(&main).ok_or_else(|| "main window has no frame".to_string())?;
    Ok(Frame {
        x: origin.x + rect.x,
        y: origin.y + rect.y,
        width: rect.width.max(MIN_WIDTH),
        height: rect.height.max(MIN_HEIGHT),
    })
}

pub fn is_tile_window(label: &str) -> bool {
    label.starts_with(LABEL_PREFIX)
}

fn validate_label(label: &str) -> Result<(), String> {
    let suffix = label.strip_prefix(LABEL_PREFIX).unwrap_or("");
    if suffix.is_empty()
        || !suffix
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
    {
        return Err(format!(
            "tile window label must be {LABEL_PREFIX}<letters, digits or dashes>, got {label:?}"
        ));
    }
    Ok(())
}

fn harness_window() -> bool {
    std::env::var("ATTN_HARNESS_ALWAYS_ON_TOP").is_ok_and(|value| value == "1")
}

fn tile_window(app: &AppHandle, label: &str) -> Result<Window, String> {
    validate_label(label)?;
    app.get_window(label)
        .ok_or_else(|| format!("tile window {label} is not open"))
}

#[tauri::command]
pub fn tile_window_prepare(label: String, frame: Frame) -> Result<(), String> {
    validate_label(&label)?;
    *NEXT_OPENING.lock().map_err(|error| error.to_string())? = Some(Opening { label, frame });
    Ok(())
}

#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
pub fn create(app: &AppHandle, url: Url, features: NewWindowFeatures) -> NewWindowResponse<Wry> {
    let Some(opening) = NEXT_OPENING.lock().ok().and_then(|mut next| next.take()) else {
        eprintln!("[TileWindow] window.open without tile_window_prepare; denied");
        return NewWindowResponse::Deny;
    };
    let outer = fit_on_screen(app, window_rect(opening.frame));
    let built = WebviewWindowBuilder::new(app, &opening.label, WebviewUrl::External(url))
        .window_features(features)
        .position(outer.x, outer.y)
        .inner_size(outer.width, outer.height)
        .min_inner_size(MIN_WIDTH, MIN_HEIGHT + title_bar_height())
        .title("attn")
        .focused(!harness_window())
        .background_throttling(tauri::utils::config::BackgroundThrottlingPolicy::Disabled)
        .on_document_title_changed(|window, title| {
            let _ = window.set_title(&title);
        })
        .build();
    match built {
        Ok(window) => {
            if let Some(native) = app.get_window(&opening.label) {
                if let Err(error) = place(app, &native, opening.frame) {
                    eprintln!("[TileWindow] placing {} failed: {error}", opening.label);
                }
            }
            watch(app, &window);
            if harness_window() {
                let _ = window.set_always_on_top(true);
                let _ = window.set_focusable(false);
                #[cfg(target_os = "macos")]
                order_front_without_focus(window.ns_window());
            }
            NewWindowResponse::Create { window }
        }
        Err(error) => {
            eprintln!("[TileWindow] building {} failed: {error}", opening.label);
            NewWindowResponse::Deny
        }
    }
}

#[cfg(target_os = "macos")]
fn order_front_without_focus(ns_window: tauri::Result<*mut std::ffi::c_void>) {
    if let Ok(pointer) = ns_window {
        let ns: &objc2_app_kit::NSWindow = unsafe { &*pointer.cast() };
        ns.orderFrontRegardless();
    }
}

fn watch(app: &AppHandle, window: &WebviewWindow) {
    let label = window.label().to_string();
    let app = app.clone();
    window.on_window_event(move |event| match event {
        WindowEvent::CloseRequested { api, .. } => {
            api.prevent_close();
            tell_main(
                &app,
                "attn:tile-window-close",
                &serde_json::json!({ "label": label }),
            );
        }
        WindowEvent::Moved(_) | WindowEvent::Resized(_) => {
            let Some(window) = app.get_window(&label) else {
                return;
            };
            if !window.is_visible().unwrap_or(false) {
                return;
            }
            if let Some(frame) = webview_rect(&window) {
                tell_main(
                    &app,
                    "attn:tile-window-frame",
                    &serde_json::json!({ "label": label, "frame": frame }),
                );
            }
        }
        _ => {}
    });
}

fn tell_main(app: &AppHandle, event: &str, detail: &serde_json::Value) {
    let Some(main) = app.get_webview("main") else {
        return;
    };
    let _ = main.eval(format!(
        "window.dispatchEvent(new CustomEvent({event:?}, {{ detail: {detail} }}));"
    ));
}

fn title_bar_height() -> f64 {
    TITLE_BAR_HEIGHT.get().copied().unwrap_or(0.0)
}

fn window_rect(webview: Frame) -> Frame {
    let title_bar = title_bar_height();
    Frame {
        y: webview.y - title_bar,
        height: webview.height + title_bar,
        ..webview
    }
}

#[cfg(target_os = "macos")]
fn webview_rect(window: &Window) -> Option<Frame> {
    let scale = window.scale_factor().ok()?;
    let top_left = window.outer_position().ok()?.to_logical::<f64>(scale);
    let pointer = window.ns_window().ok()?;
    let ns: &objc2_app_kit::NSWindow = unsafe { &*pointer.cast() };
    let frame = ns.frame();
    if ns.titlebarAppearsTransparent() {
        return Some(Frame {
            x: top_left.x,
            y: top_left.y,
            width: frame.size.width,
            height: frame.size.height,
        });
    }
    let content = ns.contentLayoutRect();
    Some(Frame {
        x: top_left.x + content.origin.x,
        y: top_left.y + frame.size.height - content.origin.y - content.size.height,
        width: content.size.width,
        height: content.size.height,
    })
}

#[cfg(not(target_os = "macos"))]
fn webview_rect(window: &Window) -> Option<Frame> {
    let scale = window.scale_factor().ok()?;
    let position = window.inner_position().ok()?.to_logical::<f64>(scale);
    let size = window.inner_size().ok()?.to_logical::<f64>(scale);
    Some(Frame {
        x: position.x,
        y: position.y,
        width: size.width,
        height: size.height,
    })
}

#[derive(Clone, Copy)]
struct Area {
    x: f64,
    y: f64,
    width: f64,
    height: f64,
}

fn logical_work_area(monitor: &Monitor) -> Area {
    let scale = monitor.scale_factor();
    let area = monitor.work_area();
    Area {
        x: area.position.x as f64 / scale,
        y: area.position.y as f64 / scale,
        width: area.size.width as f64 / scale,
        height: area.size.height as f64 / scale,
    }
}

fn grip_visible(frame: &Frame, area: &Area) -> bool {
    frame.x + frame.width > area.x + VISIBLE_GRIP
        && frame.x < area.x + area.width - VISIBLE_GRIP
        && frame.y >= area.y
        && frame.y < area.y + area.height - VISIBLE_GRIP
}

fn parked_for_harness(app: &AppHandle, frame: Frame) -> Frame {
    let Some(visible) = std::env::var("ATTN_HARNESS_PARK_VISIBLE_PX")
        .ok()
        .and_then(|value| value.parse::<f64>().ok())
        .filter(|value| *value > 0.0)
    else {
        return frame;
    };
    let Some(area) = app
        .get_window("main")
        .and_then(|main| main.primary_monitor().ok().flatten())
        .map(|monitor| logical_work_area(&monitor))
    else {
        return frame;
    };
    Frame {
        x: area.x + area.width - visible,
        ..frame
    }
}

fn fit_on_screen(app: &AppHandle, frame: Frame) -> Frame {
    parked_for_harness(app, fit_inside_displays(app, frame))
}

fn fit_inside_displays(app: &AppHandle, frame: Frame) -> Frame {
    let Some(main) = app.get_window("main") else {
        return frame;
    };
    let monitors = main.available_monitors().unwrap_or_default();
    let areas: Vec<Area> = monitors.iter().map(logical_work_area).collect();
    if areas.is_empty() || areas.iter().any(|area| grip_visible(&frame, area)) {
        return frame;
    }
    let target = main
        .current_monitor()
        .ok()
        .flatten()
        .or_else(|| main.primary_monitor().ok().flatten())
        .map(|monitor| logical_work_area(&monitor))
        .unwrap_or(areas[0]);
    let width = frame.width.min(target.width);
    let height = frame.height.min(target.height);
    Frame {
        x: target.x + (target.width - width) / 2.0,
        y: target.y + (target.height - height) / 2.0,
        width,
        height,
    }
}

fn place(app: &AppHandle, window: &Window, frame: Frame) -> Result<(), String> {
    let outer = fit_on_screen(app, window_rect(frame));
    if webview_rect(window).map(window_rect) == Some(outer) {
        return Ok(());
    }
    window
        .set_position(LogicalPosition::new(outer.x, outer.y))
        .map_err(|error| format!("move tile window: {error}"))?;
    window
        .set_size(LogicalSize::new(outer.width, outer.height))
        .map_err(|error| format!("resize tile window: {error}"))
}

#[tauri::command]
pub fn tile_window_show(app: AppHandle, label: String, frame: Frame) -> Result<(), String> {
    let window = tile_window(&app, &label)?;
    place(&app, &window, frame)?;
    if !window.is_visible().unwrap_or(false) {
        window
            .show()
            .map_err(|error| format!("show tile window: {error}"))?;
        #[cfg(target_os = "macos")]
        if harness_window() {
            order_front_without_focus(window.ns_window());
        }
    }
    Ok(())
}

#[tauri::command]
pub fn tile_window_hide(app: AppHandle, label: String) -> Result<(), String> {
    let window = tile_window(&app, &label)?;
    window
        .hide()
        .map_err(|error| format!("hide tile window: {error}"))?;
    if !any_tile_window_visible(&app) {
        show_main(&app);
    }
    Ok(())
}

#[tauri::command]
pub fn tile_window_raise(app: AppHandle, label: String) -> Result<(), String> {
    let window = tile_window(&app, &label)?;
    if harness_window() {
        return Ok(());
    }
    window
        .unminimize()
        .map_err(|error| format!("unminimize tile window: {error}"))?;
    window
        .set_focus()
        .map_err(|error| format!("focus tile window: {error}"))
}

fn any_tile_window_visible(app: &AppHandle) -> bool {
    app.windows()
        .iter()
        .any(|(label, window)| is_tile_window(label) && window.is_visible().unwrap_or(false))
}

pub fn main_close_requested(app: &AppHandle, api: &tauri::CloseRequestApi) {
    if !any_tile_window_visible(app) {
        app.exit(0);
        return;
    }
    api.prevent_close();
    if let Some(main) = app.get_window("main") {
        let _ = main.hide();
    }
}

pub fn show_main(app: &AppHandle) {
    let Some(main) = app.get_window("main") else {
        return;
    };
    if main.is_visible().unwrap_or(true) {
        return;
    }
    let _ = main.show();
    if !harness_window() {
        let _ = main.set_focus();
    }
}

#[cfg(target_os = "macos")]
pub fn allow_scripted_windows(window: &WebviewWindow) -> Result<(), String> {
    window
        .with_webview(|platform| unsafe {
            let view: &objc2_web_kit::WKWebView = &*platform.inner().cast();
            view.configuration()
                .preferences()
                .setJavaScriptCanOpenWindowsAutomatically(true);
        })
        .map_err(|error| format!("allow scripted windows: {error}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_frame_counts_as_on_screen_while_its_title_bar_is_reachable() {
        let area = Area {
            x: 0.0,
            y: 25.0,
            width: 1440.0,
            height: 875.0,
        };
        let frame = |x: f64, y: f64| Frame {
            x,
            y,
            width: 800.0,
            height: 600.0,
        };
        assert!(grip_visible(&frame(100.0, 100.0), &area));
        assert!(grip_visible(&frame(-700.0, 100.0), &area));
        assert!(!grip_visible(&frame(-790.0, 100.0), &area));
        assert!(!grip_visible(&frame(1420.0, 100.0), &area));
        assert!(!grip_visible(&frame(100.0, 10.0), &area));
        assert!(!grip_visible(&frame(100.0, 880.0), &area));
    }
}
