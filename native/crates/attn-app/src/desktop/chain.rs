use std::collections::{HashMap, HashSet};

use attn_client::Facts;
use attn_protocol::{ActorView, Seed, Session, StandbyTender};
use attn_ui::Tokens;
use attn_ui::blocks::{Block, HeaderPiece, Inline, Popover, Prose, Stack};
use attn_ui::system::{
    DotShape, DotSize, IconSize, ListRow, Size, Tone, TreeLines, Type, dot, glyph, keys_hint, tag,
    words,
};
use rune::{Color, El, Key, KeyEvent, Tx, col};
use rune_kit::Placement;

use super::agent_card::{
    Card, Go, Link, doing, follow, go_button, goes, open_seed, role_ink, select, who,
};
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

/// What the card of `session` shows: the chain it sits in while it is live, and the agent alone
/// once it closed.
fn shown_rows<'a>(session: &str, state: &'a State) -> (Vec<(Link<'a>, usize)>, Option<String>) {
    let live = state.facts.sessions.iter().any(|s| s.id == session);
    match state.terminals.closed_session(session).filter(|_| !live) {
        Some(entry) => (vec![(Link::Closed(entry), 0)], None),
        None => tree(session, &state.facts),
    }
}

/// Where the chain of `session` starts its pick: on `session` itself.
pub(crate) fn current_row(session: &str, facts: &Facts) -> usize {
    tree(session, facts)
        .0
        .iter()
        .position(|(l, _)| l.id() == session)
        .unwrap_or(0)
}

/// The live agent on row `i` of the chain of `session`.
pub(crate) fn agent_at(session: &str, i: usize, facts: &Facts) -> Option<String> {
    let (rows, _) = tree(session, facts);
    match rows.get(i.min(rows.len().checked_sub(1)?))?.0 {
        Link::Live(session) => Some(session.id.clone()),
        Link::Dormant(..) | Link::Closed(_) => None,
    }
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

/// How wide the open chain stands: a delegate four levels down keeps "attn--native-sidebar"
/// whole beside a "waiting for you" badge and its role.
const CHAIN_W: f32 = 400.0;

/// One agent as the chain shows it: its row in the tree and, while picked, the card's top.
struct Agent<'a> {
    card: Card<'a>,
    depth: usize,
    /// The name less the start it shares with its dispatcher's, as "-terms".
    folded: Option<String>,
    lines: TreeLines,
}

impl Agent<'_> {
    fn shown_name(&self) -> String {
        self.folded
            .clone()
            .unwrap_or_else(|| self.card.name.clone())
    }
}

/// `name` less the start it shares with its dispatcher's name, "-terms" for
/// "attn--glossary-sweep-terms" under "attn--glossary-sweep": a delegate's worktree is named after
/// its dispatcher's, so the end is what tells delegates apart.
fn folded(name: &str, dispatcher: &str) -> Option<String> {
    let rest = name.strip_prefix(dispatcher)?;
    (rest.chars().count() > 1 && rest.starts_with(['-', '_', '/', ' '])).then(|| rest.to_owned())
}

/// The lines of the row at `i` of a tree listed in order at these depths.
fn lines_of(depths: &[usize], i: usize) -> TreeLines {
    let depth = depths[i];
    let later = &depths[i + 1..];
    let goes_on = |level: usize| {
        later
            .iter()
            .take_while(|d| **d >= level)
            .any(|d| *d == level)
    };
    TreeLines {
        through: (1..depth).map(goes_on).collect(),
        more: depth > 0 && goes_on(depth),
    }
}

fn agents<'a>(rows: &[(Link<'a>, usize)], facts: &'a Facts, now_ms: i64) -> Vec<Agent<'a>> {
    let cards: Vec<Card<'a>> = rows
        .iter()
        .map(|(link, _)| Card::of(*link, facts, now_ms))
        .collect();
    let depths: Vec<usize> = rows.iter().map(|(_, depth)| *depth).collect();
    let folds: Vec<Option<String>> = (0..rows.len())
        .map(|i| dispatcher_row(&depths, i).and_then(|up| folded(&cards[i].name, &cards[up].name)))
        .collect();
    cards
        .into_iter()
        .zip(folds)
        .enumerate()
        .map(|(i, (card, folded))| Agent {
            card,
            depth: depths[i],
            folded,
            lines: lines_of(&depths, i),
        })
        .collect()
}

