//! The Worktrees list: every worktree the daemon tracks, what its sweep decided, and the way to
//! keep, delete or visit one; and the worktrees already removed.

mod view;

use std::collections::HashMap;

use attn_client::WorktreePush;
use attn_protocol::{
    DeleteWorktreeMessage, Worktree, WorktreeKeepMessage, WorktreeKeepResultEvent,
    WorktreeListMessage, WorktreeListResultEvent, WorktreeRefreshMessage, WorktreeRepository,
    WorktreeSweepEntry, WorktreeSweepLogMessage, WorktreeSweepLogResultEvent,
};
use rune::{TextEdit, Tx};

use super::Note;
use super::query::{base_name, matches_words};
use crate::State;
use crate::requests::Answer;

pub(crate) use view::{back_out, inspector, list, panel_key, status, toolbar};

/// As many removals as the Tauri app reads when Removed opens.
const SWEEP_LOG_PAGE: i64 = 100;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Scope {
    #[default]
    Active,
    Removed,
}

impl Scope {
    pub const ALL: [Scope; 2] = [Scope::Active, Scope::Removed];

    pub fn label(self) -> &'static str {
        match self {
            Scope::Active => "Active",
            Scope::Removed => "Removed",
        }
    }
}

#[derive(Default)]
pub struct Worktrees {
    pub scope: Scope,
    pub query: TextEdit,
    registry: Option<Registry>,
    sweep_log: Vec<WorktreeSweepEntry>,
    error: Option<String>,
    /// Rise with every read, so an answer to an earlier one is dropped.
    pub(super) epoch: u64,
    pub(super) log_epoch: u64,
    /// The last of `Facts::worktrees` the list took in.
    seen: u64,
    pub selected: Option<String>,
    pub menu: Option<String>,
    /// The worktree whose delete waits for the person to confirm it.
    pub confirming: Option<String>,
    notes: HashMap<String, Note>,
    refresh: Option<Refresh>,
}

struct Registry {
    worktrees: Vec<Worktree>,
    repositories: Vec<WorktreeRepository>,
}

/// The last background refresh asked of the daemon.
pub(crate) enum Refresh {
    Asked(i64),
    Refused(String),
}

/// A worktree's actions, first the one Enter runs.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Verb {
    Keep(bool),
    SessionsHere,
    GoTo { id: String, name: String },
    Delete,
    DeleteNow { losing_changes: bool },
    Cancel,
}

impl Verb {
    pub fn label(&self) -> String {
        match self {
            Verb::Keep(true) => "Keep".into(),
            Verb::Keep(false) => "Unpin".into(),
            Verb::SessionsHere => "Sessions here".into(),
            Verb::GoTo { name, .. } => format!("Go to {name}"),
            Verb::Delete => "Delete…".into(),
            Verb::DeleteNow {
                losing_changes: true,
            } => "Delete, losing changes".into(),
            Verb::DeleteNow {
                losing_changes: false,
            } => "Delete for real".into(),
            Verb::Cancel => "Cancel".into(),
        }
    }
}

impl Worktrees {
    pub(crate) fn note(&self, path: &str) -> Option<&Note> {
        self.notes.get(path)
    }

    pub(crate) fn error(&self) -> Option<&str> {
        self.error.as_deref()
    }

    pub(crate) fn refresh(&self) -> Option<&Refresh> {
        self.refresh.as_ref()
    }

    pub(crate) fn loaded(&self) -> bool {
        self.registry.is_some()
    }

    pub(crate) fn all(&self) -> &[Worktree] {
        self.registry.as_ref().map_or(&[], |r| &r.worktrees)
    }

    pub(crate) fn sweep_log(&self) -> &[WorktreeSweepEntry] {
        &self.sweep_log
    }

    pub(crate) fn integration(&self, main_repo: &str) -> Option<&str> {
        self.registry
            .iter()
            .flat_map(|r| &r.repositories)
            .find(|r| r.main_repo == main_repo)
            .and_then(|r| r.integration_branch.as_deref())
    }
}

/// A live session working in a worktree.
pub(crate) struct LiveSession {
    pub id: String,
    pub name: String,
}

pub(crate) fn live_sessions(state: &State, path: &str) -> Vec<LiveSession> {
    let inside = format!("{path}/");
    state
        .facts
        .sessions
        .iter()
        .filter(|s| s.directory == path || s.directory.starts_with(&inside))
        .map(|s| LiveSession {
            id: s.id.clone(),
            name: crate::sidebar::model::display_name(s),
        })
        .collect()
}

pub(crate) fn refreshing(state: &State, path: &str) -> bool {
    state.facts.git_running.values().any(|p| p == path)
}

