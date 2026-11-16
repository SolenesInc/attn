//! Launcher 2.0, the new-session launcher the `new_session_launcher_enabled` setting turns on:
//! one screen that searches recent projects, browses folders once the text starts with `~` or
//! `/`, and offers a repository's destinations (the Where list) before it starts an agent or a
//! terminal.

mod view;

use std::collections::BTreeMap;

use attn_protocol::{
    BrowseDirectoryMessage, BrowseDirectoryResultMessage, CreateWorktreeMessage,
    CreateWorktreeResultMessage, DeleteWorktreeMessage, DeleteWorktreeResultMessage,
    DirectoryEntry, GetRecentLocationsMessage, GetRepoInfoMessage, GetRepoInfoResultMessage,
    InspectPathMessage, InspectPathResultMessage, RecentLocationsResultMessage, RepoInfo,
};
use rune::{Chord, Key, KeyEvent, KeyPlatform, TextEdit, Tx};

use crate::State;
use crate::picker::{
    Choice, Direction, Spawn, TERMINAL, Which, agent_key, close, memory_key, paths, set_setting,
    spawn_then, worktree_name,
};
pub(crate) use view::frame;

pub(crate) struct Launcher {
    pub(crate) which: Which,
    pub(crate) opening: u64,
    /// The desktop a start lands on: the inline launcher's own; `None` lands on the current one.
    pub(crate) desktop: Option<String>,
    anchor: Option<String>,
    direction: Direction,
    pub(crate) choice: Choice,
    pub(crate) query: TextEdit,
    /// Whether this opening's recents have arrived; until then no row is highlighted.
    fresh: bool,
    /// The row the user moved to. While searching, none means the top row.
    highlight: Option<usize>,
    /// Folders the daemon listed, by the folder's normalized path.
    listings: BTreeMap<String, Listing>,
    /// The repository whose destinations show, once Tab opened them.
    pub(crate) place: Option<Where>,
    pub(crate) problem: Option<String>,
    /// A start is on its way; the launcher takes no input until it answers.
    pub(crate) busy: bool,
    pub(crate) deletion: Option<Deletion>,
    /// Bumped by every change that makes answers to earlier requests stale.
    generation: u64,
    /// The worktree name offered for each repository, kept for this opening.
    names: BTreeMap<String, String>,
    /// Worktrees this opening made, by repository, branch and starting point, so a start that
    /// failed after its worktree was made starts there again instead of making another.
    created: BTreeMap<String, String>,
}

/// A repository's destinations: a new worktree from its default or current branch, its main
/// checkout, and its other worktrees.
pub(crate) struct Where {
    pub(crate) repo: String,
    pub(crate) info: Option<RepoInfo>,
    /// The name typed for a new worktree, which also filters the existing ones.
    pub(crate) branch: TextEdit,
    pub(crate) highlight: Option<usize>,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum WhereRow {
    New {
        current: bool,
        branch: String,
    },
    Main {
        branch: String,
    },
    Existing {
        path: String,
        branch: String,
        created_at: Option<String>,
    },
}

pub(crate) struct Deletion {
    pub(crate) path: String,
    pub(crate) force: bool,
}

pub(crate) enum Listing {
    Pending,
    Listed {
        directory: String,
        entries: Vec<DirectoryEntry>,
    },
    Failed,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    Recent,
    Folder,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Row {
    pub(crate) kind: Kind,
    pub(crate) path: String,
    pub(crate) name: String,
    pub(crate) repo_root: Option<String>,
    pub(crate) last_seen: Option<String>,
}

/// What the typed text asks for.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Mode<'a> {
    /// Recent projects whose path holds the text.
    Search(&'a str),
    /// The folders of a path that starts with `~` or `/`.
    Browse(&'a str),
}

pub(crate) fn mode(query: &str) -> Mode<'_> {
    let text = query.trim();
    if text.starts_with('~') || text.starts_with('/') {
        Mode::Browse(text)
    } else {
        Mode::Search(text)
    }
}

/// A browsed path's folder and the start of the name typed in it.
pub(crate) fn split_browse(path: &str) -> (String, String) {
    match path.rfind('/') {
        None => ("~".into(), path[1..].into()),
        Some(0) => ("/".into(), path[1..].into()),
        Some(slash) => (path[..slash].into(), path[slash + 1..].into()),
    }
}

/// The folders whose name holds `fragment`, those starting with it first.
fn matching_folders<'a>(entries: &'a [DirectoryEntry], fragment: &str) -> Vec<&'a DirectoryEntry> {
    let needle = fragment.to_lowercase();
    let (starts, rest): (Vec<_>, Vec<_>) = entries
        .iter()
        .filter(|e| e.is_dir && e.name.to_lowercase().contains(&needle))
        .partition(|e| e.name.to_lowercase().starts_with(&needle));
    starts.into_iter().chain(rest).collect()
}

