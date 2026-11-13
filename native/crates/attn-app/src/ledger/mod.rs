//! The ledger: every session, live and closed, with the way back to each. The Sessions list
//! reads the daemon's ledger a page at a time, keeps the page current as sessions close, and
//! reopens a closed session under its own id.

pub(crate) mod query;
mod view;

use std::collections::HashMap;

use attn_protocol::{
    KeptConversationKeepMessage, OpenSeedMessage, SessionLedgerEntry, SessionLedgerFacets,
    SessionListResultMessage, SessionReopenMessage, SessionReopenResultMessage, SetSettingMessage,
};
use rune::{TextEdit, Tx};

use crate::State;
use crate::requests::{Answer, Refused};
use crate::shell::Layer;
use query::{Filters, ProfileChoice, Scope, Verdict};

pub(crate) use view::view;

mod conversations;
mod worktrees;

/// Rows per page, as the Tauri app reads them.
pub const PAGE: i64 = 50;

pub(crate) const LIST: &str = "ledger.list";
pub(crate) const QUERY: &str = "ledger.query.field";

/// The ledger's lists, in the order `[` and `]` walk them.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Tab {
    #[default]
    Sessions,
    Worktrees,
    Conversations,
}

impl Tab {
    pub const ALL: [Tab; 3] = [Tab::Sessions, Tab::Worktrees, Tab::Conversations];

    pub fn label(self) -> &'static str {
        match self {
            Tab::Sessions => "Sessions",
            Tab::Worktrees => "Worktrees",
            Tab::Conversations => "Conversations",
        }
    }
}

#[derive(Default)]
pub struct Ledger {
    pub tab: Tab,
    pub conversations: conversations::Kept,
    pub worktrees: worktrees::Worktrees,
    /// What the page shows; the scope and the daemon's filters the query resolved to.
    pub filters: Filters,
    pub query: TextEdit,
    /// The profiles by id and name as the query last named them, so a rename follows.
    named_with: Vec<(String, String)>,
    /// A profile the query chose, kept among the choices when a later page leaves it out.
    chosen_profile: Option<ProfileChoice>,
    read: Read,
    omitted: i64,
    next_before: Option<String>,
    loading: bool,
    loading_more: bool,
    /// Rises with every read, so an answer to an earlier one is dropped.
    epoch: u64,
    /// The last close of `Facts::closes` the page took in.
    seen_closes: u64,
    /// What a page read reprices: the settings that price usage.
    priced_with: String,
    pub selected: Option<String>,
    /// The row whose verb menu is open.
    pub menu: Option<String>,
    attempts: HashMap<String, Attempt>,
    /// A conversation pin asked of a row, until the daemon answers.
    keep_notes: HashMap<String, Note>,
    /// The last `Facts::kept_conversations` the shown list took in.
    seen_kept: u64,
    pub legend: bool,
    pub copied: Option<String>,
}

/// One read of the ledger: the rows of the filters it answered, or why it failed.
#[derive(Default)]
struct Read {
    filters: Option<Filters>,
    entries: Vec<SessionLedgerEntry>,
    facets: Option<SessionLedgerFacets>,
    error: Option<String>,
}

/// The last reopen asked of a row, while that row stays closed at the same moment.
struct Attempt {
    closed_at: String,
    note: Option<Note>,
    verdict: Option<Verdict>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Note {
    Busy(String),
    Refused(String),
}

/// A row's actions, first the one Enter runs.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub enum Verb {
    Focus,
    Reopen(String),
    Seed { id: String, title: String },
    ShowWorktree,
    Keep(bool),
}

impl Verb {
    pub fn label(&self) -> String {
        match self {
            Verb::Focus => "Focus".into(),
            Verb::Reopen(action) => query::action_label(action).into(),
            Verb::Seed { title, .. } => format!("Seed · {title}"),
            Verb::ShowWorktree => "Show worktree".into(),
            Verb::Keep(true) => "Keep conversation".into(),
            Verb::Keep(false) => "Unkeep".into(),
        }
    }
}

pub(crate) fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::Ledger)
}

pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        close(state, tx);
    } else {
        open(state, Tab::Sessions, tx);
    }
}

/// Opens the ledger on `tab`, the Sessions list on the filters it was last left with, and reads
/// what `tab` shows.
pub(crate) fn open(state: &mut State, tab: Tab, tx: &mut Tx) {
    let filters = Filters::remembered(
        state
            .facts
            .settings
            .get(query::FILTERS_SETTING)
            .map(String::as_str),
    );
    let named_with = profile_names(state);
    let chosen_profile = (!filters.profile_id.is_empty()).then(|| ProfileChoice {
        profile_id: filters.profile_id.clone(),
        name: named_with
            .iter()
            .find(|(id, _)| *id == filters.profile_id)
            .map(|(_, n)| n.clone())
            .unwrap_or_default(),
        deleted: false,
    });
    let epoch = state.window.ledger.epoch;
    let mut conversations = conversations::Kept::default();
    conversations.epoch = state.window.ledger.conversations.epoch;
    let worktrees = state.window.ledger.worktrees.reset(None);
    state.window.ledger = Ledger {
        tab,
        conversations,
        worktrees,
        seen_kept: state.facts.kept_conversations,
        query: TextEdit::new(query::format(&filters, &named_with)),
        filters,
        named_with,
        chosen_profile,
        epoch,
        seen_closes: state.facts.closes.seq,
        priced_with: pricing(state),
        ..Ledger::default()
    };
    state.window.layer = Some(Layer::Ledger);
    tx.focus(LIST, false);
    read_tab(state, tx);
}

/// Shows `tab` and reads it, with the keyboard on its rows.
pub(crate) fn show_tab(state: &mut State, tab: Tab, tx: &mut Tx) {
    leave_for(state, tab, tx);
    match tab {
        Tab::Worktrees => worktrees::enter(state, None, tx),
        _ => read_tab(state, tx),
    }
}

fn leave_for(state: &mut State, tab: Tab, tx: &mut Tx) {
    let ledger = &mut state.window.ledger;
    ledger.tab = tab;
    ledger.menu = None;
    ledger.legend = false;
    ledger.conversations.leave();
    tx.focus(LIST, false);
}

/// Shows the Worktrees list on the worktree a session ran in.
fn show_worktree(state: &mut State, path: String, tx: &mut Tx) {
    leave_for(state, Tab::Worktrees, tx);
    worktrees::enter(state, Some(path), tx);
}

/// Shows the Sessions list narrowed to the sessions that ran in `path`.
fn show_sessions_in(state: &mut State, path: &str, tx: &mut Tx) {
    show_tab(state, Tab::Sessions, tx);
    let ledger = &mut state.window.ledger;
    ledger.query = TextEdit::new(format!("dir:{path}"));
    ledger.selected = None;
    query_edited(state, tx);
}

/// Moves `by` lists along, wrapping at either end.
pub(crate) fn step_tab(state: &mut State, by: isize, tx: &mut Tx) {
    let at = Tab::ALL
        .iter()
        .position(|t| *t == state.window.ledger.tab)
        .unwrap_or(0);
    let next = (at as isize + by).rem_euclid(Tab::ALL.len() as isize) as usize;
    show_tab(state, Tab::ALL[next], tx);
}

fn read_tab(state: &mut State, tx: &mut Tx) {
    match state.window.ledger.tab {
        Tab::Sessions => read(state, tx),
        Tab::Worktrees => worktrees::read(state, tx),
        Tab::Conversations => conversations::read(state, tx),
    }
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    if !is_open(state) {
        return;
    }
    forget_reads(&mut state.window.ledger);
    state.window.layer = None;
    crate::picker::refocus_inline(state, tx);
}

