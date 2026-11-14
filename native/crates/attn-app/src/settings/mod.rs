//! The settings dialog: its pages, its search and the autosave that keeps each change.

mod agents;
mod analytics;
mod appearance;
pub(crate) mod autosave;
mod background;
mod bus;
mod catalogs;
mod delegation;
mod desktop;
mod harnesses;
mod hygiene;
mod models;
pub(crate) mod outposts;
mod pages;
pub(crate) mod phones;
mod plugins;
mod route;
mod sections;
mod senders;
mod tasks;
mod updates;
mod view;

use attn_client::Slices;
use rune::{TextEdit, Tx};

use crate::State;
use crate::action::Action;
use crate::shell::Layer;
pub(crate) use appearance::{garden_scale, text_scale};
pub(crate) use sections::Section;
pub(crate) use updates::updatable;
pub(crate) use view::dialog;

/// The text fields the dialog shows, each the daemon setting it edits, and whether its value
/// goes to the daemon trimmed.
const TEXT_FIELDS: &[(&str, bool)] = &[
    (appearance::NARROWEST_TILE_COLS, true),
    (crate::sidebar::width::SETTING, true),
    (desktop::EDITOR, false),
    (desktop::PROJECTS_DIRECTORY, false),
    (desktop::NOTEBOOK_ROOT, false),
    (hygiene::AUTO_SETTLE_ARM, true),
    (hygiene::AUTO_SETTLE_COUNTDOWN, true),
];

