use attn_protocol::{
    Session, SetSessionPriorityMessage, SetSettingMessage, SettleTurnMessage, WakeTurnMessage,
};
use rune::Tx;

use super::open_agent;
use super::queue::{self, QUEUE_MODE};
use crate::State;
use crate::setting;
use crate::shell::View;

pub(crate) fn queue_flow(state: &State) -> bool {
    setting::on(&state.facts.settings, QUEUE_MODE)
}

/// Switches between queue flow and desktop flow, a daemon setting every profile shares.
pub(crate) fn toggle_flow(state: &mut State, _: &mut Tx) {
    let value = if queue_flow(state) { "false" } else { "true" };
    let _ = state.daemon.send(&SetSettingMessage {
        key: QUEUE_MODE.into(),
        value: value.into(),
        ..SetSettingMessage::new()
    });
}

/// Opens the turn at the head of the queue, as ⌘J and the "N waiting" head do.
pub(crate) fn jump_to_waiting(state: &mut State, tx: &mut Tx) {
    let head = queue::bands(&state.facts, state.window.now_ms)
        .head()
        .map(|s| s.id.clone());
    if let Some(id) = head {
        open_agent(state, &id, tx);
    }
}

pub(crate) fn toggle_agent_list(state: &mut State, tx: &mut Tx) {
    let window = &mut state.window;
    window.agent_list_open = !window.agent_list_open;
    if window.agent_list_open {
        tx.focus(super::queue_view::FILTER_FIELD, false);
    } else {
        window.agent_filter = rune::TextEdit::default();
    }
}

/// ⌘↑ and ⌘↓ in queue flow: the previous or next agent of the queue, wrapping.
pub(crate) fn step(state: &mut State, by: isize, tx: &mut Tx) {
    let on_screen = state.window.on_screen(&state.facts);
    let next = queue::bands(&state.facts, state.window.now_ms)
        .step(on_screen.as_deref(), by)
        .map(|s| s.id.clone());
    if let Some(id) = next {
        open_agent(state, &id, tx);
    }
}

fn session<'a>(state: &'a State, id: &str) -> Option<&'a Session> {
    state.facts.sessions.iter().find(|s| s.id == id)
}

pub(crate) fn settle(state: &State, session: &str) {
    let _ = state.daemon.send(&SettleTurnMessage {
        session_id: session.into(),
        ..SettleTurnMessage::new()
    });
}

pub(crate) fn wake(state: &State, session: &str) {
    let _ = state.daemon.send(&WakeTurnMessage {
        session_id: session.into(),
        ..WakeTurnMessage::new()
    });
}

/// The settle key: settles the turn of the agent on screen. On an agent with no turn to
/// settle, queue flow moves on to the head of the queue, or home when nothing is owed.
pub(crate) fn settle_on_screen(state: &mut State, tx: &mut Tx) {
    let Some(id) = state.window.on_screen(&state.facts) else {
        return;
    };
    let now_ms = state.window.now_ms;
    let Some(agent) = session(state, &id) else {
        return;
    };
    if queue::actions(&state.facts, agent, now_ms).settle {
        return settle(state, &id);
    }
    if !queue_flow(state) {
        return;
    }
    let bands = queue::bands(&state.facts, now_ms);
    if !bands.settled.iter().any(|row| row.session.id == id) {
        return;
    }
    match bands.head().map(|head| head.id.clone()) {
        Some(head) => open_agent(state, &head, tx),
        None => state.window.go_home_following(),
    }
}

/// The priority key: puts the agent on screen ahead of the other turns, or back among them.
pub(crate) fn toggle_priority_on_screen(state: &mut State) {
    let Some(id) = state.window.on_screen(&state.facts) else {
        return;
    };
    let Some(priority) = session(state, &id).map(|s| s.priority == Some(true)) else {
        return;
    };
    let _ = state.daemon.send(&SetSessionPriorityMessage {
        session_id: id,
        priority: !priority,
        ..SetSessionPriorityMessage::new()
    });
}

/// After each change the daemon sends, in queue flow: when the turn of the agent on screen
/// closed without the agent closing it, the window moves to the next turn, or home to follow
/// the next one to open; home following opens a turn as soon as one is owed.
pub(crate) fn advance(state: &mut State, tx: &mut Tx) {
    if !queue_flow(state) {
        state.window.queue_turns.clear();
        return;
    }
    let on_screen = state.window.on_screen(&state.facts);
    let bands = queue::bands(&state.facts, state.window.now_ms);
    let turns = bands.turns.iter().map(|r| r.session.id.clone()).collect();
    let previous = std::mem::replace(&mut state.window.queue_turns, turns);
    let to = match state.window.view {
        View::Home if state.window.follow_next_turn => bands.head().map(|h| Some(h.id.clone())),
        View::Desktop => bands.advance_after_turn_closed(&previous, on_screen.as_deref()),
        View::Home => None,
    };
    match to {
        Some(Some(id)) => open_agent(state, &id, tx),
        Some(None) => state.window.go_home_following(),
        None => {}
    }
}
