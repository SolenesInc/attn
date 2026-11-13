mod agents;
mod names;
mod paths;
mod repo;
mod view;

use attn_protocol::{
    BrowseDirectoryMessage, BrowseDirectoryResultMessage, CreateWorktreeMessage,
    CreateWorktreeResultMessage, DelegationPreferencesGetMessage,
    DelegationPreferencesResultMessage, GetRecentLocationsMessage, GetRepoInfoMessage,
    GetRepoInfoResultMessage, InspectPathMessage, InspectPathResultMessage, RecentLocation,
    RecentLocationsResultMessage, SessionPlacement, SetSettingMessage, SpawnResultMessage,
    SpawnSessionMessage,
};
use rune::{Chord, Key, KeyEvent, KeyPlatform, TextEdit, Tx};

use crate::State;
pub(crate) use agents::{Agents, TERMINAL};
pub(crate) use view::{dialog, inline, worktree_setup};

/// How long typing settles before the picker browses the typed folder, as in the Tauri app.
const BROWSE_DELAY: f64 = 0.15;
const MAX_RECENTS: usize = 10;
const SAVED_AGENT: &str = "new_session_agent";
const SAVED_YOLO: &str = "new_session_yolo_local";
const PROJECTS: &str = "projects_directory";
pub(crate) const NO_DESKTOP: &str =
    "No desktop is open yet; choose a profile before starting an agent.";

/// Where a new pane goes beside the one it starts from.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) enum Direction {
    #[default]
    Vertical,
    Horizontal,
}

impl Direction {
    fn wire(self) -> &'static str {
        match self {
            Direction::Vertical => "vertical",
            Direction::Horizontal => "horizontal",
        }
    }
}

/// The picker over the app (⌘N) or the one an empty desktop shows in place of tiles.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Which {
    Dialog,
    Inline,
}