/// The row of the dispatcher of the row at `i`: the nearest row above it one level up.
fn dispatcher_row(depths: &[usize], i: usize) -> Option<usize> {
    (0..i).rev().find(|up| depths[*up] < depths[i])
}

/// The row of the first delegate of the row at `i`: the row under it, one level down.
fn first_delegate_row(depths: &[usize], i: usize) -> Option<usize> {
    depths
        .get(i + 1)
        .filter(|d| **d == depths[i] + 1)
        .map(|_| i + 1)
}

/// The open delegation chain, under its role, or beside the sidebar for a sidebar row: who the
/// picked agent is, what it does and where that leads, over the chain's agents as a tree, each with
/// its state, what it needs from the user and its role.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let (rows, earlier) = shown_rows(&pop.session, state);
    let now_ms = state.window.now_ms;
    let agents = agents(&rows, &state.facts, now_ms);
    let picked = state
        .window
        .desk
        .header_pop
        .picked
        .min(agents.len().checked_sub(1)?);
    let surface_key = pop.surface();
    let lone = agents.len() == 1;
    let tree_el =
        (!lone).then(|| {
            col()
                .key(agents_key(pop))
                .role("tree")
                .label("Agents in delegation chain")
                .w_full()
                .children(agents.iter().enumerate().map(|(i, agent)| {
                    agent_row(pop, i, agent, &state.facts, now_ms, i == picked, t)
                }))
        });
    let mut blocks = Vec::new();
    blocks.extend(earlier.map(|name| {
        Prose::hint(
            format!("{surface_key}.earlier"),
            format!("Started by {name}, which attn cannot show."),
        )
        .build(t)
    }));
    blocks.extend(tree_el.map(|tree| Block::custom(format!("{surface_key}.tree"), tree)));
    let need = agents.iter().filter(|a| a.card.needs.is_some()).count();
    let need = (need > 0).then(|| {
        words(format!("{need} need you"), Type::Small, t.state_waiting)
            .single_line()
            .key(format!("{surface_key}.needs"))
    });
    let enter = match opens(agents[picked].card.link, state) {
        Some(Pick::Go(Go::Resume { action, .. })) => {
            Some(super::agent_card::resume_said(&action).to_lowercase())
        }
        Some(Pick::Go(_) | Pick::Agent(_) | Pick::Seed(_)) => Some("open".to_owned()),
        None => None,
    };
    let moves = (!lone)
        .then(|| keys_hint(["↑", "↓", "←", "→"], "move", t))
        .into_iter()
        .chain(enter.map(|said| keys_hint(["↵"], said, t)));
    let surface = Popover::new(surface_key.clone(), "Delegation chain")
        .sectioned()
        .holding(pop.pinned)
        .about(Block::custom(
            details_key(pop),
            details(pop, &agents, picked, state, t),
        ))
        .push_all(blocks);
    let surface = match header_pop::foot(state, pop, moves, need, t) {
        Some(foot) => surface.actions(foot),
        None => surface,
    };
    let surface = surface
        .build(t)
        .w(CHAIN_W)
        .active_descendant(row_key(pop, picked))
        .on_hover_change(|state: &mut State, inside, tx: &mut Tx| {
            header_pop::hover_popover(state, inside, tx)
        });
    let surface = if pop.pinned {
        surface
            .on_key(pinned_key)
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
        header_pop::Host::Mark(_) => (pop.anchor(), Placement::BottomStart),
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

fn details_key(pop: &Pop) -> String {
    format!("{}.picked", pop.surface())
}

/// Keeps the picked agent of the open chain in view.
pub(crate) fn reveal(pop: &Pop, picked: usize, tx: &mut Tx) {
    tx.scroll_into_view_in(body_key(pop), row_key(pop, picked));
}

/// The role's mark on a row, and the room of one where the agent has no role, so what stands
/// beside it lines up.
pub(crate) fn role_slot(role: Option<&attn_protocol::SessionDelegationRole>, t: &Tokens) -> El {
    match role {
        Some(role) => glyph(
            Mark::of(Some(role)).glyph(),
            IconSize::Small,
            role_ink(role, t),
        ),
        None => glyph(Mark::Chain.glyph(), IconSize::Small, Color::TRANSPARENT),
    }
}

/// An agent's name on its row, the picked one's bold, a closed one's quiet, cut in the middle.
fn row_name(key: &str, agent: &Agent<'_>, picked: bool, t: &Tokens) -> El {
    let (kind, ink) = match (picked, agent.card.link) {
        (true, _) => (Type::Name, t.text_primary),
        (false, Link::Live(_)) => (Type::Body, t.text_primary),
        (false, Link::Dormant(..) | Link::Closed(_)) => (Type::Body, t.text_secondary),
    };
    words(agent.shown_name(), kind, ink)
        .key(format!("{key}.title"))
        .single_line()
        .truncate_middle()
        .shrink()
}

fn agent_row(
    pop: &Pop,
    i: usize,
    agent: &Agent<'_>,
    facts: &Facts,
    now_ms: i64,
    picked: bool,
    t: &Tokens,
) -> El {
    let key = row_key(pop, i);
    let row = match agent.card.link {
        Link::Live(session) => {
            let kind = session
                .delegation_role
                .as_ref()
                .map(|r| r.name.clone())
                .unwrap_or_else(|| harness_label(&facts.harnesses, &session.agent));
            let current = session.id == pop.session;
            let label = format!(
                "{} · {kind}{}",
                agent.card.name,
                if current { " · current" } else { "" }
            );
            let ink = crate::sidebar::state_color(AgentState::parse(&session.state), t);
            let id = session.id.clone();
            let row = ListRow::new(key.clone(), label)
                .lead(dot(ink, DotShape::Filled, DotSize::Regular))
                .title_el(row_name(&key, agent, picked, t));
            let row = if current && !picked {
                row.aside(tag(format!("{key}.here"), "here", Tone::Accent, t))
            } else {
                row
            };
            let row = match &agent.card.needs {
                Some(needs) => row.aside(needs.badge(format!("{key}.needs"), t)),
                None => row,
            };
            row.aside(role_slot(session.delegation_role.as_ref(), t))
                .on_press_tx(move |state: &mut State, tx: &mut Tx| select(state, &id, tx))
        }
        Link::Dormant(seed, _) => {
            let standby = crate::garden::model::standby_of(seed, now_ms);
            let waits = standby
                .as_ref()
                .map(|s| s.waits())
                .filter(|w| !w.is_empty());
            let kind = std::iter::once("on standby".to_owned())
                .chain(waits)
                .collect::<Vec<_>>()
                .join(" · ");
            let blocked = standby.is_some_and(|s| !s.resume_blocked.is_empty());
            let said = if blocked {
                words("resume blocked", Type::Small, t.warning)
            } else {
                words("standby", Type::Small, t.text_tertiary)
            };
            let opens = (seed.id.clone(), pop.beside().to_owned());
            ListRow::new(key.clone(), format!("{} · {kind}", agent.card.name))
                .lead(dot(t.text_muted, DotShape::Hollow, DotSize::Regular))
                .title_el(row_name(&key, agent, picked, t))
                .aside(said.single_line())
                .aside(role_slot(None, t))
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    open_seed(state, &opens.0, &opens.1, tx)
                })
        }
        Link::Closed(_) => ListRow::new(key.clone(), agent.card.name.clone())
            .lead(dot(t.text_muted, DotShape::Hollow, DotSize::Regular))
            .title_el(row_name(&key, agent, picked, t)),
    };
    row.role("treeitem")
        .unfocusable()
        .depth(agent.depth.min(u8::MAX as usize) as u8)
        .tree_lines(agent.lines.clone())
        .cursor(picked)
        .chosen(picked)
        .tint(picked.then_some(t.accent))
        .build(t)
        .level(agent.depth as u32 + 1)
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