/// Takes in what changed in the daemon's facts while the list is open: a close lands on the
/// page in place, a reconnect, a profile rename or a price change reads the page again.
pub(crate) fn follow(state: &mut State, was_connected: bool, tx: &mut Tx) {
    if !is_open(state) {
        return;
    }
    let connected = state.facts.connection == attn_client::Connection::Connected;
    let names = profile_names(state);
    let pricing = pricing(state);
    let ledger = &mut state.window.ledger;
    if !connected {
        forget_reads(ledger);
        ledger.loading = false;
        ledger.loading_more = false;
        ledger.seen_closes = state.facts.closes.seq;
        return;
    }
    let mut reread = !was_connected
        || ledger.priced_with != pricing
        || ledger.seen_kept != state.facts.kept_conversations;
    ledger.seen_kept = state.facts.kept_conversations;
    ledger.priced_with = pricing;
    if ledger.named_with != names {
        let text = query::rename_profile_tokens(&ledger.query.text, &ledger.named_with, &names);
        if text != ledger.query.text {
            ledger.query = TextEdit::new(text);
        }
        ledger.named_with = names;
        reread = true;
    }
    let closes = &state.facts.closes;
    match closes.since(ledger.seen_closes) {
        Some(new) => {
            let now = state.epoch_ms;
            for entry in new {
                take_close(ledger, entry.clone(), now);
            }
        }
        None => reread = true,
    }
    ledger.seen_closes = closes.seq;
    if reread {
        read_tab(state, tx);
    } else if state.window.ledger.tab == Tab::Worktrees {
        worktrees::follow(state, tx);
    }
    keep_selection(state);
}

/// Drops every read in flight, so no answer lands on a list that moved on.
fn forget_reads(ledger: &mut Ledger) {
    ledger.epoch += 1;
    ledger.conversations.epoch += 1;
    ledger.worktrees.epoch += 1;
    ledger.worktrees.log_epoch += 1;
}

fn take_close(ledger: &mut Ledger, entry: SessionLedgerEntry, now_ms: i64) {
    let entries = &mut ledger.read.entries;
    match entries.iter().position(|row| row.id == entry.id) {
        Some(at) if ledger.filters.scope == Scope::Live => {
            entries.remove(at);
        }
        Some(at) => entries[at] = entry,
        None if ledger.filters.holds_close(&entry, now_ms) => entries.insert(0, entry),
        None => {}
    }
}

fn profile_names(state: &State) -> Vec<(String, String)> {
    state
        .facts
        .profiles
        .all
        .iter()
        .map(|p| (p.id.clone(), p.name.clone()))
        .collect()
}

fn pricing(state: &State) -> String {
    state
        .facts
        .settings
        .iter()
        .filter(|(k, _)| k.starts_with("session_cost."))
        .map(|(k, v)| format!("{k}={v}\n"))
        .collect()
}

/// Reads the first page of the filters shown, replacing the page when it answers.
fn read(state: &mut State, tx: &mut Tx) {
    let ledger = &mut state.window.ledger;
    ledger.epoch += 1;
    ledger.loading_more = false;
    ledger.next_before = None;
    ledger.omitted = 0;
    let filters = ledger.filters.clone();
    let Ok(request) = filters.request(state.epoch_ms) else {
        ledger.loading = false;
        return;
    };
    if ledger.read.filters.as_ref() == Some(&filters) {
        ledger.read.error = None;
    }
    if state.facts.connection != attn_client::Connection::Connected {
        ledger.loading = false;
        return;
    }
    ledger.loading = true;
    let epoch = ledger.epoch;
    state.ask(
        |request_id| attn_protocol::SessionListMessage {
            request_id: Some(request_id),
            limit: Some(PAGE),
            ..request
        },
        move |state, answer, tx: &mut Tx| {
            let ledger = &mut state.window.ledger;
            if ledger.epoch != epoch {
                return;
            }
            ledger.loading = false;
            let page = match answer {
                Answer::NotSent | Answer::Dropped => return,
                Answer::Reply(_) => answer.parse::<SessionListResultMessage>(),
            };
            match page.filter(|p| p.success).and_then(|p| p.result) {
                Some(result) => {
                    ledger.read = Read {
                        filters: Some(filters),
                        entries: result.entries,
                        facets: result.facets,
                        error: None,
                    };
                    ledger.omitted = result.omitted;
                    ledger.next_before = result.next_before;
                    query_edited(state, tx);
                }
                None => {
                    let why = answer_error(&answer, "Reading the session ledger failed");
                    if ledger.read.filters.as_ref() == Some(&filters) {
                        ledger.read.error = Some(why);
                    } else {
                        ledger.read = Read {
                            filters: Some(filters),
                            error: Some(why),
                            ..Read::default()
                        };
                    }
                }
            }
        },
        tx,
    );
}

