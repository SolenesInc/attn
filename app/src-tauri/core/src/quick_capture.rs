use tauri::AppHandle;

#[cfg(target_os = "macos")]
mod mac {
    use global_hotkey::{hotkey::HotKey, GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState};
    use objc2_app_kit::{
        NSApplication, NSApplicationActivationOptions, NSRunningApplication, NSWindow,
        NSWindowCollectionBehavior, NSWorkspace,
    };
    use objc2_foundation::MainThreadMarker;
    use panel::CapturePanel;
    use std::sync::{mpsc, Mutex};
    use tauri::{Emitter, Manager};
    use tauri_nspanel::{ManagerExt as PanelManagerExt, WebviewWindowExt};

    mod panel {
        use tauri_nspanel::tauri_panel;
        tauri_panel! {
            panel!(CapturePanel {
                config: {
                    can_become_key_window: true,
                    can_become_main_window: false,
                    is_floating_panel: true
                }
            })
        }
    }

    pub struct CaptureState {
        manager: Option<GlobalHotKeyManager>,
        preference: Option<String>,
        error: Option<String>,
        diagnostics: Vec<String>,
        origin: Option<i32>,
        binding: Option<HotKey>,
        was_hidden: bool,
    }

    pub fn show(app: &tauri::AppHandle) -> Result<(), String> {
        let result = show_on_main(app);
        if let Err(error) = &result {
            crate::ui_automation::append_log(app, &format!("[QuickCapture] Open failed: {error}"));
            app.state::<Mutex<CaptureState>>().lock().unwrap().error = Some(error.clone());
            let _ = app.emit("capture-error", error);
        }
        result
    }

    fn show_on_main(app: &tauri::AppHandle) -> Result<(), String> {
        let opened_at = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_millis() as u64;
        let window = app
            .get_webview_window("capture")
            .ok_or("capture window missing")?;
        log_focus(app, "before open");
        let state = app.state::<Mutex<CaptureState>>();
        if !window.is_visible().unwrap_or(false) {
            state.lock().unwrap().was_hidden = NSApplication::sharedApplication(
                MainThreadMarker::new().ok_or("Capture show requires the main thread")?,
            )
            .isHidden();
        }
        if !window.is_focused().unwrap_or(false) {
            state.lock().unwrap().origin = NSWorkspace::sharedWorkspace()
                .frontmostApplication()
                .map(|a| a.processIdentifier());
        }
        focus_panel(app)?;
        log_focus(app, "after open");
        window
            .emit("capture-open", opened_at)
            .map_err(|e| e.to_string())
    }

    fn focus_panel(app: &tauri::AppHandle) -> Result<(), String> {
        let window = app
            .get_webview_window("capture")
            .ok_or("capture window missing")?;
        let panel = app
            .get_webview_panel("capture")
            .map_err(|e| format!("Capture panel missing: {e:?}"))?;
        panel.order_front_regardless();
        panel.make_key_window();
        // Focus the embedded WKWebView without activating the application.
        let webview: &tauri::Webview = window.as_ref();
        webview.set_focus().map_err(|e| e.to_string())
    }

    fn record_trace(app: &tauri::AppHandle, message: String) {
        if crate::instance::automation_enabled() {
            let at = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_millis();
            app.state::<Mutex<CaptureState>>()
                .lock()
                .unwrap()
                .diagnostics
                .push(format!("{at} {message}"));
        }
    }

    fn log_focus(app: &tauri::AppHandle, phase: &str) {
        if !crate::instance::automation_enabled() {
            return;
        }
        let ns_app = NSApplication::sharedApplication(MainThreadMarker::new().unwrap());
        let panel = app.get_webview_panel("capture").unwrap();
        let window: &NSWindow = panel.as_panel();
        let key_window = ns_app.keyWindow().map(|window| window.windowNumber());
        let frontmost = NSWorkspace::sharedWorkspace()
            .frontmostApplication()
            .map(|app| app.processIdentifier());
        let responder = window
            .firstResponder()
            .map(|responder| format!("{responder:?}"));
        record_trace(app, format!(
            "[QuickCapture] {phase}: panel_key={} panel_window={} app_active={} visible={} occlusion={:?} alpha={} opaque={} ignores_mouse={} key_window={key_window:?} frontmost={frontmost:?} first_responder={responder:?}",
            window.isKeyWindow(), window.windowNumber(), ns_app.isActive(), window.isVisible(),
            window.occlusionState(), window.alphaValue(), window.isOpaque(), window.ignoresMouseEvents(),
        ));
    }

