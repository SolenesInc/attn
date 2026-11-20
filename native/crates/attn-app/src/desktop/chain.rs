use std::collections::{HashMap, HashSet};

use attn_protocol::{ActorView, Session};
use attn_ui::blocks::{Actions, Block, HeaderPiece, Popover, Prose};
use attn_ui::system::{
    DotShape, DotSize, IconSize, ListRow, Type, dot, icon_button, keys_hint, words,
};
use attn_ui::{Tokens, glyph, type_};
use rune::{Color, El, Key, KeyEvent, Tx, col};
use rune_kit::Placement;

use super::fit::{Level, Widths};
use super::header_pop::{self, Pop, What};
use super::role_icon::Mark;
use crate::State;
use crate::sidebar::model::{AgentState, display_name, harness_label};

/// The header's way into the delegation chain: the session's role, or a plain mark when it
/// has none.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Trigger {
    text: String,
    label: String,
    mark: Mark,
    leads: bool,
}

const MARK_PX: f32 = 14.0;

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

/// The role's ink: the accent while it leads its chain.
fn ink(trigger: &Trigger, t: &Tokens) -> Color {
    if trigger.leads {
        t.accent
    } else {
        t.text_tertiary
    }
}

/// The role at `level` in `ink`, its name keyed `name_key`.
fn face(trigger: &Trigger, name_key: String, level: Level, ink: Color) -> HeaderPiece {
    let mark = trigger.mark.el(MARK_PX, type_::CAPTION, ink);
    let piece = HeaderPiece::new().drawn(mark, trigger.mark.width(MARK_PX, type_::CAPTION));
    if level == Level::Full {
        piece.words_keyed(name_key, trigger.text.clone(), ink, None)
    } else {
        piece
    }
}

/// The role in the header of `leaf`, which shows the delegation chain on hover and pins it on a
/// press.
pub(super) fn piece(leaf: &str, session: &str, trigger: &Trigger, level: Level, t: &Tokens) -> El {
    let pressed = (leaf.to_owned(), session.to_owned());
    let anchor = header_pop::anchor(leaf, What::Chain);
    let role = face(trigger, format!("{anchor}.name"), level, ink(trigger, t))
        .button(anchor, trigger.label.clone(), t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::press(state, &pressed.0, &pressed.1, What::Chain, tx)
        })
        .keeps_press_from_within();
    header_pop::hovers(role, leaf, session, What::Chain)
}

/// The role's width with its name, and as its mark alone.
pub(super) fn widths(trigger: &Trigger) -> Widths {
    let at = |level| face(trigger, String::new(), level, Color::TRANSPARENT).button_width();
    Widths {
        full: at(Level::Full),
        short: None,
        icon: Some(at(Level::Icon)),
    }
}

/// The sidebar row keyed `key` of an agent in a delegation chain, in either flow: its role chip
/// a kept control at the row's end, which pins the chain beside the sidebar.
pub(crate) fn on_row(
    row: attn_ui::system::ListRow,
    key: &str,
    session: &str,
    trigger: &Trigger,
    t: &Tokens,
) -> attn_ui::system::ListRow {
    row.controls(
        [chip(key, session, trigger, t)],
        attn_ui::system::Reveal::Always,
    )
}

fn chip(row: &str, session: &str, trigger: &Trigger, t: &Tokens) -> El {
    let host = header_pop::Host::Row(row.to_owned());
    let pressed = (host.clone(), session.to_owned());
    face(trigger, String::new(), Level::Icon, ink(trigger, t))
        .button(header_pop::row_anchor(row), trigger.label.clone(), t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::press_on(state, pressed.0.clone(), &pressed.1, What::Chain, tx)
        })
}

/// A sidebar row of an agent in a delegation chain, which shows the chain while the pointer
/// rests on it.
pub(crate) fn row_hovers(row_el: El, row: &str, session: &str) -> El {
    header_pop::hovers_on(
        row_el,
        header_pop::Host::Row(row.to_owned()),
        session,
        What::Chain,
    )
}

/// How wide the open chain stands: an agent named after its worktree,
/// "attn--ds-page-blocks-delegation", keeps its name whole in `Name` two levels down.
const CHAIN_W: f32 = 360.0;