/// Where a repository's last start went, as the user left it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Memory {
    MainRepo,
    NewWorktree,
}

impl Memory {
    fn value(self) -> &'static str {
        match self {
            Memory::MainRepo => "main_repo",
            Memory::NewWorktree => "new_worktree",
        }
    }
}

pub(crate) fn remembered(state: &State, repo: &str) -> Memory {
    match state
        .facts
        .settings
        .get(&memory_key(repo))
        .map(|v| v.trim())
    {
        Some("new_worktree") => Memory::NewWorktree,
        _ => Memory::MainRepo,
    }
}

impl Launcher {
    pub(crate) fn open(
        which: Which,
        desktop: Option<String>,
        anchor: Option<String>,
        direction: Direction,
        agent: Option<&str>,
        state: &State,
    ) -> Launcher {
        Launcher {
            which,
            opening: state.window.launching.openings() + 1,
            desktop,
            anchor,
            direction,
            choice: Choice::new(agent, &state.facts.settings),
            query: TextEdit::default(),
            fresh: false,
            highlight: None,
            listings: BTreeMap::new(),
            place: None,
            problem: None,
            busy: false,
            deletion: None,
            generation: 0,
            names: BTreeMap::new(),
            created: BTreeMap::new(),
        }
    }

    pub(crate) fn field_key(&self) -> String {
        format!("{}.path.field", self.which.key())
    }

    fn home(state: &State) -> &str {
        &state.window.launching.home
    }

    pub(crate) fn listing(&self, dir: &str, home: &str) -> Option<&Listing> {
        self.listings.get(&paths::normalize(dir, home))
    }

    /// The recents that match the typed text, newest first; while browsing, the recents under
    /// the typed path and then the folders the daemon listed.
    pub(crate) fn rows(&self, state: &State) -> Vec<Row> {
        let home = Self::home(state);
        let mut recents: Vec<_> = state.window.launching.recents.iter().collect();
        recents.sort_by(|a, b| b.last_seen.cmp(&a.last_seen));
        let recent_row = |r: &attn_protocol::RecentLocation| Row {
            kind: Kind::Recent,
            path: r.path.clone(),
            name: paths::basename(&r.path).to_owned(),
            repo_root: r.repo_root.clone().filter(|root| !root.is_empty()),
            last_seen: Some(r.last_seen.clone()),
        };
        match mode(&self.query.text) {
            Mode::Search(text) => {
                let needle = paths::expand(text, home).to_lowercase();
                recents
                    .into_iter()
                    .filter(|r| r.path.to_lowercase().contains(&needle))
                    .map(recent_row)
                    .collect()
            }
            Mode::Browse(path) => {
                let needle = paths::expand(path, home).to_lowercase();
                let bare = needle.trim_end_matches('/');
                let (dir, fragment) = split_browse(path);
                let folders = match self.listing(&dir, home) {
                    Some(Listing::Listed { entries, .. }) => matching_folders(entries, &fragment)
                        .into_iter()
                        .map(|e| Row {
                            kind: Kind::Folder,
                            path: e.path.clone(),
                            name: e.name.clone(),
                            repo_root: (e.is_repo == Some(true)).then(|| e.path.clone()),
                            last_seen: None,
                        })
                        .collect(),
                    _ => Vec::new(),
                };
                recents
                    .into_iter()
                    .filter(|r| {
                        let path = r.path.to_lowercase();
                        path.starts_with(&needle) && path.trim_end_matches('/') != bare
                    })
                    .map(recent_row)
                    .chain(folders)
                    .collect()
            }
        }
    }

    /// The highlighted row: while searching, the one the user moved to or else the top one,
    /// once this opening's recents arrived; while browsing, only one the user moved to.
    pub(crate) fn selected(&self, rows: &[Row]) -> Option<usize> {
        let moved = self.highlight.filter(|i| *i < rows.len());
        match mode(&self.query.text) {
            Mode::Browse(_) => moved,
            Mode::Search(_) if !self.fresh || rows.is_empty() => None,
            Mode::Search(_) => Some(moved.unwrap_or(0)),
        }
    }

