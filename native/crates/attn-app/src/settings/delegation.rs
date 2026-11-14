//! Settings → Delegation: the roles table agents read with `attn delegate roles`, the model each
//! role runs on, the fallback for work no role fits, and the switch that routes delegations
//! through it.

mod table;
mod view;

use std::collections::BTreeMap;

use attn_protocol::{DelegationPreferences, DelegationPreferencesSaveMessage};
use rune::{TextEdit, Tx};

use super::harnesses;
use super::sections::Section;
use crate::State;
pub(super) use view::{overlay, page, pill};

/// The delegation preferences as the page edits them, and the saves that keep them.
#[derive(Default)]
pub(crate) struct Delegation {
    /// The table the page shows: the last confirmed one with the edits made since.
    preferences: Option<DelegationPreferences>,
    /// The table the daemon last confirmed.
    confirmed: Option<DelegationPreferences>,
    revision: i64,
    /// Rises with each edit and each table that changed elsewhere, so an undo offered before
    /// either is withdrawn.
    generation: u64,
    error: String,
    /// The latest edit, waiting for the save in flight.
    pending: Option<Pending>,
    flight: Option<Flight>,
    /// A reload asked for during a save, run once the save drains.
    deferred: bool,
    /// Reads sent at or before this one answer with a table older than the edits since.
    floor: u64,
    undo: Option<Undo>,
    expanded: Option<String>,
    expanded_alt: Option<String>,
    /// Where each maintained role goes, by its built-in kind: `NEW` or the role it replaces.
    adoption: Option<BTreeMap<String, String>>,
    adoption_open: Option<String>,
    /// The text of each field while the focus is in it.
    drafts: BTreeMap<String, Draft>,
}

struct Pending {
    value: DelegationPreferences,
    install_workflow_skill: bool,
}

enum Flight {
    Saving,
    /// The daemon refused a save with this reason, and the table is being read again.
    Recovering(String),
}

struct Undo {
    label: String,
    previous: DelegationPreferences,
    generation: u64,
}

/// A field's text from its first edit. The runtime also edits a field it moves the focus
/// past, so only a draft whose field held the focus is kept when the focus leaves it.
struct Draft {
    edit: TextEdit,
    field: table::Field,
    held_focus: bool,
}

fn page_state(state: &State) -> &Delegation {
    &state.window.settings.delegation
}

fn page_state_mut(state: &mut State) -> &mut Delegation {
    &mut state.window.settings.delegation
}

/// Whether a save is out or waiting: a read then would carry a table older than the edits.
pub(super) fn busy(state: &State) -> bool {
    let d = page_state(state);
    d.flight.is_some() || d.pending.is_some()
}

/// Shows the page afresh, reading the table again when one was read before.
pub(super) fn arrive(state: &mut State, tx: &mut Tx) {
    if state.window.settings.section != Section::Delegation {
        return;
    }
    let d = page_state_mut(state);
    d.undo = None;
    d.expanded = None;
    d.expanded_alt = None;
    d.adoption = None;
    d.adoption_open = None;
    d.drafts.clear();
    if d.preferences.is_some() {
        reload(state, tx);
    }
}

/// Reads the table again, once the save in flight drains.
pub(super) fn reload(state: &mut State, tx: &mut Tx) {
    let d = page_state_mut(state);
    if d.flight.is_some() {
        d.deferred = true;
        return;
    }
    d.error.clear();
    fetch(state, tx);
}

fn fetch(state: &mut State, tx: &mut Tx) {
    let floor = page_state(state).floor;
    if harnesses::reading(state).is_some_and(|epoch| epoch > floor) {
        return;
    }
    harnesses::read(state, tx);
}

/// Takes the table a read answered with, unless an edit was made after the read was sent.
pub(super) fn fetched(
    state: &mut State,
    epoch: u64,
    read: Result<DelegationPreferences, String>,
    tx: &mut Tx,
) {
    let d = page_state_mut(state);
    if epoch <= d.floor {
        return;
    }
    match read {
        Ok(preferences) => {
            if preferences.revision != d.revision {
                d.generation += 1;
            }
            d.apply(preferences);
        }
        Err(why) => d.error = why,
    }
    if let Some(Flight::Recovering(refusal)) = &d.flight {
        let refusal = refusal.clone();
        if d.pending.take().is_some() {
            d.error = refusal;
        }
        land(state, tx);
    }
}

impl Delegation {
    fn confirm(&mut self, preferences: DelegationPreferences) {
        self.revision = preferences.revision;
        self.confirmed = Some(preferences);
    }

    fn apply(&mut self, preferences: DelegationPreferences) {
        self.preferences = Some(preferences.clone());
        self.confirm(preferences);
    }

    fn live_undo(&self) -> Option<&Undo> {
        self.undo
            .as_ref()
            .filter(|undo| undo.generation == self.generation)
    }
}

