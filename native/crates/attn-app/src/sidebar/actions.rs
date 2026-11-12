use attn_protocol::SetSettingMessage;
use rune::Tx;

use super::open_agent;
use super::queue::{self, QUEUE_MODE};
use crate::State;
use crate::setting;

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