    /// Whether the daemon listed `path` as a repository, when it listed its folder at all.
    pub(crate) fn known_repo(&self, path: &str, home: &str) -> Option<bool> {
        let target = paths::normalize(path, home);
        let parent = match target.rfind('/') {
            Some(0) | None => "/",
            Some(slash) => &target[..slash],
        };
        match self.listing(parent, home)? {
            Listing::Listed { entries, .. } => entries
                .iter()
                .find(|e| e.path == target)
                .map(|e| e.is_repo == Some(true)),
            _ => None,
        }
    }

    /// The name a new worktree gets: the typed one, else the one offered.
    pub(crate) fn worktree_name(&self) -> String {
        let Some(place) = &self.place else {
            return String::new();
        };
        match place.branch.text.trim() {
            "" => self.names.get(&place.repo).cloned().unwrap_or_default(),
            typed => typed.to_owned(),
        }
    }
}

impl Where {
    pub(crate) fn rows(&self) -> Vec<WhereRow> {
        let Some(info) = &self.info else {
            return Vec::new();
        };
        let typed = self.branch.text.trim().to_lowercase();
        let mut rows = vec![WhereRow::New {
            current: false,
            branch: info.default_branch.clone(),
        }];
        if info.current_branch != info.default_branch {
            rows.push(WhereRow::New {
                current: true,
                branch: info.current_branch.clone(),
            });
        }
        rows.push(WhereRow::Main {
            branch: info.current_branch.clone(),
        });
        rows.extend(
            info.worktrees
                .iter()
                .filter(|w| w.path != self.repo && w.branch.to_lowercase().contains(&typed))
                .map(|w| WhereRow::Existing {
                    path: w.path.clone(),
                    branch: w.branch.clone(),
                    created_at: w.created_at.clone(),
                }),
        );
        rows
    }

    /// The row a fresh look at the list lands on: the worktree named exactly as typed, else a
    /// new one; with nothing typed, where the repository's last start went.
    fn aim(&mut self, memory: Memory) {
        if self.info.is_none() {
            return;
        }
        let rows = self.rows();
        let typed = self.branch.text.trim();
        self.highlight = Some(if !typed.is_empty() {
            rows.iter()
                .position(|r| matches!(r, WhereRow::Existing { branch, .. } if branch == typed))
                .unwrap_or(0)
        } else if memory == Memory::NewWorktree {
            0
        } else {
            rows.iter()
                .position(|r| matches!(r, WhereRow::Main { .. }))
                .unwrap_or(0)
        });
    }
}

fn this(state: &mut State, which: Which, opening: u64) -> Option<&mut Launcher> {
    state
        .window
        .launching
        .launcher_mut(which)
        .filter(|l| l.opening == opening)
}

/// The launcher the answer to request `generation` is for, while nothing made it stale.
fn current(
    state: &mut State,
    which: Which,
    opening: u64,
    generation: u64,
) -> Option<&mut Launcher> {
    this(state, which, opening).filter(|l| l.generation == generation)
}

/// Asks for this machine's recents, newest of which the launcher then highlights.
pub(crate) fn opened(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(opening) = state.window.launching.launcher(which).map(|l| l.opening) else {
        return;
    };
    state.ask(
        |id| GetRecentLocationsMessage {
            home_only: Some(true),
            request_id: Some(id),
            ..GetRecentLocationsMessage::new()
        },
        move |state, reply, _| {
            let result: Option<RecentLocationsResultMessage> = reply.parse();
            let Some(result) = result.filter(|r| r.success) else {
                if let Some(launcher) = this(state, which, opening) {
                    launcher.problem = Some("Failed to load recent locations".into());
                }
                return;
            };
            let launching = &mut state.window.launching;
            if let Some(home) = result.home_path.filter(|h| !h.is_empty()) {
                launching.home = home;
            }
            launching.recents = result.recent_locations;
            if let Some(launcher) = this(state, which, opening) {
                launcher.fresh = true;
            }
        },
        tx,
    );
}

/// The text field the launcher shows: the path, or the new worktree's name while the Where
/// list shows.
pub(crate) fn field_mut(state: &mut State, which: Which) -> &mut TextEdit {
    let launching = &mut state.window.launching;
    if launching.launcher(which).is_none() {
        return launching.spare();
    }
    let launcher = launching.launcher_mut(which).expect("the launcher is open");
    match &mut launcher.place {
        Some(place) => &mut place.branch,
        None => &mut launcher.query,
    }
}