    pub fn hide(app: &tauri::AppHandle) -> Result<(), String> {
        on_main(app, hide_on_main)
    }

    fn on_main(
        app: &tauri::AppHandle,
        action: impl FnOnce(&tauri::AppHandle) -> Result<(), String> + Send + 'static,
    ) -> Result<(), String> {
        if MainThreadMarker::new().is_some() {
            return action(app);
        }
        let (sender, receiver) = mpsc::channel();
        let handle = app.clone();
        app.run_on_main_thread(move || {
            let _ = sender.send(action(&handle));
        })
        .map_err(|error| error.to_string())?;
        receiver.recv().map_err(|error| error.to_string())?
    }

    fn hide_on_main(app: &tauri::AppHandle) -> Result<(), String> {
        let window = app
            .get_webview_window("capture")
            .ok_or("capture window missing")?;
        let frontmost_is_ours = NSWorkspace::sharedWorkspace()
            .frontmostApplication()
            .is_some_and(|a| a.processIdentifier() == std::process::id() as i32);
        let ours = window.is_focused().unwrap_or(false) && frontmost_is_ours;
        let (origin, was_hidden) = {
            let state = app.state::<Mutex<CaptureState>>();
            let mut state = state.lock().unwrap();
            (state.origin.take(), std::mem::take(&mut state.was_hidden))
        };
        // Hide the app before its key window so the main window cannot surface.
        let ns_app = NSApplication::sharedApplication(
            MainThreadMarker::new().ok_or("Capture hide requires the main thread")?,
        );
        if was_hidden && (ours || !frontmost_is_ours) {
            ns_app.hide(None);
        }
        window.hide().map_err(|e| e.to_string())?;
        if ours {
            if let Some(origin) =
                origin.and_then(NSRunningApplication::runningApplicationWithProcessIdentifier)
            {
                #[allow(deprecated)]
                origin.activateWithOptions(NSApplicationActivationOptions::empty());
            }
        }
        window.emit("capture-hidden", ()).map_err(|e| e.to_string())
    }

    pub fn bind(app: &tauri::AppHandle, binding: Option<String>) -> Result<(), String> {
        on_main(app, move |app| bind_on_main(app, binding))
    }

    fn bind_on_main(app: &tauri::AppHandle, binding: Option<String>) -> Result<(), String> {
        let state = app.state::<Mutex<CaptureState>>();
        let mut state = state.lock().unwrap();
        let next = binding
            .map(|s| s.parse::<HotKey>().map_err(|e| e.to_string()))
            .transpose()?;
        if next.is_some_and(|key| {
            !key.mods.intersects(
                global_hotkey::hotkey::Modifiers::CONTROL
                    | global_hotkey::hotkey::Modifiers::ALT
                    | global_hotkey::hotkey::Modifiers::SUPER
                    | global_hotkey::hotkey::Modifiers::META,
            )
        }) {
            return Err(
                "Use Command, Control or Option with another key for a system-wide shortcut."
                    .into(),
            );
        }
        if next == state.binding {
            state.error = None;
            return Ok(());
        }
        if let Some(next) = next {
            state
                .manager
                .as_ref()
                .ok_or("Capture is shutting down")?
                .register_exclusive(next)
                .map_err(|e| {
                    format!("Cannot register {next}: {e}. Previous shortcut remains active.")
                })?;
        }
        if let Some(previous) = state.binding {
            if let Err(error) = state
                .manager
                .as_ref()
                .ok_or("Capture is shutting down")?
                .unregister(previous)
            {
                if let Some(next) = next {
                    state.manager.as_ref().ok_or("Capture is shutting down")?.unregister(next)
                        .map_err(|rollback| format!("Cannot unregister {previous}: {error}. Replacement rollback also failed: {rollback}"))?;
                }
                return Err(format!("Cannot unregister {previous}: {error}"));
            }
        }
        state.binding = next;
        state.error = None;
        Ok(())
    }