fn answer_error(answer: &Answer, fallback: &str) -> String {
    match answer {
        Answer::Reply(reply) => reply.body["error"]
            .as_str()
            .filter(|e| !e.is_empty())
            .unwrap_or(fallback)
            .to_owned(),
        Answer::NotSent | Answer::Dropped => fallback.to_owned(),
    }
}

/// Reads the page after the last one and adds its rows.
pub(crate) fn load_more(state: &mut State, tx: &mut Tx) {
    let ledger = &mut state.window.ledger;
    let Some(before) = ledger.next_before.clone() else {
        return;
    };
    if ledger.loading || ledger.loading_more {
        return;
    }
    let Ok(request) = ledger.filters.request(state.epoch_ms) else {
        return;
    };
    ledger.loading_more = true;
    let epoch = ledger.epoch;
    state.ask(
        |request_id| attn_protocol::SessionListMessage {
            request_id: Some(request_id),
            limit: Some(PAGE),
            before: Some(before),
            ..request
        },
        move |state, answer, _: &mut Tx| {
            let ledger = &mut state.window.ledger;
            if ledger.epoch != epoch {
                return;
            }
            ledger.loading_more = false;
            match answer
                .parse::<SessionListResultMessage>()
                .filter(|p| p.success)
                .and_then(|p| p.result)
            {
                Some(result) => {
                    for entry in result.entries {
                        if !ledger.read.entries.iter().any(|e| e.id == entry.id) {
                            ledger.read.entries.push(entry);
                        }
                    }
                    ledger.omitted = result.omitted;
                    ledger.next_before = result.next_before;
                }
                None => {
                    ledger.read.error =
                        Some(answer_error(&answer, "Reading the session ledger failed"))
                }
            }
        },
        tx,
    );
}

/// The typed query changed: the page narrows at once, and the daemon reads the filters it
/// names when they changed.
pub(crate) fn query_edited(state: &mut State, tx: &mut Tx) {
    let ledger = &state.window.ledger;
    let facets = ledger.read_facets();
    let profiles =
        query::profile_choices(&ledger.named_with, facets, ledger.chosen_profile.as_ref());
    let parsed = query::parse(
        &ledger.query.text,
        facets,
        &profiles,
        &ledger.filters.repository,
    );
    let next = query::applied(&ledger.filters, &parsed, facets.is_some());
    let chosen = profiles
        .iter()
        .find(|c| c.profile_id == next.profile_id)
        .cloned();
    set_filters(state, next, tx);
    if chosen.is_some() {
        state.window.ledger.chosen_profile = chosen;
    }
}

pub(crate) fn set_scope(state: &mut State, scope: Scope, tx: &mut Tx) {
    let filters = Filters {
        scope,
        ..state.window.ledger.filters.clone()
    };
    set_filters(state, filters, tx);
}

fn set_filters(state: &mut State, filters: Filters, tx: &mut Tx) {
    if state.window.ledger.filters == filters {
        return;
    }
    state.window.ledger.filters = filters;
    let _ = state.daemon.send(&SetSettingMessage {
        key: query::FILTERS_SETTING.into(),
        value: state.window.ledger.filters.setting(),
        ..SetSettingMessage::new()
    });
    read(state, tx);
}

impl Ledger {
    /// The rows of the filters shown, or none while a read of other filters is pending.
    pub(crate) fn entries(&self) -> &[SessionLedgerEntry] {
        match &self.read.filters {
            Some(f) if *f == self.filters => &self.read.entries,
            _ => &[],
        }
    }

