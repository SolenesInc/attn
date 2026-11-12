use attn_protocol::{
    CancelCountdownMessage, Session, SetSessionPriorityMessage, SetSettingMessage,
    SettleTurnMessage, WakeTurnMessage,
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

/// Shows the oldest document that arrived unseen on the current desktop, else opens the turn
/// at the head of the queue, as ⌘J and the "N waiting" head do.
pub(crate) fn jump_to_waiting(state: &mut State, tx: &mut Tx) {
    if crate::desktop::show_unseen(state, tx) {
        return;
    }
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

/// The agent filter's keys: Down and Up walk into the rows from the top or the bottom, and
/// Escape steps back one layer.
pub(crate) fn filter_key(state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        rune::Key::Down | rune::Key::Up if plain(ev) => {
            walk(state, None, ev.key == rune::Key::Down, tx)
        }
        rune::Key::Escape => step_back(state, tx),
        _ => false,
    }
}

/// A queue row's keys: Down and Up walk the rows and wrap at both ends, Return opens the row's
/// agent, a letter or digit types into the open list's filter, and Escape steps back one layer.
pub(crate) fn row_key(state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx) -> bool {
    if !plain(ev) {
        return false;
    }
    match &ev.key {
        rune::Key::Down | rune::Key::Up => {
            let from = tx.focused().map(str::to_owned);
            walk(state, from.as_deref(), ev.key == rune::Key::Down, tx)
        }
        rune::Key::Enter => {
            let from = tx.focused().map(str::to_owned);
            let row = super::queue_view::agent_rows(state)
                .into_iter()
                .find(|row| Some(&row.key) == from.as_ref());
            row.is_some_and(|row| {
                open_agent(state, &row.session, tx);
                true
            })
        }
        rune::Key::Escape if state.window.agent_list_open => step_back(state, tx),
        rune::Key::Text(typed) if state.window.agent_list_open && is_word_char(typed) => {
            let filter = format!("{}{typed}", state.window.agent_filter.text);
            state.window.agent_filter = rune::TextEdit::new(filter);
            tx.focus(super::queue_view::FILTER_FIELD, false);
            true
        }
        _ => false,
    }
}

fn plain(ev: &rune::KeyEvent) -> bool {
    !(ev.mods.cmd || ev.mods.ctrl || ev.mods.alt)
}

fn is_word_char(typed: &str) -> bool {
    let mut chars = typed.chars();
    chars.next().is_some_and(char::is_alphanumeric) && chars.next().is_none()
}

fn step_back(state: &mut State, tx: &mut Tx) -> bool {
    if state.window.agent_filter.text.is_empty() {
        toggle_agent_list(state, tx);
    } else {
        state.window.agent_filter.clear();
        tx.focus(super::queue_view::FILTER_FIELD, false);
    }
    true
}

fn walk(state: &State, from: Option<&str>, down: bool, tx: &mut Tx) -> bool {
    let rows = super::queue_view::agent_rows(state);
    if rows.is_empty() {
        return false;
    }
    let last = rows.len() - 1;
    let at = from.and_then(|from| rows.iter().position(|row| row.key == from));
    let next = match (at, down) {
        (None, true) => 0,
        (None, false) => last,
        (Some(at), true) if at == last => 0,
        (Some(at), true) => at + 1,
        (Some(0), false) => last,
        (Some(at), false) => at - 1,
    };
    tx.focus(rows[next].key.clone(), true);
    true
}

/// The agent the settle, priority and snooze keys act on: the queue row holding focus, or the
/// agent on screen when no row does.
struct Target {
    session: String,
    row: Option<String>,
}

fn target(state: &State, tx: &Tx) -> Option<Target> {
    let focused = tx.focused().unwrap_or_default();
    let row = super::queue_view::agent_rows(state)
        .into_iter()
        .find(|row| {
            focused == row.key
                || focused
                    .strip_prefix(row.key.as_str())
                    .is_some_and(|rest| rest.starts_with('.'))
        });
    match row {
        Some(row) => Some(Target {
            session: row.session,
            row: Some(row.key),
        }),
        None => state
            .window
            .on_screen(&state.facts)
            .map(|session| Target { session, row: None }),
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

/// The settle key: settles the turn of its target. On an agent with no turn to settle, queue
/// flow moves on to the head of the queue, or home when nothing is owed.
pub(crate) fn settle_key(state: &mut State, tx: &mut Tx) {
    let Some(Target { session: id, .. }) = target(state, tx) else {
        return;
    };
    state.tick(tx);
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

/// The snooze key: the snooze menu for its target, under the focused row or in the middle of
/// the window.
pub(crate) fn snooze_key(state: &mut State, tx: &mut Tx) {
    let Some(Target { session: id, row }) = target(state, tx) else {
        return;
    };
    state.tick(tx);
    let now_ms = state.window.now_ms;
    let Some(agent) = session(state, &id) else {
        return;
    };
    if queue::actions(&state.facts, agent, now_ms).snooze {
        let name = super::model::display_name(agent);
        let under = row.map(|row| format!("{row}.snooze"));
        super::snooze::open(state, &id, name, under, tx);
    }
}

/// The cancel-countdown key: stops every countdown on the desktop shown, or, with none
/// running, keeps the next turn of the agent shown from settling on its own.
pub(crate) fn cancel_countdown(state: &State) {
    let on_screen = match state.window.view {
        View::Desktop => crate::desktop::on_screen_sessions(state),
        _ => Vec::new(),
    };
    let counting: Vec<&String> = on_screen
        .iter()
        .filter(|id| {
            session(state, id)
                .is_some_and(|s| super::model::Settling::of(s) != super::model::Settling::Off)
        })
        .collect();
    let shown = state
        .window
        .on_screen(&state.facts)
        .filter(|id| on_screen.contains(id));
    let targets: Vec<String> = if counting.is_empty() {
        shown.into_iter().collect()
    } else {
        counting.into_iter().cloned().collect()
    };
    for session_id in targets {
        let _ = state.daemon.send(&CancelCountdownMessage {
            session_id,
            ..CancelCountdownMessage::new()
        });
    }
}

/// The priority key: puts its target ahead of the other turns, or back among them.
pub(crate) fn priority_key(state: &mut State, tx: &Tx) {
    if let Some(target) = target(state, tx) {
        toggle_priority(state, &target.session);
    }
}

pub(crate) fn toggle_priority(state: &State, id: &str) {
    let Some(priority) = session(state, id).map(|s| s.priority == Some(true)) else {
        return;
    };
    set_priority(state, id, !priority);
}

pub(crate) fn set_priority(state: &State, session: &str, priority: bool) {
    let _ = state.daemon.send(&SetSessionPriorityMessage {
        session_id: session.into(),
        priority,
        ..SetSessionPriorityMessage::new()
    });
}

/// In queue flow, leaving an agent on screen that is closing opens the oldest turn owed by
/// another agent.
pub(crate) fn move_on_from(state: &mut State, leaving: &str, tx: &mut Tx) {
    if !queue_flow(state) {
        return;
    }
    let next = queue::bands(&state.facts, state.window.now_ms)
        .turns
        .iter()
        .map(|row| row.session.id.clone())
        .find(|id| id != leaving);
    if let Some(id) = next {
        open_agent(state, &id, tx);
    }
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
