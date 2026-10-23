use tauri::AppHandle;

#[cfg(target_os = "macos")]
mod mac {
    use global_hotkey::{hotkey::HotKey, GlobalHotKeyEvent, GlobalHotKeyManager, HotKeyState};
    use objc2_app_kit::{
        NSApplication, NSApplicationActivationOptions, NSEvent, NSRunningApplication, NSScreen,
        NSWindow, NSWindowCollectionBehavior, NSWorkspace,
    };
    use objc2_foundation::{MainThreadMarker, NSPoint};
    use panel::QuickCapturePanel;
    use std::sync::{mpsc, Mutex};
    use tauri::{Emitter, Manager};
    use tauri_nspanel::{ManagerExt as PanelManagerExt, WebviewWindowExt};

    mod panel {
        use tauri_nspanel::tauri_panel;
        tauri_panel! {
            panel!(QuickCapturePanel {
                config: {
                    can_become_key_window: true,
                    can_become_main_window: false,
                    is_floating_panel: true
                }
            })
        }
    }

    pub struct QuickCaptureState {
        manager: Option<GlobalHotKeyManager>,
        preference: Option<String>,
        diagnostics: Vec<String>,
        origin: Option<i32>,
        binding: Option<HotKey>,
        was_hidden: bool,
    }

    pub fn show(app: &tauri::AppHandle) {
        let opened_at = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_millis() as u64;
        let window = app
            .get_webview_window("capture")
            .expect("enabled capture window");
        log_focus(app, "before open");
        let state = app.state::<Mutex<QuickCaptureState>>();
        if !window.is_visible().unwrap_or(false) {
            state.lock().unwrap().was_hidden = NSApplication::sharedApplication(
                MainThreadMarker::new().expect("capture opens on the main thread"),
            )
            .isHidden();
        }
        if !window.is_focused().unwrap_or(false) {
            state.lock().unwrap().origin = NSWorkspace::sharedWorkspace()
                .frontmostApplication()
                .map(|a| a.processIdentifier());
        }
        let panel = app
            .get_webview_panel("capture")
            .expect("enabled capture panel");
        let native: &NSWindow = panel.as_panel();
        let mouse = NSEvent::mouseLocation();
        let screens =
            NSScreen::screens(MainThreadMarker::new().expect("capture opens on the main thread"));
        let screen = screens
            .iter()
            .find(|screen| {
                let frame = screen.frame();
                mouse.x >= frame.origin.x
                    && mouse.x < frame.origin.x + frame.size.width
                    && mouse.y >= frame.origin.y
                    && mouse.y < frame.origin.y + frame.size.height
            })
            .expect("mouse is on a screen");
        let frame = screen.visibleFrame();
        let size = native.frame().size;
        native.setFrameOrigin(NSPoint::new(
            frame.origin.x + (frame.size.width - size.width) / 2.0,
            frame.origin.y + (frame.size.height - size.height) / 2.0,
        ));
        focus_panel(app);
        log_focus(app, "after open");
        let _ = window.emit("capture-open", opened_at);
    }

    fn focus_panel(app: &tauri::AppHandle) {
        let window = app
            .get_webview_window("capture")
            .expect("enabled capture window");
        let panel = app
            .get_webview_panel("capture")
            .expect("enabled capture panel");
        panel.order_front_regardless();
        panel.make_key_window();
        let webview: &tauri::Webview = window.as_ref();
        let _ = webview.set_focus();
    }

