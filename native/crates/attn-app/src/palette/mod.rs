mod agents;
pub(crate) mod commands;
mod view;

pub(crate) use view::row_face;

use attn_protocol::{
    CommandUsage, GetCommandUsageMessage, GetCommandUsageResultMessage, RecordCommandUsageMessage,
    RecordCommandUsageResultMessage, Session,
};
use rune::{Key, KeyEvent, TextEdit, Tx};

use crate::keymap::Binding;
use crate::shell::{Layer, Palette};
use crate::sidebar::model::{AgentRow, display_name};
use crate::sidebar::queue::{self, QueueActions};
use crate::sidebar::snooze::{self, CHOICES, Choice};
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

/// The palette's search: its query, the row the keyboard is on, the command history it
/// ranks commands by, read once each time it opens, and the agent it is choosing a snooze
/// for.
#[derive(Default)]
pub struct Search {
    pub(crate) query: TextEdit,
    highlight: Option<String>,
    history: History,
    opening: u64,
    pointer: Option<(f32, f32)>,
    snoozing: Option<Snoozing>,
}

#[derive(Default)]
enum History {
    #[default]
    Loading,
    Ready(Vec<CommandUsage>),
}

/// The agent the palette is choosing a snooze for, the moment its times were read, and the
/// query the agents face goes back to.
struct Snoozing {
    session: String,
    opened_ms: i64,
    query: TextEdit,
}

/// A row the palette lists, how it looks, and what picking it does; a row nothing picks
/// parts the others.
pub(crate) struct Row {
    pub(crate) key: String,
    pub(crate) label: String,
    pub(crate) look: Look,
    pick: Option<Pick>,
}

impl Row {
    pub(crate) fn selectable(&self) -> bool {
        self.pick.is_some()
    }
}

/// The kind of thing a command acts on, which its row's icon shows, as the Tauri app's
/// command icons.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Icon {
    Context,
    Attention,
    Keys,
    Desktops,
    Notebook,
    Notifications,
    Garden,
    Delegation,
    Automations,
    Editor,
    ChiefSetup,
    ChiefStart,
    ChiefClose,
}

impl Icon {
    pub(crate) fn word(self) -> &'static str {
        match self {
            Icon::Context => "Context",
            Icon::Attention => "Attention",
            Icon::Keys => "Keys",
            Icon::Desktops => "Desktops",
            Icon::Notebook => "Notebook",
            Icon::Notifications => "Notifications",
            Icon::Garden => "Garden",
            Icon::Delegation => "Delegation",
            Icon::Automations => "Automations",
            Icon::Editor => "Editor",
            Icon::ChiefSetup | Icon::ChiefStart | Icon::ChiefClose => "Chief",
        }
    }

    pub(crate) fn glyph(self) -> attn_ui::Glyph {
        use attn_ui::{glyph, icon};
        match self {
            Icon::Context => (&icon::CONTEXT).into(),
            Icon::Attention => (&icon::ATTENTION).into(),
            Icon::Notifications => (&icon::BELL).into(),
            Icon::Keys => (&icon::KEYBOARD).into(),
            Icon::Desktops => (&icon::BOARD).into(),
            Icon::Notebook => (&icon::NOTEBOOK).into(),
            Icon::Garden => (&icon::GARDEN).into(),
            Icon::Delegation => glyph::SITEMAP.into(),
            Icon::Automations => (&icon::AUTOMATIONS).into(),
            Icon::Editor => (&icon::EDITOR).into(),
            Icon::ChiefSetup => (&icon::CHIEF).into(),
            Icon::ChiefStart => glyph::SUNRISE.into(),
            Icon::ChiefClose => (&icon::MOON).into(),
        }
    }
}

/// Where an agent row's agent stands with the user, as the Tauri app's `agentStatus`.
#[derive(Clone, Copy, PartialEq, Eq)]
pub(crate) enum Status {
    Unreachable,
    Chief,
    Snoozed,
    Approval,
    Waiting,
    Working,
    Idle,
}

impl Status {
    pub(crate) fn word(self) -> &'static str {
        match self {
            Status::Unreachable => "unreachable",
            Status::Chief => "chief",
            Status::Snoozed => "snoozed",
            Status::Approval => "approval",
            Status::Waiting => "waiting",
            Status::Working => "working",
            Status::Idle => "idle",
        }
    }
}

