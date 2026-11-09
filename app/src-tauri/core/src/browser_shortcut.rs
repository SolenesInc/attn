use std::collections::HashMap;
use std::ptr::NonNull;
use std::sync::{LazyLock, Mutex};

use block2::RcBlock;
use objc2::MainThreadMarker;
use objc2_app_kit::{NSApplication, NSEvent, NSEventMask, NSEventModifierFlags};
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

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Configuration {
    pop_out: Option<Binding>,
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
        let key_matches = if self.alt && self.code.is_some() {
            self.code.as_deref() == Some(key.code.as_str())
        } else {
            self.key.to_lowercase() == key.key.to_lowercase()
                || self.code.as_deref() == Some(key.code.as_str())
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
                if let Some(tauri::menu::MenuItemKind::MenuItem(item)) =
                    submenu.get(crate::POP_OUT_TILE_MENU_ID)
                {
                    item.set_accelerator(accelerator)
                        .map_err(|error| error.to_string())?;
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

fn decide(state: &mut State, label: &str, key: &Key) -> (bool, Option<String>) {
    if state.capture_suspended {
        return (false, None);
    }
    if key.meta && !key.ctrl && !key.alt && !key.shift && key.key.eq_ignore_ascii_case("w") {
        state.pending = None;
        return (false, None);
    }
    let Some(config) = state.config.as_ref() else {
        return (false, None);
    };
    if state.pending.as_ref().is_some_and(|pending| {
        pending.label != label || (key.timestamp - pending.started) * 1000.0 >= config.timeout_ms
    }) {
        state.pending = None;
    }
    if let Some(pending) = state.pending.as_mut() {
        if key.key == "Escape" {
            state.pending = None;
            return (true, None);
        }
        if let Some(candidate) = pending
            .candidates
            .iter()
            .find(|candidate| candidate.then.matches(key))
        {
            let id = candidate.id.clone();
            state.pending = None;
            return (true, Some(id));
        }
        if key.repeat || pending.leader.matches(key) {
            pending.started = key.timestamp;
            return (true, None);
        }
        state.pending = None;
        return (true, None);
    }
    match config.pop_out.as_ref() {
        Some(Binding::Single(combo)) if combo.matches(key) => (true, Some("tile.popOut".into())),
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
            (true, None)
        }
        _ => (false, None),
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
        let (consumed, id) = decide(&mut state, &label, &key);
        drop(state);
        if let Some(id) = id {
            dispatch(&app, &label, &id);
        }
        if consumed {
            std::ptr::null_mut()
        } else {
            event.as_ptr()
        }
    });
    let monitor = unsafe {
        NSEvent::addLocalMonitorForEventsMatchingMask_handler(NSEventMask::KeyDown, &block)
    };
    std::mem::forget(monitor);
    std::mem::forget(block);
}