/// Shows `value` at once and saves it after the save in flight; edits made meanwhile collapse
/// into one follow-up, which keeps an install of the workflow skill any of them asked for.
fn persist(state: &mut State, value: DelegationPreferences, install: bool, tx: &mut Tx) {
    let d = page_state_mut(state);
    d.preferences = Some(value.clone());
    d.error.clear();
    d.generation += 1;
    let install_workflow_skill =
        install || d.pending.as_ref().is_some_and(|p| p.install_workflow_skill);
    d.pending = Some(Pending {
        value,
        install_workflow_skill,
    });
    if d.flight.is_some() {
        return;
    }
    let floor = harnesses::epoch(state);
    let d = page_state_mut(state);
    d.floor = floor;
    d.flight = Some(Flight::Saving);
    drain(state, tx);
}

fn drain(state: &mut State, tx: &mut Tx) {
    let d = page_state_mut(state);
    let Some(Pending {
        mut value,
        install_workflow_skill,
    }) = d.pending.take()
    else {
        land(state, tx);
        return;
    };
    value.revision = d.revision;
    state.ask(
        move |request_id| DelegationPreferencesSaveMessage {
            request_id,
            preferences: value,
            install_workflow_skill: install_workflow_skill.then_some(true),
            ..DelegationPreferencesSaveMessage::new()
        },
        |state, answer, tx: &mut Tx| {
            let saved = answer.outcome().and_then(|reply| {
                harnesses::answered(&reply, "Saving delegation preferences failed")
            });
            match saved {
                Ok(read) => {
                    let preferences = read.preferences.clone();
                    harnesses::remember(state, read);
                    let d = page_state_mut(state);
                    if d.pending.is_some() {
                        d.confirm(preferences);
                    } else {
                        d.apply(preferences);
                    }
                    drain(state, tx);
                }
                Err(why) => {
                    let d = page_state_mut(state);
                    d.pending = None;
                    d.error = why.clone();
                    if let Some(confirmed) = d.confirmed.clone() {
                        d.preferences = Some(confirmed);
                    }
                    d.generation += 1;
                    d.flight = Some(Flight::Recovering(why));
                    fetch(state, tx);
                }
            }
        },
        tx,
    );
}

/// The saves drained: a reload asked for meanwhile runs now.
fn land(state: &mut State, tx: &mut Tx) {
    harnesses::caught_up(state);
    super::invalidate(state);
    let d = page_state_mut(state);
    d.flight = None;
    if std::mem::take(&mut d.deferred) {
        fetch(state, tx);
    }
}

fn commit(state: &mut State, next: DelegationPreferences, tx: &mut Tx) {
    persist(state, next, false, tx);
}

/// Saves `next` and offers to put back the table as it was, as `label` says.
fn commit_undoable(state: &mut State, next: DelegationPreferences, label: String, tx: &mut Tx) {
    let Some(previous) = page_state(state).preferences.clone() else {
        return;
    };
    persist(state, next, false, tx);
    let d = page_state_mut(state);
    d.undo = Some(Undo {
        label,
        previous,
        generation: d.generation,
    });
}

fn undo(state: &mut State, tx: &mut Tx) {
    let Some(previous) = page_state_mut(state).undo.take().map(|u| u.previous) else {
        return;
    };
    commit(state, previous, tx);
}

/// Commits each field the focus left; whether one changed the table.
pub(super) fn poll(state: &mut State, tx: &mut Tx) -> bool {
    if page_state(state).drafts.is_empty() {
        return false;
    }
    let focused = tx.focused().map(str::to_owned);
    let d = page_state_mut(state);
    if let Some(draft) = focused.as_ref().and_then(|key| d.drafts.get_mut(key)) {
        draft.held_focus = true;
    }
    d.drafts
        .retain(|key, draft| draft.held_focus || Some(key) == focused.as_ref());
    let left: Vec<String> = d
        .drafts
        .keys()
        .filter(|key| Some(*key) != focused.as_ref())
        .cloned()
        .collect();
    let mut changed = false;
    for key in left {
        changed |= commit_draft(state, &key, tx);
    }
    changed
}

fn commit_draft(state: &mut State, key: &str, tx: &mut Tx) -> bool {
    let Some(draft) = page_state_mut(state).drafts.remove(key) else {
        return false;
    };
    let Some(preferences) = page_state(state).preferences.clone() else {
        return false;
    };
    match draft.field.commit(&preferences, &draft.edit.text) {
        Some(next) => {
            commit(state, next, tx);
            true
        }
        None => false,
    }
}

/// The text a field edits, a draft of it from the first edit until the focus leaves.
fn draft_mut<'a>(state: &'a mut State, field: &table::Field) -> &'a mut TextEdit {
    let key = field.key();
    let value = page_state(state)
        .preferences
        .as_ref()
        .map(|p| field.value(p))
        .unwrap_or_default();
    &mut page_state_mut(state)
        .drafts
        .entry(key)
        .or_insert_with(|| Draft {
            edit: TextEdit::new(value),
            field: field.clone(),
            held_focus: false,
        })
        .edit
}

/// The harnesses the page's routes run on whose models were discovered before: a new
/// executable of one is discovered again.
pub(super) fn wanted(state: &State) -> Vec<(String, bool)> {
    let Some(preferences) = &page_state(state).preferences else {
        return Vec::new();
    };
    let mut wanted: Vec<(String, bool)> = Vec::new();
    for harness in table::harnesses_routed(preferences) {
        if super::catalogs::asked(state, &harness) && !wanted.iter().any(|(h, _)| *h == harness) {
            wanted.push((harness, true));
        }
    }
    wanted
}
