use attn_app::{Action, Binding, Keymap};
use rune::{Chord, ChordKey, ChordMod, Platform};

fn chord(keymap: &Keymap, action: &Action) -> Chord {
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
    let linux = Keymap::from_setting(None, Platform::Linux);
    for action in Action::ALL {
        let mods = chord(&linux, action).mods_on(Platform::Linux);
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
    let linux = Keymap::from_setting(None, Platform::Linux);
    for action in Action::ALL {
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
        assert!(!reserved, "{} takes a reserved ctrl+alt chord", action.id());
    }
}

#[test]
fn a_mac_cmd_and_letter_action_keeps_its_letter_on_linux_behind_shift_or_alt() {
    let mac = Keymap::from_setting(None, Platform::Mac);
    let linux = Keymap::from_setting(None, Platform::Linux);
    for action in Action::ALL {
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
    let linux = Keymap::from_setting(None, Platform::Linux);
    for action in [
        Action::TerminalSplitHorizontal,
        Action::TerminalToggleZoom,
        Action::SessionNewHorizontal,
        Action::SessionGoToDashboard,
        Action::SessionSettle,
        Action::SessionSnooze,
        Action::SessionToggleSidebar,
        Action::BoardOpen,
    ] {
        let mods = chord(&linux, &action).mods_on(Platform::Linux);
        assert_eq!(mods, [ChordMod::Primary, ChordMod::Alt], "{}", action.id());
    }
}
