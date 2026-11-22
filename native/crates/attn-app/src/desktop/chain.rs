use std::collections::{HashMap, HashSet};

use attn_client::Facts;
use attn_protocol::{ActorView, Seed, Session, StandbyTender};
use attn_ui::blocks::{Actions, Block, HeaderPiece, Popover, Prose};
use attn_ui::system::{
    DotShape, DotSize, IconSize, ListRow, Type, dot, icon_button, keys_hint, words,
};
use attn_ui::{Tokens, glyph};
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

fn dispatched_by_party(session: &Session) -> bool {
    session
        .dispatcher
        .as_ref()
        .is_some_and(|d| d.r#ref.starts_with("member:") || d.r#ref.starts_with("session:"))
}

/// One agent in a delegation chain: a live session, or a closed one that tends a seed on
/// standby and resumes when mail reaches it.
#[derive(Clone, Copy)]
pub(crate) enum Link<'a> {
    Live(&'a Session),
    Dormant(&'a Seed, &'a StandbyTender),
}

impl<'a> Link<'a> {
    fn id(self) -> &'a str {
        match self {
            Link::Live(session) => &session.id,
            Link::Dormant(_, tender) => &tender.session_id,
        }
    }

    fn dispatcher_id(self) -> Option<&'a str> {
        match self {
            Link::Live(session) => session.dispatcher_session_id.as_deref(),
            Link::Dormant(_, tender) => tender.dispatcher_session_id.as_deref(),
        }
    }

    fn dispatcher(self) -> Option<&'a ActorView> {
        match self {
            Link::Live(session) => session.dispatcher.as_ref(),
            Link::Dormant(_, tender) => tender.dispatcher.as_ref(),
        }
    }
}

/// The seeds on standby whose tender is a closed session.
fn on_standby(facts: &Facts) -> impl Iterator<Item = (&Seed, &StandbyTender)> {
    facts
        .garden
        .of(&facts.profiles.selected)
        .iter()
        .filter_map(|seed| Some((seed.as_ref(), seed.standby.as_ref()?.tender.as_ref()?)))
}

/// Every agent a chain can show: the live sessions, and the closed tenders of seeds on
/// standby.
fn links(facts: &Facts) -> Vec<Link<'_>> {
    let live: HashSet<&str> = facts.sessions.iter().map(|s| s.id.as_str()).collect();
    let dormant = on_standby(facts)
        .filter(|(_, tender)| !live.contains(tender.session_id.as_str()))
        .map(|(seed, tender)| Link::Dormant(seed, tender));
    facts
        .sessions
        .iter()
        .map(Link::Live)
        .chain(dormant)
        .collect()
}

/// Whether `session` sits in a delegation chain: it has a role, a dispatcher or delegates,
/// live or on standby.
pub(crate) fn shown(session: &Session, facts: &Facts) -> bool {
    let dispatches = |dispatcher: Option<&str>| dispatcher == Some(session.id.as_str());
    session.delegation_role.is_some()
        || session.dispatcher_session_id.is_some()
        || dispatched_by_party(session)
        || facts
            .sessions
            .iter()
            .any(|s| dispatches(s.dispatcher_session_id.as_deref()))
        || on_standby(facts).any(|(_, t)| dispatches(t.dispatcher_session_id.as_deref()))
}

