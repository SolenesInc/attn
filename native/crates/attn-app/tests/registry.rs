use std::collections::BTreeMap;

use attn_app::{Action, Binding, Keymap};
use rune::{Chord, ChordKey, ChordMod, KeyPlatform};

const REGISTRY: &str = include_str!("../../../../app/src/shortcuts/registry.ts");

/// The Tauri app's default table `name`, as `keybindings_config` overrides.
fn tauri_table(name: &str) -> BTreeMap<String, String> {
    let start = REGISTRY
        .find(&format!("export const {name} = {{"))
        .unwrap_or_else(|| panic!("{name} is not in registry.ts"));
    let body = &REGISTRY[start..];
    let body = &body[..body.find("\n}").expect("the table ends")];
    body.lines()
        .map(str::trim)
        .filter(|line| line.starts_with('\''))
        .map(|line| {
            let (id, combo) = line[1..].split_once("': ").expect("an entry is 'id': {..}");
            let combo = combo.split(" //").next().unwrap_or(combo).trim();
            let mut json = combo.trim_end_matches(',').replace('\'', "\"");
            for field in [
                "key",
                "code",
                "meta",
                "shift",
                "ctrl",
                "alt",
                "editableTarget",
            ] {
                json = json.replace(&format!("{field}:"), &format!("\"{field}\":"));
            }
            (id.to_owned(), json)
        })
        .collect()
}

fn same_as_tauri(name: &str, platform: KeyPlatform) {
    let table = tauri_table(name);
    let ids: Vec<&str> = Action::ALL.iter().map(|a| a.id()).collect();
    let tauri_ids: Vec<&str> = table.keys().map(String::as_str).collect();
    let mut sorted = ids.clone();
    sorted.sort();
    assert_eq!(
        sorted, tauri_ids,
        "the native actions are the Tauri app's shortcut ids"
    );

    let overrides: Vec<String> = table
        .iter()
        .map(|(id, combo)| format!("\"{id}\":{combo}"))
        .collect();
    let setting = format!(
        "{{\"version\":1,\"overrides\":{{{}}}}}",
        overrides.join(",")
    );
    serde_json::from_str::<serde_json::Value>(&setting)
        .unwrap_or_else(|why| panic!("{name} did not read as JSON ({why}): {setting}"));
    let tauri = Keymap::from_setting(Some(&setting), platform);
    let native = Keymap::from_setting(None, platform);
    for &action in Action::ALL {
        assert!(
            tauri.binding(action).is_some(),
            "{} did not parse: {}",
            action.id(),
            table[action.id()]
        );
        assert_eq!(
            native.binding(action),
            tauri.binding(action),
            "{} on {platform:?}: Tauri has {}",
            action.id(),
            table[action.id()]
        );
        assert_eq!(
            native.kept_by_fields(action),
            tauri.kept_by_fields(action),
            "whether a field keeps {}'s keys",
            action.id()
        );
    }
}

#[test]
fn the_mac_defaults_are_the_tauri_apps() {
    same_as_tauri("MAC_SHORTCUTS", KeyPlatform::Mac);
}

#[test]
fn the_linux_defaults_are_the_tauri_apps() {
    same_as_tauri("LINUX_SHORTCUTS", KeyPlatform::Linux);
}

fn chord(keymap: &Keymap, action: Action) -> Chord {
    match keymap.binding(action) {
        Some(Binding::Chord(chord)) => chord,
        other => panic!("{} defaults to {other:?}, not one chord", action.id()),
    }
}

fn letter(chord: &Chord) -> Option<char> {
    match chord.key() {
        ChordKey::Char(c) if c.is_ascii_alphabetic() => Some(c),
        _ => None,
    }
}

#[test]
fn every_linux_default_leaves_plain_ctrl_and_a_letter_to_the_shell() {
    let linux = Keymap::from_setting(None, KeyPlatform::Linux);
    for &action in Action::ALL {
        let mods = chord(&linux, action).mods_on(KeyPlatform::Linux);
        assert!(
            mods.contains(&ChordMod::Primary),
            "{} needs Ctrl",
            action.id()
        );
        assert!(
            mods.contains(&ChordMod::Shift) || mods.contains(&ChordMod::Alt),
            "{} needs Shift or Alt beside Ctrl",
            action.id()
        );
    }
}

#[test]
fn no_linux_default_takes_a_ctrl_alt_chord_the_ubuntu_desktop_reserves() {
    let linux = Keymap::from_setting(None, KeyPlatform::Linux);
    for &action in Action::ALL {
        let chord = chord(&linux, action);
        if !chord.has(ChordMod::Alt) || chord.has(ChordMod::Shift) {
            continue;
        }
        let reserved = matches!(
            chord.key(),
            ChordKey::Char('t')
                | ChordKey::Delete
                | ChordKey::Tab
                | ChordKey::Escape
                | ChordKey::Left
                | ChordKey::Right
                | ChordKey::Up
                | ChordKey::Down
        );
        assert!(!reserved, "{} takes a reserved Ctrl+Alt chord", action.id());
    }
}

#[test]
fn a_mac_cmd_and_letter_action_keeps_its_letter_on_linux_behind_shift_or_alt() {
    let mac = Keymap::from_setting(None, KeyPlatform::Mac);
    let linux = Keymap::from_setting(None, KeyPlatform::Linux);
    for &action in Action::ALL {
        let on_mac = chord(&mac, action);
        let Some(key) = letter(&on_mac) else { continue };
        if !on_mac.has(ChordMod::Primary) || on_mac.has(ChordMod::Shift) {
            continue;
        }
        let on_linux = chord(&linux, action);
        assert_eq!(letter(&on_linux), Some(key), "{}", action.id());
        assert!(
            on_linux.has(ChordMod::Shift) || on_linux.has(ChordMod::Alt),
            "{}",
            action.id()
        );
    }
}

#[test]
fn the_former_cmd_shift_letter_actions_sit_on_ctrl_alt_on_linux() {
    let linux = Keymap::from_setting(None, KeyPlatform::Linux);
    for action in [
        Action::TerminalSplitHorizontal,
        Action::TerminalToggleZoom,
        Action::SessionNewHorizontal,
        Action::SessionGoToDashboard,
        Action::SessionSettle,
        Action::SessionSnooze,
        Action::SessionToggleSidebar,
        Action::DockAttention,
        Action::BoardOpen,
    ] {
        let mods = chord(&linux, action).mods_on(KeyPlatform::Linux);
        assert_eq!(mods, [ChordMod::Primary, ChordMod::Alt], "{}", action.id());
    }
}