    fn record_trace(app: &tauri::AppHandle, message: String) {
        if crate::instance::automation_enabled() {
            let at = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_millis();
            app.state::<Mutex<QuickCaptureState>>()
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
            .expect("enabled capture window");
        let frontmost_is_ours = NSWorkspace::sharedWorkspace()
            .frontmostApplication()
            .is_some_and(|a| a.processIdentifier() == std::process::id() as i32);
        let ours = window.is_focused().unwrap_or(false) && frontmost_is_ours;
        let (origin, was_hidden) = {
            let state = app.state::<Mutex<QuickCaptureState>>();
            let mut state = state.lock().unwrap();
            (state.origin.take(), std::mem::take(&mut state.was_hidden))
        };
        let ns_app = NSApplication::sharedApplication(
            MainThreadMarker::new().expect("capture hides on the main thread"),
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
        let state = app.state::<Mutex<QuickCaptureState>>();
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
        Ok(())
    }

    pub fn status(app: &tauri::AppHandle) -> super::QuickCaptureStatus {
        let state = app.state::<Mutex<QuickCaptureState>>();
        let state = state.lock().unwrap();
        super::QuickCaptureStatus {
            binding: state.preference.clone(),
            active: state.binding.map(|binding| binding.to_string()),
            diagnostics: state.diagnostics.clone(),
        }
    }

    pub fn cache(app: &tauri::AppHandle, binding: Option<String>) -> Result<(), String> {
        super::save_preference(&binding)?;
        app.state::<Mutex<QuickCaptureState>>()
            .lock()
            .unwrap()
            .preference = binding;
        Ok(())
    }

    pub fn shutdown(app: &tauri::AppHandle) {
        let manager = {
            let state = app.state::<Mutex<QuickCaptureState>>();
            let mut state = state.lock().unwrap();
            state.binding = None;
            state.manager.take()
        };
        drop(manager);
    }

    pub fn install(app: &mut tauri::App) -> Result<(), Box<dyn std::error::Error>> {
        super::forget_downloads();
        let cached = super::load_preference();
        app.manage(Mutex::new(QuickCaptureState {
            manager: Some(GlobalHotKeyManager::new()?),
            preference: cached.clone(),
            binding: None,
            origin: None,
            was_hidden: false,
            diagnostics: Vec::new(),
        }));
        app.handle().plugin(tauri_nspanel::init())?;
        let handle = app.handle().clone();
        GlobalHotKeyEvent::set_event_handler(Some(move |event: GlobalHotKeyEvent| {
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
            if let Some(ms) = std::env::var("ATTN_SPIKE_HOTKEY_DELAY_MS")
                .ok()
                .and_then(|v| v.parse::<u64>().ok())
            {
                std::thread::sleep(std::time::Duration::from_millis(ms));
            }
            let current = handle
                .state::<Mutex<QuickCaptureState>>()
                .lock()
                .unwrap()
                .binding
                .map(|binding| binding.id());
            if current != Some(event.id) {
                return;
            }
            let app = handle.clone();
            let _ = handle.run_on_main_thread(move || {
                show(&app);
            });
        }));
        Ok(())
    }

    pub fn enable(app: &tauri::AppHandle) -> Result<(), String> {
        on_main(app, |app| {
            if app.get_webview_window("capture").is_none() {
                create_panel(app).map_err(|error| error.to_string())?;
            }
            set_menu_enabled(app, true)
        })
    }

    pub fn disable(app: &tauri::AppHandle) -> Result<(), String> {
        on_main(app, |app| {
            if app.get_webview_window("capture").is_some() {
                hide_on_main(app)?;
            }
            set_menu_enabled(app, false)
        })
    }

    fn set_menu_enabled(app: &tauri::AppHandle, enabled: bool) -> Result<(), String> {
        app.menu()
            .expect("app menu")
            .items()
            .map_err(|error| error.to_string())?
            .into_iter()
            .find_map(|item| item.as_submenu()?.get("quick-capture"))
            .expect("capture menu item")
            .as_menuitem()
            .expect("capture menu item")
            .set_enabled(enabled)
            .map_err(|error| error.to_string())
    }

    fn create_panel(app: &tauri::AppHandle) -> Result<(), Box<dyn std::error::Error>> {
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
        let panel = window.to_panel::<QuickCapturePanel>()?;
        panel.add_style_mask(
            tauri_nspanel::StyleMask::empty()
                .nonactivating_panel()
                .into(),
        )?;
        panel.set_hides_on_deactivate(false);
        let ns_window: &NSWindow = panel.as_panel();
        ns_window.setCanHide(false);
        ns_window.setLevel(objc2_app_kit::NSModalPanelWindowLevel);
        ns_window.setCollectionBehavior(
            NSWindowCollectionBehavior::CanJoinAllSpaces
                | NSWindowCollectionBehavior::CanJoinAllApplications
                | NSWindowCollectionBehavior::FullScreenAuxiliary,
        );
        window.on_window_event({
            let handle = app.clone();
            move |event| {
                if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    let _ = hide(&handle);
                }
            }
        });
        window.on_webview_event({
            let handle = app.clone();
            move |event| {
                if !matches!(
                    event,
                    tauri::WebviewEvent::DragDrop(tauri::DragDropEvent::Drop { .. })
                ) {
                    return;
                }
                let app = handle.clone();
                tauri::async_runtime::spawn(async move {
                    let focused = app.clone();
                    let _ = app.run_on_main_thread(move || {
                        log_focus(&focused, "before drop focus");
                        focus_panel(&focused);
                        log_focus(&focused, "after drop focus");
                    });
                });
            }
        });
        Ok(())
    }
}

#[tauri::command]
pub fn capture_enable(app: AppHandle) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    return mac::enable(&app);
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        Err("Quick Capture is available on macOS".into())
    }
}