/// The picked agent: who it is, what it is doing, and a button for each of its seed, its pull
/// request and its oldest open ask. Each part keeps the room the chain's agents need at most, so
/// the tree under it holds still while the pick moves.
fn details(pop: &Pop, agents: &[Agent<'_>], picked: usize, state: &State, t: &Tokens) -> El {
    let key = details_key(pop);
    let facts = &state.facts;
    let agent = &agents[picked];
    let others = || agents.iter().enumerate().filter(move |(i, _)| *i != picked);
    let goes_of = |agent: &Agent<'_>| goes(agent.card.link, state);
    let buttons = goes_of(agent)
        .into_iter()
        .map(|go| {
            go_button(
                format!("{key}.{}", go.letter()),
                &go,
                agent.card.link,
                pop.pinned,
                facts,
                pop.beside(),
                t,
            )
        })
        .collect::<Vec<_>>();
    let shown_doing = doing(&agent.card, state, t);
    let room_doing: Vec<El> = others()
        .filter_map(|(_, a)| doing(&a.card, state, t))
        .collect();
    let acts = shown_doing.is_some() || !room_doing.is_empty();
    let leads = agents.iter().any(|a| !goes_of(a).is_empty());
    Stack::loose()
        .w_full()
        .min_w(0.0)
        .child(Stack::steady(
            who(Some(&key), &agent.card, state, t),
            others().map(|(_, a)| who(None, &a.card, state, t)),
        ))
        .children(acts.then(|| {
            let shown = shown_doing.map(|d| d.key(format!("{key}.doing")));
            Stack::steady(shown.unwrap_or_else(rune::el), room_doing)
        }))
        .children(leads.then(|| {
            Inline::joined()
                .key(format!("{key}.goes"))
                .w_full()
                .h(Size::Compact.height())
                .children(buttons)
        }))
}

/// What a key reaches from one row of the chain.
struct Target {
    depth: usize,
    opens: Option<Pick>,
    goes: Vec<Go>,
}

/// What Enter does on an agent's row: opens the agent, opens the seed of one on standby, or
/// resumes a closed one.
fn opens(link: Link<'_>, state: &State) -> Option<Pick> {
    match link {
        Link::Live(session) => Some(Pick::Agent(session.id.clone())),
        Link::Dormant(seed, _) => Some(Pick::Seed(seed.id.clone())),
        Link::Closed(_) => goes(link, state)
            .into_iter()
            .find(|go| matches!(go, Go::Resume { .. }))
            .map(Pick::Go),
    }
}

fn pinned_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if header_pop::switch_key(state, ev, tx) {
        return true;
    }
    let Some(open) = state.window.desk.header_pop.open.as_ref() else {
        return false;
    };
    let beside = open.beside().to_owned();
    let targets: Vec<Target> = shown_rows(&open.session, state)
        .0
        .iter()
        .map(|(link, depth)| Target {
            depth: *depth,
            opens: opens(*link, state),
            goes: goes(*link, state),
        })
        .collect();
    let depths: Vec<usize> = targets.iter().map(|t| t.depth).collect();
    let pop = &mut state.window.desk.header_pop;
    let last = targets.len().saturating_sub(1);
    let at = pop.picked.min(last);
    let plain = ev.mods == rune::Mods::default();
    match &ev.key {
        Key::Down => pop.picked = (at + 1).min(last),
        Key::Up => pop.picked = at.saturating_sub(1),
        Key::Home => pop.picked = 0,
        Key::End => pop.picked = last,
        Key::Left => pop.picked = dispatcher_row(&depths, at).unwrap_or(at),
        Key::Right => pop.picked = first_delegate_row(&depths, at).unwrap_or(at),
        Key::Enter => {
            match targets.get(at).and_then(|t| t.opens.clone()) {
                Some(Pick::Agent(id)) => select(state, &id, tx),
                Some(Pick::Seed(id)) => open_seed(state, &id, &beside, tx),
                Some(Pick::Go(go)) => follow(state, &go, &beside, tx),
                None => {}
            }
            return true;
        }
        Key::Text(c) if plain => {
            let Some(go) = targets
                .get(at)
                .and_then(|t| t.goes.iter().find(|g| g.letter() == c.as_str()))
                .cloned()
            else {
                return false;
            };
            follow(state, &go, &beside, tx);
            return true;
        }
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
    Go(Go),
}
