use attn_term::keys;
use rune::{ClipboardContent, Ime, Key, KeyEvent, KeyPlatform, Mods, Tx};

use attn_protocol::RecordAnalyticsMessage;

use crate::State;
use crate::action::Action;
use crate::dispatch::{Outcome, run};
use crate::keymap::{Focus, Press};
use crate::shell::{LEADER_TIMEOUT, Leader};

/// A key the window hears: from Rune when nothing has focus, or after a focused control
/// passed it on. A focused text field has already offered it to `on_field_key` and taken
/// typing and its edit commands.
///
/// The order: a pending sequence takes the next key; the terminal holding the keyboard keeps
/// what types text and the shell's own Ctrl keys on Linux; then the keymap, unless a text field
/// passed the key on; then the terminal; then nothing. A modal dialog keeps every bound key
/// from acting, and from the menu bar.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if matches!(ev.key, Key::Modifier { .. }) {
        return false;
    }
    if modal(state) {
        return bound(state, ev, tx);
    }
    if let Some(leader) = state.window.leader.take().filter(|l| tx.clock() < l.until) {
        if ev.key == Key::Escape {
            return true;
        }
        if let Some(action) = state.keymap.then(ev, tx, &leader.sequences) {
            run_shortcut(state, action, tx);
        } else if tx.key_repeat() || state.keymap.matches(&leader.chord, ev, tx) {
            state.window.leader = Some(Leader {
                until: tx.clock() + LEADER_TIMEOUT,
                ..leader
            });
            tx.wake_at(tx.clock() + LEADER_TIMEOUT);
        }
        return true;
    }
    let field = tx.in_text_field();
    if !field && crate::markdown::annotating::on_key(state, ev, tx) {
        return true;
    }
    if ev.key == Key::Escape
        && tx.focused().is_none()
        && crate::annotations::draft::escape(state, tx)
    {
        return true;
    }
    if let Key::Text(typed) = &ev.key
        && tx.focused().is_none()
        && !ev.mods.cmd
        && !ev.mods.ctrl
        && let Some(digit) = typed.chars().next().and_then(|c| c.to_digit(10))
        && typed.chars().count() == 1
        && crate::annotations::draft::digit(state, digit, tx)
    {
        return true;
    }
    if ev.key == Key::Escape && !field && crate::markdown::sending::escape(state) {
        return true;
    }
    if ev.key == Key::Escape && tx.focused().is_none() && crate::shell::escape_drawers(state, tx) {
        return true;
    }
    if ev.key == Key::Escape && tx.focused().is_none() && crate::desktop::escape(state, tx) {
        return true;
    }
    if ev.key == Key::Escape && crate::garden::climb_tile(state, tx) {
        return true;
    }
    if field {
        return false;
    }
    let terminal = match tx.focused() {
        None => state.window.typing_terminal(&state.facts),
        Some(_) => None,
    };
    if let Some(id) = &terminal
        && let Some(chord) = clipboard_chord(ev)
        && clipboard(state, id, chord, tx)
    {
        return true;
    }
    if terminal.is_some() && (types_text(ev) || is_shell_ctrl_letter(ev)) {
        return send_to_terminal(state, terminal, ev, tx);
    }
    let focus = Focus {
        field: false,
        terminal: terminal.is_some(),
    };
    match state.keymap.press(ev, tx, focus) {
        Press::Action(action) => {
            run_shortcut(state, action, tx) || send_to_terminal(state, terminal, ev, tx)
        }
        Press::Unbound => send_to_terminal(state, terminal, ev, tx),
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
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Clipboard {
    Copy,
    CopyCommand,
    Paste,
}

/// The terminal's clipboard keys, as the Tauri terminal reads them: ⌘C, ⇧⌘C and ⌘V on macOS,
/// and Ctrl+Shift+C, Ctrl+Alt+C and Ctrl+Shift+V elsewhere, where plain Ctrl+C belongs to the
/// shell.
fn clipboard_chord(ev: &KeyEvent) -> Option<Clipboard> {
    let Key::Command(letter) = ev.key else {
        return None;
    };
    let letter = letter.to_ascii_lowercase();
    if !matches!(letter, 'c' | 'v') {
        return None;
    }
    let m = ev.mods;
    let mac = KeyPlatform::current() == KeyPlatform::Mac;
    if mac {
        return match (letter, m.cmd, m.shift) {
            ('v', true, false) => Some(Clipboard::Paste),
            ('c', true, false) => Some(Clipboard::Copy),
            ('c', true, true) => Some(Clipboard::CopyCommand),
            _ => None,
        };
    }
    if !m.ctrl {
        return None;
    }
    match (letter, m.shift, m.alt) {
        ('v', true, false) => Some(Clipboard::Paste),
        ('c', true, false) => Some(Clipboard::Copy),
        ('c', false, true) => Some(Clipboard::CopyCommand),
        _ => None,
    }
}

/// Copies the terminal's selection or selected block, or pastes into it where the window does
/// not already: true when the key is the terminal's, so it never reaches the program. Copy is
/// always the terminal's, even with nothing to copy; ⇧⌘C copies the selection as Markdown, or
/// else the selected block's command, and is the program's when there is neither.
fn clipboard(state: &mut State, id: &str, chord: Clipboard, tx: &mut Tx) -> bool {
    match chord {
        Clipboard::Copy => {
            if let Some(text) = state.terminals.copy_text(id) {
                tx.copy(text);
            }
            true
        }
        Clipboard::CopyCommand => {
            let text = state.terminals.copy_markdown(id).or_else(|| {
                let block = state.terminals.block_id(id)?;
                state
                    .terminals
                    .block_text(id, block, crate::terminal::Part::Command)
            });
            text.map(|text| tx.copy(text)).is_some()
        }
        Clipboard::Paste if KeyPlatform::current() == KeyPlatform::Mac => false,
        Clipboard::Paste => {
            tx.read_clipboard(on_paste);
            true
        }
    }
}

/// A key that types a character, which a binding never takes from the terminal it would type
/// into.
fn types_text(ev: &KeyEvent) -> bool {
    matches!(ev.key, Key::Text(_)) && !ev.mods.cmd && !ev.mods.ctrl
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

/// ⌘ or Ctrl went down or up: over a link in a terminal, the cursor shows it opens.
pub(crate) fn on_modifiers(state: &mut State, mods: Mods, tx: &mut Tx) {
    if !state
        .terminals
        .accelerator(crate::terminal::accelerator(mods))
    {
        tx.live_only();
    }
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
    let Some(id) = state.window.typing_terminal(&state.facts) else {
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

/// A paste into the terminal holding the keyboard: text bracketed when its program asked, or
/// for an image the Ctrl-V byte, which tells an agent to read the image off the clipboard
/// itself, as the Tauri terminal sends it.
pub(crate) fn on_paste(state: &mut State, content: &ClipboardContent, tx: &mut Tx) {
    if tx.focused().is_some() {
        return;
    }
    let Some(id) = state.window.typing_terminal(&state.facts) else {
        return;
    };
    let bytes = if content.image().is_some() {
        Some(b"\x16".to_vec())
    } else if content.text().is_empty() {
        None
    } else {
        state.terminals.encode_paste(&id, content.text())
    };
    if let Some(bytes) = bytes {
        state.terminals.input(&id, &bytes, &state.daemon);
    }
}

/// A key pressed in a focused text field, before the field hears it, as the Tauri app's
/// shortcuts run before an input does: a chord bound to an action the field does not keep runs
/// it. A field never starts a sequence, and a modal dialog's field hears every key first.
pub(crate) fn on_field_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if modal(state) {
        return false;
    }
    let focus = Focus {
        field: true,
        terminal: false,
    };
    match state.keymap.press(ev, tx, focus) {
        Press::Action(action) => run_shortcut(state, action, tx),
        Press::Leader { .. } | Press::Unbound => false,
    }
}

fn modal(state: &State) -> bool {
    use crate::shell::Layer;
    matches!(
        state.window.layer,
        Some(Layer::NewSession | Layer::CloseDesktop)
    )
}

fn bound(state: &State, ev: &KeyEvent, tx: &Tx) -> bool {
    let focus = Focus {
        field: tx.in_text_field(),
        terminal: false,
    };
    state.keymap.press(ev, tx, focus) != Press::Unbound
}

/// Whether the shortcut's action ran.
fn run_shortcut(state: &mut State, action: Action, tx: &mut Tx) -> bool {
    let ran = run(state, action.clone(), tx) == Outcome::Ran;
    if ran {
        record_shortcut(state, &action);
    }
    ran
}

fn record_shortcut(state: &State, action: &Action) {
    if !crate::setting::on(&state.facts.settings, "analytics.enabled") {
        return;
    }
    let _ = state.daemon.send(&RecordAnalyticsMessage {
        stream: "app".into(),
        name: "shortcut".into(),
        props: Some(
            [("id".to_owned(), serde_json::Value::from(action.id()))]
                .into_iter()
                .collect(),
        ),
        ..RecordAnalyticsMessage::new()
    });
}
