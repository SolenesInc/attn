mod commands;
mod view;

use attn_protocol::{
    CommandUsage, GetCommandUsageMessage, GetCommandUsageResultMessage, RecordCommandUsageMessage,
    RecordCommandUsageResultMessage,
};
use rune::{Key, KeyEvent, TextEdit, Tx};

use crate::shell::{Layer, Palette};
use crate::{Action, State};

pub(crate) use view::view;

const FIELD: &str = "palette.field";
const COMMANDS: char = '>';

/// What the search palette lists: the agents, or, behind a leading `>`, the commands.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Face {
    Agents,
    Commands,
}

fn face(query: &str) -> Face {
    if query.starts_with(COMMANDS) {
        Face::Commands
    } else {
        Face::Agents
    }
}

/// The palette's search: its query, the row the keyboard is on, and the command history it
/// ranks commands by, read once each time it opens.
#[derive(Default)]
pub struct Search {
    pub(crate) query: TextEdit,
    highlight: Option<String>,
    history: History,
    opening: u64,
    asked_for: String,
    pointer: Option<(f32, f32)>,
}

#[derive(Default)]
enum History {
    #[default]
    Loading,
    Ready(Vec<CommandUsage>),
}

/// A row the palette lists, and what picking it does.
pub(crate) struct Row {
    pub(crate) key: String,
    pub(crate) title: String,
    pub(crate) description: Option<String>,
    pub(crate) shortcut: Option<String>,
    command: Option<String>,
    action: Action,
}

/// Opens the search palette on `wanted`, or, while it is open, closes it when it already shows
/// `wanted` and otherwise turns it to `wanted`, keeping what the user typed.
pub(crate) fn open(state: &mut State, wanted: Face, tx: &mut Tx) {
    if state.window.layer == Some(Layer::Palette(Palette::Search)) {
        let text = state.window.search.query.text.clone();
        if face(&text) == wanted {
            close(state, tx);
            return;
        }
        let turned = match wanted {
            Face::Commands => format!("{COMMANDS}{text}"),
            Face::Agents => text[COMMANDS.len_utf8()..].to_owned(),
        };
        state.window.search.query = at_end(turned);
        state.window.search.highlight = None;
        return;
    }
    state.window.layer = Some(Layer::Palette(Palette::Search));
    let search = &mut state.window.search;
    search.query = at_end(match wanted {
        Face::Commands => ">".to_owned(),
        Face::Agents => String::new(),
    });
    search.highlight = None;
    search.pointer = None;
    search.opening += 1;
    ask_history(state, tx);
    tx.focus(FIELD, false);
}

fn at_end(text: String) -> TextEdit {
    let mut edit = TextEdit::new(text);
    edit.set_caret(edit.text.len());
    edit
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    state.window.layer = None;
    crate::picker::refocus_inline(state, tx);
}

fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::Palette(Palette::Search))
}

fn ask_history(state: &mut State, tx: &mut Tx) {
    let profile = state.facts.profiles.selected.clone();
    let search = &mut state.window.search;
    search.asked_for = profile.clone();
    if profile.is_empty() {
        search.history = History::Ready(Vec::new());
        return;
    }
    search.history = History::Loading;
    let opening = search.opening;
    let asked = profile.clone();
    state.ask(
        |id| GetCommandUsageMessage {
            request_id: id,
            profile_id: profile,
            ..GetCommandUsageMessage::new()
        },
        move |state, answer, tx| {
            let open = is_open(state);
            let search = &mut state.window.search;
            if !open || search.opening != opening || search.asked_for != asked {
                return;
            }
            let result: Option<GetCommandUsageResultMessage> = answer.parse();
            match result {
                Some(r) if r.success => search.history = History::Ready(r.entries),
                other => {
                    search.history = History::Ready(Vec::new());
                    let error = other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "History unavailable".into());
                    state.window.notify(format!("Command history: {error}"), tx);
                }
            }
        },
        tx,
    );
}