    pub fn status(app: &tauri::AppHandle) -> super::CaptureStatus {
        let state = app.state::<Mutex<CaptureState>>();
        let state = state.lock().unwrap();
        super::CaptureStatus {
            binding: state.preference.clone(),
            active: state.binding.map(|binding| binding.to_string()),
            error: state.error.clone(),
            diagnostics: state.diagnostics.clone(),
        }
    }

    pub fn cache(app: &tauri::AppHandle, binding: Option<String>) -> Result<(), String> {
        super::save_preference(&binding)?;
        app.state::<Mutex<CaptureState>>()
            .lock()
            .unwrap()
            .preference = binding;
        Ok(())
    }

    pub fn shutdown(app: &tauri::AppHandle) {
        let manager = {
            let state = app.state::<Mutex<CaptureState>>();
            let mut state = state.lock().unwrap();
            state.binding = None;
            state.manager.take()
        };
        drop(manager);
    }

    pub fn install(app: &mut tauri::App) -> Result<(), Box<dyn std::error::Error>> {
        let cached = super::load_preference();
        app.manage(Mutex::new(CaptureState {
            manager: Some(GlobalHotKeyManager::new()?),
            preference: cached.clone(),
            binding: None,
            origin: None,
            was_hidden: false,
            error: None,
            diagnostics: Vec::new(),
        }));
        app.handle().plugin(tauri_nspanel::init())?;
        let handle = app.handle().clone();
        GlobalHotKeyEvent::set_event_handler(Some(move |event: GlobalHotKeyEvent| {
            // Keep the shortcut-to-focus path free of diagnostic filesystem I/O.
            record_trace(
                &handle,
                format!(
                    "[QuickCapture] hotkey id={} state={:?} main_thread={}",
                    event.id,
                    event.state,
                    MainThreadMarker::new().is_some()
                ),
            );
            if event.state != HotKeyState::Pressed {
                return;
            }
            let current = handle
                .state::<Mutex<CaptureState>>()
                .lock()
                .unwrap()
                .binding
                .map(|binding| binding.id());
            if current != Some(event.id) {
                return;
            }
            let app = handle.clone();
            let _ = handle.run_on_main_thread(move || {
                let _ = show(&app);
            });
        }));
        let window = tauri::WebviewWindowBuilder::new(
            app,
            "capture",
            tauri::WebviewUrl::App("index.html?window=capture".into()),
        )
        .initialization_script(format!(
            "window.__ATTN_AUTOMATION_ENABLED = {};",
            crate::instance::automation_enabled()
        ))
        .title("Quick Capture")
        .inner_size(560.0, 390.0)
        .resizable(false)
        .decorations(false)
        .transparent(true)
        .shadow(true)
        .always_on_top(true)
        .visible_on_all_workspaces(true)
        .visible(false)
        .focused(false)
        .center()
        .build()?;
        let panel = window.to_panel::<CapturePanel>()?;
        panel.add_style_mask(
            tauri_nspanel::StyleMask::empty()
                .nonactivating_panel()
                .into(),
        )?;
        panel.set_hides_on_deactivate(false);
        let ns_window: &NSWindow = panel.as_panel();
        // Capture stays independent of the main app's hidden state.
        ns_window.setCanHide(false);
        ns_window.setLevel(objc2_app_kit::NSModalPanelWindowLevel);
        ns_window.setCollectionBehavior(
            NSWindowCollectionBehavior::CanJoinAllSpaces
                | NSWindowCollectionBehavior::CanJoinAllApplications
                | NSWindowCollectionBehavior::FullScreenAuxiliary,
        );
        window.on_window_event({
            let handle = app.handle().clone();
            move |event| {
                if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    let _ = hide(&handle);
                }
            }
        });
        window.on_webview_event({
            let handle = app.handle().clone();
            move |event| {
                if !matches!(
                    event,
                    tauri::WebviewEvent::DragDrop(tauri::DragDropEvent::Drop { .. })
                ) {
                    return;
                }
                let app = handle.clone();
                // Return from AppKit's drag callback before reclaiming key status.
                tauri::async_runtime::spawn(async move {
                    let focused = app.clone();
                    let _ = app.run_on_main_thread(move || {
                        log_focus(&focused, "before drop focus");
                        if let Err(error) = focus_panel(&focused) {
                            crate::ui_automation::append_log(
                                &focused,
                                &format!("[QuickCapture] Drop focus failed: {error}"),
                            );
                            let _ = focused.emit("capture-error", error);
                        }
                        log_focus(&focused, "after drop focus");
                    });
                });
            }
        });
        if let Err(error) = bind(app.handle(), cached) {
            app.state::<Mutex<CaptureState>>().lock().unwrap().error = Some(error);
        }
        Ok(())
    }
}