pub(crate) fn verbs(state: &State, worktree: &Worktree) -> Vec<Verb> {
    let ledger = &state.window.ledger.worktrees;
    if ledger.confirming.as_deref() == Some(worktree.path.as_str()) {
        return vec![
            Verb::DeleteNow {
                losing_changes: worktree.dirty.unwrap_or_default(),
            },
            Verb::Cancel,
        ];
    }
    let flagged = |id: &str| {
        state
            .facts
            .sessions
            .iter()
            .any(|s| s.id == id && s.priority.unwrap_or_default())
    };
    let mut verbs = vec![
        Verb::Keep(!worktree.pinned.unwrap_or_default()),
        Verb::SessionsHere,
    ];
    verbs.extend(
        live_sessions(state, &worktree.path)
            .into_iter()
            .map(|s| Verb::GoTo {
                name: if flagged(&s.id) {
                    format!("⚑ {}", s.name)
                } else {
                    s.name
                },
                id: s.id,
            }),
    );
    verbs.push(Verb::Delete);
    verbs
}

struct Parsed {
    repo: String,
    words: Vec<String>,
}

fn parse(text: &str) -> Parsed {
    let mut parsed = Parsed {
        repo: String::new(),
        words: Vec::new(),
    };
    for token in text.split_whitespace() {
        let lower = token.to_lowercase();
        match lower.strip_prefix("repo:") {
            Some(repo) => parsed.repo = repo.to_owned(),
            None => parsed.words.push(lower),
        }
    }
    parsed
}

impl Parsed {
    fn repo_matches(&self, main_repo: &str) -> bool {
        self.repo.is_empty()
            || base_name(main_repo).to_lowercase() == self.repo
            || main_repo.to_lowercase() == self.repo
    }
}

/// The worktrees the typed query leaves, by repository, then path.
pub(crate) fn active_rows(state: &State) -> Vec<&Worktree> {
    let ledger = &state.window.ledger.worktrees;
    let parsed = parse(&ledger.query.text);
    let mut rows: Vec<&Worktree> = ledger
        .all()
        .iter()
        .filter(|w| parsed.repo_matches(&w.main_repo))
        .filter(|w| {
            let pinned = if w.pinned.unwrap_or_default() {
                "kept pinned"
            } else {
                ""
            };
            matches_words(
                &[
                    &w.path,
                    &w.branch,
                    w.sweep_status.as_deref().unwrap_or_default(),
                    w.merged_signal.as_deref().unwrap_or_default(),
                    pinned,
                ],
                &parsed.words,
            )
        })
        .collect();
    rows.sort_by(|a, b| (&a.main_repo, &a.path).cmp(&(&b.main_repo, &b.path)));
    rows
}

/// The removals the typed query leaves, by repository, then newest first.
pub(crate) fn removed_rows(state: &State) -> Vec<&WorktreeSweepEntry> {
    let ledger = &state.window.ledger.worktrees;
    let parsed = parse(&ledger.query.text);
    let mut rows: Vec<&WorktreeSweepEntry> = ledger
        .sweep_log
        .iter()
        .filter(|e| parsed.repo_matches(&e.main_repo))
        .filter(|e| {
            matches_words(
                &[
                    &e.path,
                    e.branch.as_deref().unwrap_or_default(),
                    &e.action,
                    e.reason.as_deref().unwrap_or_default(),
                ],
                &parsed.words,
            )
        })
        .collect();
    rows.sort_by(|a, b| a.main_repo.cmp(&b.main_repo).then(b.at.cmp(&a.at)));
    rows
}

/// The keys of the shown rows, in list order: worktree paths, or removal ids.
fn row_keys(state: &State) -> Vec<String> {
    match state.window.ledger.worktrees.scope {
        Scope::Active => active_rows(state).iter().map(|w| w.path.clone()).collect(),
        Scope::Removed => removed_rows(state).iter().map(|e| e.id.clone()).collect(),
    }
}

/// The row the inspector reads and the keys act on: the one chosen, or the first.
pub(crate) fn selected_key(state: &State) -> Option<String> {
    let keys = row_keys(state);
    let chosen = state.window.ledger.worktrees.selected.as_ref();
    chosen
        .filter(|k| keys.contains(k))
        .cloned()
        .or_else(|| keys.into_iter().next())
}

/// Shows the list afresh, on `path` when a session asked to see its worktree.
pub(crate) fn enter(state: &mut State, path: Option<String>, tx: &mut Tx) {
    let ledger = &mut state.window.ledger.worktrees;
    *ledger = Worktrees {
        registry: ledger.registry.take(),
        epoch: ledger.epoch,
        log_epoch: ledger.log_epoch,
        selected: path,
        ..Worktrees::default()
    };
    read(state, tx);
}

