use std::collections::HashMap;
use std::ptr::NonNull;
use std::sync::{LazyLock, Mutex};

use block2::RcBlock;
use objc2::MainThreadMarker;
use objc2_app_kit::{NSApplication, NSEvent, NSEventMask, NSEventModifierFlags};
use objc2_web_kit::WKWebView;
use serde::Deserialize;
use tauri::{AppHandle, Manager};

use crate::browser_host::TrustedMainWebview;

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Combo {
    key: String,
    code: Option<String>,
    #[serde(default)]
    meta: bool,
    #[serde(default)]
    shift: bool,
    #[serde(default)]
    alt: bool,
}

#[derive(Clone, Deserialize)]
#[serde(untagged)]
pub enum Binding {
    Chord { leader: Combo },
    Single(Combo),
}

#[derive(Clone, Deserialize)]
pub struct Chord {
    id: String,
    leader: Combo,
    then: Combo,
}

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum PageAction {
    Back,
    Forward,
    ZoomIn,
    ZoomOut,
    ZoomReset,
}

#[derive(Deserialize)]
pub struct PageKey {
    action: PageAction,
    combo: Combo,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Configuration {
    pop_out: Option<Binding>,
    page_keys: Vec<PageKey>,
    chords: Vec<Chord>,
    timeout_ms: f64,
    default_menu: bool,
}

struct Key {
    key: String,
    code: String,
    meta: bool,
    shift: bool,
    alt: bool,
    ctrl: bool,
    repeat: bool,
    timestamp: f64,
}

impl Combo {
    fn matches(&self, key: &Key) -> bool {
        let code = self.code.as_deref().filter(|code| !code.is_empty());
        let key_matches = if self.alt && code.is_some() {
            code == Some(key.code.as_str())
        } else {
            self.key.to_lowercase() == key.key.to_lowercase() || code == Some(key.code.as_str())
        };
        key_matches
            && (if self.meta {
                key.meta
            } else {
                !key.meta && !key.ctrl
            })
            && self.shift == key.shift
            && self.alt == key.alt
    }
}

struct Pending {
    label: String,
    leader: Combo,
    candidates: Vec<Chord>,
    started: f64,
}

#[derive(Default)]
struct State {
    config: Option<Configuration>,
    pending: Option<Pending>,
    previous: Option<(usize, u16, f64)>,
    capture_suspended: bool,
}

static STATE: LazyLock<Mutex<State>> = LazyLock::new(|| Mutex::new(State::default()));
static BROWSERS: LazyLock<Mutex<HashMap<usize, String>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

pub fn register_browser(pointer: usize, label: String) {
    if let Ok(mut browsers) = BROWSERS.lock() {
        browsers.insert(pointer, label);
    }
}

pub fn forget_browser(label: &str) {
    if let Ok(mut browsers) = BROWSERS.lock() {
        browsers.retain(|_, value| value != label);
    }
}

#[tauri::command]
pub fn browser_shortcut_configure(
    _caller: TrustedMainWebview,
    app: AppHandle,
    config: Configuration,
) -> Result<(), String> {
    let default_menu = config.default_menu;
    let suspended = {
        let mut state = STATE.lock().map_err(|error| error.to_string())?;
        state.config = Some(config);
        state.pending = None;
        state.capture_suspended
    };
    set_menu_accelerator(&app, default_menu && !suspended)
}

fn set_menu_accelerator(app: &AppHandle, enabled: bool) -> Result<(), String> {
    let accelerator = enabled.then_some("CmdOrCtrl+Shift+O");
    if let Some(menu) = app.menu() {
        for item in menu.items().map_err(|error| error.to_string())? {
            if let tauri::menu::MenuItemKind::Submenu(submenu) = item {
                let items = submenu.items().map_err(|error| error.to_string())?;
                if let Some((position, tauri::menu::MenuItemKind::MenuItem(item))) = items
                    .into_iter()
                    .enumerate()
                    .find(|(_, item)| item.id() == crate::POP_OUT_TILE_MENU_ID)
                {
                    item.set_accelerator(accelerator)
                        .map_err(|error| error.to_string())?;
                    if accelerator.is_none() {
                        submenu.remove(&item).map_err(|error| error.to_string())?;
                        submenu
                            .insert(&item, position)
                            .map_err(|error| error.to_string())?;
                    }
                    break;
                }
            }
        }
    }
    Ok(())
}

#[tauri::command]
pub fn browser_shortcut_capture(
    _caller: TrustedMainWebview,
    app: AppHandle,
    suspended: bool,
) -> Result<(), String> {
    let default_menu = {
        let mut state = STATE.lock().map_err(|error| error.to_string())?;
        state.capture_suspended = suspended;
        state.pending = None;
        state
            .config
            .as_ref()
            .is_some_and(|config| config.default_menu)
    };
    set_menu_accelerator(&app, default_menu && !suspended)
}

fn key(event: &NSEvent) -> Key {
    let flags = event.modifierFlags();
    let meta = flags.contains(NSEventModifierFlags::Command);
    let shift = flags.contains(NSEventModifierFlags::Shift);
    let alt = flags.contains(NSEventModifierFlags::Option);
    let ctrl = flags.contains(NSEventModifierFlags::Control);
    let code = format!(
        "{:?}",
        tao::keyboard::KeyCode::from_scancode(event.keyCode().into())
    );
    let named = match code.as_str() {
        "Enter" | "NumpadEnter" => Some("Enter"),
        "Tab" => Some("Tab"),
        "Space" => Some(" "),
        "Backspace" => Some("Backspace"),
        "Delete" => Some("Delete"),
        "Escape" => Some("Escape"),
        "ArrowLeft" | "ArrowRight" | "ArrowUp" | "ArrowDown" | "Home" | "End" | "PageUp"
        | "PageDown" | "Insert" => Some(code.as_str()),
        name if name.starts_with('F') && name[1..].bytes().all(|byte| byte.is_ascii_digit()) => {
            Some(name)
        }
        _ => None,
    };
    let characters = if alt && !ctrl {
        event.characters()
    } else {
        event.charactersIgnoringModifiers()
    };
    Key {
        key: named.map(str::to_owned).unwrap_or_else(|| {
            characters
                .map(|value| value.to_string())
                .unwrap_or_default()
        }),
        code,
        meta,
        shift,
        alt,
        ctrl,
        repeat: event.isARepeat(),
        timestamp: event.timestamp(),
    }
}

enum Decision {
    Pass,
    Swallow,
    Shortcut(String),
    Page(PageAction),
}

fn decide(state: &mut State, label: &str, key: &Key) -> Decision {
    if state.capture_suspended {
        return Decision::Pass;
    }
    if key.meta && !key.ctrl && !key.alt && !key.shift && key.key.eq_ignore_ascii_case("w") {
        state.pending = None;
        return Decision::Pass;
    }
    let Some(config) = state.config.as_ref() else {
        return Decision::Pass;
    };
    if state.pending.as_ref().is_some_and(|pending| {
        pending.label != label || (key.timestamp - pending.started) * 1000.0 >= config.timeout_ms
    }) {
        state.pending = None;
    }
    if let Some(pending) = state.pending.as_mut() {
        if key.key == "Escape" {
            state.pending = None;
            return Decision::Swallow;
        }
        if let Some(candidate) = pending
            .candidates
            .iter()
            .find(|candidate| candidate.then.matches(key))
        {
            let id = candidate.id.clone();
            state.pending = None;
            return Decision::Shortcut(id);
        }
        if key.repeat || pending.leader.matches(key) {
            pending.started = key.timestamp;
            return Decision::Swallow;
        }
        state.pending = None;
        return Decision::Swallow;
    }
    if let Some(page_key) = config
        .page_keys
        .iter()
        .find(|page_key| page_key.combo.matches(key))
    {
        return Decision::Page(page_key.action);
    }
    match config.pop_out.as_ref() {
        Some(Binding::Single(combo)) if combo.matches(key) => {
            Decision::Shortcut("tile.popOut".into())
        }
        Some(Binding::Chord { leader, .. }) if leader.matches(key) => {
            let candidates = config
                .chords
                .iter()
                .filter(|candidate| candidate.leader.matches(key))
                .cloned()
                .collect();
            state.pending = Some(Pending {
                label: label.into(),
                leader: leader.clone(),
                candidates,
                started: key.timestamp,
            });
            Decision::Swallow
        }
        _ => Decision::Pass,
    }
}

const PAGE_ZOOM_STEPS: [f64; 11] = [0.5, 0.75, 0.85, 1.0, 1.15, 1.25, 1.5, 1.75, 2.0, 2.5, 3.0];

fn zoom_step_above(current: f64) -> f64 {
    PAGE_ZOOM_STEPS
        .into_iter()
        .find(|step| *step > current + f64::EPSILON)
        .unwrap_or(current)
}

fn zoom_step_below(current: f64) -> f64 {
    PAGE_ZOOM_STEPS
        .into_iter()
        .rev()
        .find(|step| *step < current - f64::EPSILON)
        .unwrap_or(current)
}

fn act_on_page(web: &WKWebView, action: PageAction) {
    unsafe {
        match action {
            PageAction::Back => web.goBack_(None),
            PageAction::Forward => web.goForward_(None),
            PageAction::ZoomIn => web.setPageZoom(zoom_step_above(web.pageZoom())),
            PageAction::ZoomOut => web.setPageZoom(zoom_step_below(web.pageZoom())),
            PageAction::ZoomReset => web.setPageZoom(1.0),
        }
    }
}

fn dispatch(app: &AppHandle, label: &str, id: &str) {
    let Some(main) = app.get_webview("main") else {
        return;
    };
    let detail = serde_json::json!({ "label": label, "id": id });
    let _ = main.eval(format!("window.dispatchEvent(new CustomEvent('attn:native-browser-shortcut', {{ detail: {detail} }}));"));
}

pub fn install(app: &AppHandle) {
    let app = app.clone();
    let block = RcBlock::new(move |event: NonNull<NSEvent>| -> *mut NSEvent {
        let event_ref = unsafe { event.as_ref() };
        let mtm = unsafe { MainThreadMarker::new_unchecked() };
        let responder = NSApplication::sharedApplication(mtm)
            .keyWindow()
            .and_then(|window| window.firstResponder())
            .map(|responder| std::ptr::from_ref(&*responder) as usize);
        let target = responder.and_then(|pointer| {
            BROWSERS
                .lock()
                .ok()
                .and_then(|browsers| browsers.get(&pointer).cloned())
                .map(|label| (pointer, label))
        });
        let Some((responder, label)) = target else {
            if let Ok(mut state) = STATE.lock() {
                state.pending = None;
            }
            return event.as_ptr();
        };
        let key = key(event_ref);
        let Ok(mut state) = STATE.lock() else {
            return event.as_ptr();
        };
        let fingerprint = (responder, event_ref.keyCode(), key.timestamp);
        if state
            .previous
            .is_some_and(|previous| key.timestamp < previous.2 || previous == fingerprint)
        {
            return event.as_ptr();
        }
        state.previous = Some(fingerprint);
        let decision = decide(&mut state, &label, &key);
        drop(state);
        match decision {
            Decision::Pass => return event.as_ptr(),
            Decision::Swallow => {}
            Decision::Shortcut(id) => dispatch(&app, &label, &id),
            Decision::Page(action) => {
                act_on_page(unsafe { &*(responder as *const WKWebView) }, action)
            }
        }
        std::ptr::null_mut()
    });
    let monitor = unsafe {
        NSEvent::addLocalMonitorForEventsMatchingMask_handler(NSEventMask::KeyDown, &block)
    };
    std::mem::forget(monitor);
    std::mem::forget(block);
}