#[tauri::command]
pub fn capture_disable(app: AppHandle) -> Result<(), String> {
    #[cfg(target_os = "macos")]
    return mac::disable(&app);
    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        Err("Quick Capture is available on macOS".into())
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
pub struct QuickCaptureStatus {
    pub binding: Option<String>,
    pub active: Option<String>,
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
            Some("Control+Alt+Super+Space".into())
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
pub fn capture_status(app: AppHandle) -> Result<QuickCaptureStatus, String> {
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

fn is_uuid(id: &str) -> bool {
    id.len() == 36
        && id.bytes().enumerate().all(|(index, byte)| {
            if matches!(index, 8 | 13 | 18 | 23) {
                byte == b'-'
            } else {
                byte.is_ascii_hexdigit()
            }
        })
}

fn profile_dir(root: &str, profile_id: &str) -> Result<std::path::PathBuf, String> {
    let id = profile_id
        .strip_prefix("profile-")
        .ok_or("Quick Capture needs a profile identity")?;
    if !is_uuid(id) {
        return Err("Quick Capture profile id must contain a UUID".into());
    }
    Ok(crate::instance::data_dir()?.join(root).join(profile_id))
}

fn draft_dir(profile_id: &str) -> Result<std::path::PathBuf, String> {
    profile_dir("quick-capture-drafts", profile_id)
}

fn draft_files_dir(profile_id: &str) -> Result<std::path::PathBuf, String> {
    Ok(draft_dir(profile_id)?.join("capture-draft-files"))
}

fn draft_file_name(id: &str) -> Result<String, String> {
    if !is_uuid(id) {
        return Err("Draft file id must be a UUID".into());
    }
    Ok(format!("{id}.file"))
}

fn draft_file_path(profile_id: &str, id: &str) -> Result<std::path::PathBuf, String> {
    Ok(draft_files_dir(profile_id)?.join(draft_file_name(id)?))
}

fn download_path(
    profile_id: &str,
    capture_id: &str,
    attachment_id: &str,
) -> Result<std::path::PathBuf, String> {
    if !is_uuid(capture_id) || !is_uuid(attachment_id) {
        return Err("Capture and attachment ids must be UUIDs".into());
    }
    Ok(profile_dir("quick-capture-downloads", profile_id)?
        .join(format!("{capture_id}-{attachment_id}")))
}

fn io_error(path: &std::path::Path) -> impl Fn(std::io::Error) -> String + '_ {
    move |error| format!("{}: {error}", path.display())
}

fn commit(temporary: &std::path::Path, path: &std::path::Path) -> Result<(), String> {
    std::fs::File::open(temporary)
        .and_then(|file| file.sync_all())
        .map_err(io_error(temporary))?;
    std::fs::rename(temporary, path).map_err(io_error(path))?;
    let parent = path.parent().ok_or("Cache path has no parent")?;
    std::fs::File::open(parent)
        .and_then(|dir| dir.sync_all())
        .map_err(io_error(parent))
}

fn write_cache(path: &std::path::Path, bytes: &[u8]) -> Result<(), String> {
    let parent = path.parent().ok_or("Cache path has no parent")?;
    std::fs::create_dir_all(parent).map_err(io_error(parent))?;
    let temporary = path.with_extension("tmp");
    std::fs::write(&temporary, bytes).map_err(io_error(&temporary))?;
    commit(&temporary, path)
}

fn append_at(path: &std::path::Path, offset: u64, bytes: &[u8]) -> Result<(), String> {
    use std::io::Write;
    let parent = path.parent().ok_or("Cache path has no parent")?;
    std::fs::create_dir_all(parent).map_err(io_error(parent))?;
    let mut file = if offset == 0 {
        std::fs::File::create(path)
    } else {
        std::fs::OpenOptions::new().append(true).open(path)
    }
    .map_err(io_error(path))?;
    let length = file.metadata().map_err(io_error(path))?.len();
    if length != offset {
        return Err(format!(
            "{} holds {length} bytes, asked to write at offset {offset}",
            path.display()
        ));
    }
    file.write_all(bytes).map_err(io_error(path))
}

#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
pub struct StoredFile {
    size: u64,
    media_type: String,
    path: String,
}

fn stored_file(app: &AppHandle, path: &std::path::Path, name: &str) -> Result<StoredFile, String> {
    use std::io::Read;
    use tauri::Manager;
    let mut head = Vec::new();
    let file = std::fs::File::open(path).map_err(io_error(path))?;
    let size = file.metadata().map_err(io_error(path))?.len();
    file.take(8192)
        .read_to_end(&mut head)
        .map_err(io_error(path))?;
    let directory = path.parent().ok_or("Stored file has no parent")?;
    app.asset_protocol_scope()
        .allow_directory(directory, false)
        .map_err(|error| error.to_string())?;
    Ok(StoredFile {
        size,
        media_type: tauri::utils::mime_type::MimeType::parse_with_fallback(
            &head,
            name,
            tauri::utils::mime_type::MimeType::OctetStream,
        ),
        path: path.to_string_lossy().into_owned(),
    })
}

fn draft_file_ids(draft: &serde_json::Value) -> Result<Vec<String>, String> {
    draft["files"]
        .as_array()
        .ok_or("Draft files are missing")?
        .iter()
        .map(|file| draft_file_name(file["id"].as_str().ok_or("Draft file id is missing")?))
        .collect()
}

fn stored_draft(dir: &std::path::Path) -> Result<Option<serde_json::Value>, String> {
    let path = dir.join("quick-capture-draft.json");
    match std::fs::read(&path) {
        Ok(bytes) => serde_json::from_slice(&bytes)
            .map(Some)
            .map_err(|error| error.to_string()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(io_error(&path)(error)),
    }
}

fn read_draft(app: &AppHandle, profile_id: String) -> Result<Option<serde_json::Value>, String> {
    let dir = draft_dir(&profile_id)?;
    let files_dir = draft_files_dir(&profile_id)?;
    let Some(mut draft) = stored_draft(&dir)? else {
        return Ok(None);
    };
    let kept = draft_file_ids(&draft)?;
    if let Ok(entries) = std::fs::read_dir(&files_dir) {
        for entry in entries.flatten() {
            if !kept.contains(&entry.file_name().to_string_lossy().into_owned()) {
                let _ = std::fs::remove_file(entry.path());
            }
        }
    }
    for file in draft["files"]
        .as_array_mut()
        .ok_or("Draft files are missing")?
    {
        let id = file["id"].as_str().ok_or("Draft file id is missing")?;
        let name = file["name"].as_str().unwrap_or_default().to_owned();
        let stored = stored_file(app, &draft_file_path(&profile_id, id)?, &name)?;
        file["size"] = stored.size.into();
        file["mediaType"] = stored.media_type.into();
        file["path"] = stored.path.into();
    }
    Ok(Some(draft))
}

fn write_draft(profile_id: String, draft: serde_json::Value) -> Result<(), String> {
    let dir = draft_dir(&profile_id)?;
    let files_dir = draft_files_dir(&profile_id)?;
    let kept = draft_file_ids(&draft)?;
    let previous = match stored_draft(&dir) {
        Ok(Some(previous)) => draft_file_ids(&previous)?,
        _ => Vec::new(),
    };
    write_cache(
        &dir.join("quick-capture-draft.json"),
        &serde_json::to_vec(&draft).map_err(|error| error.to_string())?,
    )?;
    for name in previous.iter().filter(|name| !kept.contains(name)) {
        match std::fs::remove_file(files_dir.join(name)) {
            Err(error) if error.kind() != std::io::ErrorKind::NotFound => {
                return Err(io_error(&files_dir.join(name))(error))
            }
            _ => {}
        }
    }
    Ok(())
}

async fn blocking<T: Send + 'static>(
    work: impl FnOnce() -> Result<T, String> + Send + 'static,
) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(work)
        .await
        .map_err(|error| error.to_string())?
}

#[tauri::command]
pub async fn quick_capture_draft_read(
    app: AppHandle,
    profile_id: String,
) -> Result<Option<serde_json::Value>, String> {
    blocking(move || read_draft(&app, profile_id)).await
}

#[tauri::command]
pub async fn quick_capture_draft_write(
    profile_id: String,
    draft: serde_json::Value,
) -> Result<(), String> {
    blocking(move || write_draft(profile_id, draft)).await
}

#[tauri::command]
pub async fn quick_capture_draft_file_copy(
    app: AppHandle,
    profile_id: String,
    id: String,
    path: String,
) -> Result<StoredFile, String> {
    blocking(move || {
        let target = draft_file_path(&profile_id, &id)?;
        let parent = target.parent().ok_or("Draft file has no parent")?;
        std::fs::create_dir_all(parent).map_err(io_error(parent))?;
        let temporary = target.with_extension("tmp");
        std::fs::copy(&path, &temporary).map_err(|error| format!("Cannot read {path}: {error}"))?;
        commit(&temporary, &target)?;
        stored_file(&app, &target, &path)
    })
    .await
}

#[tauri::command]
pub async fn quick_capture_draft_file_append(
    request: tauri::ipc::Request<'_>,
) -> Result<(), String> {
    let tauri::ipc::InvokeBody::Raw(bytes) = request.body() else {
        return Err("Draft file chunks arrive as raw bytes".into());
    };
    let header = |name: &str| {
        request
            .headers()
            .get(name)
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned)
            .ok_or(format!("Draft file chunk is missing the {name} header"))
    };
    let path = draft_file_path(&header("profile-id")?, &header("file-id")?)?.with_extension("part");
    let offset = header("offset")?
        .parse::<u64>()
        .map_err(|error| format!("Draft file chunk offset: {error}"))?;
    let bytes = bytes.clone();
    blocking(move || append_at(&path, offset, &bytes)).await
}

