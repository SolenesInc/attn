//! The system-wide shortcut that opens Quick Capture: bound from the daemon's `capture.shortcut`
//! setting, and before that from the binding cached in `capture-shortcut.json`, both written
//! as the Tauri app writes them (`Control+Alt+Super+Space`), so either app takes up the
//! other's choice.

use std::path::{Path, PathBuf};

use attn_protocol::SetSettingMessage;
use rune::{Shortcut, ShortcutError};

use crate::State;

pub(crate) const SETTING: &str = "capture.shortcut";
pub(crate) const DEFAULT: &str = "Control+Alt+Super+Space";
const CACHE: &str = "capture-shortcut.json";
const NEEDS_MODIFIER: &str =
    "Use Command, Control or Option with another key for a system-wide shortcut.";

/// The shortcut as the person chose it and as the system holds it.
#[derive(Default)]
pub(crate) struct Global {
    pub data_dir: PathBuf,
    /// The binding the system holds, as stored.
    pub active: Option<String>,
    pub error: Option<String>,
    /// The binding was taken up once the app connected.
    pub followed: bool,
}

/// A stored binding as Rune registers it, refusing one that would take a key from typing.
pub(crate) fn parse(binding: &str) -> Result<Shortcut, String> {
    let shortcut: Shortcut = binding.parse().map_err(|e: ShortcutError| e.to_string())?;
    shortcut
        .require_modifier()
        .map_err(|_| NEEDS_MODIFIER.to_owned())?;
    Ok(shortcut)
}

/// `shortcut` as the Tauri app stores it: ⌘, ⌃, ⌥ and ⇧ by name, then the key by its code.
pub(crate) fn stored(shortcut: &Shortcut) -> String {
    let display = shortcut.to_string();
    let mut parts: Vec<String> = Vec::new();
    let (mods, key) = display.rsplit_once('+').unwrap_or(("", &display));
    let held: Vec<&str> = mods.split('+').collect();
    for (name, stored) in [
        ("super", "Super"),
        ("ctrl", "Control"),
        ("alt", "Alt"),
        ("shift", "Shift"),
    ] {
        if held.contains(&name) {
            parts.push(stored.into());
        }
    }
    parts.push(code(key));
    parts.join("+")
}

fn code(key: &str) -> String {
    let named = match key {
        "left" => "ArrowLeft",
        "right" => "ArrowRight",
        "up" => "ArrowUp",
        "down" => "ArrowDown",
        "pageup" => "PageUp",
        "pagedown" => "PageDown",
        "bracketleft" => "BracketLeft",
        "bracketright" => "BracketRight",
        _ => "",
    };
    if !named.is_empty() {
        return named.into();
    }
    let mut chars = key.chars();
    match (chars.next(), chars.next()) {
        (Some(c), None) if c.is_ascii_lowercase() => format!("Key{}", c.to_ascii_uppercase()),
        (Some(c), None) if c.is_ascii_digit() => format!("Digit{c}"),
        (Some('f'), Some(d)) if d.is_ascii_digit() => key.to_uppercase(),
        _ => {
            let mut chars = key.chars();
            chars
                .next()
                .map(|c| c.to_ascii_uppercase().to_string() + chars.as_str())
                .unwrap_or_default()
        }
    }
}

impl Global {
    /// The binding cached by the last app that bound one, or the one this kind of app starts
    /// with: `DEFAULT` in production, none on a named instance.
    fn cached(&self, production: bool) -> Option<String> {
        std::fs::read(self.data_dir.join(CACHE))
            .ok()
            .and_then(|bytes| serde_json::from_slice::<Option<String>>(&bytes).ok())
            .unwrap_or_else(|| production.then(|| DEFAULT.to_owned()))
    }

    fn cache(&self, binding: &Option<String>) -> Result<(), String> {
        let bytes = serde_json::to_vec(binding).map_err(|e| e.to_string())?;
        super::draft::commit(&self.data_dir.join(CACHE), &bytes)
    }
}

impl State {
    /// Gives this window the Quick Capture panel's window, whose cached shortcut lives in
    /// `data_dir`.
    pub fn use_quick_capture(
        &mut self,
        window: std::sync::Arc<dyn super::Window>,
        data_dir: &Path,
    ) {
        self.capture = Some(window);
        self.capture_shortcut = Global {
            data_dir: data_dir.to_owned(),
            ..Global::default()
        };
        follow(self);
    }
}

/// Binds the shortcut the daemon's setting names once connected, the cached one while the
/// setting is unset, and none when it is empty.
pub(crate) fn follow(state: &mut State) {
    let Some(window) = state.capture.clone() else {
        return;
    };
    if state.facts.connection != attn_client::Connection::Connected {
        return;
    }
    let wanted = match state.facts.settings.get(SETTING) {
        Some(set) if set.is_empty() => None,
        Some(set) => Some(set.clone()),
        None => state.capture_shortcut.cached(state.instance == "default"),
    };
    let global = &mut state.capture_shortcut;
    if global.followed && wanted == global.active {
        return;
    }
    global.followed = true;
    match bind(&*window, global.active.as_deref(), wanted.as_deref()) {
        Ok(()) => {
            global.active = wanted.clone();
            global.error = global.cache(&wanted).err();
        }
        Err(why) => global.error = Some(why),
    }
}

/// Registers `next` with the system in place of `previous`, keeping `previous` when `next`
/// cannot be had.
fn bind(
    window: &dyn super::Window,
    previous: Option<&str>,
    next: Option<&str>,
) -> Result<(), String> {
    if previous == next {
        return Ok(());
    }
    let shortcut = next.map(parse).transpose()?;
    window.bind(shortcut.as_ref()).map_err(|why| match next {
        Some(next) => format!("Cannot register {next}: {why}. Previous shortcut remains active."),
        None => why,
    })
}

/// The person chose `next` in the shortcut editor: the system takes it first, then the
/// daemon's setting records it, and a setting that cannot be sent puts the previous one back.
pub(crate) fn choose(state: &mut State, next: Option<String>) -> Result<(), String> {
    let Some(window) = state.capture.clone() else {
        return Ok(());
    };
    let previous = state.capture_shortcut.active.clone();
    let result = bind(&*window, previous.as_deref(), next.as_deref()).and_then(|()| {
        let sent = state.daemon.send(&SetSettingMessage {
            key: SETTING.into(),
            value: next.clone().unwrap_or_default(),
            ..SetSettingMessage::new()
        });
        if let Err(why) = sent {
            let _ = bind(&*window, next.as_deref(), previous.as_deref());
            return Err(why.to_string());
        }
        let global = &mut state.capture_shortcut;
        global.active = next.clone();
        global.cache(&next)
    });
    state.capture_shortcut.error = result.clone().err();
    result
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stores_each_key_as_the_tauri_app_does() {
        for binding in [
            "Super+Control+Alt+Space",
            "Control+Alt+KeyB",
            "Super+Shift+Digit7",
            "Control+Alt+F5",
            "Super+Alt+ArrowLeft",
            "Control+Alt+BracketLeft",
            "Super+Control+Enter",
            "Control+Alt+Minus",
        ] {
            assert_eq!(stored(&parse(binding).unwrap()), binding);
        }
        assert_eq!(stored(&parse(DEFAULT).unwrap()), "Super+Control+Alt+Space");
    }

    #[test]
    fn refuses_a_binding_that_would_take_a_key_from_typing() {
        assert_eq!(parse("Shift+KeyA").unwrap_err(), NEEDS_MODIFIER);
    }
}