pub(crate) enum Look {
    Command {
        icon: Option<Icon>,
        description: Option<String>,
        shortcut: Option<Vec<String>>,
    },
    Agent {
        agent: Box<AgentRow>,
        crew: bool,
        status: Status,
        slot: String,
        age: String,
        tag: Option<String>,
    },
    Tile {
        word: &'static str,
        slot: String,
    },
    /// A crew member between sessions, or the Chief before its setup.
    Member {
        chief: bool,
        unconfigured: bool,
    },
    Choice {
        detail: String,
    },
    Divider,
}

enum Pick {
    Command { id: String, action: Action },
    Open(Action),
    Snooze(Choice),
}

/// What the palette shows now.
pub(crate) enum Mode {
    Agents,
    Commands,
    Snooze { name: String },
}

/// The palette's rows for what it shows, with its count, and whether the commands' history is
/// still loading.
pub(crate) struct Listing {
    pub(crate) mode: Mode,
    pub(crate) rows: Vec<Row>,
    pub(crate) count: Option<String>,
    pub(crate) loading: bool,
}

/// Opens the search palette on `wanted`, or, while it is open, closes it when it already shows
/// `wanted` and otherwise turns it to `wanted`, keeping what the user typed. Choosing a
/// snooze, either face's keys leave the choices for that face.
pub(crate) fn open(state: &mut State, wanted: Face, tx: &mut Tx) {
    if matches!(
        state.window.layer,
        Some(
            Layer::Settings
                | Layer::Shortcuts
                | Layer::ShortcutEditor
                | Layer::WhatsNew
                | Layer::DiagnosticReport
        )
    ) {
        return;
    }
    if is_open(state) {
        let snoozing = state.window.search.snoozing.is_some();
        leave_snooze(state);
        let search = &mut state.window.search;
        let text = search.query.text.clone();
        if face(&text) == wanted {
            if !snoozing {
                close(state, tx);
            }
            return;
        }
        let turned = match wanted {
            Face::Commands => format!("{COMMANDS}{text}"),
            Face::Agents => text[COMMANDS.len_utf8()..].to_owned(),
        };
        search.query = at_end(turned);
        search.highlight = None;
        return;
    }
    state.window.layer = Some(Layer::Palette(Palette::Search));
    state.tick(tx);
    let search = &mut state.window.search;
    search.query = at_end(match wanted {
        Face::Commands => ">".to_owned(),
        Face::Agents => String::new(),
    });
    search.highlight = None;
    search.pointer = None;
    search.snoozing = None;
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
    state.window.search.snoozing = None;
    crate::picker::refocus_inline(state, tx);
}

fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::Palette(Palette::Search))
}