/// The dialog's page, search and edits. They outlast the dialog closing, as the Tauri app's
/// do while it runs.
#[derive(Default)]
pub(crate) struct Dialog {
    pub(crate) section: Section,
    pub(crate) search: TextEdit,
    pub(crate) autosave: autosave::Autosave,
    anchors: pages::Anchors,
    tasks: tasks::Tasks,
    analytics: analytics::Analytics,
    updates: updates::Updates,
    bus: bus::Bus,
    plugins: plugins::Plugins,
    hygiene: hygiene::Hygiene,
    background: background::Background,
    agents: agents::Agents,
    harnesses: harnesses::Harnesses,
    catalogs: catalogs::Catalogs,
    delegation: delegation::Delegation,
    outposts: outposts::Outposts,
    phones: phones::Phones,
    senders: senders::Senders,
    route_picker: Option<route::Picker>,
    /// Whether what the page lists changed since it was last brought up to date: the
    /// settings, plugins, harnesses or connection, or the page itself.
    stale: bool,
    /// The key of the text field the focus was in at the last frame.
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
const FIELD_SUFFIX: &str = ".field";

/// The key of the text field that edits `setting`.
pub(crate) fn field_key(setting: &str) -> String {
    format!("{FIELD_PREFIX}{setting}{FIELD_SUFFIX}")
}

/// The key of the field that edits `part` of the setting `setting`, for a setting several fields
/// edit together.
pub(crate) fn part_field_key(setting: &str, part: &str) -> String {
    format!("{FIELD_PREFIX}{setting}#{part}{FIELD_SUFFIX}")
}

fn setting_of_field(key: &str) -> Option<&str> {
    let field = key.strip_prefix(FIELD_PREFIX)?.strip_suffix(FIELD_SUFFIX)?;
    field.split('#').next()
}

/// The fields the page shows besides the dialog's own, each with how it reads and saves.
fn page_fields(state: &State) -> Vec<(String, autosave::Codec)> {
    match state.window.settings.section {
        Section::Agents => agents::fields(state),
        Section::BackgroundAgents => background::fields(),
        Section::Models => models::fields(),
        _ => Vec::new(),
    }
}

/// Follows the fields the page shows, before it first draws them; whether one is new.
fn ensure_page_fields(state: &mut State) -> bool {
    let mut added = false;
    for (key, codec) in page_fields(state) {
        let fields = &mut state.window.settings.autosave;
        added |= fields.field(&key).is_none();
        fields.ensure(&key, &state.facts.settings, codec);
    }
    added
}

/// Brings the page up to what it lists: its fields, the drafts its own fields show, the
/// harnesses and the model catalogs. Runs once each time one of them changed.
fn refresh(state: &mut State, tx: &mut Tx) -> bool {
    let mut shown = ensure_page_fields(state);
    shown |= background::follow(state);
    shown |= agents::follow(state);
    shown | harnesses::ensure(state, tx) | discover(state, tx)
}

/// Has the page brought up to date before the next frame.
fn invalidate(state: &mut State) {
    state.window.settings.stale = true;
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
    let settings = state.facts.settings.clone();
    for (key, trim) in TEXT_FIELDS {
        let codec = if *trim {
            autosave::TRIMMED
        } else {
            autosave::PLAIN
        };
        state.window.settings.autosave.ensure(key, &settings, codec);
    }
    ensure_page_fields(state);
    invalidate(state);
    harnesses::opened(state);
    state.window.settings.after_flush = None;
    hygiene::forget(state);
    state.window.layer = Some(Layer::Settings);
    tx.blur();
    plugins::open(state, tx);
    arrive(state, tx);
}

/// Reads what the page just shown lists from the daemon.
fn arrive(state: &mut State, tx: &mut Tx) {
    tasks::arrive(state, tx);
    bus::arrive(state, tx);
    analytics::arrive(state, tx);
    updates::arrive(state);
    delegation::arrive(state, tx);
    outposts::arrive(state);
    phones::arrive(state);
    senders::arrive(state);
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
    let dialog = &state.window.settings;
    let refused = dialog
        .autosave
        .refusals()
        .next()
        .map(|(key, _)| dialog.anchors.focus(key));
    match refused {
        Some(focus) => tx.focus(focus, false),
        None => go(state, to, tx),
    }
}

fn go(state: &mut State, to: Leave, tx: &mut Tx) {
    match to {
        Leave::Close => {
            if is_open(state) {
                state.window.settings.route_picker = None;
                state.window.layer = None;
                crate::picker::refocus_inline(state, tx);
            }
        }
        Leave::Show(section) if section != state.window.settings.section => {
            state.window.settings.section = section;
            state.window.settings.route_picker = None;
            ensure_page_fields(state);
            invalidate(state);
            arrive(state, tx);
        }
        Leave::Show(_) => {}
    }
}

/// Forgets the unmutes the lost connection may have dropped, so their rows show again.
pub(crate) fn disconnected(state: &mut State) {
    hygiene::forget(state);
}

/// Takes the daemon's settings into the fields the user has not changed, and reads the tasks
/// again when they changed under the Task runner page.
pub(crate) fn follow(state: &mut State, changed: Slices, tx: &mut Tx) {
    let listed = Slices::SETTINGS | Slices::PLUGINS | Slices::DELEGATION | Slices::CONNECTION;
    if changed.intersects(listed) {
        invalidate(state);
    }
    let settings = state.facts.settings.clone();
    state.window.settings.autosave.sync(&settings);
    catalogs::follow(state);
    tasks::follow(state, tx);
    outposts::follow(state, changed);
    phones::follow(state, changed);
    senders::follow(state, changed);
}

/// Before each frame: commits a text field the focus just left, reads the event bus when its
/// refresh is due, forgets unmutes the daemon has taken, cancels pairing codes the Phones page
/// left, and lets Saved marks go; whether one went.
pub(crate) fn poll(state: &mut State, now: f64, tx: &mut Tx) -> bool {
    hygiene::poll(state);
    phones::poll(state, tx);
    let open = is_open(state);
    let focused = tx
        .focused()
        .filter(|key| open && setting_of_field(key).is_some());
    if state.window.settings.editing.as_deref() != focused {
        let was = std::mem::replace(
            &mut state.window.settings.editing,
            focused.map(str::to_owned),
        );
        if let Some(key) = was.as_deref().and_then(setting_of_field) {
            let key = key.to_owned();
            autosave::commit_on_leave(state, &key, tx);
        }
    }
    let focus = tx.focused().map(str::to_owned).filter(|_| open);
    let mut shown = false;
    if is_open(state) {
        if std::mem::take(&mut state.window.settings.stale) {
            shown |= refresh(state, tx);
        }
        agents::poll(state, tx);
    }
    bus::poll(state, now, tx);
    shown |= delegation::poll(state, tx);
    let plugin_marks_went = plugins::poll(state, focus.as_deref(), now, tx);
    state.window.settings.autosave.expire(now) | plugin_marks_went | shown
}

/// Discovers the models the page's route fields and the open picker show.
fn discover(state: &mut State, tx: &mut Tx) -> bool {
    let wanted = match state.window.settings.section {
        Section::Agents => agents::wanted(state),
        Section::BackgroundAgents => background::wanted(state),
        Section::Delegation => delegation::wanted(state),
        Section::Models => models::wanted(state),
        _ => Vec::new(),
    };
    route::discover_wanted(state, wanted, tx)
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
