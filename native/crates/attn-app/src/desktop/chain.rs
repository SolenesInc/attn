use std::collections::{HashMap, HashSet};

use attn_protocol::{ActorView, Session};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Key, KeyEvent, Side, TextStyle, Tx, col, el, row, text};

use super::fit::{Level, Widths, text_w};
use super::header_pop::{self, Pop, What};
use super::role_icon::Mark;
use crate::State;
use crate::sidebar::model::{AgentState, display_name, harness_label};

/// The header's way into the delegation chain: the session's role, or a plain mark when it
/// has none.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Trigger {
    text: String,
    label: String,
    mark: Mark,
    leads: bool,
}

const MARK_PX: f32 = 14.0;
const MARK_GAP: f32 = 5.0;
const TRIGGER_PAD: f32 = 4.0;

fn dispatched_by_party(session: &Session) -> bool {
    session
        .dispatcher
        .as_ref()
        .is_some_and(|d| d.r#ref.starts_with("member:") || d.r#ref.starts_with("session:"))
}

/// Whether `session` sits in a delegation chain: it has a role, a dispatcher or delegates.
pub(crate) fn shown(session: &Session, sessions: &[Session]) -> bool {
    session.delegation_role.is_some()
        || session.dispatcher_session_id.is_some()
        || dispatched_by_party(session)
        || sessions
            .iter()
            .any(|s| s.dispatcher_session_id.as_deref() == Some(session.id.as_str()))
}

pub(crate) fn trigger(session: &Session, sessions: &[Session]) -> Option<Trigger> {
    if !shown(session, sessions) {
        return None;
    }
    let role = session.delegation_role.as_ref().map(|r| r.name.clone());
    let chain = format!("Show delegation chain for {}", display_name(session));
    let role_of = session.delegation_role.as_ref();
    Some(Trigger {
        label: match &role {
            Some(role) => format!("{role} · {chain}"),
            None => chain,
        },
        text: role.unwrap_or_else(|| "Delegation chain".into()),
        mark: Mark::of(role_of),
        leads: role_of.and_then(|r| r.builtin.as_deref()) == Some("orchestrator"),
    })
}

fn earlier_dispatcher_name(dispatcher: Option<&ActorView>) -> String {
    match dispatcher {
        Some(d) if d.r#ref.starts_with("member:") && !d.name.trim().is_empty() => {
            d.name.trim().to_owned()
        }
        _ => "Earlier session".into(),
    }
}