pub(crate) fn trigger(session: &Session, facts: &Facts) -> Option<Trigger> {
    if !shown(session, facts) {
        return None;
    }
    let role = session.delegation_role.as_ref().map(|r| r.name.clone());
    let chain = format!(
        "Show delegation chain for {}",
        display_name(session, &facts.harnesses)
    );
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
pub(crate) fn tree<'a>(id: &str, facts: &'a Facts) -> (Vec<(Link<'a>, usize)>, Option<String>) {
    let all = links(facts);
    let Some(current) = all.iter().copied().find(|l| l.id() == id) else {
        return (Vec::new(), None);
    };
    let by_id: HashMap<&str, Link<'a>> = all.iter().map(|l| (l.id(), *l)).collect();
    let mut delegates: HashMap<&str, Vec<Link<'a>>> = HashMap::new();
    for link in &all {
        if let Some(dispatcher) = link.dispatcher_id() {
            delegates.entry(dispatcher).or_default().push(*link);
        }
    }
    let mut ancestors = HashSet::new();
    let mut root = current;
    while let Some(up) = root.dispatcher_id() {
        if ancestors.contains(up) {
            break;
        }
        ancestors.insert(root.id());
        let Some(parent) = by_id.get(up) else { break };
        root = *parent;
    }
    let missing_parent = root.dispatcher_id().filter(|up| !by_id.contains_key(up));
    let earlier = (missing_parent.is_some()
        || (root.dispatcher_id().is_none()
            && matches!(root, Link::Live(s) if dispatched_by_party(s))))
    .then(|| earlier_dispatcher_name(root.dispatcher()));
    let roots = missing_parent
        .and_then(|up| delegates.get(up).cloned())
        .unwrap_or_else(|| vec![root]);
    let mut pending: Vec<(Link<'a>, usize)> = roots.into_iter().rev().map(|l| (l, 0)).collect();
    let mut rows = Vec::new();
    let mut visited = HashSet::new();
    while let Some((link, depth)) = pending.pop() {
        if !visited.insert(link.id()) {
            continue;
        }
        rows.push((link, depth));
        for delegate in delegates.get(link.id()).into_iter().flatten().rev() {
            pending.push((*delegate, depth + 1));
        }
    }
    (rows, earlier)
}

/// Where the chain of `session` starts its pick: on `session` itself.
pub(crate) fn current_row(session: &str, facts: &Facts) -> usize {
    tree(session, facts)
        .0
        .iter()
        .position(|(l, _)| l.id() == session)
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
    let piece = HeaderPiece::new().mark(trigger.mark.glyph(), ink);
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
    state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let (rows, earlier) = tree(&pop.session, &state.facts);
    let now_ms = state.window.now_ms;
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
                .map(|(i, (link, depth))| match link {
                    Link::Live(session) => agent_row(
                        pop,
                        i,
                        session,
                        &state.facts.harnesses,
                        *depth,
                        i == picked,
                        t,
                    ),
                    Link::Dormant(seed, tender) => dormant_row(
                        pop,
                        i,
                        (seed, tender),
                        &state.facts.harnesses,
                        now_ms,
                        *depth,
                        i == picked,
                        t,
                    ),
                }),
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

fn agent_row(
    pop: &Pop,
    i: usize,
    session: &Session,
    harnesses: &[attn_protocol::Harness],
    depth: usize,
    picked: bool,
    t: &Tokens,
) -> El {
    let name = display_name(session, harnesses);
    let kind = session
        .delegation_role
        .as_ref()
        .map(|r| r.name.clone())
        .unwrap_or_else(|| harness_label(harnesses, &session.agent));
    let current = session.id == pop.session;
    let label = format!("{name} · {kind}{}", if current { " · current" } else { "" });
    let agent_state = AgentState::parse(&session.state);
    let id = session.id.clone();
    let row = ListRow::new(row_key(pop, i), label)
        .lead(dot(
            crate::sidebar::state_color(agent_state, t),
            DotShape::Filled,
            DotSize::Regular,
        ))
        .title(name)
        .subtitle(kind, t)
        .aside(words(agent_state.spoken(), Type::Small, t.text_tertiary))
        .in_view(current)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| select(state, &id, tx));
    chain_row(row, i, depth, picked, t)
}

/// The row of a closed agent that tends a seed on standby: its name, "on standby" and what it
/// waits for. Activating it opens the seed.
#[allow(clippy::too_many_arguments)]
fn dormant_row(
    pop: &Pop,
    i: usize,
    (seed, tender): (&Seed, &StandbyTender),
    harnesses: &[attn_protocol::Harness],
    now_ms: i64,
    depth: usize,
    picked: bool,
    t: &Tokens,
) -> El {
    let name = dormant_name(tender, harnesses);
    let kind = std::iter::once("on standby".to_owned())
        .chain(
            crate::garden::model::standby_of(seed, now_ms)
                .map(|s| s.waits())
                .filter(|w| !w.is_empty()),
        )
        .collect::<Vec<_>>()
        .join(" · ");
    let opens = (seed.id.clone(), pop.session.clone());
    let row = ListRow::new(row_key(pop, i), format!("{name} · {kind}"))
        .lead(dot(t.text_muted, DotShape::Hollow, DotSize::Regular))
        .title(name)
        .subtitle(kind, t)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            open_seed(state, &opens.0, &opens.1, tx)
        });
    chain_row(row, i, depth, picked, t)
}

/// How attn names a closed agent on standby.
pub(crate) fn dormant_name(tender: &StandbyTender, harnesses: &[attn_protocol::Harness]) -> String {
    if tender.label.is_empty() {
        harness_label(harnesses, &tender.agent)
    } else {
        tender.label.clone()
    }
}

fn chain_row(row: ListRow, i: usize, depth: usize, picked: bool, t: &Tokens) -> El {
    row.role("treeitem")
        .unfocusable()
        .depth(depth.min(u8::MAX as usize) as u8)
        .cursor(picked)
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
    let picks: Vec<Pick> = tree(&session, &state.facts)
        .0
        .iter()
        .map(|(link, _)| match link {
            Link::Live(s) => Pick::Agent(s.id.clone()),
            Link::Dormant(seed, _) => Pick::Seed(seed.id.clone()),
        })
        .collect();
    let pop = &mut state.window.desk.header_pop;
    let last = picks.len().saturating_sub(1);
    match &ev.key {
        Key::Down => pop.picked = (pop.picked + 1).min(last),
        Key::Up => pop.picked = pop.picked.saturating_sub(1),
        Key::Home => pop.picked = 0,
        Key::End => pop.picked = last,
        Key::Enter => match picks.get(pop.picked.min(last)).cloned() {
            Some(Pick::Agent(id)) => select(state, &id, tx),
            Some(Pick::Seed(id)) => open_seed(state, &id, &session, tx),
            None => {}
        },
        _ => return false,
    }
    if let Some(open) = &state.window.desk.header_pop.open {
        reveal(open, state.window.desk.header_pop.picked, tx);
    }
    true
}

#[derive(Clone)]
enum Pick {
    Agent(String),
    Seed(String),
}

fn open_seed(state: &mut State, seed: &str, session: &str, tx: &mut Tx) {
    header_pop::close(state);
    crate::garden::open_tile_beside(state, seed, Some(session.to_owned()), tx);
}

fn select(state: &mut State, session: &str, tx: &mut Tx) {
    header_pop::close(state);
    crate::sidebar::open_agent(state, session, tx);
}