/// Reads the registry, and the removals while Removed shows.
pub(crate) fn read(state: &mut State, tx: &mut Tx) {
    let ledger = &mut state.window.ledger.worktrees;
    ledger.epoch += 1;
    ledger.seen = state.facts.worktrees.seq;
    let epoch = ledger.epoch;
    state.ask(
        |request_id| WorktreeListMessage {
            request_id: Some(request_id),
            ..WorktreeListMessage::new()
        },
        move |state, answer, _| {
            let ledger = &mut state.window.ledger.worktrees;
            if ledger.epoch != epoch || matches!(answer, Answer::NotSent | Answer::Dropped) {
                return;
            }
            let reply = answer.parse::<WorktreeListResultEvent>();
            match reply.filter(|r| r.success) {
                Some(reply) => {
                    let result = reply.worktree_list_result;
                    ledger.registry = Some(Registry {
                        worktrees: result.worktrees,
                        repositories: result.repositories,
                    });
                    ledger.error = None;
                }
                None => {
                    ledger.error = Some(super::answer_error(&answer, "Reading worktrees failed"))
                }
            }
            keep_selection(state);
        },
        tx,
    );
    if state.window.ledger.worktrees.scope == Scope::Removed {
        read_sweep_log(state, tx);
    }
}

fn read_sweep_log(state: &mut State, tx: &mut Tx) {
    let ledger = &mut state.window.ledger.worktrees;
    ledger.log_epoch += 1;
    let epoch = ledger.log_epoch;
    state.ask(
        |request_id| WorktreeSweepLogMessage {
            request_id: Some(request_id),
            limit: Some(SWEEP_LOG_PAGE),
            ..WorktreeSweepLogMessage::new()
        },
        move |state, answer, _| {
            let ledger = &mut state.window.ledger.worktrees;
            if ledger.log_epoch != epoch || matches!(answer, Answer::NotSent | Answer::Dropped) {
                return;
            }
            match answer
                .parse::<WorktreeSweepLogResultEvent>()
                .filter(|r| r.success)
            {
                Some(reply) => ledger.sweep_log = reply.worktree_sweep_log_result.entries,
                None => {
                    ledger.error = Some(super::answer_error(
                        &answer,
                        "Reading removed worktrees failed",
                    ))
                }
            }
            keep_selection(state);
        },
        tx,
    );
}

/// Takes in the worktrees the daemon pushed since the list last looked, or reads the list again
/// when it missed some.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let pushes = state.facts.worktrees.clone();
    let ledger = &mut state.window.ledger.worktrees;
    if pushes.seq == ledger.seen {
        return;
    }
    let Some(news) = pushes.since(ledger.seen) else {
        read(state, tx);
        return;
    };
    ledger.seen = pushes.seq;
    for push in news {
        match push {
            WorktreePush::Observed(worktree) => {
                if let Some(registry) = &mut ledger.registry {
                    match registry
                        .worktrees
                        .iter_mut()
                        .find(|w| w.path == worktree.path)
                    {
                        Some(row) => *row = worktree.clone(),
                        None => registry.worktrees.push(worktree.clone()),
                    }
                }
            }
            WorktreePush::Swept(entry) => {
                if let Some(registry) = &mut ledger.registry {
                    registry.worktrees.retain(|w| w.path != entry.path);
                }
                ledger.sweep_log.retain(|e| e.id != entry.id);
                ledger.sweep_log.insert(0, entry.clone());
                ledger.sweep_log.truncate(attn_client::SWEEPS_KEPT);
            }
        }
    }
    keep_selection(state);
}

fn keep_selection(state: &mut State) {
    let keys = row_keys(state);
    let ledger = &mut state.window.ledger.worktrees;
    for chosen in [&mut ledger.menu, &mut ledger.confirming] {
        if chosen.as_ref().is_some_and(|k| !keys.contains(k)) {
            *chosen = None;
        }
    }
}

pub(crate) fn set_scope(state: &mut State, scope: Scope, tx: &mut Tx) {
    let ledger = &mut state.window.ledger.worktrees;
    if ledger.scope == scope {
        return;
    }
    ledger.scope = scope;
    ledger.selected = None;
    ledger.menu = None;
    ledger.confirming = None;
    if scope == Scope::Removed {
        read_sweep_log(state, tx);
    }
}

pub(crate) fn query_edited(state: &mut State) {
    keep_selection(state);
}

pub(crate) fn select(state: &mut State, key: &str) {
    let ledger = &mut state.window.ledger.worktrees;
    if ledger.selected.as_deref() != Some(key) {
        ledger.menu = None;
    }
    if ledger.confirming.as_deref() != Some(key) {
        ledger.confirming = None;
    }
    ledger.selected = Some(key.to_owned());
}

/// Moves the selection `by` rows, stopping at either end.
pub(crate) fn step(state: &mut State, by: isize) {
    let keys = row_keys(state);
    if keys.is_empty() {
        return;
    }
    let current = selected_key(state)
        .and_then(|s| keys.iter().position(|k| *k == s))
        .unwrap_or(0);
    let next = current.saturating_add_signed(by).min(keys.len() - 1);
    select(state, &keys[next]);
}

