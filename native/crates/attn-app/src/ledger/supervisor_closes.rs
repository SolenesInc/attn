//! The way back from a close the attention supervisor made: a notice that names the agent and
//! offers Reopen.

use attn_protocol::SessionLedgerEntry;
use rune::Tx;

use crate::State;
use crate::action::Action;

const SUPERVISOR: &str = "supervisor";

/// Offers Reopen for the newest close the supervisor made in the selected profile since the last
/// one this took in.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let closes = &state.facts.closes;
    let seen = std::mem::replace(&mut state.window.told_closes, closes.seq);
    let profile = &state.facts.profiles.selected;
    let Some(entry) = closes
        .since(seen)
        .unwrap_or(&closes.recent)
        .iter()
        .rev()
        .find(|e| e.closed_by.as_deref() == Some(SUPERVISOR) && &e.profile_id == profile)
    else {
        return;
    };
    let text = format!("attn closed {}", super::title(&state.facts, entry));
    let action = Action::ReopenSession {
        session: entry.id.clone(),
    };
    state.window.notify_offering_for(
        text,
        "Reopen",
        action,
        crate::shell::REOPEN_NOTICE_SECONDS,
        tx,
    );
}

/// Reopens `session`, which the supervisor closed, onto a desktop.
pub(crate) fn reopen(state: &mut State, session: &str, tx: &mut Tx) {
    let entry: Option<SessionLedgerEntry> = state
        .facts
        .closes
        .recent
        .iter()
        .rev()
        .find(|e| e.id == session)
        .cloned();
    if let Some(entry) = entry {
        super::resume(state, entry, "reopen".into(), tx);
    }
}