    fn read_facets(&self) -> Option<&SessionLedgerFacets> {
        match &self.read.filters {
            Some(f) if *f == self.filters => self.read.facets.as_ref(),
            _ => None,
        }
    }

    pub(crate) fn note(&self, entry: &SessionLedgerEntry) -> Option<&Note> {
        self.keep_notes
            .get(&entry.id)
            .or_else(|| self.attempt(entry).and_then(|a| a.note.as_ref()))
    }

    pub(crate) fn verdict(&self, entry: &SessionLedgerEntry) -> Option<&Verdict> {
        self.attempt(entry).and_then(|a| a.verdict.as_ref())
    }

    fn attempt(&self, entry: &SessionLedgerEntry) -> Option<&Attempt> {
        self.attempts
            .get(&entry.id)
            .filter(|a| a.closed_at == entry.closed_at.clone().unwrap_or_default())
    }

    fn record(&mut self, entry: &SessionLedgerEntry, note: Option<Note>, verdict: Option<Verdict>) {
        let closed_at = entry.closed_at.clone().unwrap_or_default();
        let kept = self
            .attempts
            .remove(&entry.id)
            .filter(|a| a.closed_at == closed_at);
        let verdict = verdict.or_else(|| kept.and_then(|a| a.verdict));
        self.attempts.insert(
            entry.id.clone(),
            Attempt {
                closed_at,
                note,
                verdict,
            },
        );
    }

    /// Why the page shows no rows.
    pub(crate) fn empty_message(&self, now_ms: i64) -> String {
        if let Err(why) = self.filters.window(now_ms) {
            return why;
        }
        let read = (self.read.filters.as_ref() == Some(&self.filters)).then_some(&self.read);
        if let Some(error) = read.and_then(|r| r.error.clone()) {
            return error;
        }
        if self.loading && self.entries().is_empty() {
            return "Reading the ledger…".into();
        }
        if !self.entries().is_empty() {
            return "Nothing on this page matches the query.".into();
        }
        match self.filters.scope {
            Scope::Closed => "No closed sessions yet. Closing one records it here.".into(),
            Scope::Live => "No live sessions right now.".into(),
            Scope::All => "The ledger is empty.".into(),
        }
    }

    pub(crate) fn failed(&self, now_ms: i64) -> bool {
        self.filters.window(now_ms).is_err()
            || self.read.filters.as_ref() == Some(&self.filters) && self.read.error.is_some()
    }

    pub(crate) fn omitted(&self) -> i64 {
        if self.next_before.is_some() {
            self.omitted
        } else {
            0
        }
    }

    pub(crate) fn loading_more(&self) -> bool {
        self.loading_more
    }
}

/// A row as the list shows it.
pub(crate) struct Row<'a> {
    pub entry: &'a SessionLedgerEntry,
    pub title: String,
    pub live: bool,
    pub verbs: Vec<Verb>,
}

/// The page's rows the typed `dir:` and words leave, newest first.
pub(crate) fn rows(state: &State) -> Vec<Row<'_>> {
    let ledger = &state.window.ledger;
    let parsed = query::parse(&ledger.query.text, None, &[], "");
    ledger
        .entries()
        .iter()
        .filter(|e| query::matches_dir(&e.directory, &parsed.dir))
        .filter(|e| {
            query::matches_words(
                &[
                    &e.label,
                    &e.id,
                    &e.agent,
                    e.branch.as_deref().unwrap_or_default(),
                    &e.directory,
                    &e.profile_name,
                ],
                &parsed.words,
            )
        })
        .map(|entry| row(state, entry))
        .collect()
}

