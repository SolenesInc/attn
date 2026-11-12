use attn_term::keys;
use rune::{Ime, Key, KeyEvent, KeyPlatform, Tx};

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
        return send_to_terminal(state, terminal, ev, tx);
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
        Press::Unbound => send_to_terminal(state, terminal, ev, tx),
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

fn send_to_terminal(state: &mut State, terminal: Option<String>, ev: &KeyEvent, tx: &Tx) -> bool {
    let Some(id) = terminal else {
        return false;
    };
    let platform = KeyPlatform::current();
    let bytes = match keys::press(ev, tx.physical_key(), tx.key_repeat(), platform) {
        None => return false,
        Some(keys::Press::Bytes(bytes)) => bytes.to_vec(),
        Some(keys::Press::Encode(key)) => match state.terminals.encode_key(&id, &key) {
            Some(bytes) => bytes,
            None => return false,
        },
    };
    if bytes.is_empty() {
        return true;
    }
    state.terminals.input(&id, &bytes, &state.daemon)
}

/// What an input method composed for the terminal holding the keyboard: a dead key's accent,
/// a CJK conversion, the emoji picker, dictation.
pub(crate) fn on_ime(state: &mut State, ime: &Ime, tx: &mut Tx) -> bool {
    let composing = match ime {
        Ime::Preedit(text, _) if !text.is_empty() => Some(text),
        _ => None,
    };
    if composing.is_none() {
        state.terminals.end_composition();
    }
    if tx.focused().is_some() {
        return false;
    }
    let Some(id) = state.window.keyboard_terminal(&state.facts) else {
        return false;
    };
    match ime {
        Ime::Preedit(..) => {
            if let Some(text) = composing {
                state.terminals.compose(&id, text);
            }
            true
        }
        Ime::Commit(text) => {
            !text.is_empty() && state.terminals.input(&id, text.as_bytes(), &state.daemon)
        }
        Ime::Enabled | Ime::Disabled => false,
    }
}

/// A paste into the terminal holding the keyboard, bracketed when its program asked.
pub(crate) fn on_paste(state: &mut State, text: &str, tx: &mut Tx) {
    if tx.focused().is_some() {
        return;
    }
    let Some(id) = state.window.keyboard_terminal(&state.facts) else {
        return;
    };
    if let Some(bytes) = state.terminals.encode_paste(&id, text) {
        state.terminals.input(&id, &bytes, &state.daemon);
    }
}