#[tauri::command]
pub fn capture_hide(app: AppHandle) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    return mac::hide(&app);
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        Err("Quick Capture is available on macOS".into())
    }
}

#[tauri::command]
pub fn capture_bind(app: AppHandle, binding: Option<String>) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    return mac::bind(&app, binding);
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (app, binding);
        Err("Quick Capture is available on macOS".into())
    }
}

#[cfg(target_os = "macos")]
pub use mac::{install, show, shutdown};

#[derive(serde::Serialize, serde::Deserialize)]
pub struct CaptureStatus {
    pub binding: Option<String>,
    pub active: Option<String>,
    pub error: Option<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub diagnostics: Vec<String>,
}

#[cfg(target_os = "macos")]
fn load_preference() -> Option<String> {
    let cached = crate::instance::data_dir()
        .ok()
        .and_then(|dir| std::fs::read(dir.join("capture-shortcut.json")).ok())
        .and_then(|bytes| serde_json::from_slice::<Option<String>>(&bytes).ok());
    cached.unwrap_or_else(|| {
        if crate::instance::build_instance().is_empty() {
            Some("Control+Alt+Space".into())
        } else {
            None
        }
    })
}

#[cfg(target_os = "macos")]
fn save_preference(binding: &Option<String>) -> Result<(), String> {
    let dir = crate::instance::data_dir()?;
    std::fs::create_dir_all(&dir).map_err(|error| error.to_string())?;
    let temporary = dir.join("capture-shortcut.json.tmp");
    std::fs::write(
        &temporary,
        serde_json::to_vec(binding).map_err(|e| e.to_string())?,
    )
    .map_err(|error| error.to_string())?;
    std::fs::rename(temporary, dir.join("capture-shortcut.json")).map_err(|e| e.to_string())
}

#[tauri::command]
pub fn capture_status(app: AppHandle) -> Result<CaptureStatus, String> {
    #[cfg(target_os = "macos")]
    return Ok(mac::status(&app));
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        Err("Quick Capture is available on macOS".into())
    }
}

#[tauri::command]
pub fn capture_cache(app: AppHandle, binding: Option<String>) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    return mac::cache(&app, binding);
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (app, binding);
        Err("Quick Capture is available on macOS".into())
    }
}

fn draft_image_name(id: &str) -> Result<String, String> {
    if id.len() != 36 || !id.chars().all(|ch| ch.is_ascii_hexdigit() || ch == '-') {
        return Err("Draft image id must be a UUID".into());
    }
    Ok(format!("{id}.image"))
}

fn write_cache(path: &std::path::Path, bytes: &[u8]) -> Result<(), String> {
    use std::io::Write;
    let parent = path.parent().ok_or("Cache path has no parent")?;
    std::fs::create_dir_all(parent).map_err(|error| error.to_string())?;
    let temporary = path.with_extension("tmp");
    let mut file = std::fs::File::create(&temporary).map_err(|error| error.to_string())?;
    file.write_all(bytes).map_err(|error| error.to_string())?;
    file.sync_all().map_err(|error| error.to_string())?;
    std::fs::rename(temporary, path).map_err(|error| error.to_string())?;
    std::fs::File::open(parent)
        .and_then(|dir| dir.sync_all())
        .map_err(|error| error.to_string())
}