fn worktree(state: &State, path: &str) -> Option<Worktree> {
    state
        .window
        .ledger
        .worktrees
        .all()
        .iter()
        .find(|w| w.path == path)
        .cloned()
}

/// Runs the worktree's verb at `index`, its first for Enter.
pub(crate) fn run_nth(state: &mut State, path: &str, index: usize, tx: &mut Tx) {
    let verb = worktree(state, path).and_then(|w| verbs(state, &w).get(index).cloned());
    if let Some(verb) = verb {
        run(state, path, verb, tx);
    }
}

pub(crate) fn run(state: &mut State, path: &str, verb: Verb, tx: &mut Tx) {
    let Some(worktree) = worktree(state, path) else {
        return;
    };
    let ledger = &mut state.window.ledger.worktrees;
    ledger.selected = Some(path.to_owned());
    ledger.menu = None;
    let busy = matches!(ledger.notes.get(path), Some(Note::Busy(_)));
    match verb {
        Verb::SessionsHere => super::show_sessions_in(state, path, tx),
        Verb::GoTo { id, .. } => {
            super::close(state, tx);
            crate::sidebar::open_agent(state, &id, tx);
        }
        Verb::Delete => ledger.confirming = Some(path.to_owned()),
        Verb::Cancel => ledger.confirming = None,
        Verb::Keep(keep) if !busy => keep_worktree(state, worktree, keep, tx),
        Verb::DeleteNow { losing_changes } if !busy => {
            if ledger.confirming.take().as_deref() == Some(path) {
                delete(state, worktree.path, losing_changes, tx);
            }
        }
        Verb::Keep(_) | Verb::DeleteNow { .. } => {}
    }
}

fn keep_worktree(state: &mut State, worktree: Worktree, keep: bool, tx: &mut Tx) {
    let path = worktree.path;
    let words = if keep { "keeping…" } else { "releasing…" };
    let notes = &mut state.window.ledger.worktrees.notes;
    notes.insert(path.clone(), Note::Busy(words.into()));
    let asked = path.clone();
    state.ask(
        |request_id| WorktreeKeepMessage {
            request_id: Some(request_id),
            path: asked,
            keep,
            ..WorktreeKeepMessage::new()
        },
        move |state, answer, _| {
            let updated = answer
                .parse::<WorktreeKeepResultEvent>()
                .filter(|r| r.success)
                .and_then(|r| r.worktrees)
                .and_then(|w| w.into_iter().next());
            let ledger = &mut state.window.ledger.worktrees;
            match updated {
                Some(updated) => {
                    ledger.notes.remove(&path);
                    ledger.error = None;
                    if let Some(row) = ledger
                        .registry
                        .as_mut()
                        .and_then(|r| r.worktrees.iter_mut().find(|w| w.path == updated.path))
                    {
                        *row = updated;
                    }
                }
                None => {
                    let why = super::answer_error(&answer, "Changing the keep failed");
                    ledger.notes.insert(path, Note::Refused(why));
                }
            }
        },
        tx,
    );
}

/// Deletes the worktree; the daemon's sweep push drops the row once it is gone.
fn delete(state: &mut State, path: String, force: bool, tx: &mut Tx) {
    let notes = &mut state.window.ledger.worktrees.notes;
    notes.insert(path.clone(), Note::Busy("deleting…".into()));
    let asked = path.clone();
    state.ask(
        |request_id| DeleteWorktreeMessage {
            request_id: Some(request_id),
            path: asked,
            force: force.then_some(true),
            ..DeleteWorktreeMessage::new()
        },
        move |state, answer, _| {
            let notes = &mut state.window.ledger.worktrees.notes;
            match answer.outcome() {
                Ok(_) => notes.remove(&path),
                Err(why) => notes.insert(path, Note::Refused(why)),
            };
        },
        tx,
    );
}

/// Asks the daemon to refresh every worktree in the background.
pub(crate) fn refresh(state: &mut State, tx: &mut Tx) {
    state.ask(
        |request_id| WorktreeRefreshMessage {
            request_id: Some(request_id),
            ..WorktreeRefreshMessage::new()
        },
        |state, answer, _| {
            let refused = match answer {
                Answer::Reply(reply)
                    if reply.event == "worktree_refresh_result"
                        && reply.body["success"] != true =>
                {
                    Some("The daemon has no job queue running, so no refresh was queued".to_owned())
                }
                other => other.outcome().err(),
            };
            let ledger = &mut state.window.ledger.worktrees;
            ledger.refresh = Some(match refused {
                None => Refresh::Asked(state.epoch_ms),
                Some(why) => Refresh::Refused(why),
            });
        },
        tx,
    );
}