fn ask_history(state: &mut State, tx: &mut Tx) {
    let profile = state.facts.profiles.selected.clone();
    let search = &mut state.window.search;
    if profile.is_empty() {
        search.history = History::Ready(Vec::new());
        return;
    }
    search.history = History::Loading;
    let opening = search.opening;
    state.ask(
        |id| GetCommandUsageMessage {
            request_id: id,
            profile_id: profile,
            ..GetCommandUsageMessage::new()
        },
        move |state, answer, tx| {
            let open = is_open(state);
            let search = &mut state.window.search;
            if !open || search.opening != opening {
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

fn session<'a>(state: &'a State, id: &str) -> Option<&'a Session> {
    state.facts.sessions.iter().find(|s| s.id == id)
}

fn allowed(state: &State, session: &Session) -> QueueActions {
    queue::actions(&state.facts, session, state.window.now_ms)
}

/// The agent being snoozed, while the queue still lets the user snooze it.
fn snoozed_agent(state: &State) -> Option<&Session> {
    let snoozing = state.window.search.snoozing.as_ref()?;
    session(state, &snoozing.session).filter(|s| allowed(state, s).snooze)
}

/// What the palette lists for its query.
pub(crate) fn listing(state: &State) -> Listing {
    let search = &state.window.search;
    if let (Some(snoozing), Some(agent)) = (&search.snoozing, snoozed_agent(state)) {
        return Listing {
            mode: Mode::Snooze {
                name: display_name(agent),
            },
            rows: CHOICES
                .iter()
                .map(|&choice| Row {
                    key: format!("snooze:{}", choice.id()),
                    label: choice.label().into(),
                    look: Look::Choice {
                        detail: choice.detail(snoozing.opened_ms),
                    },
                    pick: Some(Pick::Snooze(choice)),
                })
                .collect(),
            count: None,
            loading: false,
        };
    }
    let query = &search.query.text;
    match face(query) {
        Face::Agents => {
            let rows = agents::rows(state, query);
            let selectable = |rows: &[Row]| rows.iter().filter(|r| r.selectable()).count();
            let shown = selectable(&rows);
            let all = if query.trim().is_empty() {
                shown
            } else {
                selectable(&agents::rows(state, ""))
            };
            let count = format!("{shown} of {all}");
            Listing {
                mode: Mode::Agents,
                rows,
                count: Some(count),
                loading: false,
            }
        }
        Face::Commands => match &search.history {
            History::Loading => Listing {
                mode: Mode::Commands,
                rows: Vec::new(),
                count: None,
                loading: true,
            },
            History::Ready(history) => {
                let rows = commands::ranked(state, &query[COMMANDS.len_utf8()..], history);
                let count = format!("{} of {}", rows.len(), commands::catalog(state).len());
                Listing {
                    mode: Mode::Commands,
                    rows,
                    count: Some(count),
                    loading: false,
                }
            }
        },
    }
}

/// The row the keyboard is on: the one it was moved to while it is listed, else the first
/// that can be picked.
pub(crate) fn highlighted(search: &Search, rows: &[Row]) -> Option<usize> {
    search
        .highlight
        .as_ref()
        .and_then(|key| rows.iter().position(|r| &r.key == key && r.selectable()))
        .or_else(|| rows.iter().position(Row::selectable))
}

/// An edit searches again from the top; typing while choosing a snooze searches the agents
/// for what was typed.
pub(crate) fn edited(state: &mut State, _: &mut Tx) {
    let search = &mut state.window.search;
    search.snoozing = None;
    search.highlight = None;
}

/// Settle, priority and snooze while the palette is open: they act on the highlighted agent of
/// the agents face and on nothing elsewhere in the palette. Whether the palette took `action`.
pub(crate) fn queue_action(state: &mut State, action: &Action, tx: &mut Tx) -> bool {
    if !is_open(state)
        || !matches!(
            action,
            Action::SessionSettle | Action::SessionPriority | Action::SessionSnooze
        )
    {
        return false;
    }
    let listing = listing(state);
    let at = highlighted(&state.window.search, &listing.rows);
    if let (Mode::Agents, Some(Look::Agent { agent, .. })) =
        (&listing.mode, at.map(|i| &listing.rows[i].look))
    {
        act_on(state, action.clone(), &agent.id.clone(), tx);
    }
    true
}

/// Arrows move the highlight over the rows that can be picked, without wrapping; Enter picks
/// the highlighted row; choosing a snooze, Escape goes back to the agents.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let listing = listing(state);
    let at = highlighted(&state.window.search, &listing.rows);
    if matches!(listing.mode, Mode::Snooze { .. }) && ev.key == Key::Escape {
        leave_snooze(state);
        return true;
    }
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    match ev.key {
        Key::Down | Key::Up => {
            if let Some(at) = at {
                let pickable = |i: &usize| listing.rows[*i].selectable();
                let to = match ev.key {
                    Key::Down => (at + 1..listing.rows.len()).find(pickable),
                    _ => (0..at).rev().find(pickable),
                };
                if let Some(to) = to {
                    state.window.search.highlight = Some(listing.rows[to].key.clone());
                }
            }
            true
        }
        Key::Enter => {
            if let Some(at) = at {
                let row = listing
                    .rows
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

fn act_on(state: &mut State, action: Action, agent: &str, tx: &mut Tx) {
    state.tick(tx);
    let Some(allowed) = session(state, agent).map(|s| allowed(state, s)) else {
        return;
    };
    match action {
        Action::SessionSettle if allowed.settle => crate::sidebar::settle(state, agent),
        Action::SessionPriority => crate::sidebar::toggle_priority(state, agent),
        Action::SessionSnooze if allowed.snooze => {
            let opened_ms = state.window.now_ms;
            let search = &mut state.window.search;
            let query = std::mem::take(&mut search.query);
            search.snoozing = Some(Snoozing {
                session: agent.to_owned(),
                opened_ms,
                query,
            });
            search.highlight = None;
        }
        _ => {}
    }
}

/// Back to the agents with the query they had and the snoozed agent highlighted.
fn leave_snooze(state: &mut State) {
    let search = &mut state.window.search;
    if let Some(snoozing) = search.snoozing.take() {
        search.query = snoozing.query;
        search.highlight = Some(format!("agent:{}", snoozing.session));
    }
}

/// Drops the snooze choices once their agent can no longer be snoozed.
pub(crate) fn follow(state: &mut State) {
    if state.window.search.snoozing.is_some() && snoozed_agent(state).is_none() {
        leave_snooze(state);
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
    if let Some(row) = listing(state).rows.into_iter().find(|r| r.key == key) {
        pick(state, row, tx);
    }
}

/// A picked command counts toward the history of the profile it was picked in; then the
/// palette closes and the row's action runs. A snooze choice snoozes the agent and goes back
/// to the agents.
fn pick(state: &mut State, row: Row, tx: &mut Tx) {
    let action = match row.pick {
        None => return,
        Some(Pick::Snooze(choice)) => {
            if let Some(snoozing) = &state.window.search.snoozing {
                snooze::snooze_turn(state, &snoozing.session, choice, snoozing.opened_ms);
            }
            leave_snooze(state);
            return;
        }
        Some(Pick::Command { id, action }) => {
            record(state, id, tx);
            action
        }
        Some(Pick::Open(action)) => action,
    };
    close(state, tx);
    crate::dispatch(state, action, tx);
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

/// The most agent rows the queue bar's peek lists.
const PEEK_ROWS: usize = 16;

/// The agents face with no query as the queue bar's peek lists it, as the Tauri app's
/// `WaitingPeek`: no automation runs, at most `PEEK_ROWS` rows besides the dividers, and how
/// many more there are.
pub(crate) fn peek_rows(state: &State) -> (Vec<Row>, usize) {
    let run = |row: &Row| {
        row.key.strip_prefix("agent:").is_some_and(|id| {
            state
                .facts
                .sessions
                .iter()
                .any(|s| s.id == id && s.automation.is_some())
        })
    };
    let rows: Vec<Row> = agents::rows(state, "")
        .into_iter()
        .filter(|row| !run(row))
        .collect();
    let total = rows
        .iter()
        .filter(|r| !matches!(r.look, Look::Divider))
        .count();
    let mut shown = Vec::new();
    let mut entries = 0;
    for row in rows {
        if !matches!(row.look, Look::Divider) {
            if entries == PEEK_ROWS {
                break;
            }
            entries += 1;
        }
        shown.push(row);
    }
    (shown, total - entries)
}

pub(crate) use agents::run_rows;

/// Opens what the peek's row `key` names.
pub(crate) fn open_peeked(state: &mut State, key: &str, tx: &mut Tx) {
    let picked = peek_rows(state)
        .0
        .into_iter()
        .chain(run_rows(state))
        .find(|r| r.key == key)
        .and_then(|r| r.pick);
    if let Some(Pick::Open(action)) = picked {
        crate::dispatch(state, action, tx);
    }
}

/// The keys bound to `action`, as the user reads them.
pub(crate) fn label_of(state: &State, action: &Action) -> Option<String> {
    state.keymap.binding(action).map(|b| state.keymap.label(b))
}

fn keycaps_of(state: &State, action: &Action) -> Option<Vec<String>> {
    state
        .keymap
        .binding(action)
        .map(|b| state.keymap.keycaps(b))
}

/// The palette's badge: the keys that open what it shows.
pub(crate) fn badge(state: &State, mode: &Mode) -> Option<String> {
    let action = match mode {
        Mode::Commands => Action::UiCommandPalette,
        Mode::Agents | Mode::Snooze { .. } => Action::UiActionMenu,
    };
    label_of(state, &action)
}

/// The footer's hint for a queue action: its keys, or why it has none here.
pub(crate) fn action_hint(state: &State, action: &Action, word: &str) -> String {
    match state.keymap.binding(action) {
        Some(binding @ Binding::Chord(_)) => format!("{} {word}", state.keymap.label(binding)),
        _ => format!("{word} needs a single-key shortcut (chords don't work here)"),
    }
}

/// What the queue lets the user do to the agent on `row`.
pub(crate) fn row_actions(state: &State, row: Option<&Row>) -> QueueActions {
    match row.map(|r| &r.look) {
        Some(Look::Agent { agent, .. }) => session(state, &agent.id)
            .map(|s| allowed(state, s))
            .unwrap_or_default(),
        _ => QueueActions::default(),
    }
}

/// The next wall-clock moment an agent row of the open palette reads differently: a turn age
/// changes, or a snooze ends.
pub(crate) fn next_change(state: &State, now_ms: i64) -> Option<i64> {
    if !is_open(state) {
        return None;
    }
    state
        .facts
        .sessions
        .iter()
        .filter_map(|s| {
            let until = s.turn_snoozed_until.as_deref();
            if queue::is_snoozed(until, now_ms) {
                until.and_then(queue::epoch_ms)
            } else if s.turn_owed == Some(true) {
                queue::next_age_change(s.turn_opened_at.as_deref(), now_ms)
            } else {
                None
            }
        })
        .min()
}

pub(crate) const fn field_key() -> &'static str {
    FIELD
}
