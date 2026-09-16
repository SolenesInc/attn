use serde_json::Value;
use tauri::{AppHandle, Runtime};

pub const ACTIONS: [&str; 3] = ["native_key", "native_text", "native_mouse"];

const DEFAULT_WINDOW_LABEL: &str = "main";

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Modifiers {
    pub command: bool,
    pub option: bool,
    pub shift: bool,
    pub control: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct KeyStroke {
    pub key_code: u16,
    pub characters: String,
    pub characters_ignoring_modifiers: String,
    pub modifiers: Modifiers,
    pub function_key: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum MouseButton {
    Left,
    Right,
}

#[derive(Clone, Debug, PartialEq)]
pub enum MouseGesture {
    Move {
        to: (f64, f64),
    },
    Click {
        at: (f64, f64),
        button: MouseButton,
        click_count: i64,
    },
    Drag {
        from: (f64, f64),
        to: (f64, f64),
        steps: usize,
    },
}

#[derive(Clone, Debug, PartialEq)]
pub struct MouseRequest {
    pub gesture: MouseGesture,
    pub modifiers: Modifiers,
}

const NAMED_KEYS: &[(&str, u16, &str)] = &[
    ("enter", 36, "\r"),
    ("return", 36, "\r"),
    ("tab", 48, "\t"),
    ("space", 49, " "),
    ("backspace", 51, "\u{7f}"),
    ("delete", 51, "\u{7f}"),
    ("escape", 53, "\u{1b}"),
    ("home", 115, "\u{f729}"),
    ("pageup", 116, "\u{f72c}"),
    ("forwarddelete", 117, "\u{f728}"),
    ("end", 119, "\u{f72b}"),
    ("pagedown", 121, "\u{f72d}"),
    ("arrowleft", 123, "\u{f702}"),
    ("arrowright", 124, "\u{f703}"),
    ("arrowdown", 125, "\u{f701}"),
    ("arrowup", 126, "\u{f700}"),
];

const FUNCTION_KEY_CODES: &[u16] = &[115, 116, 117, 119, 121, 123, 124, 125, 126];

const PRINTABLE_KEYS: &[(char, char, u16)] = &[
    ('a', 'A', 0),
    ('s', 'S', 1),
    ('d', 'D', 2),
    ('f', 'F', 3),
    ('h', 'H', 4),
    ('g', 'G', 5),
    ('z', 'Z', 6),
    ('x', 'X', 7),
    ('c', 'C', 8),
    ('v', 'V', 9),
    ('b', 'B', 11),
    ('q', 'Q', 12),
    ('w', 'W', 13),
    ('e', 'E', 14),
    ('r', 'R', 15),
    ('y', 'Y', 16),
    ('t', 'T', 17),
    ('1', '!', 18),
    ('2', '@', 19),
    ('3', '#', 20),
    ('4', '$', 21),
    ('6', '^', 22),
    ('5', '%', 23),
    ('=', '+', 24),
    ('9', '(', 25),
    ('7', '&', 26),
    ('-', '_', 27),
    ('8', '*', 28),
    ('0', ')', 29),
    (']', '}', 30),
    ('o', 'O', 31),
    ('u', 'U', 32),
    ('[', '{', 33),
    ('i', 'I', 34),
    ('p', 'P', 35),
    ('l', 'L', 37),
    ('j', 'J', 38),
    ('\'', '"', 39),
    ('k', 'K', 40),
    (';', ':', 41),
    ('\\', '|', 42),
    (',', '<', 43),
    ('/', '?', 44),
    ('n', 'N', 45),
    ('m', 'M', 46),
    ('.', '>', 47),
    (' ', ' ', 49),
    ('`', '~', 50),
];

pub fn parse_modifiers(value: Option<&Value>) -> Result<Modifiers, String> {
    let names: Vec<String> = match value {
        None | Some(Value::Null) => Vec::new(),
        Some(Value::String(joined)) => joined
            .split(',')
            .map(str::trim)
            .filter(|name| !name.is_empty())
            .map(str::to_string)
            .collect(),
        Some(Value::Array(items)) => items
            .iter()
            .map(|item| {
                item.as_str()
                    .map(str::to_string)
                    .ok_or_else(|| format!("modifiers must be strings, got {item}"))
            })
            .collect::<Result<_, _>>()?,
        Some(other) => {
            return Err(format!(
                "modifiers must be a string list or comma-joined string, got {other}"
            ))
        }
    };
    let mut modifiers = Modifiers::default();
    for name in names {
        match name.to_ascii_lowercase().as_str() {
            "command" | "cmd" | "meta" => modifiers.command = true,
            "option" | "alt" => modifiers.option = true,
            "shift" => modifiers.shift = true,
            "control" | "ctrl" => modifiers.control = true,
            other => {
                return Err(format!(
                    "unknown modifier {other:?}; use command, option, shift or control"
                ))
            }
        }
    }
    Ok(modifiers)
}

fn control_character(base: char) -> Option<char> {
    match base {
        'a'..='z' => char::from_u32(base as u32 - 'a' as u32 + 1),
        '@' | '[' | '\\' | ']' | '^' | '_' => char::from_u32(base as u32 - '@' as u32),
        _ => None,
    }
}

fn printable_stroke(
    unshifted: char,
    shifted: char,
    key_code: u16,
    modifiers: Modifiers,
) -> KeyStroke {
    let base = if modifiers.shift { shifted } else { unshifted };
    let characters = if modifiers.control {
        control_character(base).unwrap_or(base)
    } else {
        base
    };
    KeyStroke {
        key_code,
        characters: characters.to_string(),
        characters_ignoring_modifiers: base.to_string(),
        modifiers,
        function_key: false,
    }
}

fn named_stroke(key_code: u16, characters: &str, modifiers: Modifiers) -> KeyStroke {
    KeyStroke {
        key_code,
        characters: characters.to_string(),
        characters_ignoring_modifiers: characters.to_string(),
        modifiers,
        function_key: FUNCTION_KEY_CODES.contains(&key_code),
    }
}

pub fn key_stroke(key: &str, modifiers: Modifiers) -> Result<KeyStroke, String> {
    let mut chars = key.chars();
    if let (Some(single), None) = (chars.next(), chars.next()) {
        let lowered = single.to_ascii_lowercase();
        if let Some((unshifted, shifted, code)) = PRINTABLE_KEYS
            .iter()
            .find(|(unshifted, _, _)| *unshifted == lowered)
        {
            return Ok(printable_stroke(*unshifted, *shifted, *code, modifiers));
        }
        if let Some((unshifted, shifted, code)) = PRINTABLE_KEYS
            .iter()
            .find(|(_, shifted, _)| *shifted == single)
        {
            return Ok(printable_stroke(
                *unshifted,
                *shifted,
                *code,
                Modifiers {
                    shift: true,
                    ..modifiers
                },
            ));
        }
    }
    let normalized = key.to_ascii_lowercase();
    NAMED_KEYS
        .iter()
        .find(|(name, _, _)| *name == normalized)
        .map(|(_, code, characters)| named_stroke(*code, characters, modifiers))
        .ok_or_else(|| {
            let names: Vec<&str> = NAMED_KEYS.iter().map(|(name, _, _)| *name).collect();
            format!(
                "unsupported key {key:?}; use one printable ASCII character or one of {}",
                names.join(", ")
            )
        })
}

pub fn key_code_stroke(key_code: u16, modifiers: Modifiers) -> Result<KeyStroke, String> {
    if let Some((_, _, characters)) = NAMED_KEYS.iter().find(|(_, code, _)| *code == key_code) {
        return Ok(named_stroke(key_code, characters, modifiers));
    }
    PRINTABLE_KEYS
        .iter()
        .find(|(_, _, code)| *code == key_code)
        .map(|(unshifted, shifted, code)| printable_stroke(*unshifted, *shifted, *code, modifiers))
        .ok_or_else(|| format!("unsupported keyCode {key_code}; it is not on the US layout table"))
}

pub fn text_strokes(text: &str) -> Result<Vec<KeyStroke>, String> {
    text.chars()
        .enumerate()
        .map(|(index, character)| match character {
            '\n' => key_stroke("enter", Modifiers::default()),
            '\t' => key_stroke("tab", Modifiers::default()),
            _ => PRINTABLE_KEYS
                .iter()
                .find(|(unshifted, shifted, _)| *unshifted == character || *shifted == character)
                .map(|(unshifted, shifted, code)| {
                    printable_stroke(
                        *unshifted,
                        *shifted,
                        *code,
                        Modifiers {
                            shift: *shifted == character && *unshifted != character,
                            ..Modifiers::default()
                        },
                    )
                })
                .ok_or_else(|| {
                    format!(
                        "unsupported character {character:?} at index {index} in native_text; \
                         printable ASCII, newline and tab are typed as keystrokes"
                    )
                }),
        })
        .collect()
}

fn relative_point(payload: &Value, x_field: &str, y_field: &str) -> Result<(f64, f64), String> {
    let read = |field: &str| {
        payload
            .get(field)
            .and_then(Value::as_f64)
            .ok_or_else(|| format!("{field} must be a number between 0 and 1"))
    };
    Ok((
        read(x_field)?.clamp(0.0, 1.0),
        read(y_field)?.clamp(0.0, 1.0),
    ))
}

pub fn parse_mouse(payload: &Value) -> Result<MouseRequest, String> {
    let modifiers = parse_modifiers(payload.get("modifiers"))?;
    let action = payload
        .get("action")
        .and_then(Value::as_str)
        .ok_or_else(|| "action must be one of click, right_click, move, drag".to_string())?;
    let gesture = match action {
        "move" => MouseGesture::Move {
            to: relative_point(payload, "x", "y")?,
        },
        "click" | "right_click" => MouseGesture::Click {
            at: relative_point(payload, "x", "y")?,
            button: if action == "click" {
                MouseButton::Left
            } else {
                MouseButton::Right
            },
            click_count: payload
                .get("clickCount")
                .and_then(Value::as_i64)
                .unwrap_or(1)
                .max(1),
        },
        "drag" => MouseGesture::Drag {
            from: relative_point(payload, "x", "y")?,
            to: relative_point(payload, "toX", "toY")?,
            steps: payload
                .get("steps")
                .and_then(Value::as_u64)
                .map(|steps| steps as usize)
                .unwrap_or(12)
                .max(2),
        },
        other => {
            return Err(format!(
                "unknown mouse action {other:?}; use click, right_click, move or drag"
            ))
        }
    };
    Ok(MouseRequest { gesture, modifiers })
}

fn window_label(payload: &Value) -> String {
    payload
        .get("window")
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|label| !label.is_empty())
        .unwrap_or(DEFAULT_WINDOW_LABEL)
        .to_string()
}

pub fn inject<R: Runtime>(
    app: &AppHandle<R>,
    action: &str,
    payload: &Value,
) -> Result<Value, String> {
    let label = window_label(payload);
    match action {
        "native_key" => {
            let modifiers = parse_modifiers(payload.get("modifiers"))?;
            let stroke = match (
                payload.get("key").and_then(Value::as_str),
                payload.get("keyCode").and_then(Value::as_u64),
            ) {
                (Some(key), _) => key_stroke(key, modifiers)?,
                (None, Some(code)) => key_code_stroke(
                    u16::try_from(code).map_err(|_| format!("keyCode {code} is out of range"))?,
                    modifiers,
                )?,
                (None, None) => return Err("native_key needs key or keyCode".to_string()),
            };
            platform::send_keys(app, &label, &[stroke])
        }
        "native_text" => {
            let text = payload
                .get("text")
                .and_then(Value::as_str)
                .ok_or_else(|| "native_text needs text".to_string())?;
            platform::send_keys(app, &label, &text_strokes(text)?)
        }
        "native_mouse" => platform::send_mouse(app, &label, &parse_mouse(payload)?),
        other => Err(format!("{other} is not a native input action")),
    }
}

#[cfg(target_os = "macos")]
mod platform {
    use super::{KeyStroke, Modifiers, MouseButton, MouseGesture, MouseRequest};
    use objc2::rc::Retained;
    use objc2::runtime::NSObjectProtocol;
    use objc2::MainThreadMarker;
    use objc2_app_kit::{
        NSApplication, NSEvent, NSEventModifierFlags, NSEventType, NSMenu, NSView, NSWindow,
    };
    use objc2_foundation::{NSPoint, NSProcessInfo, NSString};
    use objc2_web_kit::WKWebView;
    use serde_json::Value;
    use std::sync::mpsc;
    use std::time::Duration;
    use tauri::{AppHandle, Manager, Runtime};

    const BUTTON_HOLD: Duration = Duration::from_millis(20);
    const DRAG_STEP: Duration = Duration::from_millis(16);
    const MAIN_THREAD_TIMEOUT: Duration = Duration::from_secs(5);

    struct Target {
        view: Retained<NSView>,
        window: Retained<NSWindow>,
        mtm: MainThreadMarker,
    }

    fn on_webview<R: Runtime, T: Send + 'static>(
        app: &AppHandle<R>,
        label: &str,
        f: impl FnOnce(&Target) -> Result<T, String> + Send + 'static,
    ) -> Result<T, String> {
        let webview = app
            .get_webview_window(label)
            .ok_or_else(|| format!("window {label:?} not found"))?;
        let (sender, receiver) = mpsc::channel();
        webview
            .with_webview(move |platform| {
                let mtm = unsafe { MainThreadMarker::new_unchecked() };
                let view: Retained<NSView> = unsafe { Retained::retain(platform.inner().cast()) }
                    .expect("wry hands over a live WKWebView");
                let result = view
                    .window()
                    .ok_or_else(|| "the webview has no window".to_string())
                    .and_then(|window| f(&Target { view, window, mtm }));
                let _ = sender.send(result);
            })
            .map_err(|error| format!("failed to reach the webview: {error}"))?;
        receiver
            .recv_timeout(MAIN_THREAD_TIMEOUT)
            .map_err(|_| "timed out waiting for the main thread to inject input".to_string())?
    }

    fn event_flags(modifiers: Modifiers) -> NSEventModifierFlags {
        let mut flags = NSEventModifierFlags::empty();
        if modifiers.command {
            flags |= NSEventModifierFlags::Command;
        }
        if modifiers.option {
            flags |= NSEventModifierFlags::Option;
        }
        if modifiers.shift {
            flags |= NSEventModifierFlags::Shift;
        }
        if modifiers.control {
            flags |= NSEventModifierFlags::Control;
        }
        flags
    }

    fn uptime() -> f64 {
        NSProcessInfo::processInfo().systemUptime()
    }

    fn make_webview_first_responder(target: &Target) {
        let already = target.window.firstResponder().is_some_and(|responder| {
            Retained::as_ptr(&responder).cast::<NSView>() == Retained::as_ptr(&target.view)
        });
        if !already {
            target.window.makeFirstResponder(Some(&target.view));
        }
    }

    fn key_event(
        target: &Target,
        kind: NSEventType,
        stroke: &KeyStroke,
    ) -> Result<Retained<NSEvent>, String> {
        let mut flags = event_flags(stroke.modifiers);
        if stroke.function_key {
            flags |= NSEventModifierFlags::Function;
        }
        NSEvent::keyEventWithType_location_modifierFlags_timestamp_windowNumber_context_characters_charactersIgnoringModifiers_isARepeat_keyCode(
            kind,
            NSPoint::ZERO,
            flags,
            uptime(),
            target.window.windowNumber(),
            None,
            &NSString::from_str(&stroke.characters),
            &NSString::from_str(&stroke.characters_ignoring_modifiers),
            false,
            stroke.key_code,
        )
        .ok_or_else(|| format!("AppKit refused a key event for keyCode {}", stroke.key_code))
    }

    fn route_menu_actions_to(menu: &NSMenu, view: &NSView) {
        for item in menu.itemArray().iter() {
            if let Some(submenu) = item.submenu() {
                route_menu_actions_to(&submenu, view);
                continue;
            }
            let Some(action) = item.action() else {
                continue;
            };
            if !view.respondsToSelector(action) {
                continue;
            }
            let other_webview = item.target().is_some_and(|current| {
                current.downcast_ref::<WKWebView>().is_some()
                    && Retained::as_ptr(&current).cast::<NSView>() != std::ptr::from_ref(view)
            });
            if item.target().is_none() || other_webview {
                unsafe { item.setTarget(Some(view)) };
            }
        }
    }

    fn dispatch_key_equivalent(target: &Target, down: &NSEvent) -> Option<&'static str> {
        let main_menu = NSApplication::sharedApplication(target.mtm).mainMenu()?;
        route_menu_actions_to(&main_menu, &target.view);
        if target.view.performKeyEquivalent(down) {
            Some("webview")
        } else if main_menu.performKeyEquivalent(down) {
            Some("menu")
        } else {
            None
        }
    }

    fn send_key(target: &Target, stroke: &KeyStroke) -> Result<&'static str, String> {
        make_webview_first_responder(target);
        let down = key_event(target, NSEventType::KeyDown, stroke)?;
        let up = key_event(target, NSEventType::KeyUp, stroke)?;
        let chord = stroke.modifiers.command || stroke.modifiers.control;
        let handled_by = chord
            .then(|| dispatch_key_equivalent(target, &down))
            .flatten()
            .unwrap_or_else(|| {
                target.window.sendEvent(&down);
                "responder"
            });
        target.window.sendEvent(&up);
        Ok(handled_by)
    }

    pub fn send_keys<R: Runtime>(
        app: &AppHandle<R>,
        label: &str,
        strokes: &[KeyStroke],
    ) -> Result<Value, String> {
        let mut handled = Vec::with_capacity(strokes.len());
        for stroke in strokes {
            let stroke = stroke.clone();
            handled.push(on_webview(app, label, move |target| {
                send_key(target, &stroke)
            })?);
        }
        Ok(serde_json::json!({ "delivered": strokes.len(), "handledBy": handled }))
    }

    fn window_point(target: &Target, relative: (f64, f64)) -> NSPoint {
        let frame = target.window.frame();
        NSPoint::new(
            frame.size.width * relative.0,
            frame.size.height * (1.0 - relative.1),
        )
    }

    fn mouse_event(
        target: &Target,
        kind: NSEventType,
        point: NSPoint,
        modifiers: Modifiers,
        click_count: i64,
    ) -> Result<Retained<NSEvent>, String> {
        let pressed = matches!(
            kind,
            NSEventType::LeftMouseDown
                | NSEventType::RightMouseDown
                | NSEventType::LeftMouseDragged
        );
        NSEvent::mouseEventWithType_location_modifierFlags_timestamp_windowNumber_context_eventNumber_clickCount_pressure(
            kind,
            point,
            event_flags(modifiers),
            uptime(),
            target.window.windowNumber(),
            None,
            0,
            click_count as isize,
            if pressed { 1.0 } else { 0.0 },
        )
        .ok_or_else(|| format!("AppKit refused a mouse event of type {}", kind.0))
    }

    fn send_mouse_event(
        target: &Target,
        kind: NSEventType,
        relative: (f64, f64),
        modifiers: Modifiers,
        click_count: i64,
    ) -> Result<(), String> {
        let point = window_point(target, relative);
        let event = mouse_event(target, kind, point, modifiers, click_count)?;
        if kind == NSEventType::MouseMoved {
            target.view.mouseMoved(&event);
        } else {
            target.window.sendEvent(&event);
        }
        Ok(())
    }

    fn one<R: Runtime>(
        app: &AppHandle<R>,
        label: &str,
        kind: NSEventType,
        relative: (f64, f64),
        modifiers: Modifiers,
        click_count: i64,
    ) -> Result<(), String> {
        on_webview(app, label, move |target| {
            send_mouse_event(target, kind, relative, modifiers, click_count)
        })
    }

    pub fn send_mouse<R: Runtime>(
        app: &AppHandle<R>,
        label: &str,
        request: &MouseRequest,
    ) -> Result<Value, String> {
        let modifiers = request.modifiers;
        let mut delivered = 0usize;
        match request.gesture {
            MouseGesture::Move { to } => {
                one(app, label, NSEventType::MouseMoved, to, modifiers, 0)?;
                delivered += 1;
            }
            MouseGesture::Click {
                at,
                button,
                click_count,
            } => {
                let (down, up) = match button {
                    MouseButton::Left => (NSEventType::LeftMouseDown, NSEventType::LeftMouseUp),
                    MouseButton::Right => (NSEventType::RightMouseDown, NSEventType::RightMouseUp),
                };
                one(app, label, NSEventType::MouseMoved, at, modifiers, 0)?;
                std::thread::sleep(BUTTON_HOLD);
                for count in 1..=click_count {
                    one(app, label, down, at, modifiers, count)?;
                    std::thread::sleep(BUTTON_HOLD);
                    one(app, label, up, at, modifiers, count)?;
                    delivered += 2;
                    if count < click_count {
                        std::thread::sleep(BUTTON_HOLD);
                    }
                }
                delivered += 1;
            }
            MouseGesture::Drag { from, to, steps } => {
                one(app, label, NSEventType::MouseMoved, from, modifiers, 0)?;
                std::thread::sleep(BUTTON_HOLD);
                one(app, label, NSEventType::LeftMouseDown, from, modifiers, 1)?;
                std::thread::sleep(Duration::from_millis(50));
                for step in 1..=steps {
                    let t = step as f64 / steps as f64;
                    let point = (from.0 + (to.0 - from.0) * t, from.1 + (to.1 - from.1) * t);
                    one(
                        app,
                        label,
                        NSEventType::LeftMouseDragged,
                        point,
                        modifiers,
                        1,
                    )?;
                    std::thread::sleep(DRAG_STEP);
                }
                std::thread::sleep(Duration::from_millis(50));
                one(app, label, NSEventType::LeftMouseUp, to, modifiers, 1)?;
                delivered += steps + 3;
            }
        }
        Ok(serde_json::json!({ "delivered": delivered }))
    }
}