impl Which {
    pub(crate) fn key(self) -> &'static str {
        match self {
            Which::Dialog => "new-session",
            Which::Inline => "launcher",
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Catalog {
    Unasked,
    Loading,
    Loaded(Vec<String>),
    Failed(String),
}

/// What the new-session pickers share, and remember between openings.
pub struct Launching {
    pub(crate) dialog: Option<Picker>,
    /// The launcher of an empty desktop, kept while that desktop stays empty so a pick the
    /// user left behind still starts there.
    pub(crate) inline: Option<Picker>,
    pub(crate) starting: Option<Starting>,
    /// The empty desktop on screen, whose launcher shows.
    shown_on: Option<String>,
    pub(crate) home: String,
    pub(crate) recents: Vec<RecentLocation>,
    pub(crate) catalog: Catalog,
    pub(crate) worktree: Option<repo::Setup>,
    openings: u64,
    /// The text field of a picker that is gone, so a late edit has somewhere to land.
    spare: TextEdit,
}

impl Launching {
    /// Which launchers are kept, and where: what `follow` changes.
    fn kept(&self) -> (Option<u64>, Option<String>, Option<String>) {
        (
            self.inline.as_ref().map(|p| p.opening),
            self.starting.as_ref().map(|s| s.desktop.clone()),
            self.shown_on.clone(),
        )
    }

    /// The launcher on screen: the one of the empty desktop shown.
    pub(crate) fn shown_inline(&self) -> Option<&Picker> {
        self.inline
            .as_ref()
            .filter(|p| p.desktop.is_some() && p.desktop == self.shown_on)
    }

    pub(crate) fn shown_starting(&self) -> Option<&Starting> {
        self.starting
            .as_ref()
            .filter(|s| self.shown_on.as_deref() == Some(s.desktop.as_str()))
    }
}

impl Default for Launching {
    fn default() -> Self {
        Launching {
            dialog: None,
            inline: None,
            starting: None,
            shown_on: None,
            home: String::new(),
            recents: Vec::new(),
            catalog: Catalog::Unasked,
            worktree: None,
            openings: 0,
            spare: TextEdit::default(),
        }
    }
}

/// An empty desktop's pick that is starting, by folder name, until its pane arrives.
pub(crate) struct Starting {
    pub(crate) desktop: String,
    pub(crate) name: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Operation {
    Inspecting,
    LoadingRepo,
}

impl Operation {
    pub(crate) fn label(self) -> &'static str {
        match self {
            Operation::Inspecting => "Inspecting path",
            Operation::LoadingRepo => "Loading repo options",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct Highlight {
    pub(crate) index: usize,
    /// The picker put it on the top recent itself; the user did not move to it.
    pub(crate) automatic: bool,
}

pub(crate) struct Picker {
    pub(crate) which: Which,
    /// Which opening this is, so an answer asked for by an earlier one is not taken for its.
    opening: u64,
    /// The desktop a pick lands on: the inline launcher's own, or the one the dialog was asked
    /// from. `None` lands on the current desktop.
    pub(crate) desktop: Option<String>,
    /// The pane a pick goes beside, when the dialog was asked from one.
    anchor: Option<String>,
    direction: Direction,
    pub(crate) input: TextEdit,
    pub(crate) highlight: Option<Highlight>,
    /// Whether the user has typed or completed since the picker opened.
    typed: bool,
    recents_fresh: bool,
    pub(crate) directories: Vec<String>,
    pub(crate) browsing: Option<String>,
    browse_at: Option<f64>,
    pub(crate) operation: Option<(Operation, String)>,
    pub(crate) error: Option<String>,
    /// Bumped by every change that makes answers to earlier requests stale.
    generation: u64,
    pub(crate) chooser: Option<repo::Chooser>,
    /// The agent the user picked, or the saved one; [`Picker::agent`] is what launches.
    wanted: String,
    pub(crate) yolo: bool,
}

/// One row the path list offers.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Row {
    pub(crate) recent: bool,
    pub(crate) name: String,
    /// What Tab fills in and Enter opens.
    pub(crate) path: String,
    pub(crate) shown_path: Option<String>,
}

impl Picker {
    fn open(
        which: Which,
        desktop: Option<String>,
        anchor: Option<String>,
        direction: Direction,
        agent: Option<&str>,
        state: &State,
    ) -> Picker {
        let settings = &state.facts.settings;
        let launching = &state.window.launching;
        let projects = settings.get(PROJECTS).map(String::as_str).unwrap_or("");
        let text = paths::initial_input(projects, &launching.home);
        let preferred = agent
            .or(settings.get(SAVED_AGENT).map(String::as_str))
            .unwrap_or("claude");
        let mut input = TextEdit::new(text.clone());
        input.anchor = text.len();
        input.focus = text.len();
        Picker {
            which,
            opening: launching.openings + 1,
            desktop,
            anchor,
            direction,
            browse_at: None,
            input,
            highlight: None,
            typed: false,
            recents_fresh: false,
            directories: Vec::new(),
            browsing: None,
            operation: None,
            error: None,
            generation: 0,
            chooser: None,
            wanted: preferred.to_owned(),
            yolo: agents::saved_yolo(settings),
        }
    }

    pub(crate) fn field_key(&self) -> String {
        format!("{}.path.field", self.which.key())
    }

    /// The recents that match the typed text, then the folders the daemon listed.
    pub(crate) fn rows(&self, launching: &Launching) -> Vec<Row> {
        let home = &launching.home;
        let wanted = paths::expand(&self.input.text, home).to_lowercase();
        let recents = launching
            .recents
            .iter()
            .filter(|r| r.path.to_lowercase().contains(&wanted))
            .take(MAX_RECENTS)
            .map(|r| {
                let shown = paths::display(&r.path, home);
                Row {
                    recent: true,
                    name: paths::basename(&r.path).to_owned(),
                    path: shown.clone(),
                    shown_path: Some(shown),
                }
            });
        let directories = self.directories.iter().map(|path| Row {
            recent: false,
            name: paths::basename(path).to_owned(),
            path: path.clone(),
            shown_path: None,
        });
        recents.chain(directories).collect()
    }

    /// The match Tab completes to when nothing is highlighted.
    pub(crate) fn ghost(&self, rows: &[Row]) -> Option<String> {
        let typed = self.input.text.trim();
        if typed.is_empty() {
            return None;
        }
        let candidate = rows
            .iter()
            .find(|row| paths::starts_with_ignoring_case(&row.path, typed))?;
        (candidate.path != self.input.text
            && paths::starts_with_ignoring_case(&candidate.path, &self.input.text))
        .then(|| candidate.path.clone())
    }

    pub(crate) fn agents(&self, state: &State) -> Agents {
        Agents::from_settings(&state.facts.settings)
    }

    /// The agent a launch starts: the wanted one while this machine has it, else the first
    /// that is there.
    pub(crate) fn agent(&self, state: &State) -> String {
        self.agents(state).resolve(&self.wanted)
    }

    fn stale(&mut self) {
        self.generation += 1;
    }
}

impl Launching {
    pub(crate) fn picker(&self, which: Which) -> Option<&Picker> {
        match which {
            Which::Dialog => self.dialog.as_ref(),
            Which::Inline => self.inline.as_ref(),
        }
    }

    pub(crate) fn picker_mut(&mut self, which: Which) -> Option<&mut Picker> {
        match which {
            Which::Dialog => self.dialog.as_mut(),
            Which::Inline => self.inline.as_mut(),
        }
    }

    pub(crate) fn input_mut(&mut self, which: Which) -> &mut TextEdit {
        match which {
            Which::Dialog => self.dialog.as_mut(),
            Which::Inline => self.inline.as_mut(),
        }
        .map_or(&mut self.spare, |picker| &mut picker.input)
    }
}

/// ⌘N, ⌘⇧N, ⌘T and New agent: the empty desktop's launcher takes it when one shows and no
/// harness was named, else the dialog opens.
pub(crate) fn new_session(
    state: &mut State,
    direction: Direction,
    agent: Option<&str>,
    tx: &mut Tx,
) {
    if agent.is_none()
        && let Some(picker) = state.window.launching.shown_inline()
    {
        let key = focus_key(picker);
        if state.window.layer.is_none() {
            tx.focus(key, false);
        }
        return;
    }
    let picker = Picker::open(Which::Dialog, None, None, direction, agent, state);
    let key = picker.field_key();
    state.window.launching.openings = picker.opening;
    state.window.launching.dialog = Some(picker);
    state.window.layer = Some(crate::shell::Layer::NewSession);
    tx.focus(key, false);
    opened(state, Which::Dialog, tx);
}

fn focus_key(picker: &Picker) -> String {
    match &picker.chooser {
        Some(chooser) => chooser.focus_key(picker.which),
        None => picker.field_key(),
    }
}

/// Asks what a newly opened picker shows: this machine's recents and, until it has them, the
/// harness catalog. A typed starting path browses once typing would.
fn opened(state: &mut State, which: Which, tx: &mut Tx) {
    if matches!(
        state.window.launching.catalog,
        Catalog::Unasked | Catalog::Failed(_)
    ) {
        ask_catalog(state, tx);
    }
    let opening = state
        .window
        .launching
        .picker(which)
        .map_or(0, |p| p.opening);
    state.ask(
        |id| GetRecentLocationsMessage {
            home_only: Some(true),
            request_id: Some(id),
            ..GetRecentLocationsMessage::new()
        },
        move |state, reply, _| {
            let result: Option<RecentLocationsResultMessage> = reply.parse();
            let launching = &mut state.window.launching;
            let Some(result) = result.filter(|r| r.success) else {
                launching.recents.clear();
                return;
            };
            if let Some(home) = result.home_path.filter(|h| !h.is_empty()) {
                launching.home = home;
            }
            launching.recents = result.recent_locations;
            if let Some(picker) = launching.picker_mut(which).filter(|p| p.opening == opening) {
                picker.recents_fresh = true;
            }
            highlight_top_recent(state, which);
        },
        tx,
    );
    if let Some(picker) = state.window.launching.picker_mut(which)
        && !picker.input.text.is_empty()
    {
        let at = tx.clock() + BROWSE_DELAY;
        picker.browse_at = Some(at);
        tx.wake_at(at);
    }
}

/// Asks again what the open pickers show, after the connection that would have answered them
/// ended.
pub(crate) fn reconnected(state: &mut State, tx: &mut Tx) {
    if state.window.launching.catalog == Catalog::Loading {
        state.window.launching.catalog = Catalog::Unasked;
    }
    for which in [Which::Dialog, Which::Inline] {
        if state.window.launching.picker(which).is_some() {
            opened(state, which, tx);
        }
    }
}

fn ask_catalog(state: &mut State, tx: &mut Tx) {
    state.window.launching.catalog = Catalog::Loading;
    state.ask(
        |id| DelegationPreferencesGetMessage {
            request_id: id,
            ..DelegationPreferencesGetMessage::new()
        },
        |state, reply, _| {
            let result: Option<DelegationPreferencesResultMessage> = reply.parse();
            state.window.launching.catalog = match result {
                Some(r) if r.success && r.preferences.is_some() => Catalog::Loaded(
                    r.harnesses
                        .unwrap_or_default()
                        .into_iter()
                        .map(|h| h.id)
                        .collect(),
                ),
                Some(r) => Catalog::Failed(
                    r.error
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Failed to load harnesses".into()),
                ),
                None => Catalog::Failed("Failed to load harnesses".into()),
            };
        },
        tx,
    );
}

pub(crate) fn retry_catalog(state: &mut State, tx: &mut Tx) {
    ask_catalog(state, tx);
}

fn highlight_top_recent(state: &mut State, which: Which) {
    let launching = &state.window.launching;
    let Some(picker) = launching.picker(which) else {
        return;
    };
    if picker.typed
        || !picker.recents_fresh
        || picker.highlight.is_some()
        || picker.chooser.is_some()
        || !picker.rows(launching).first().is_some_and(|row| row.recent)
    {
        return;
    }
    if let Some(picker) = state.window.launching.picker_mut(which) {
        picker.highlight = Some(Highlight {
            index: 0,
            automatic: true,
        });
    }
}

/// The user changed a picker's path: earlier answers go stale, the highlight goes, and the
/// folder is browsed once typing settles.
pub(crate) fn edited(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(picker) = state.window.launching.picker_mut(which) else {
        return;
    };
    picker.stale();
    picker.typed = true;
    picker.highlight = None;
    picker.operation = None;
    picker.error = None;
    let at = tx.clock() + BROWSE_DELAY;
    picker.browse_at = Some(at);
    tx.wake_at(at);
}

/// Browses each picker whose typing has settled. Returns whether any did.
pub(crate) fn browse_due(state: &mut State, tx: &mut Tx) -> bool {
    let due: Vec<Which> = [Which::Dialog, Which::Inline]
        .into_iter()
        .filter(|which| {
            state
                .window
                .launching
                .picker_mut(*which)
                .and_then(|p| p.browse_at.take_if(|at| tx.clock() >= *at))
                .is_some()
        })
        .collect();
    for which in &due {
        browse(state, *which, tx);
    }
    !due.is_empty()
}

fn browse(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(picker) = state.window.launching.picker_mut(which) else {
        return;
    };
    let input = picker.input.text.clone();
    if input.is_empty() {
        picker.directories.clear();
        picker.browsing = None;
        return;
    }
    let (asked, opening) = (input.clone(), picker.opening);
    state.ask(
        |id| BrowseDirectoryMessage {
            input_path: input,
            request_id: Some(id),
            ..BrowseDirectoryMessage::new()
        },
        move |state, reply, _| {
            let result: Option<BrowseDirectoryResultMessage> = reply.parse();
            let launching = &mut state.window.launching;
            if let Some(home) = result
                .as_ref()
                .and_then(|r| r.home_path.clone())
                .filter(|h| !h.is_empty())
            {
                launching.home = home;
            }
            let home = launching.home.clone();
            let Some(picker) = launching
                .picker_mut(which)
                .filter(|p| p.opening == opening && p.input.text == asked)
            else {
                return;
            };
            match result.filter(|r| r.success) {
                Some(result) => {
                    picker.directories = result
                        .entries
                        .iter()
                        .filter(|e| e.is_dir)
                        .map(|e| paths::display(&e.path, &home))
                        .collect();
                    picker.browsing = (!result.directory.is_empty())
                        .then(|| paths::display(&result.directory, &home));
                }
                None => {
                    picker.directories.clear();
                    picker.browsing = None;
                }
            }
            let launching = &state.window.launching;
            let rows = launching
                .picker(which)
                .map_or(0, |p| p.rows(launching).len());
            if let Some(picker) = state.window.launching.picker_mut(which)
                && picker.highlight.is_some_and(|h| h.index >= rows)
            {
                picker.highlight = None;
            }
        },
        tx,
    );
}

/// Keys a picker hears while it, or a control in it, has focus. Returns whether it took the
/// key.
pub(crate) fn on_key(state: &mut State, which: Which, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if agent_key(state, which, ev, tx) {
        return true;
    }
    let Some(picker) = state.window.launching.picker(which) else {
        return false;
    };
    if picker.chooser.is_some() {
        return repo::on_key(state, which, ev, tx);
    }
    let rows = picker.rows(&state.window.launching);
    let picker = state
        .window
        .launching
        .picker_mut(which)
        .expect("the picker is open");
    match ev.key {
        Key::Down if !rows.is_empty() => {
            let index = picker.highlight.map_or(0, |h| (h.index + 1) % rows.len());
            picker.highlight = Some(Highlight {
                index,
                automatic: false,
            });
            true
        }
        Key::Up if !rows.is_empty() => {
            let index = match picker.highlight {
                None | Some(Highlight { index: 0, .. }) => rows.len() - 1,
                Some(h) => h.index - 1,
            };
            picker.highlight = Some(Highlight {
                index,
                automatic: false,
            });
            true
        }
        Key::Tab => {
            let completion = picker
                .highlight
                .and_then(|h| rows.get(h.index))
                .map(|row| row.path.clone())
                .or_else(|| picker.ghost(&rows));
            if let Some(text) = completion.filter(|t| !t.is_empty()) {
                picker.input = TextEdit::new(text.clone());
                picker.input.anchor = text.len();
                picker.input.focus = text.len();
                edited(state, which, tx);
            }
            true
        }
        Key::Enter => {
            let raw = picker
                .highlight
                .and_then(|h| rows.get(h.index))
                .map(|row| row.path.clone())
                .unwrap_or_else(|| picker.input.text.clone());
            if !raw.trim().is_empty() {
                open_path(state, which, &raw, tx);
            }
            true
        }
        Key::Escape => {
            step_back(state, which, tx);
            true
        }
        _ => false,
    }
}

/// Escape: a highlight the user made goes first, then the dialog closes. The inline launcher
/// never goes.
fn step_back(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(picker) = state.window.launching.picker_mut(which) else {
        return;
    };
    if picker.highlight.is_some_and(|h| !h.automatic) {
        picker.highlight = None;
        return;
    }
    if which == Which::Dialog {
        close(state, tx);
    }
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    state.window.launching.dialog = None;
    if state.window.layer == Some(crate::shell::Layer::NewSession) {
        state.window.layer = None;
    }
    refocus_inline(state, tx);
}

/// Gives the empty desktop's launcher the keyboard back once nothing covers it.
pub(crate) fn refocus_inline(state: &State, tx: &mut Tx) {
    refocus_inline_window(&state.window, tx);
}

pub(crate) fn refocus_inline_window(window: &crate::shell::Window, tx: &mut Tx) {
    if window.layer.is_none()
        && let Some(picker) = window.launching.shown_inline()
    {
        tx.focus(focus_key(picker), false);
    }
}

/// ⌥T, ⌥Q and ⌥1 to ⌥9, by key position, wherever the keyboard is in the picker.
fn agent_key(state: &mut State, which: Which, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if !ev.mods.alt || ev.mods.cmd || ev.mods.ctrl {
        return false;
    }
    let platform = KeyPlatform::current();
    let pressed = |spec: &str| {
        Chord::parse(spec)
            .expect("a picker chord parses")
            .matches_tx_on(ev, tx, platform)
    };
    let Some(picker) = state.window.launching.picker(which) else {
        return false;
    };
    let agents = picker.agents(state);
    if pressed("alt+q") {
        toggle_yolo(state, which);
        return true;
    }
    if pressed("alt+t") {
        choose_agent(state, which, TERMINAL);
        return true;
    }
    let numbered = numbered(&agents, &state.window.launching.catalog);
    for (n, agent) in numbered.iter().enumerate().take(9) {
        if pressed(&format!("alt+{}", n + 1)) {
            if agents.selectable(agent) {
                let agent = agent.clone();
                choose_agent(state, which, &agent);
            }
            return true;
        }
    }
    (1..=9).any(|n| pressed(&format!("alt+{n}")))
}

/// The agents ⌥1 to ⌥9 pick, in list order, once the catalog says the order.
pub(crate) fn numbered(agents: &Agents, catalog: &Catalog) -> Vec<String> {
    match catalog {
        Catalog::Loaded(order) => agents
            .listed(Some(order))
            .into_iter()
            .filter(|a| a != TERMINAL)
            .collect(),
        _ => Vec::new(),
    }
}

pub(crate) fn listed(agents: &Agents, catalog: &Catalog) -> Vec<String> {
    match catalog {
        Catalog::Loaded(order) => agents.listed(Some(order)),
        Catalog::Failed(_) => agents.listed(Some(&[])),
        Catalog::Unasked | Catalog::Loading => agents.listed(None),
    }
}

pub(crate) fn choose_agent(state: &mut State, which: Which, agent: &str) {
    let Some(picker) = state.window.launching.picker_mut(which) else {
        return;
    };
    if !Agents::from_settings(&state.facts.settings).selectable(agent) {
        return;
    }
    picker.wanted = agent.to_owned();
    if agent != TERMINAL {
        set_setting(state, SAVED_AGENT, agent);
    }
}

pub(crate) fn toggle_yolo(state: &mut State, which: Which) {
    let supported = state
        .window
        .launching
        .picker(which)
        .is_some_and(|p| agents::supports_yolo(&state.facts.settings, &p.agent(state)));
    let Some(picker) = state
        .window
        .launching
        .picker_mut(which)
        .filter(|_| supported)
    else {
        return;
    };
    picker.yolo = !picker.yolo;
    let value = if picker.yolo { "true" } else { "false" };
    set_setting(state, SAVED_YOLO, value);
}

pub(crate) fn yolo_on(state: &State, picker: &Picker) -> bool {
    picker.yolo && agents::supports_yolo(&state.facts.settings, &picker.agent(state))
}

fn set_setting(state: &State, key: &str, value: &str) {
    let _ = state.daemon.send(&SetSettingMessage {
        key: key.into(),
        value: value.into(),
        ..SetSettingMessage::new()
    });
}

/// Opens what the user picked or typed: the daemon says whether it is a folder and whether it
/// sits in a repository, whose checkout and worktrees the picker then offers.
pub(crate) fn open_path(state: &mut State, which: Which, raw: &str, tx: &mut Tx) {
    let home = state.window.launching.home.clone();
    let path = paths::normalize(raw, &home);
    let Some(picker) = state.window.launching.picker_mut(which) else {
        return;
    };
    if path.is_empty() {
        return;
    }
    picker.stale();
    picker.error = None;
    picker.operation = Some((Operation::Inspecting, paths::display(&path, &home)));
    let generation = picker.generation;
    let asked = path.clone();
    state.ask(
        |id| InspectPathMessage {
            path: asked,
            request_id: Some(id),
            ..InspectPathMessage::new()
        },
        move |state, reply, tx| {
            let Some(picker) = current(state, which, generation) else {
                return;
            };
            picker.operation = None;
            let result: Option<InspectPathResultMessage> = reply.parse();
            let inspection = match result {
                Some(InspectPathResultMessage {
                    success: true,
                    inspection: Some(inspection),
                    ..
                }) => inspection,
                Some(result) => {
                    picker.error = Some(
                        result
                            .error
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Failed to inspect path".into()),
                    );
                    return;
                }
                None => {
                    picker.error = Some("Failed to inspect path".into());
                    return;
                }
            };
            if !inspection.exists || !inspection.is_directory {
                picker.error = Some(format!("Directory not found: {path}"));
                return;
            }
            if let Some(home) = inspection.home_path.filter(|h| !h.is_empty()) {
                state.window.launching.home = home;
            }
            let resolved = inspection.resolved_path;
            match inspection.repo_root.filter(|r| !r.is_empty()) {
                Some(root) => load_repo(state, which, generation, root, resolved, tx),
                None => launch(state, which, &resolved, tx),
            }
        },
        tx,
    );
}

/// The picker the answer to request `generation` is for, while it is still current.
fn current(state: &mut State, which: Which, generation: u64) -> Option<&mut Picker> {
    state
        .window
        .launching
        .picker_mut(which)
        .filter(|p| p.generation == generation)
}

fn load_repo(
    state: &mut State,
    which: Which,
    generation: u64,
    root: String,
    resolved: String,
    tx: &mut Tx,
) {
    let home = state.window.launching.home.clone();
    if let Some(picker) = current(state, which, generation) {
        picker.operation = Some((Operation::LoadingRepo, paths::display(&resolved, &home)));
    }
    let repo = root.clone();
    state.ask(
        |id| GetRepoInfoMessage {
            repo,
            request_id: Some(id),
            ..GetRepoInfoMessage::new()
        },
        move |state, reply, tx| {
            let Some(picker) = current(state, which, generation) else {
                return;
            };
            picker.operation = None;
            let result: Option<GetRepoInfoResultMessage> = reply.parse();
            match result {
                Some(GetRepoInfoResultMessage {
                    success: true,
                    info: Some(info),
                    ..
                }) => repo::show(state, which, root, resolved, info, tx),
                other => {
                    state.window.notify(
                        other
                            .and_then(|r| r.error)
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Failed to load repo options".into()),
                        tx,
                    );
                    launch(state, which, &resolved, tx);
                }
            }
        },
        tx,
    );
}

/// Where a pick lands: its desktop, the pane it goes beside, the session it was started from.
struct Target {
    desktop: String,
    anchor: Option<String>,
    spawned_from: Option<String>,
}

fn target(state: &State, desktop: Option<&str>, anchor: Option<&str>) -> Option<Target> {
    let facts = &state.facts;
    let desktop = match desktop {
        Some(id) => facts.desktops.iter().find(|d| d.id == id),
        None => facts.current_desktop(),
    }?;
    let anchor = anchor
        .filter(|a| desktop.panes.iter().any(|p| p.pane_id == *a))
        .map(str::to_owned)
        .or_else(|| (!desktop.active_pane_id.is_empty()).then(|| desktop.active_pane_id.clone()));
    let spawned_from = anchor
        .as_ref()
        .and_then(|a| desktop.panes.iter().find(|p| p.pane_id == *a))
        .filter(|p| p.kind == "agent" && !p.session_id.is_empty())
        .map(|p| p.session_id.clone());
    Some(Target {
        desktop: desktop.id.clone(),
        anchor,
        spawned_from,
    })
}

/// Starts the picked agent in `path` and puts the picker away.
pub(crate) fn launch(state: &mut State, which: Which, path: &str, tx: &mut Tx) {
    let Some(picker) = state.window.launching.picker(which) else {
        return;
    };
    let agent = picker.agent(state);
    let yolo = yolo_on(state, picker) && agent != TERMINAL;
    let (desktop, anchor, direction) = (
        picker.desktop.clone(),
        picker.anchor.clone(),
        picker.direction,
    );
    finish(state, which, Some(paths::basename(path).to_owned()), tx);
    spawn(
        state,
        Spawn {
            path: path.to_owned(),
            agent,
            yolo,
            desktop,
            anchor,
            direction,
            label: None,
        },
        tx,
    );
}

/// A picker that has launched: the dialog closes; the inline launcher says what is starting
/// until the spawn answers, then starts over.
fn finish(state: &mut State, which: Which, starting: Option<String>, tx: &mut Tx) {
    match which {
        Which::Dialog => close(state, tx),
        Which::Inline => {
            let launching = &mut state.window.launching;
            let desktop = launching.inline.take().and_then(|p| p.desktop);
            launching.starting = desktop
                .zip(starting)
                .map(|(desktop, name)| Starting { desktop, name });
        }
    }
}

pub(crate) struct Spawn {
    pub(crate) path: String,
    pub(crate) agent: String,
    pub(crate) yolo: bool,
    pub(crate) desktop: Option<String>,
    pub(crate) anchor: Option<String>,
    pub(crate) direction: Direction,
    /// The session's name; the folder's name when left out, none for a shell.
    pub(crate) label: Option<String>,
}

pub(crate) fn spawn(state: &mut State, spawn: Spawn, tx: &mut Tx) {
    let Some(target) = target(state, spawn.desktop.as_deref(), spawn.anchor.as_deref()) else {
        state.window.launching.starting = None;
        state.window.notify(NO_DESKTOP.into(), tx);
        return;
    };
    let label = spawn.label.or_else(|| {
        (spawn.agent != TERMINAL).then(|| match paths::basename(&spawn.path) {
            "" => "session".to_owned(),
            name => name.to_owned(),
        })
    });
    let id = session_id();
    let started = crate::desktop::arrival::Shown::Session(id.clone());
    crate::desktop::arrival::expect(state, started.clone(), false, tx);
    let message = SpawnSessionMessage {
        id: id.clone(),
        cwd: spawn.path,
        agent: spawn.agent,
        placement: Some(SessionPlacement {
            desktop_id: Some(target.desktop.clone()),
            direction: Some(spawn.direction.wire().into()),
            anchor_pane_id: target.anchor,
        }),
        cols: 80,
        rows: 24,
        label,
        yolo_mode: spawn.yolo.then_some(true),
        spawned_from: target.spawned_from,
        ..SpawnSessionMessage::new()
    };
    state.ask_as(
        id,
        &message,
        move |state, reply, tx| {
            state.window.launching.starting = None;
            let result: Option<SpawnResultMessage> = reply.parse();
            match result {
                Some(SpawnResultMessage {
                    success: true,
                    desktop_id,
                    pane_id: Some(pane),
                    ..
                }) => {
                    let shown = state.facts.current_desktop().map(|d| d.id.clone());
                    let desktop = desktop_id.unwrap_or(target.desktop);
                    crate::desktop::arrival::forget(state, &started);
                    if shown.as_deref() == Some(&desktop) {
                        state.window.show_desktop(tx);
                        state.window.follow_next_turn = false;
                        crate::desktop::activate_started(state, &desktop, &pane, tx);
                    }
                }
                Some(SpawnResultMessage { success: true, .. }) => {
                    crate::desktop::arrival::forget(state, &started);
                }
                other => {
                    crate::desktop::arrival::forget(state, &started);
                    state.window.notify(
                        other
                            .and_then(|r| r.error)
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Failed to start the agent".into()),
                        tx,
                    );
                }
            }
            let _ = follow(state, tx);
        },
        tx,
    );
}

fn session_id() -> String {
    use std::hash::{BuildHasher, Hasher};
    let random = || {
        std::collections::hash_map::RandomState::new()
            .build_hasher()
            .finish()
    };
    let (a, b) = (random(), random());
    let b = (b & 0x3fff_ffff_ffff_ffff) | 0x8000_0000_0000_0000;
    let a = (a & 0xffff_ffff_ffff_0fff) | 0x0000_0000_0000_4000;
    format!(
        "{:08x}-{:04x}-{:04x}-{:04x}-{:012x}",
        a >> 32,
        (a >> 16) & 0xffff,
        a & 0xffff,
        b >> 48,
        b & 0xffff_ffff_ffff
    )
}

/// Keeps a launcher for the empty desktop on screen, and for one the user left while it stays
/// empty; it takes the keyboard when its desktop comes on screen and nothing covers it.
/// Returns whether the launchers changed.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) -> bool {
    let facts = &state.facts;
    let empty = |id: &str| {
        facts
            .desktops
            .iter()
            .any(|d| d.id == id && crate::desktop::holds_nothing(d))
    };
    let shown = facts
        .current_desktop()
        .filter(|d| {
            crate::desktop::holds_nothing(d) && state.window.view == crate::shell::View::Desktop
        })
        .map(|d| d.id.clone());
    let launching = &mut state.window.launching;
    let before = launching.kept();
    if launching
        .inline
        .as_ref()
        .is_some_and(|p| !p.desktop.as_deref().is_some_and(empty))
    {
        launching.inline = None;
    }
    if launching
        .starting
        .as_ref()
        .is_some_and(|s| !empty(&s.desktop))
    {
        launching.starting = None;
    }
    let arrived = shown.is_some() && shown != launching.shown_on;
    launching.shown_on = shown.clone();
    if let Some(desktop) = shown {
        if launching.shown_inline().is_none() && launching.shown_starting().is_none() {
            let picker = Picker::open(
                Which::Inline,
                Some(desktop),
                None,
                Direction::Vertical,
                None,
                state,
            );
            state.window.launching.openings = picker.opening;
            state.window.launching.inline = Some(picker);
            opened(state, Which::Inline, tx);
            refocus_inline(state, tx);
        } else if arrived {
            refocus_inline(state, tx);
        }
    }
    state.window.launching.kept() != before
}

/// Starts an agent in a new worktree of `repo`: the daemon makes the worktree, then the agent
/// starts in it, while a prompt in the corner says how far it got.
pub(crate) fn create_worktree(
    state: &mut State,
    which: Which,
    repo: String,
    branch: String,
    from: String,
    tx: &mut Tx,
) {
    if state.window.launching.worktree.is_some() {
        state
            .window
            .notify("A worktree session is already being created.".into(), tx);
        return;
    }
    let Some(picker) = state.window.launching.picker(which) else {
        return;
    };
    let agent = picker.agent(state);
    let yolo = yolo_on(state, picker) && agent != TERMINAL;
    let (desktop, anchor, direction) = (
        picker.desktop.clone(),
        picker.anchor.clone(),
        picker.direction,
    );
    let desktop = desktop.or_else(|| state.facts.current_desktop().map(|d| d.id.clone()));
    finish(state, which, None, tx);
    state.window.launching.worktree = Some(repo::Setup::creating(&branch, &repo));
    let (main_repo, wanted) = (repo.clone(), branch.clone());
    state.ask(
        |id| CreateWorktreeMessage {
            main_repo,
            branch: wanted,
            starting_from: Some(from),
            request_id: Some(id),
            ..CreateWorktreeMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<CreateWorktreeResultMessage> = reply.parse();
            let path = match result {
                Some(CreateWorktreeResultMessage {
                    success: true,
                    path: Some(path),
                    ..
                }) => path,
                other => {
                    let error = other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Failed to create worktree".into());
                    if let Some(setup) = &mut state.window.launching.worktree {
                        setup.fail(error);
                    }
                    return;
                }
            };
            state.window.launching.worktree = None;
            spawn(
                state,
                Spawn {
                    path,
                    agent,
                    yolo,
                    desktop,
                    anchor,
                    direction,
                    label: None,
                },
                tx,
            );
        },
        tx,
    );
}