fn row<'a>(state: &State, entry: &'a SessionLedgerEntry) -> Row<'a> {
    let closed = query::is_closed(entry);
    let live_session = state.facts.sessions.iter().find(|s| s.id == entry.id);
    let live = !closed && live_session.is_some();
    let ledger = &state.window.ledger;
    let mut verbs = Vec::new();
    if live {
        verbs.push(Verb::Focus);
    }
    if closed && !entry.profile_deleted.unwrap_or_default() {
        match ledger.verdict(entry) {
            Some(verdict) => verbs.extend(verdict.actions.iter().cloned().map(Verb::Reopen)),
            None => verbs.push(Verb::Reopen("reopen".into())),
        }
    }
    if let Some(seed) = crate::garden::seeds(state).iter().find(|s| {
        s.tender
            .as_ref()
            .is_some_and(|t| t.session_id.as_deref() == Some(entry.id.as_str()))
    }) {
        verbs.push(Verb::Seed {
            id: seed.id.clone(),
            title: seed.title.clone(),
        });
    }
    let directory_missing = ledger
        .verdict(entry)
        .is_some_and(|v| v.directory_state == "missing");
    if entry.host.is_none() && entry.is_worktree.unwrap_or_default() && !directory_missing {
        verbs.push(Verb::ShowWorktree);
    }
    if keeps_conversation(entry) {
        verbs.push(Verb::Keep(entry.conversation_pinned_at.is_none()));
    }
    let shell_name = live_session
        .filter(|_| entry.agent == "shell")
        .map(|s| s.label.clone());
    let title = entry
        .member_name
        .clone()
        .filter(|n| !n.is_empty())
        .or(shell_name)
        .unwrap_or_else(|| entry.label.clone());
    Row {
        entry,
        title: if title.is_empty() {
            "untitled session".into()
        } else {
            title
        },
        live,
        verbs,
    }
}

/// attn keeps copies of Claude Code conversations that ran at the home.
pub(crate) fn keeps_conversation(entry: &SessionLedgerEntry) -> bool {
    entry.agent == "claude" && entry.host.is_none()
}

/// The row the inspector reads and the keys act on: the one chosen, or the first.
pub(crate) fn selected<'a>(state: &State, rows: &'a [Row<'a>]) -> Option<&'a Row<'a>> {
    let chosen = state.window.ledger.selected.as_deref();
    rows.iter()
        .find(|r| Some(r.entry.id.as_str()) == chosen)
        .or_else(|| rows.first())
}

fn keep_selection(state: &mut State) {
    let ids: Vec<String> = rows(state).iter().map(|r| r.entry.id.clone()).collect();
    let ledger = &mut state.window.ledger;
    if ledger.selected.as_ref().is_some_and(|s| !ids.contains(s)) {
        ledger.selected = None;
    }
    if ledger.menu.as_ref().is_some_and(|m| !ids.contains(m)) {
        ledger.menu = None;
    }
}

pub(crate) fn select(state: &mut State, id: &str) {
    let ledger = &mut state.window.ledger;
    if ledger.selected.as_deref() != Some(id) {
        ledger.menu = None;
    }
    ledger.selected = Some(id.to_owned());
}

/// Moves the selection `by` rows, stopping at either end.
pub(crate) fn step(state: &mut State, by: isize) {
    let ids: Vec<String> = rows(state).iter().map(|r| r.entry.id.clone()).collect();
    if ids.is_empty() {
        return;
    }
    let current = state
        .window
        .ledger
        .selected
        .as_ref()
        .and_then(|s| ids.iter().position(|i| i == s))
        .unwrap_or(0);
    let next = current.saturating_add_signed(by).min(ids.len() - 1);
    let ledger = &mut state.window.ledger;
    ledger.selected = Some(ids[next].clone());
    ledger.menu = None;
}

/// Runs a row's verb.
pub(crate) fn run(state: &mut State, id: &str, verb: Verb, tx: &mut Tx) {
    let Some(entry) = state
        .window
        .ledger
        .entries()
        .iter()
        .find(|e| e.id == id)
        .cloned()
    else {
        return;
    };
    let ledger = &mut state.window.ledger;
    ledger.selected = Some(entry.id.clone());
    ledger.menu = None;
    let busy = match verb {
        Verb::Keep(_) => ledger.keep_notes.get(&entry.id),
        _ => ledger.attempt(&entry).and_then(|a| a.note.as_ref()),
    };
    if matches!(busy, Some(Note::Busy(_))) {
        return;
    }
    match verb {
        Verb::Focus => {
            close(state, tx);
            crate::sidebar::open_agent(state, &entry.id, tx);
        }
        Verb::Seed { id, .. } => open_seed(state, id, tx),
        Verb::ShowWorktree => show_worktree(state, entry.directory, tx),
        Verb::Keep(keep) => keep_conversation(state, entry.id, keep, tx),
        Verb::Reopen(action) => {
            state.window.ledger.keep_notes.remove(&entry.id);
            reopen(state, entry, action, tx)
        }
    }
}

