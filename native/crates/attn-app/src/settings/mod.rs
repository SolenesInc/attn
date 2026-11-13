//! The settings dialog: its pages, its search and the autosave that keeps each change.

mod analytics;
mod appearance;
pub(crate) mod autosave;
mod bus;
mod pages;
mod sections;
mod tasks;
mod updates;
mod view;

use rune::{TextEdit, Tx};

use crate::State;
use crate::action::Action;
use crate::shell::Layer;
pub(crate) use appearance::text_scale;
pub(crate) use sections::Section;
pub(crate) use updates::updatable;
pub(crate) use view::dialog;

/// The text fields the dialog shows, each the daemon setting it edits, and whether its value
/// goes to the daemon trimmed.
const TEXT_FIELDS: &[(&str, bool)] = &[
    (appearance::NARROWEST_TILE_COLS, true),
    (crate::sidebar::width::SETTING, true),
];

/// The dialog's page, search and edits. They outlast the dialog closing, as the Tauri app's
/// do while it runs.
#[derive(Default)]
pub(crate) struct Dialog {
    pub(crate) section: Section,
    pub(crate) search: TextEdit,
    pub(crate) autosave: autosave::Autosave,
    tasks: tasks::Tasks,
    analytics: analytics::Analytics,
    updates: updates::Updates,
    bus: bus::Bus,
    /// The text field the focus was in at the last frame.
    editing: Option<String>,
    /// What waits for the fields to land: the dialog closing, or another page.
    after_flush: Option<Leave>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Leave {
    Close,
    Show(Section),
}

const FIELD_PREFIX: &str = "settings.input.";

/// The key of the text field that edits `setting`.
pub(crate) fn field_key(setting: &str) -> String {
    format!("{FIELD_PREFIX}{setting}{}", crate::keys::FIELD_KEY_SUFFIX)
}

fn setting_of_field(key: &str) -> Option<&str> {
    key.strip_prefix(FIELD_PREFIX)?
        .strip_suffix(crate::keys::FIELD_KEY_SUFFIX)
}

pub(crate) fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::Settings)
}

/// ⌘, opens the dialog, and closes it when it is open.
pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        close(state, tx);
    } else {
        open(state, tx);
    }
}

pub(crate) fn open(state: &mut State, tx: &mut Tx) {
    for (key, trim) in TEXT_FIELDS {
        let actual = state.facts.settings.get(*key).cloned().unwrap_or_default();
        state.window.settings.autosave.ensure(key, &actual, *trim);
    }
    state.window.settings.after_flush = None;
    state.window.layer = Some(Layer::Settings);
    tx.blur();
    arrive(state, tx);
}

/// Reads what the page just shown lists from the daemon.
fn arrive(state: &mut State, tx: &mut Tx) {
    tasks::arrive(state, tx);
    bus::arrive(state, tx);
    analytics::arrive(state, tx);
    updates::arrive(state);
}

/// Closes the dialog once every field has landed; a refused field keeps it open, showing why.
pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    leave(state, Leave::Close, tx);
}

pub(crate) fn select(state: &mut State, section: Section, tx: &mut Tx) {
    leave(state, Leave::Show(section), tx);
}

fn leave(state: &mut State, to: Leave, tx: &mut Tx) {
    if !state.window.settings.autosave.needs_flush() {
        go(state, to, tx);
        return;
    }
    state.window.settings.after_flush = Some(to);
    autosave::flush(state, tx);
    flushed(state, tx);
}

/// Goes where the dialog was asked to once the fields it flushed have landed, unless one was
/// refused: then it stays where it is, and the refusal shows.
pub(crate) fn flushed(state: &mut State, tx: &mut Tx) {
    let dialog = &state.window.settings;
    if dialog.autosave.saving() {
        return;
    }
    let Some(to) = state.window.settings.after_flush.take() else {
        return;
    };
    let refused_field = state
        .window
        .settings
        .autosave
        .refusals()
        .map(|(key, _)| key)
        .find(|key| TEXT_FIELDS.iter().any(|(field, _)| field == key))
        .map(field_key);
    match refused_field {
        Some(field) => tx.focus(field, false),
        None if state.window.settings.autosave.refusals().next().is_none() => go(state, to, tx),
        None => {}
    }
}

fn go(state: &mut State, to: Leave, tx: &mut Tx) {
    match to {
        Leave::Close => {
            if is_open(state) {
                state.window.layer = None;
                crate::picker::refocus_inline(state, tx);
            }
        }
        Leave::Show(section) if section != state.window.settings.section => {
            state.window.settings.section = section;
            arrive(state, tx);
        }
        Leave::Show(_) => {}
    }
}

/// Takes the daemon's settings into the fields the user has not changed, and reads the tasks
/// again when they changed under the Task runner page.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let settings = state.facts.settings.clone();
    state.window.settings.autosave.sync(&settings);
    tasks::follow(state, tx);
}

/// Before each frame: commits a text field the focus just left, reads the event bus when its
/// refresh is due, and lets Saved marks go.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    let focused = tx
        .focused()
        .and_then(setting_of_field)
        .map(str::to_owned)
        .filter(|_| is_open(state));
    let left = match &state.window.settings.editing {
        Some(was) if Some(was) != focused.as_ref() => Some(was.clone()),
        _ => None,
    };
    state.window.settings.editing = focused;
    if let Some(key) = left {
        autosave::commit_on_leave(state, &key, tx);
    }
    bus::poll(state, now, tx);
    state.window.settings.autosave.expire(now)
}

/// Runs the settings actions a key or the palette can ask for.
pub(crate) fn dispatch(state: &mut State, action: &Action, tx: &mut Tx) -> bool {
    match action {
        Action::UiOpenSettings => toggle(state, tx),
        Action::UiIncreaseFontSize | Action::UiDecreaseFontSize | Action::UiResetFontSize => {
            appearance::step_ui_scale(state, action, tx)
        }
        _ => return false,
    }
    true
}

/// The dialog as the automation `state` reports it, in the Tauri bridge's
/// `settings_get_state` words: whether it is open, its page and its search.
pub(crate) fn automation_state(state: &State) -> serde_json::Value {
    let dialog = &state.window.settings;
    serde_json::json!({
        "open": is_open(state),
        "activeSection": dialog.section.id(),
        "search": dialog.search.text,
        "saving": dialog.autosave.saving(),
        "refusals": dialog.autosave.refusals().map(|(k, r)| serde_json::json!({"key": k, "reason": r})).collect::<Vec<_>>(),
    })
}
