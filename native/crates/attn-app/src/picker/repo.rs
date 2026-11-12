use attn_protocol::{
    DeleteWorktreeMessage, DeleteWorktreeResultMessage, GetRepoInfoMessage,
    GetRepoInfoResultMessage, RepoInfo, SetSettingMessage,
};
use rune::{Key, KeyEvent, TextEdit, Tx};

use super::names::{SystemDraw, worktree_name};
use super::{Which, launch, paths};
use crate::State;

/// Which half of the chooser has the keyboard.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Zone {
    /// The new worktree form, with the branch name field focused.
    Create,
    /// The checkout and its worktrees.
    Destinations,
}

/// Where a new worktree branches from.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Start {
    Default,
    Current,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Delete {
    Asking {
        path: String,
    },
    Deleting {
        path: String,
    },
    Failed {
        path: String,
        error: String,
        forceable: bool,
    },
}

impl Delete {
    pub(crate) fn path(&self) -> &str {
        match self {
            Delete::Asking { path } | Delete::Deleting { path } | Delete::Failed { path, .. } => {
                path
            }
        }
    }
}

/// A repository's checkout and worktrees, and a form for a new worktree, offered once the
/// typed path turned out to be in a repository.
pub(crate) struct Chooser {
    pub(crate) root: String,
    pub(crate) info: RepoInfo,
    pub(crate) zone: Zone,
    pub(crate) committed: usize,
    pub(crate) name: TextEdit,
    pub(crate) start: Start,
    pub(crate) refreshing: bool,
    pub(crate) delete: Option<Delete>,
    /// Wall-clock seconds when the rows were read, which their ages count to.
    pub(crate) read_at: f64,
}

/// One row of the chooser's destinations.
pub(crate) struct Destination {
    pub(crate) path: String,
    pub(crate) branch: String,
    pub(crate) main: bool,
}

impl Chooser {
    pub(crate) fn destinations(&self) -> Vec<Destination> {
        let main = Destination {
            path: self.info.repo.clone(),
            branch: self.info.current_branch.clone(),
            main: true,
        };
        std::iter::once(main)
            .chain(self.info.worktrees.iter().map(|w| Destination {
                path: w.path.clone(),
                branch: w.branch.clone(),
                main: false,
            }))
            .collect()
    }

    pub(crate) fn focus_key(&self, which: Which) -> String {
        match self.zone {
            Zone::Create => name_key(which),
            Zone::Destinations => root_key(which),
        }
    }

    /// The branch a new worktree starts from when it starts from the current branch.
    pub(crate) fn current_branch(&self) -> String {
        self.destinations()
            .get(self.committed)
            .map(|d| d.branch.clone())
            .filter(|b| !b.is_empty())
            .unwrap_or_else(|| self.info.current_branch.clone())
    }

    fn taken(&self) -> Vec<String> {
        std::iter::once(self.info.current_branch.clone())
            .chain(self.info.worktrees.iter().map(|w| w.branch.clone()))
            .collect()
    }

    pub(crate) fn reroll(&mut self) {
        let name = worktree_name(&self.taken(), &mut SystemDraw);
        self.name = TextEdit::new(name.clone());
        self.name.anchor = 0;
        self.name.focus = name.len();
    }
}

pub(crate) fn name_key(which: Which) -> String {
    format!("{}.repo.name.field", which.key())
}

pub(crate) fn root_key(which: Which) -> String {
    format!("{}.repo", which.key())
}