/// Leaves the ledger for a seed's tile.
pub(crate) fn open_seed(state: &mut State, id: String, tx: &mut Tx) {
    close(state, tx);
    state.act(
        |request_id| OpenSeedMessage {
            seed_id: id,
            request_id: Some(request_id),
            ..OpenSeedMessage::new()
        },
        Refused::Prefixed("Could not open the seed"),
        |_, _, _| {},
        tx,
    );
}

fn keep_conversation(state: &mut State, id: String, keep: bool, tx: &mut Tx) {
    state
        .window
        .ledger
        .keep_notes
        .insert(id.clone(), Note::Busy("changing conversation pin…".into()));
    let session_id = format!("session:{id}");
    state.ask(
        |request_id| KeptConversationKeepMessage {
            request_id: Some(request_id),
            session_id,
            keep,
            ..KeptConversationKeepMessage::new()
        },
        move |state, answer, _| {
            let notes = &mut state.window.ledger.keep_notes;
            match answer.outcome() {
                Ok(_) => notes.remove(&id),
                Err(why) => notes.insert(id, Note::Refused(why)),
            };
        },
        tx,
    );
}

/// Runs the row's verb at `index`, its first for Enter.
pub(crate) fn run_nth(state: &mut State, id: &str, index: usize, tx: &mut Tx) {
    let verb = rows(state)
        .iter()
        .find(|r| r.entry.id == id)
        .and_then(|r| r.verbs.get(index).cloned());
    if let Some(verb) = verb {
        run(state, id, verb, tx);
    }
}

fn reopen(state: &mut State, entry: SessionLedgerEntry, action: String, tx: &mut Tx) {
    state
        .window
        .ledger
        .record(&entry, Some(Note::Busy("reopening…".into())), None);
    let session_id = entry.id.clone();
    let asked = action.clone();
    state.ask(
        |request_id| SessionReopenMessage {
            request_id: Some(request_id),
            session_id,
            action: Some(asked),
            ..SessionReopenMessage::new()
        },
        move |state, answer, tx| {
            let reply = answer.parse::<SessionReopenResultMessage>();
            if let Some(result) = reply
                .as_ref()
                .filter(|r| r.success)
                .and_then(|r| r.result.clone())
            {
                state.window.ledger.record(&entry, None, None);
                if is_open(state) {
                    close(state, tx);
                }
                crate::sidebar::open_agent(state, &result.session_id, tx);
                return;
            }
            if let Some(offer) = reply.as_ref().and_then(|r| r.reopen.as_ref()) {
                let verdict = Verdict::of(offer);
                let note = Note::Refused(verdict.refusal(&action));
                state
                    .window
                    .ledger
                    .record(&entry, Some(note), Some(verdict));
                read(state, tx);
                return;
            }
            let why = answer_error(&answer, "Reopening that session failed");
            let held = state
                .facts
                .desktops
                .iter()
                .any(|d| d.panes.iter().any(|p| p.session_id == entry.id));
            if held {
                state.window.notify(why, tx);
                state.window.ledger.record(&entry, None, None);
                if is_open(state) {
                    close(state, tx);
                }
                crate::sidebar::open_agent(state, &entry.id, tx);
                return;
            }
            let note = Note::Refused(query::compact_paths(&why));
            state.window.ledger.record(&entry, Some(note), None);
            read(state, tx);
        },
        tx,
    );
}

/// Copies `text` and says so in the status line.
pub(crate) fn copy(state: &mut State, text: String, tx: &mut Tx) {
    tx.copy(text.clone());
    state.window.ledger.copied = Some(text);
}