/// The user typed into the field.
pub(crate) fn edited(state: &mut State, which: Which, tx: &mut Tx) {
    let memory = match state
        .window
        .launching
        .launcher(which)
        .and_then(|l| l.place.as_ref())
    {
        Some(place) => remembered(state, &place.repo),
        None => {
            query_changed(state, which, tx);
            return;
        }
    };
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    launcher.problem = None;
    if let Some(place) = &mut launcher.place {
        place.aim(memory);
    }
}

/// The path changed: earlier answers go stale, the highlight goes back to the top, and a newly
/// browsed folder is listed.
fn query_changed(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    launcher.generation += 1;
    launcher.highlight = None;
    launcher.problem = None;
    list_browsed(state, which, tx);
}

/// `text` in place of what the field held, with the caret at its end. The field keeps the
/// keyboard: Rune moves focus with the edit's `focused` flag.
fn replaced(edit: &TextEdit, text: String) -> TextEdit {
    let mut new = TextEdit::new(text);
    new.anchor = new.text.len();
    new.focus = new.text.len();
    new.focused = edit.focused;
    new
}

fn set_query(state: &mut State, which: Which, text: String, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    launcher.query = replaced(&launcher.query, text);
    query_changed(state, which, tx);
}

/// Lists the folder the typed path is in, once per opening.
fn list_browsed(state: &mut State, which: Which, tx: &mut Tx) {
    let home = state.window.launching.home.clone();
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    let Mode::Browse(path) = mode(&launcher.query.text) else {
        return;
    };
    let (dir, _) = split_browse(path);
    let key = paths::normalize(&dir, &home);
    if launcher.place.is_some() || launcher.listings.contains_key(&key) {
        return;
    }
    launcher.listings.insert(key.clone(), Listing::Pending);
    let opening = launcher.opening;
    let input_path = if dir == "/" {
        "/".to_owned()
    } else {
        format!("{}/", dir.trim_end_matches('/'))
    };
    state.ask(
        |id| BrowseDirectoryMessage {
            input_path,
            request_id: Some(id),
            ..BrowseDirectoryMessage::new()
        },
        move |state, reply, _| {
            let result: Option<BrowseDirectoryResultMessage> = reply.parse();
            if let Some(home) = result
                .as_ref()
                .and_then(|r| r.home_path.clone())
                .filter(|h| !h.is_empty())
            {
                state.window.launching.home = home;
            }
            let home = state.window.launching.home.clone();
            let Some(launcher) = this(state, which, opening) else {
                return;
            };
            match result.filter(|r| r.success) {
                Some(result) => {
                    let listed = || Listing::Listed {
                        directory: result.directory.clone(),
                        entries: result.entries.clone(),
                    };
                    if !result.directory.is_empty() {
                        launcher
                            .listings
                            .insert(paths::normalize(&result.directory, &home), listed());
                    }
                    launcher.listings.insert(key, listed());
                }
                None => {
                    launcher.listings.insert(key, Listing::Failed);
                }
            }
        },
        tx,
    );
}

