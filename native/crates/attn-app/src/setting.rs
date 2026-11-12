use std::collections::BTreeMap;

use attn_protocol::go_values::SETTINGS;

/// Whether the daemon's boolean setting `key` is on, as the Tauri app's `isOn` reads it: its
/// stored value when there is one, else its default.
pub(crate) fn on(settings: &BTreeMap<String, String>, key: &str) -> bool {
    match settings.get(key).map(String::as_str) {
        Some(value) if !value.is_empty() => value == "true",
        _ => SETTINGS.iter().any(|s| s.key == key && s.default_on),
    }
}