fn draft_dir(profile_id: &str) -> Result<std::path::PathBuf, String> {
    draft_image_name(
        profile_id
            .strip_prefix("profile-")
            .ok_or("Capture draft needs a profile identity")?,
    )?;
    Ok(crate::instance::data_dir()?
        .join("capture-drafts")
        .join(profile_id))
}

fn read_draft(profile_id: String) -> Result<Option<serde_json::Value>, String> {
    let dir = draft_dir(&profile_id)?;
    let bytes = match std::fs::read(dir.join("capture-draft.json")) {
        Ok(bytes) => bytes,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error.to_string()),
    };
    let mut draft: serde_json::Value =
        serde_json::from_slice(&bytes).map_err(|error| error.to_string())?;
    for image in draft["images"]
        .as_array_mut()
        .ok_or("Draft images are missing")?
    {
        let id = image["id"].as_str().ok_or("Draft image id is missing")?;
        let path = dir.join("capture-draft-images").join(draft_image_name(id)?);
        image["url"] = std::fs::read_to_string(path)
            .map_err(|error| error.to_string())?
            .into();
    }
    Ok(Some(draft))
}

fn write_draft_image(profile_id: String, id: String, url: String) -> Result<(), String> {
    let path = draft_dir(&profile_id)?
        .join("capture-draft-images")
        .join(draft_image_name(&id)?);
    write_cache(&path, url.as_bytes())
}

fn write_draft(profile_id: String, draft: serde_json::Value) -> Result<(), String> {
    let dir = draft_dir(&profile_id)?;
    let images = draft["images"]
        .as_array()
        .ok_or("Draft images are missing")?;
    let names = images
        .iter()
        .map(|image| draft_image_name(image["id"].as_str().ok_or("Draft image id is missing")?))
        .collect::<Result<std::collections::HashSet<_>, String>>()?;
    write_cache(
        &dir.join("capture-draft.json"),
        &serde_json::to_vec(&draft).map_err(|error| error.to_string())?,
    )?;
    if let Ok(entries) = std::fs::read_dir(dir.join("capture-draft-images")) {
        for entry in entries {
            let entry = entry.map_err(|error| error.to_string())?;
            if !names.contains(&entry.file_name().to_string_lossy().to_string()) {
                std::fs::remove_file(entry.path()).map_err(|error| error.to_string())?;
            }
        }
    }
    Ok(())
}

#[tauri::command]
pub async fn capture_draft_read(profile_id: String) -> Result<Option<serde_json::Value>, String> {
    tauri::async_runtime::spawn_blocking(move || read_draft(profile_id))
        .await
        .map_err(|error| error.to_string())?
}

#[tauri::command]
pub async fn capture_draft_image_write(
    profile_id: String,
    id: String,
    url: String,
) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || write_draft_image(profile_id, id, url))
        .await
        .map_err(|error| error.to_string())?
}

#[tauri::command]
pub async fn capture_draft_write(
    profile_id: String,
    draft: serde_json::Value,
) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || write_draft(profile_id, draft))
        .await
        .map_err(|error| error.to_string())?
}

#[tauri::command]
pub async fn capture_image_read(path: String) -> Result<String, String> {
    tauri::async_runtime::spawn_blocking(move || {
        use base64::Engine;
        let bytes = std::fs::read(&path).map_err(|error| format!("Cannot read {path}: {error}"))?;
        let media = tauri::utils::mime_type::MimeType::parse_with_fallback(
            &bytes,
            &path,
            tauri::utils::mime_type::MimeType::OctetStream,
        );
        Ok(format!(
            "data:{};base64,{}",
            media,
            base64::engine::general_purpose::STANDARD.encode(bytes)
        ))
    })
    .await
    .map_err(|error| error.to_string())?
}