/// Keys the launcher hears while it, or its field, has focus, after the keymap's shortcuts.
/// Returns whether it took the key.
pub(crate) fn on_key(state: &mut State, which: Which, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return false;
    };
    if launcher.busy {
        return true;
    }
    if launcher.deletion.is_some() {
        match &ev.key {
            Key::Text(t) if t.eq_ignore_ascii_case("y") => delete_worktree(state, which, tx),
            Key::Text(t) if t.eq_ignore_ascii_case("n") => cancel_deletion(state, which),
            Key::Escape => cancel_deletion(state, which),
            _ => {}
        }
        return true;
    }
    if agent_key(state, which, ev, tx) {
        return true;
    }
    let accel = |spec: &str| {
        Chord::parse(spec)
            .expect("a launcher chord parses")
            .matches_tx_on(ev, tx, KeyPlatform::current())
    };
    let launcher = state
        .window
        .launching
        .launcher(which)
        .expect("the launcher is open");
    let rows = launcher.rows(state);
    let choosing = launcher.place.as_ref().map(Where::rows);
    let branch_empty = launcher
        .place
        .as_ref()
        .is_some_and(|p| p.branch.text.is_empty());
    let selected = match &launcher.place {
        Some(place) => place.highlight,
        None => launcher.selected(&rows),
    };
    let browsing = matches!(mode(&launcher.query.text), Mode::Browse(_));
    match ev.key {
        Key::Down | Key::Up if ev.mods == rune::Mods::default() => {
            let len = choosing.as_ref().map_or(rows.len(), Vec::len);
            if len > 0 {
                let next = match (ev.key == Key::Down, selected) {
                    (true, None) => 0,
                    (true, Some(i)) => (i + 1) % len,
                    (false, None | Some(0)) => len - 1,
                    (false, Some(i)) => i - 1,
                };
                let launcher = state.window.launching.launcher_mut(which).expect("open");
                match &mut launcher.place {
                    Some(place) => place.highlight = Some(next),
                    None => launcher.highlight = Some(next),
                }
            }
            true
        }
        Key::Enter => {
            let terminal = accel("mod+enter");
            match choosing {
                Some(where_rows) => {
                    if let Some(row) = selected.and_then(|i| where_rows.get(i)).cloned() {
                        launch(state, which, None, terminal, Some(row), tx);
                    }
                }
                None => {
                    let row = selected.and_then(|i| rows.get(i)).cloned();
                    if row.is_some() || browsing {
                        launch(state, which, row, terminal, None, tx);
                    }
                }
            }
            true
        }
        Key::Tab if ev.mods == rune::Mods::default() => {
            tab(state, which, tx);
            true
        }
        Key::Escape => {
            step_back(state, which, tx);
            true
        }
        Key::Backspace if choosing.is_some() && accel("mod+backspace") => {
            let launcher = state.window.launching.launcher_mut(which).expect("open");
            launcher.problem = None;
            if let Some(WhereRow::Existing { path, .. }) =
                selected.and_then(|i| choosing.as_ref()?.get(i)).cloned()
            {
                launcher.deletion = Some(Deletion { path, force: false });
            }
            true
        }
        Key::Backspace if choosing.is_some() && branch_empty => {
            step_back(state, which, tx);
            true
        }
        _ => false,
    }
}

/// Escape: a start that is under way stays; then a deletion question goes, then the Where
/// list, then the typed text, and last the dialog. The inline launcher never goes.
fn step_back(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    if launcher.busy {
        return;
    }
    launcher.problem = None;
    if launcher.deletion.take().is_some() {
        return;
    }
    launcher.generation += 1;
    if let Some(place) = launcher.place.take() {
        launcher.query.focused = place.branch.focused;
        launcher.highlight = None;
    } else if !launcher.query.text.is_empty() {
        launcher.query = replaced(&launcher.query, String::new());
        launcher.highlight = None;
    } else if which == Which::Dialog {
        close(state, tx);
    }
}

/// Escape that reaches the dialog itself, as from its field.
pub(crate) fn escape(state: &mut State, tx: &mut Tx) {
    step_back(state, Which::Dialog, tx);
}

/// A press on the dialog's backdrop: the dialog closes unless a start is under way.
pub(crate) fn dismiss(state: &mut State, tx: &mut Tx) {
    if state
        .window
        .launching
        .launcher(Which::Dialog)
        .is_some_and(|l| !l.busy)
    {
        close(state, tx);
    }
}

/// Tab: a repository's Where list for an agent; otherwise the highlighted row, or the first
/// folder, completes into the field.
fn tab(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    if launcher.place.is_some() {
        return;
    }
    let rows = launcher.rows(state);
    let row = launcher.selected(&rows).and_then(|i| rows.get(i)).cloned();
    let agent = launcher.choice.agent(state) != TERMINAL;
    if agent && let Some(root) = row.as_ref().and_then(|r| r.repo_root.clone()) {
        open_where(state, which, root, tx);
        return;
    }
    if agent
        && row.is_none()
        && let Mode::Browse(path) = mode(&launcher.query.text)
    {
        let home = state.window.launching.home.clone();
        let path = paths::normalize(path, &home);
        match launcher.known_repo(&path, &home) {
            Some(true) => {
                open_where(state, which, path, tx);
                return;
            }
            Some(false) => {}
            None => {
                let (opening, generation) = (launcher.opening, launcher.generation);
                state.ask(
                    |id| InspectPathMessage {
                        path,
                        request_id: Some(id),
                        ..InspectPathMessage::new()
                    },
                    move |state, reply, tx| {
                        let Some(launcher) = current(state, which, opening, generation) else {
                            return;
                        };
                        let result: Option<InspectPathResultMessage> = reply.parse();
                        match result {
                            Some(InspectPathResultMessage {
                                success: true,
                                inspection: Some(inspection),
                                ..
                            }) => match inspection.repo_root.filter(|r| !r.is_empty()) {
                                Some(root) if root == inspection.resolved_path => {
                                    open_where(state, which, root, tx);
                                }
                                _ => complete_first(state, which, None, tx),
                            },
                            other => {
                                launcher.problem = Some(
                                    other
                                        .and_then(|r| r.error)
                                        .filter(|e| !e.is_empty())
                                        .unwrap_or_else(|| "Failed to inspect path".into()),
                                );
                            }
                        }
                    },
                    tx,
                );
                return;
            }
        }
    }
    complete_first(state, which, row, tx);
}