/// The open delegation chain, under its role, or beside the sidebar for a sidebar row: its
/// agents as a tree, each with its role and its state, the one on screen marked.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let sessions = &state.facts.sessions;
    sessions.iter().find(|s| s.id == pop.session)?;
    let (rows, earlier) = tree(&pop.session, sessions);
    let picked = state.window.desk.header_pop.picked;
    let surface_key = pop.surface();
    let close = icon_button(
        format!("{surface_key}.close"),
        "Close delegation chain",
        glyph::TIMES,
        IconSize::Large,
    )
    .on_press(|state: &mut State| header_pop::close(state))
    .build(t);
    let agents = col()
        .key(agents_key(pop))
        .role("tree")
        .label("Agents in delegation chain")
        .w_full()
        .children(
            rows.iter()
                .enumerate()
                .map(|(i, (session, depth))| agent_row(pop, i, session, *depth, i == picked, t)),
        );
    let mut blocks = Vec::new();
    blocks.extend(earlier.map(|name| {
        Prose::hint(
            format!("{surface_key}.earlier"),
            format!("Started by {name}, which attn cannot show."),
        )
        .build(t)
    }));
    blocks.push(Block::custom(format!("{surface_key}.tree"), agents));
    let keys = Actions::new("keys")
        .push(keys_hint(["↑", "↓"], "move", t))
        .push(keys_hint(["↵"], "open", t))
        .push(keys_hint(["esc"], "close", t));
    let surface = Popover::new(surface_key, "Delegation chain")
        .aside(close)
        .push_all(blocks)
        .actions(keys)
        .build(t)
        .w(CHAIN_W)
        .active_descendant(row_key(pop, picked))
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        });
    let surface = if pop.pinned {
        surface
            .on_key(chain_key)
            .modal_tx(true, |state: &mut State, _: &mut Tx| {
                header_pop::close(state)
            })
            .modal_press_through(true)
            .modal_wheel_closes(true)
    } else {
        surface
    };
    let (anchor, placement) = match &pop.host {
        header_pop::Host::Tile(_) => (pop.anchor(), Placement::BottomStart),
        header_pop::Host::Row(row) => (row.clone(), Placement::RightStart),
    };
    Some(rune_kit::popover::anchored(
        attn_ui::kit(t),
        surface,
        &anchor,
        placement,
        4.0,
    ))
}

fn agents_key(pop: &Pop) -> String {
    format!("{}.agents", pop.surface())
}

fn body_key(pop: &Pop) -> String {
    format!("{}.body", pop.surface())
}

fn row_key(pop: &Pop, i: usize) -> String {
    format!("{}.agent.{i}", pop.surface())
}

/// Keeps the picked agent of the open chain in view.
pub(crate) fn reveal(pop: &Pop, picked: usize, tx: &mut Tx) {
    tx.scroll_into_view_in(body_key(pop), row_key(pop, picked));
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
    let agent_state = AgentState::parse(&session.state);
    let id = session.id.clone();
    ListRow::new(row_key(pop, i), label)
        .role("treeitem")
        .unfocusable()
        .depth(depth.min(u8::MAX as usize) as u8)
        .lead(dot(
            crate::sidebar::state_color(agent_state, t),
            DotShape::Filled,
            DotSize::Regular,
        ))
        .title(name)
        .subtitle(kind, t)
        .aside(words(agent_state.spoken(), Type::Small, t.text_tertiary))
        .in_view(current)
        .cursor(picked)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| select(state, &id, tx))
        .build(t)
        .level(depth as u32 + 1)
        .focus_selected(picked)
        .on_pointer_move_local(move |state: &mut State, _, tx: &mut Tx| {
            let pop = &mut state.window.desk.header_pop;
            if pop.picked == i {
                tx.live_only();
            } else {
                pop.picked = i;
            }
        })
}

/// Up, Down, Home, End and Enter on a chain shown by hover, which takes them the way a pinned
/// chain does.
pub(crate) fn hover_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let shown = state
        .window
        .desk
        .header_pop
        .open
        .as_ref()
        .is_some_and(|p| !p.pinned && p.what == What::Chain);
    let m = ev.mods;
    shown && !(m.shift || m.cmd || m.alt || m.ctrl || m.logo) && chain_key(state, ev, tx)
}

fn chain_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(session) = state
        .window
        .desk
        .header_pop
        .open
        .as_ref()
        .map(|p| p.session.clone())
    else {
        return false;
    };
    let ids: Vec<String> = tree(&session, &state.facts.sessions)
        .0
        .iter()
        .map(|(s, _)| s.id.clone())
        .collect();
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