/// The chain `id` sits in, from its first dispatcher on screen down through every delegate in
/// order, each with its depth, and the name of a dispatcher above them that attn cannot show.
pub(crate) fn tree<'a>(
    id: &str,
    sessions: &'a [Session],
) -> (Vec<(&'a Session, usize)>, Option<String>) {
    let Some(current) = sessions.iter().find(|s| s.id == id) else {
        return (Vec::new(), None);
    };
    let by_id: HashMap<&str, &Session> = sessions.iter().map(|s| (s.id.as_str(), s)).collect();
    let mut delegates: HashMap<&str, Vec<&Session>> = HashMap::new();
    for session in sessions {
        if let Some(dispatcher) = session.dispatcher_session_id.as_deref() {
            delegates.entry(dispatcher).or_default().push(session);
        }
    }
    let mut ancestors = HashSet::new();
    let mut root = current;
    while let Some(up) = root.dispatcher_session_id.as_deref() {
        if ancestors.contains(up) {
            break;
        }
        ancestors.insert(root.id.as_str());
        let Some(parent) = by_id.get(up) else { break };
        root = parent;
    }
    let missing_parent = root
        .dispatcher_session_id
        .as_deref()
        .filter(|up| !by_id.contains_key(up));
    let earlier = (missing_parent.is_some()
        || (root.dispatcher_session_id.is_none() && dispatched_by_party(root)))
    .then(|| earlier_dispatcher_name(root.dispatcher.as_ref()));
    let roots = missing_parent
        .and_then(|up| delegates.get(up).cloned())
        .unwrap_or_else(|| vec![root]);
    let mut pending: Vec<(&Session, usize)> = roots.into_iter().rev().map(|s| (s, 0)).collect();
    let mut rows = Vec::new();
    let mut visited = HashSet::new();
    while let Some((session, depth)) = pending.pop() {
        if !visited.insert(session.id.as_str()) {
            continue;
        }
        rows.push((session, depth));
        for delegate in delegates
            .get(session.id.as_str())
            .into_iter()
            .flatten()
            .rev()
        {
            pending.push((delegate, depth + 1));
        }
    }
    (rows, earlier)
}

/// Where the chain of `session` starts its pick: on `session` itself.
pub(crate) fn current_row(session: &str, sessions: &[Session]) -> usize {
    tree(session, sessions)
        .0
        .iter()
        .position(|(s, _)| s.id == session)
        .unwrap_or(0)
}

/// The role in the header of `leaf`, which shows the delegation chain on hover and pins it on a
/// press.
pub(crate) fn piece(
    leaf: &str,
    session: &str,
    trigger: &Trigger,
    level: Level,
    font: TextStyle,
    t: &Tokens,
) -> El {
    let pressed = (leaf.to_owned(), session.to_owned());
    let color = if trigger.leads {
        t.accent
    } else {
        t.text_tertiary
    };
    let label = (level == Level::Full).then(|| {
        text(trigger.text.clone())
            .font(font)
            .color(color)
            .single_line()
    });
    let role = row()
        .key(header_pop::anchor(leaf, What::Chain))
        .role("button")
        .label(trigger.label.clone())
        .items_center()
        .gap(MARK_GAP)
        .px(TRIGGER_PAD)
        .child(trigger.mark.el(MARK_PX, font, color))
        .children(label)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::press(state, &pressed.0, &pressed.1, What::Chain, tx)
        });
    header_pop::hovers(role, leaf, session, What::Chain)
}

/// The role's width with its name, and as its mark alone.
pub(super) fn widths(trigger: &Trigger, font: TextStyle) -> Widths {
    let mark = trigger.mark.width(MARK_PX, font);
    Widths {
        full: TRIGGER_PAD * 2.0 + mark + MARK_GAP + text_w(&trigger.text, font),
        short: None,
        icon: Some(TRIGGER_PAD * 2.0 + mark),
    }
}

/// The open delegation chain, under its role.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let sessions = &state.facts.sessions;
    let current = sessions.iter().find(|s| s.id == pop.session)?;
    let (rows, earlier) = tree(&pop.session, sessions);
    let picked = state.window.desk.header_pop.picked;
    let close = text("×")
        .key(format!("{}.close", pop.surface()))
        .role("button")
        .label("Close delegation chain")
        .font(type_::LABEL)
        .color(t.text_tertiary)
        .on_press(|state: &mut State| header_pop::close(state));
    let heading = row()
        .w_full()
        .items_center()
        .child(
            text(display_name(current))
                .font(type_::LABEL)
                .color(t.text_primary)
                .truncate()
                .grow(1.0),
        )
        .child(close);
    let ids: Vec<String> = rows.iter().map(|(s, _)| s.id.clone()).collect();
    let agents = col()
        .key(agents_key(pop))
        .role("tree")
        .label("Agents in delegation chain")
        .w_full()
        .scroll(rune::ScrollSpec {
            stick_bottom: false,
        })
        .shrink()
        .min_h(0.0)
        .gap(2.0)
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, (session, depth))| agent_row(pop, i, session, *depth, i == picked, t)),
        );
    let surface = col()
        .key(pop.surface())
        .role("dialog")
        .label("Delegation chain")
        .w(300.0)
        .padding(space::S2)
        .gap(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .min_h(0.0)
        .focusable(true)
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        })
        .child(heading)
        .child(
            text("Delegation chain")
                .font(type_::LABEL)
                .color(t.text_tertiary),
        )
        .children(earlier.map(|name| {
            text(format!("↑ {name} · unavailable"))
                .font(type_::LABEL)
                .color(t.text_muted)
        }))
        .child(agents)
        .child(
            text("↑ ↓ navigate · ↵ open · esc close")
                .font(type_::LABEL)
                .color(t.text_muted),
        );
    let surface = if pop.pinned {
        surface
            .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                chain_key(state, &ids, ev, tx)
            })
            .modal_tx(true, |state: &mut State, _: &mut Tx| {
                header_pop::close(state)
            })
            .modal_press_through(true)
    } else {
        surface
    };
    Some(surface.anchored_fit(pop.anchor(), Side::Bottom, rune::Align::Start, 4.0))
}