/// Puts `row`, or else the first folder listed, into the field as a folder to browse.
fn complete_first(state: &mut State, which: Which, row: Option<Row>, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    let row = row.or_else(|| {
        launcher
            .rows(state)
            .into_iter()
            .find(|r| r.kind == Kind::Folder)
    });
    if let Some(row) = row {
        let shown = paths::display(&row.path, &state.window.launching.home);
        set_query(
            state,
            which,
            format!("{}/", shown.trim_end_matches('/')),
            tx,
        );
    }
}

fn open_where(state: &mut State, which: Which, repo: String, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    launcher.generation += 1;
    launcher.problem = None;
    launcher.place = Some(Where {
        repo: repo.clone(),
        info: None,
        branch: replaced(&launcher.query, String::new()),
        highlight: None,
    });
    let (opening, generation) = (launcher.opening, launcher.generation);
    let asked = repo.clone();
    state.ask(
        |id| GetRepoInfoMessage {
            repo: asked,
            request_id: Some(id),
            ..GetRepoInfoMessage::new()
        },
        move |state, reply, _| {
            let memory = remembered(state, &repo);
            let Some(launcher) = current(state, which, opening, generation) else {
                return;
            };
            let result: Option<GetRepoInfoResultMessage> = reply.parse();
            match result {
                Some(GetRepoInfoResultMessage {
                    success: true,
                    info: Some(info),
                    ..
                }) => {
                    if !launcher.names.contains_key(&repo) {
                        let taken: Vec<String> =
                            info.worktrees.iter().map(|w| w.branch.clone()).collect();
                        launcher.names.insert(repo.clone(), worktree_name(&taken));
                    }
                    if let Some(place) = &mut launcher.place {
                        place.info = Some(info);
                        place.aim(memory);
                    }
                }
                other => {
                    launcher.problem = Some(
                        other
                            .and_then(|r| r.error)
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Failed to read repository".into()),
                    );
                }
            }
        },
        tx,
    );
}

/// What one start needs once it is under way, whether or not the launcher is still there.
#[derive(Clone)]
struct Start {
    which: Which,
    opening: u64,
    agent: String,
    yolo: bool,
    desktop: Option<String>,
    anchor: Option<String>,
    direction: Direction,
}

impl Start {
    fn spawn(&self, path: String) -> Spawn {
        Spawn {
            path,
            agent: self.agent.clone(),
            yolo: self.yolo,
            desktop: self.desktop.clone(),
            anchor: self.anchor.clone(),
            direction: self.direction,
            label: None,
        }
    }
}

/// Starts the picked row, the typed path, or the picked destination: an agent, or a terminal
/// when `terminal`. A repository without a picked destination starts where its last start
/// went.
fn launch(
    state: &mut State,
    which: Which,
    row: Option<Row>,
    terminal: bool,
    destination: Option<WhereRow>,
    tx: &mut Tx,
) {
    let Some(launcher) = state.window.launching.launcher(which) else {
        return;
    };
    if launcher.busy {
        return;
    }
    let agent = if terminal {
        TERMINAL.to_owned()
    } else {
        launcher.choice.agent(state)
    };
    let start = Start {
        which,
        opening: launcher.opening,
        yolo: agent != TERMINAL && launcher.choice.yolo_on(state),
        agent,
        desktop: launcher.desktop.clone(),
        anchor: launcher.anchor.clone(),
        direction: launcher.direction,
    };
    let typed = match (&launcher.place, &row, mode(&launcher.query.text)) {
        (None, None, Mode::Browse(path)) => {
            Some(paths::normalize(path, &state.window.launching.home))
        }
        _ => None,
    };
    let launcher = state.window.launching.launcher_mut(which).expect("open");
    launcher.generation += 1;
    launcher.busy = true;
    launcher.problem = None;
    let Some(typed) = typed else {
        let (path, repo) = row.map_or((None, None), |r| (Some(r.path), r.repo_root));
        to_destination(state, start, path, repo, destination, tx);
        return;
    };
    let asked = typed.clone();
    state.ask(
        |id| InspectPathMessage {
            path: asked,
            request_id: Some(id),
            ..InspectPathMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<InspectPathResultMessage> = reply.parse();
            match result.and_then(|r| r.inspection) {
                Some(inspection) if inspection.exists && inspection.is_directory => {
                    let path = inspection.resolved_path;
                    let repo = inspection.repo_root.filter(|root| *root == path);
                    to_destination(state, start, Some(path), repo, None, tx);
                }
                _ => failed(state, &start, format!("Directory not found: {typed}")),
            }
        },
        tx,
    );
}