pub(crate) fn memory_key(root: &str) -> String {
    format!("new_session_destination_local_{root}")
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Memory {
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

fn remembered(state: &State, root: &str) -> Option<Memory> {
    match state.facts.settings.get(&memory_key(root))?.trim() {
        "main_repo" => Some(Memory::MainRepo),
        "new_worktree" => Some(Memory::NewWorktree),
        _ => None,
    }
}

/// Writes where the user started in this repository, when that changed.
fn remember(state: &State, root: &str, memory: Memory) {
    if remembered(state, root) == Some(memory) {
        return;
    }
    let _ = state.daemon.send(&SetSettingMessage {
        key: memory_key(root),
        value: memory.value().into(),
        ..SetSettingMessage::new()
    });
}

/// Switches the picker to the chooser for the repository at `root`, on the row the typed path
/// named. It takes the keyboard only from the picker itself or from nothing.
pub(crate) fn show(
    state: &mut State,
    which: Which,
    root: String,
    selected: String,
    info: RepoInfo,
    tx: &mut Tx,
) {
    let memory = remembered(state, &root);
    let mut chooser = Chooser {
        root,
        info,
        zone: Zone::Create,
        committed: 0,
        name: TextEdit::default(),
        start: Start::Default,
        refreshing: false,
        delete: None,
        read_at: state.wall_ms(tx) as f64 / 1000.0,
    };
    chooser.committed = chooser
        .destinations()
        .iter()
        .position(|d| d.path == selected)
        .unwrap_or(0);
    if chooser.committed > 0 || memory == Some(Memory::MainRepo) {
        chooser.zone = Zone::Destinations;
    }
    chooser.reroll();
    let key = chooser.focus_key(which);
    let Some(picker) = state.window.launching.picker_mut(which) else {
        return;
    };
    let ours = which.key();
    let takes_keyboard = match tx.focused() {
        None => true,
        Some(focused) => focused.starts_with(ours),
    };
    picker.highlight = None;
    picker.chooser = Some(chooser);
    if takes_keyboard {
        tx.focus(key.clone(), false);
        if key == name_key(which) {
            tx.select_all_text(key);
        }
    }
}

fn chooser_of(state: &mut State, which: Which) -> Option<&mut Chooser> {
    state.window.launching.picker_mut(which)?.chooser.as_mut()
}

fn set_zone(state: &mut State, which: Which, zone: Zone, tx: &mut Tx) {
    let Some(chooser) = chooser_of(state, which) else {
        return;
    };
    chooser.zone = zone;
    if zone == Zone::Create {
        chooser.delete = None;
    }
    let key = chooser.focus_key(which);
    tx.focus(key, false);
}

/// The create form took the keyboard, by a press or a key in its field: the committed row
/// stays, and a delete prompt goes.
pub(crate) fn enter_form(state: &mut State, which: Which) {
    if let Some(chooser) = chooser_of(state, which)
        && chooser.zone != Zone::Create
    {
        chooser.zone = Zone::Create;
        chooser.delete = None;
    }
}

/// Keys while the chooser shows.
pub(crate) fn on_key(state: &mut State, which: Which, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(chooser) = chooser_of(state, which) else {
        return false;
    };
    let letter = match &ev.key {
        Key::Text(text) if !ev.mods.cmd && !ev.mods.ctrl => text.chars().next(),
        _ => None,
    };
    match chooser.delete.clone() {
        Some(Delete::Deleting { .. }) => return true,
        Some(Delete::Failed {
            path, forceable, ..
        }) => {
            chooser.delete = None;
            return match letter {
                Some('y' | 'Y') if forceable => {
                    delete(state, which, path, true);
                    true
                }
                Some('n' | 'N') => true,
                _ => ev.key == Key::Escape,
            };
        }
        Some(Delete::Asking { path }) => {
            chooser.delete = None;
            return match letter {
                Some('y' | 'Y') => {
                    delete(state, which, path, false);
                    true
                }
                Some('n' | 'N') => true,
                _ => ev.key == Key::Escape,
            };
        }
        None => {}
    }
    let rows = chooser.destinations().len();
    match chooser.zone {
        Zone::Create => match ev.key {
            Key::Enter => {
                create(state, which, tx);
                true
            }
            Key::Tab => {
                chooser.start = match chooser.start {
                    Start::Default => Start::Current,
                    Start::Current => Start::Default,
                };
                true
            }
            Key::Down => {
                set_zone(state, which, Zone::Destinations, tx);
                true
            }
            Key::Command('r') if ev.mods.cmd || ev.mods.ctrl => {
                chooser.reroll();
                true
            }
            Key::Escape => {
                back(state, which, tx);
                true
            }
            _ => false,
        },
        Zone::Destinations => match (&ev.key, letter) {
            (Key::Up, _) if chooser.committed == 0 => {
                set_zone(state, which, Zone::Create, tx);
                true
            }
            (Key::Up, _) => {
                chooser.committed -= 1;
                true
            }
            (Key::Down, _) => {
                chooser.committed = (chooser.committed + 1).min(rows - 1);
                true
            }
            (Key::Enter, _) => {
                let row = chooser.committed;
                activate(state, which, row, tx);
                true
            }
            (Key::Escape, _) => {
                back(state, which, tx);
                true
            }
            (_, Some('d' | 'D')) => {
                if let Some(row) = chooser.destinations().get(chooser.committed)
                    && !row.main
                {
                    chooser.delete = Some(Delete::Asking {
                        path: row.path.clone(),
                    });
                }
                true
            }
            (_, Some('r' | 'R')) => {
                refresh(state, which);
                true
            }
            (_, Some(digit @ '1'..='9')) => {
                let row = digit as usize - '1' as usize;
                if row < rows {
                    chooser.committed = row;
                }
                true
            }
            _ => false,
        },
    }
}

/// Back to the path field, with the path as typed.
pub(crate) fn back(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(picker) = state.window.launching.picker_mut(which) else {
        return;
    };
    picker.stale();
    picker.chooser = None;
    tx.focus(picker.field_key(), false);
}

/// Opens a row: the checkout, remembered for this repository, or a worktree.
pub(crate) fn activate(state: &mut State, which: Which, row: usize, tx: &mut Tx) {
    let Some(chooser) = chooser_of(state, which) else {
        return;
    };
    chooser.delete = None;
    chooser.committed = row;
    let Some(destination) = chooser.destinations().into_iter().nth(row) else {
        return;
    };
    let root = chooser.root.clone();
    if destination.main {
        remember(state, &root, Memory::MainRepo);
    }
    launch(state, which, &destination.path, tx);
}

pub(crate) fn create(state: &mut State, which: Which, tx: &mut Tx) {
    let Some(chooser) = chooser_of(state, which) else {
        return;
    };
    let branch = chooser.name.text.trim().to_owned();
    if branch.is_empty() {
        return;
    }
    let from = match chooser.start {
        Start::Default => chooser.info.default_branch.clone(),
        Start::Current => chooser.current_branch(),
    };
    let root = chooser.root.clone();
    remember(state, &root, Memory::NewWorktree);
    super::create_worktree(state, which, root, branch, from, tx);
}

pub(crate) fn refresh(state: &mut State, which: Which) {
    let Some(chooser) = chooser_of(state, which).filter(|c| !c.refreshing) else {
        return;
    };
    chooser.refreshing = true;
    let repo = chooser.root.clone();
    let generation = state
        .window
        .launching
        .picker(which)
        .map_or(0, |p| p.generation);
    state.ask(
        |id| GetRepoInfoMessage {
            repo,
            request_id: Some(id),
            ..GetRepoInfoMessage::new()
        },
        move |state, reply, tx| {
            let read_at = state.wall_ms(tx) as f64 / 1000.0;
            let Some(chooser) =
                super::current(state, which, generation).and_then(|p| p.chooser.as_mut())
            else {
                return;
            };
            chooser.refreshing = false;
            let result: Option<GetRepoInfoResultMessage> = reply.parse();
            if let Some(info) = result.filter(|r| r.success).and_then(|r| r.info) {
                replace_info(chooser, info, None, read_at);
            }
        },
    );
}

/// New rows for the chooser, keeping the committed row on the same path, or on the row now at
/// the index of `deleted`.
fn replace_info(chooser: &mut Chooser, info: RepoInfo, deleted: Option<&str>, read_at: f64) {
    let rows = chooser.destinations();
    let kept = match deleted {
        Some(deleted) => {
            let index = rows.iter().position(|d| d.path == deleted).unwrap_or(0);
            let surviving: Vec<&Destination> = rows.iter().filter(|d| d.path != deleted).collect();
            surviving
                .get(index.min(surviving.len().saturating_sub(1)))
                .map(|d| d.path.clone())
                .unwrap_or_else(|| info.repo.clone())
        }
        None => rows
            .get(chooser.committed)
            .map(|d| d.path.clone())
            .unwrap_or_default(),
    };
    chooser.info = info;
    chooser.read_at = read_at;
    chooser.committed = chooser
        .destinations()
        .iter()
        .position(|d| d.path == kept)
        .unwrap_or(0);
}

fn delete(state: &mut State, which: Which, path: String, force: bool) {
    let Some(chooser) = chooser_of(state, which) else {
        return;
    };
    chooser.delete = Some(Delete::Deleting { path: path.clone() });
    let repo = chooser.root.clone();
    let deleting = path.clone();
    state.ask(
        |id| DeleteWorktreeMessage {
            path: deleting,
            force: force.then_some(true),
            request_id: Some(id),
            ..DeleteWorktreeMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<DeleteWorktreeResultMessage> = reply.parse();
            let Some(chooser) = chooser_of(state, which) else {
                return;
            };
            match result {
                Some(r) if r.success => {
                    reread_after_delete(state, which, repo, path);
                }
                other => {
                    let error = other
                        .as_ref()
                        .and_then(|r| r.error.clone())
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Failed to delete worktree".into());
                    let forceable = !force && other.and_then(|r| r.forceable) == Some(true);
                    chooser.delete = Some(Delete::Failed {
                        path,
                        error: error.clone(),
                        forceable,
                    });
                    if !forceable {
                        state.window.notify(error, tx);
                    }
                }
            }
        },
    );
}

fn reread_after_delete(state: &mut State, which: Which, repo: String, deleted: String) {
    state.ask(
        |id| GetRepoInfoMessage {
            repo,
            request_id: Some(id),
            ..GetRepoInfoMessage::new()
        },
        move |state, reply, tx| {
            let result: Option<GetRepoInfoResultMessage> = reply.parse();
            let read_at = state.wall_ms(tx) as f64 / 1000.0;
            let Some(chooser) = chooser_of(state, which) else {
                return;
            };
            chooser.delete = None;
            match result {
                Some(GetRepoInfoResultMessage {
                    success: true,
                    info: Some(info),
                    ..
                }) => replace_info(chooser, info, Some(&deleted), read_at),
                other => {
                    let error = other
                        .and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Failed to refresh repo options".into());
                    state.window.notify(error, tx);
                }
            }
        },
    );
}

pub(crate) fn row_detail(chooser: &Chooser, row: &Destination, now: f64) -> String {
    if !row.main {
        return paths::tildify(&row.path);
    }
    let info = &chooser.info;
    let hash: String = info.current_commit_hash.chars().take(7).collect();
    format!(
        "{} • {} • {}",
        paths::tildify(&info.repo),
        hash,
        ago(&info.current_commit_time, now)
    )
}

/// How long ago an RFC 3339 time was, as the chooser says it: minutes, hours, then days.
fn ago(time: &str, now: f64) -> String {
    let Some(then) = parse_rfc3339(time) else {
        return String::new();
    };
    let minutes = ((now - then) / 60.0).floor() as i64;
    if minutes < 60 {
        return format!("{minutes}m ago");
    }
    let hours = minutes / 60;
    if hours < 24 {
        return format!("{hours}h ago");
    }
    format!("{}d ago", hours / 24)
}

/// Seconds since the Unix epoch of `YYYY-MM-DDTHH:MM:SS` with an optional fraction and a `Z`
/// or `±HH:MM` offset.
fn parse_rfc3339(time: &str) -> Option<f64> {
    let (date, rest) = time.split_once('T')?;
    let mut date = date.split('-').map(|p| p.parse::<i64>().ok());
    let (y, m, d) = (date.next()??, date.next()??, date.next()??);
    let (clock, offset) = match rest.find(['Z', '+', '-']) {
        Some(at) => rest.split_at(at),
        None => (rest, ""),
    };
    let mut clock = clock.split(':');
    let h: i64 = clock.next()?.parse().ok()?;
    let min: i64 = clock.next()?.parse().ok()?;
    let sec: f64 = clock.next().unwrap_or("0").parse().ok()?;
    let offset_seconds = match offset.chars().next() {
        Some(sign @ ('+' | '-')) => {
            let (oh, om) = offset[1..].split_once(':')?;
            let seconds = oh.parse::<i64>().ok()? * 3600 + om.parse::<i64>().ok()? * 60;
            if sign == '+' { seconds } else { -seconds }
        }
        _ => 0,
    };
    let (y, m) = if m <= 2 { (y - 1, m + 9) } else { (y, m - 3) };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let doy = (153 * m + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146_097 + doe - 719_468;
    Some((days * 86_400 + h * 3600 + min * 60 - offset_seconds) as f64 + sec)
}

/// How far an agent started in a new worktree has got, shown in a corner without taking the
/// keyboard, and opened as a dialog when it failed and the user asks.
pub(crate) struct Setup {
    pub(crate) branch: String,
    pub(crate) repo: String,
    pub(crate) failed: Option<String>,
    pub(crate) opened: bool,
}

impl Setup {
    pub(crate) fn creating(branch: &str, repo: &str) -> Setup {
        Setup {
            branch: branch.into(),
            repo: repo.into(),
            failed: None,
            opened: false,
        }
    }

    pub(crate) fn fail(&mut self, error: String) {
        self.failed = Some(error);
    }
}