#[cfg(not(target_os = "macos"))]
mod platform {
    use super::{KeyStroke, MouseRequest};
    use serde_json::Value;
    use tauri::{AppHandle, Runtime};

    pub fn send_keys<R: Runtime>(
        _app: &AppHandle<R>,
        _label: &str,
        _strokes: &[KeyStroke],
    ) -> Result<Value, String> {
        Err("native input injection is only supported on macOS".into())
    }

    pub fn send_mouse<R: Runtime>(
        _app: &AppHandle<R>,
        _label: &str,
        _request: &MouseRequest,
    ) -> Result<Value, String> {
        Err("native input injection is only supported on macOS".into())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn stroke(key: &str, modifiers: &[&str]) -> KeyStroke {
        key_stroke(key, parse_modifiers(Some(&json!(modifiers))).unwrap()).unwrap()
    }

    #[test]
    fn letters_carry_the_case_shift_produces() {
        assert_eq!(stroke("a", &[]).characters, "a");
        assert_eq!(stroke("a", &["shift"]).characters, "A");
        assert_eq!(stroke("a", &["shift"]).characters_ignoring_modifiers, "A");
        assert_eq!(stroke("A", &[]).key_code, 0);
    }

    #[test]
    fn a_shifted_symbol_implies_shift() {
        let question = stroke("?", &[]);
        assert_eq!(question.key_code, 44);
        assert!(question.modifiers.shift);
        assert_eq!(question.characters, "?");
        assert!(!stroke("/", &[]).modifiers.shift);
    }

    #[test]
    fn command_keeps_the_plain_character() {
        let copy = stroke("c", &["command"]);
        assert_eq!(copy.characters, "c");
        assert!(copy.modifiers.command);
    }

    #[test]
    fn control_produces_the_control_character() {
        assert_eq!(stroke("c", &["control"]).characters, "\u{3}");
        assert_eq!(stroke("c", &["control"]).characters_ignoring_modifiers, "c");
        assert_eq!(stroke("[", &["control"]).characters, "\u{1b}");
    }

    #[test]
    fn named_keys_carry_appkit_function_characters() {
        let up = stroke("ArrowUp", &[]);
        assert_eq!(up.key_code, 126);
        assert_eq!(up.characters, "\u{f700}");
        assert!(up.function_key);
        let enter = stroke("Enter", &[]);
        assert_eq!(
            (
                enter.key_code,
                enter.characters.as_str(),
                enter.function_key
            ),
            (36, "\r", false)
        );
    }

    #[test]
    fn key_codes_resolve_to_the_same_strokes() {
        assert_eq!(
            key_code_stroke(53, Modifiers::default()).unwrap(),
            stroke("escape", &[])
        );
        assert_eq!(
            key_code_stroke(8, Modifiers::default()).unwrap(),
            stroke("c", &[])
        );
        assert!(key_code_stroke(999, Modifiers::default())
            .unwrap_err()
            .contains("999"));
    }

    #[test]
    fn text_types_each_character_with_its_shift() {
        let strokes = text_strokes("Hi 2!\n").unwrap();
        let summary: Vec<(u16, bool, &str)> = strokes
            .iter()
            .map(|stroke| {
                (
                    stroke.key_code,
                    stroke.modifiers.shift,
                    stroke.characters.as_str(),
                )
            })
            .collect();
        assert_eq!(
            summary,
            vec![
                (4, true, "H"),
                (34, false, "i"),
                (49, false, " "),
                (19, false, "2"),
                (18, true, "!"),
                (36, false, "\r")
            ]
        );
    }

    #[test]
    fn text_names_the_character_it_cannot_type() {
        let error = text_strokes("ok é").unwrap_err();
        assert!(error.contains("'é'"), "{error}");
        assert!(error.contains("index 3"), "{error}");
    }

    #[test]
    fn modifiers_accept_both_shapes_and_reject_strangers() {
        let list = parse_modifiers(Some(&json!(["command", "shift"]))).unwrap();
        let joined = parse_modifiers(Some(&json!("command,shift"))).unwrap();
        assert_eq!(list, joined);
        assert!(list.command && list.shift && !list.option && !list.control);
        assert!(parse_modifiers(Some(&json!(["hyper"])))
            .unwrap_err()
            .contains("hyper"));
        assert_eq!(parse_modifiers(None).unwrap(), Modifiers::default());
    }

    #[test]
    fn mouse_requests_clamp_and_default() {
        let click = parse_mouse(&json!({ "action": "click", "x": 1.5, "y": -1 })).unwrap();
        assert_eq!(
            click.gesture,
            MouseGesture::Click {
                at: (1.0, 0.0),
                button: MouseButton::Left,
                click_count: 1
            }
        );
        let drag = parse_mouse(
            &json!({ "action": "drag", "x": 0.1, "y": 0.2, "toX": 0.3, "toY": 0.4, "steps": 1 }),
        )
        .unwrap();
        assert_eq!(
            drag.gesture,
            MouseGesture::Drag {
                from: (0.1, 0.2),
                to: (0.3, 0.4),
                steps: 2
            }
        );
        let right = parse_mouse(
            &json!({ "action": "right_click", "x": 0.5, "y": 0.5, "modifiers": ["option"] }),
        )
        .unwrap();
        assert!(matches!(
            right.gesture,
            MouseGesture::Click {
                button: MouseButton::Right,
                ..
            }
        ));
        assert!(right.modifiers.option);
        assert!(parse_mouse(&json!({ "action": "hover", "x": 0, "y": 0 }))
            .unwrap_err()
            .contains("hover"));
        assert!(parse_mouse(&json!({ "action": "drag", "x": 0, "y": 0 }))
            .unwrap_err()
            .contains("toX"));
    }
}