fn to_destination(
    state: &mut State,
    start: Start,
    path: Option<String>,
    repo: Option<String>,
    destination: Option<WhereRow>,
    tx: &mut Tx,
) {
    let launcher = state.window.launching.launcher(start.which);
    let place = launcher.and_then(|l| {
        l.place
            .as_ref()
            .map(|p| (p.repo.clone(), l.worktree_name()))
    });
    let repo = place.as_ref().map(|(repo, _)| repo.clone()).or(repo);
    let destination = destination.or_else(|| {
        let repo = repo.as_ref()?;
        Some(
            if start.agent != TERMINAL && remembered(state, repo) == Memory::NewWorktree {
                WhereRow::New {
                    current: false,
                    branch: String::new(),
                }
            } else {
                WhereRow::Main {
                    branch: String::new(),
                }
            },
        )
    });
    match (destination, repo) {
        (Some(WhereRow::New { branch, .. }), Some(repo)) => {
            if let Some((_, name)) = place {
                new_worktree(state, start, repo, name, branch, tx);
                return;
            }
            let name = match state.window.launching.launcher_mut(start.which) {
                Some(launcher) => launcher
                    .names
                    .entry(repo.clone())
                    .or_insert_with(|| worktree_name(&[]))
                    .clone(),
                None => worktree_name(&[]),
            };
            let asked = repo.clone();
            state.ask(
                |id| GetRepoInfoMessage {
                    repo: asked,
                    request_id: Some(id),
                    ..GetRepoInfoMessage::new()
                },
                move |state, reply, tx| {
                    let result: Option<GetRepoInfoResultMessage> = reply.parse();
                    match result {
                        Some(GetRepoInfoResultMessage {
                            success: true,
                            info: Some(info),
                            ..
                        }) => new_worktree(state, start, repo, name, info.default_branch, tx),
                        other => failed(
                            state,
                            &start,
                            other
                                .and_then(|r| r.error)
                                .filter(|e| !e.is_empty())
                                .unwrap_or_else(|| "Failed to read repository".into()),
                        ),
                    }
                },
                tx,
            );
        }
        (destination, repo) => {
            let target = match &destination {
                Some(WhereRow::Existing { path, .. }) => Some(path.clone()),
                Some(WhereRow::Main { .. }) => repo.clone(),
                _ => path,
            };
            let Some(target) = target else {
                failed(state, &start, String::new());
                return;
            };
            let memory =
                matches!(destination, Some(WhereRow::Main { .. })).then_some(Memory::MainRepo);
            begin(state, start, target, repo.zip(memory), tx);
        }
    }
}

fn new_worktree(
    state: &mut State,
    start: Start,
    repo: String,
    branch: String,
    from: String,
    tx: &mut Tx,
) {
    let made = format!("{repo}:{branch}:{from}");
    let existing = state
        .window
        .launching
        .launcher(start.which)
        .filter(|l| l.opening == start.opening)
        .and_then(|l| l.created.get(&made).cloned());
    let remember = Some((repo.clone(), Memory::NewWorktree));
    if let Some(path) = existing {
        begin(state, start, path, remember, tx);
        return;
    }
    state.ask(
        |id| CreateWorktreeMessage {
            main_repo: repo,
            branch,
            starting_from: Some(from),
            request_id: Some(id),
            ..CreateWorktreeMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<CreateWorktreeResultMessage> = reply.parse();
            match result {
                Some(CreateWorktreeResultMessage {
                    success: true,
                    path: Some(path),
                    ..
                }) => {
                    if let Some(launcher) = this(state, start.which, start.opening) {
                        launcher.created.insert(made, path.clone());
                    }
                    begin(state, start, path, remember, tx);
                }
                other => failed(
                    state,
                    &start,
                    other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Failed to create worktree".into()),
                ),
            }
        },
        tx,
    );
}