#[tauri::command]
pub async fn quick_capture_draft_file_finish(
    app: AppHandle,
    profile_id: String,
    id: String,
    name: String,
) -> Result<StoredFile, String> {
    blocking(move || {
        let target = draft_file_path(&profile_id, &id)?;
        commit(&target.with_extension("part"), &target)?;
        stored_file(&app, &target, &name)
    })
    .await
}

#[tauri::command]
pub async fn quick_capture_draft_clipboard_image(
    app: AppHandle,
    profile_id: String,
    id: String,
) -> Result<StoredFile, String> {
    use tauri_plugin_clipboard_manager::ClipboardExt;
    blocking(move || {
        let image = app
            .clipboard()
            .read_image()
            .map_err(|error| error.to_string())?;
        let pixels =
            image::RgbaImage::from_raw(image.width(), image.height(), image.rgba().to_vec())
                .ok_or("Clipboard image size does not match its pixels")?;
        let target = draft_file_path(&profile_id, &id)?;
        let parent = target.parent().ok_or("Draft file has no parent")?;
        std::fs::create_dir_all(parent).map_err(io_error(parent))?;
        let temporary = target.with_extension("tmp");
        pixels
            .save_with_format(&temporary, image::ImageFormat::Png)
            .map_err(|error| error.to_string())?;
        commit(&temporary, &target)?;
        stored_file(&app, &target, "Pasted screenshot.png")
    })
    .await
}