fn agents_key(pop: &Pop) -> String {
    format!("{}.agents", pop.surface())
}

fn row_key(pop: &Pop, i: usize) -> String {
    format!("{}.agent.{i}", pop.surface())
}

/// Keeps the picked agent of the open chain in view.
pub(crate) fn reveal(pop: &Pop, picked: usize, tx: &mut Tx) {
    tx.scroll_into_view_in(agents_key(pop), row_key(pop, picked));
}

fn agent_row(pop: &Pop, i: usize, session: &Session, depth: usize, picked: bool, t: &Tokens) -> El {
    let name = display_name(session);
    let kind = session
        .delegation_role
        .as_ref()
        .map(|r| r.name.clone())
        .unwrap_or_else(|| harness_label(&session.agent));
    let current = session.id == pop.session;
    let label = format!("{name} · {kind}{}", if current { " · current" } else { "" });
    let id = session.id.clone();
    row()
        .key(row_key(pop, i))
        .role("treeitem")
        .label(label)
        .focus_selected(picked)
        .level(depth as u32 + 1)
        .w_full()
        .items_center()
        .gap(space::S2)
        .pl(depth as f32 * 14.0)
        .py(2.0)
        .radius(4.0)
        .fill(if picked { t.bg_selected } else { t.bg_elevated })
        .child(
            text(if depth > 0 { "↳" } else { "" })
                .font(type_::LABEL)
                .color(t.text_muted)
                .w(10.0),
        )
        .child(
            col()
                .grow(1.0)
                .child(
                    text(name)
                        .font(type_::LABEL)
                        .color(t.text_primary)
                        .truncate(),
                )
                .child(text(kind).font(type_::LABEL).color(t.text_tertiary)),
        )
        .children(current.then(|| text("current").font(type_::LABEL).color(t.text_tertiary)))
        .child(
            el().w(7.0)
                .h(7.0)
                .radius(3.5)
                .fill(crate::sidebar::state_color(
                    AgentState::parse(&session.state),
                    t,
                )),
        )
        .on_pointer_move_local(move |state: &mut State, _, tx: &mut Tx| {
            let pop = &mut state.window.desk.header_pop;
            if pop.picked == i {
                tx.live_only();
            } else {
                pop.picked = i;
            }
        })
        .on_press_tx(move |state: &mut State, tx: &mut Tx| select(state, &id, tx))
}

fn chain_key(state: &mut State, ids: &[String], ev: &KeyEvent, tx: &mut Tx) -> bool {
    let pop = &mut state.window.desk.header_pop;
    let last = ids.len().saturating_sub(1);
    match &ev.key {
        Key::Down => pop.picked = (pop.picked + 1).min(last),
        Key::Up => pop.picked = pop.picked.saturating_sub(1),
        Key::Home => pop.picked = 0,
        Key::End => pop.picked = last,
        Key::Enter => {
            if let Some(id) = ids.get(pop.picked.min(last)).cloned() {
                select(state, &id, tx);
            }
        }
        _ => return false,
    }
    if let Some(open) = &state.window.desk.header_pop.open {
        reveal(open, state.window.desk.header_pop.picked, tx);
    }
    true
}

fn select(state: &mut State, session: &str, tx: &mut Tx) {
    header_pop::close(state);
    crate::sidebar::open_agent(state, session, tx);
}