/// Starts the session in `path`; once it started, writes where the repository's start went
/// and puts the launcher away.
fn begin(
    state: &mut State,
    start: Start,
    path: String,
    remember: Option<(String, Memory)>,
    tx: &mut Tx,
) {
    let spawn = start.spawn(path);
    spawn_then(
        state,
        spawn,
        move |state, result, tx| match result {
            Ok(()) => {
                if let Some((repo, memory)) = remember
                    && start.agent != TERMINAL
                    && state
                        .facts
                        .settings
                        .get(&memory_key(&repo))
                        .map(|v| v.trim())
                        != Some(memory.value())
                {
                    set_setting(state, &memory_key(&repo), memory.value());
                }
                if this(state, start.which, start.opening).is_some() {
                    match start.which {
                        Which::Dialog => close(state, tx),
                        Which::Inline => state.window.launching.inline = None,
                    }
                }
            }
            Err(error) => failed(state, &start, error),
        },
        tx,
    );
}

/// A start that did not get through: the launcher stays as it was, saying why.
fn failed(state: &mut State, start: &Start, error: String) {
    if let Some(launcher) = this(state, start.which, start.opening) {
        launcher.busy = false;
        launcher.problem = (!error.is_empty()).then_some(error);
    }
}

fn cancel_deletion(state: &mut State, which: Which) {
    if let Some(launcher) = state.window.launching.launcher_mut(which) {
        launcher.deletion = None;
        launcher.problem = None;
    }
}

/// Deletes the worktree the user confirmed; a refusal that force would get past asks again.
fn delete_worktree(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    let Some(deletion) = &launcher.deletion else {
        return;
    };
    let (path, force, opening) = (deletion.path.clone(), deletion.force, launcher.opening);
    launcher.busy = true;
    launcher.problem = None;
    let asked = path.clone();
    state.ask(
        |id| DeleteWorktreeMessage {
            path: asked,
            force: force.then_some(true),
            request_id: Some(id),
            ..DeleteWorktreeMessage::new()
        },
        move |state, reply, _| {
            let Some(launcher) = this(state, which, opening) else {
                return;
            };
            launcher.busy = false;
            let result: Option<DeleteWorktreeResultMessage> = reply.parse();
            match result {
                Some(r) if r.success => {
                    if let Some(place) = &mut launcher.place {
                        let remaining = place.rows().len().saturating_sub(1);
                        if let Some(info) = &mut place.info {
                            info.worktrees.retain(|w| w.path != path);
                        }
                        place.highlight =
                            place.highlight.map(|h| h.min(remaining.saturating_sub(1)));
                    }
                    launcher.deletion = None;
                }
                other => {
                    let forceable = other.as_ref().and_then(|r| r.forceable) == Some(true);
                    launcher.problem = Some(
                        other
                            .and_then(|r| r.error)
                            .filter(|e| !e.is_empty())
                            .unwrap_or_else(|| "Failed to delete worktree".into()),
                    );
                    launcher.deletion = match launcher.deletion.take() {
                        Some(d) if !d.force && forceable => Some(Deletion { force: true, ..d }),
                        _ => None,
                    };
                }
            }
        },
        tx,
    );
}

/// A press on a row of the locations: highlights it and starts it.
pub(crate) fn pick(state: &mut State, which: Which, index: usize, row: Row, tx: &mut Tx) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    if launcher.busy {
        return;
    }
    launcher.highlight = Some(index);
    launch(state, which, Some(row), false, None, tx);
}

/// A press on a row of the Where list: highlights it and starts there.
pub(crate) fn pick_destination(
    state: &mut State,
    which: Which,
    index: usize,
    row: WhereRow,
    tx: &mut Tx,
) {
    let Some(launcher) = state.window.launching.launcher_mut(which) else {
        return;
    };
    if launcher.busy || launcher.deletion.is_some() {
        return;
    }
    if let Some(place) = &mut launcher.place {
        place.highlight = Some(index);
    }
    launch(state, which, None, false, Some(row), tx);
}