#[tauri::command]
pub async fn quick_capture_draft_file_chunk(
    profile_id: String,
    id: String,
    offset: u64,
    length: u64,
) -> Result<String, String> {
    blocking(move || {
        use base64::Engine;
        use std::io::{Read, Seek};
        let path = draft_file_path(&profile_id, &id)?;
        let mut file = std::fs::File::open(&path).map_err(io_error(&path))?;
        file.seek(std::io::SeekFrom::Start(offset))
            .map_err(io_error(&path))?;
        let mut bytes = Vec::new();
        file.take(length)
            .read_to_end(&mut bytes)
            .map_err(io_error(&path))?;
        Ok(base64::engine::general_purpose::STANDARD.encode(bytes))
    })
    .await
}

#[tauri::command]
pub async fn quick_capture_download_find(
    app: AppHandle,
    profile_id: String,
    capture_id: String,
    attachment_id: String,
) -> Result<Option<String>, String> {
    blocking(move || {
        let path = download_path(&profile_id, &capture_id, &attachment_id)?;
        if !path.exists() {
            return Ok(None);
        }
        Ok(Some(stored_file(&app, &path, "")?.path))
    })
    .await
}

#[tauri::command]
pub async fn quick_capture_download_append(
    app: AppHandle,
    profile_id: String,
    capture_id: String,
    attachment_id: String,
    offset: u64,
    data_base64: String,
    last: bool,
) -> Result<Option<String>, String> {
    blocking(move || {
        use base64::Engine;
        let bytes = base64::engine::general_purpose::STANDARD
            .decode(data_base64)
            .map_err(|error| error.to_string())?;
        let path = download_path(&profile_id, &capture_id, &attachment_id)?;
        let partial = path.with_extension("part");
        append_at(&partial, offset, &bytes)?;
        if !last {
            return Ok(None);
        }
        commit(&partial, &path)?;
        Ok(Some(stored_file(&app, &path, "")?.path))
    })
    .await
}

#[cfg(target_os = "macos")]
fn forget_downloads() {
    if let Ok(dir) = crate::instance::data_dir() {
        let _ = std::fs::remove_dir_all(dir.join("quick-capture-downloads"));
    }
}
