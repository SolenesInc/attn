use attn_protocol::PtyInputMessage;
use rune::{Key, KeyEvent, KeyPlatform, Tx};

use crate::State;
use crate::dispatch::dispatch;
use crate::keymap::{Focus, Press};
use crate::shell::{LEADER_TIMEOUT, Leader};

/// Keys a text field that attn draws has passed on reach the keymap with this suffix on the
/// field's key, so the keymap leaves the field the keys it keeps for itself.
pub(crate) const FIELD_KEY_SUFFIX: &str = ".field";

/// A key the window hears: from Rune when nothing has focus, or after a focused control
/// passed it on. A focused text field has already taken typing and its edit commands.
///
/// The order: a pending sequence takes the next key; the terminal holding the keyboard keeps
/// the shell's own Ctrl keys on Linux; then the keymap; then the terminal; then nothing.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if matches!(ev.key, Key::Modifier { .. }) {
        return false;
    }
    if let Some(leader) = state.window.leader.take().filter(|l| tx.clock() < l.until) {
        if ev.key == Key::Escape {
            return true;
        }
        if let Some(action) = state.keymap.then(ev, tx, &leader.sequences) {
            dispatch(state, action, tx);
        } else if tx.key_repeat() || state.keymap.matches(&leader.chord, ev, tx) {
            state.window.leader = Some(Leader {
                until: tx.clock() + LEADER_TIMEOUT,
                ..leader
            });
            tx.wake_at(tx.clock() + LEADER_TIMEOUT);
        }
        return true;
    }
    let field = tx
        .focused()
        .is_some_and(|key| key.ends_with(FIELD_KEY_SUFFIX));
    let terminal = match tx.focused() {
        None => state.window.keyboard_terminal(&state.facts),
        Some(_) => None,
    };
    if terminal.is_some() && is_shell_ctrl_letter(ev) {
        return send_to_terminal(state, terminal, ev);
    }
    let focus = Focus {
        field,
        terminal: terminal.is_some(),
    };
    match state.keymap.press(ev, tx, focus) {
        Press::Action(action) => {
            dispatch(state, action, tx);
            true
        }
        Press::Leader { chord, sequences } => {
            let until = tx.clock() + LEADER_TIMEOUT;
            state.window.leader = Some(Leader {
                chord,
                sequences,
                until,
            });
            tx.wake_at(until);
            true
        }
        Press::Unbound => send_to_terminal(state, terminal, ev),
    }
}

/// Plain Ctrl and a letter belong to the shell on Linux, whatever the keymap says.
fn is_shell_ctrl_letter(ev: &KeyEvent) -> bool {
    KeyPlatform::current() != KeyPlatform::Mac
        && ev.mods.ctrl
        && !ev.mods.alt
        && !ev.mods.shift
        && matches!(ev.key, Key::Command(c) if c.is_ascii_alphabetic())
}

fn send_to_terminal(state: &State, terminal: Option<String>, ev: &KeyEvent) -> bool {
    let (Some(id), Some(data)) = (terminal, bytes(ev)) else {
        return false;
    };
    let _ = state.daemon.send(&PtyInputMessage {
        id,
        data,
        ..PtyInputMessage::new()
    });
    true
}

/// The bytes a terminal reads for the keys that need no terminal modes to encode.
fn bytes(ev: &KeyEvent) -> Option<String> {
    let text = match &ev.key {
        Key::Text(text) if !ev.mods.cmd => text.clone(),
        Key::Command(c) if ev.mods.ctrl && c.is_ascii_alphabetic() => {
            char::from(c.to_ascii_lowercase() as u8 - b'a' + 1).to_string()
        }
        Key::Enter => "\r".into(),
        Key::Backspace => "\x7f".into(),
        Key::Tab => "\t".into(),
        Key::Escape => "\x1b".into(),
        _ => return None,
    };
    Some(text)
}