/// The rows the palette lists for its query, and whether the commands' history is still
/// loading.
pub(crate) fn rows(state: &State) -> (Vec<Row>, bool) {
    let search = &state.window.search;
    let query = &search.query.text;
    match face(query) {
        Face::Agents => (Vec::new(), false),
        Face::Commands => match &search.history {
            History::Loading => (Vec::new(), true),
            History::Ready(history) => (
                commands::ranked(state, &query[COMMANDS.len_utf8()..], history),
                false,
            ),
        },
    }
}

/// The row the keyboard is on: the one it was moved to while it is listed, else the first.
pub(crate) fn highlighted(search: &Search, rows: &[Row]) -> Option<usize> {
    search
        .highlight
        .as_ref()
        .and_then(|key| rows.iter().position(|r| &r.key == key))
        .or_else(|| (!rows.is_empty()).then_some(0))
}

pub(crate) fn edited(state: &mut State, _: &mut Tx) {
    state.window.search.highlight = None;
}

/// Arrows move the highlight without wrapping and Enter picks the highlighted row.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    let (rows, _) = rows(state);
    let at = highlighted(&state.window.search, &rows);
    match ev.key {
        Key::Down | Key::Up => {
            if let Some(at) = at {
                let to = match ev.key {
                    Key::Down => (at + 1).min(rows.len() - 1),
                    _ => at.saturating_sub(1),
                };
                state.window.search.highlight = Some(rows[to].key.clone());
            }
            true
        }
        Key::Enter => {
            if let Some(at) = at {
                let row = rows
                    .into_iter()
                    .nth(at)
                    .expect("the highlight is a listed row");
                pick(state, row, tx);
            }
            true
        }
        _ => false,
    }
}

/// The pointer moved over `key`'s row: the highlight follows it, but not a row that slid under
/// a pointer that stayed still.
pub(crate) fn pointed(state: &mut State, key: &str, at: (f32, f32)) {
    let search = &mut state.window.search;
    if search.pointer == Some(at) {
        return;
    }
    search.pointer = Some(at);
    search.highlight = Some(key.to_owned());
}

pub(crate) fn pick_key(state: &mut State, key: &str, tx: &mut Tx) {
    let (rows, _) = rows(state);
    if let Some(row) = rows.into_iter().find(|r| r.key == key) {
        pick(state, row, tx);
    }
}

/// A picked command counts toward the history of the profile it was picked in; then the
/// palette closes and the row's action runs.
fn pick(state: &mut State, row: Row, tx: &mut Tx) {
    if let Some(command) = row.command {
        record(state, command, tx);
    }
    close(state, tx);
    crate::dispatch(state, row.action, tx);
}

fn record(state: &mut State, command: String, tx: &mut Tx) {
    let profile = state.facts.profiles.selected.clone();
    if profile.is_empty() {
        return;
    }
    state.ask(
        |id| RecordCommandUsageMessage {
            request_id: id,
            profile_id: profile,
            command_id: command,
            ..RecordCommandUsageMessage::new()
        },
        |state, answer, tx| {
            let result: Option<RecordCommandUsageResultMessage> = answer.parse();
            if result.as_ref().is_some_and(|r| r.success) {
                return;
            }
            let error = result
                .and_then(|r| r.error)
                .filter(|e| !e.is_empty())
                .unwrap_or_else(|| "The command was not recorded".into());
            state.window.notify(format!("Command history: {error}"), tx);
        },
        tx,
    );
}

/// The shortcut that opens `face`, as the palette's badge shows it.
pub(crate) fn badge(state: &State, face: Face) -> Option<String> {
    let action = match face {
        Face::Agents => Action::UiActionMenu,
        Face::Commands => Action::UiCommandPalette,
    };
    state.keymap.binding(&action).map(|b| state.keymap.label(b))
}

pub(crate) fn face_of(search: &Search) -> Face {
    face(&search.query.text)
}

pub(crate) const fn field_key() -> &'static str {
    FIELD
}

pub(crate) fn command_count(state: &State) -> usize {
    commands::catalog(state).len()
}
