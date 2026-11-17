use std::collections::{HashMap, HashSet};

use attn_client::Facts;
use attn_protocol::{ActorView, Seed, Session, StandbyTender};
use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens};
use rune::tok::space;
use rune::{El, Key, KeyEvent, TextStyle, Tx, col, el, row, text};
use rune_kit::{Button, ButtonVariant, Placement};

use super::fit::{Level, Widths, text_w};
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
const MARK_GAP: f32 = 5.0;
const TRIGGER_PAD: f32 = 4.0;

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

/// Every agent a chain can show: the live sessions, and the closed tenders of seeds on
/// standby.
fn links(facts: &Facts) -> Vec<Link<'_>> {
    let live: HashSet<&str> = facts.sessions.iter().map(|s| s.id.as_str()).collect();
    let dormant = facts
        .garden
        .of(&facts.profiles.selected)
        .iter()
        .filter_map(|seed| Some((seed.as_ref(), seed.standby.as_ref()?.tender.as_ref()?)))
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
    session.delegation_role.is_some()
        || session.dispatcher_session_id.is_some()
        || dispatched_by_party(session)
        || links(facts)
            .iter()
            .any(|l| l.dispatcher_id() == Some(session.id.as_str()))
}

pub(crate) fn trigger(session: &Session, facts: &Facts) -> Option<Trigger> {
    if !shown(session, facts) {
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

/// The role in the header of `leaf`, which shows the delegation chain on hover and pins it on a
/// press.
pub(super) fn piece(
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
    let (mark, words) = (trigger.mark.clone(), trigger.text.clone());
    let full = level == Level::Full;
    let name_key = format!("{}.name", header_pop::anchor(leaf, What::Chain));
    let role = Button::new(trigger.label.clone())
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::press(state, &pressed.0, &pressed.1, What::Chain, tx)
        })
        .label_with(move |_, _| {
            row()
                .items_center()
                .gap(MARK_GAP)
                .child(mark.el(MARK_PX, font, color))
                .children(full.then(|| {
                    text(words)
                        .key(name_key)
                        .font(font)
                        .color(color)
                        .single_line()
                }))
        })
        .build(attn_ui::kit(t))
        .key(header_pop::anchor(leaf, What::Chain))
        .keeps_press_from_within()
        .h(font.line_px().max(MARK_PX))
        .px(TRIGGER_PAD);
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

/// The role chip of the sidebar row keyed `row`, which pins the delegation chain beside the
/// sidebar.
pub(crate) fn chip(row: &str, session: &str, trigger: &Trigger, t: &Tokens) -> El {
    let host = header_pop::Host::Row(row.to_owned());
    let pressed = (host.clone(), session.to_owned());
    let color = if trigger.leads {
        t.accent
    } else {
        t.text_tertiary
    };
    let mark = trigger.mark.clone();
    Button::new(trigger.label.clone())
        .variant(ButtonVariant::GhostMuted)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            header_pop::press_on(state, pressed.0.clone(), &pressed.1, What::Chain, tx)
        })
        .label_with(move |_, _| mark.el(MARK_PX, type_::LABEL, color))
        .build(attn_ui::kit(t))
        .key(header_pop::row_anchor(row))
        .h(MARK_PX + 4.0)
        .px(2.0)
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

/// The open delegation chain, under its role, or beside the sidebar for a sidebar row.
pub(crate) fn popover(state: &State, pop: &Pop, t: &Tokens) -> Option<El> {
    let current = state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let (rows, earlier) = tree(&pop.session, &state.facts);
    let now_ms = state.window.now_ms;
    let picked = state.window.desk.header_pop.picked;
    let close = GlyphButton::new("×", "Close delegation chain")
        .variant(ButtonVariant::GhostFaint)
        .font(type_::LABEL)
        .side(type_::LABEL.line_px())
        .on_press(|state: &mut State| header_pop::close(state))
        .build(attn_ui::kit(t))
        .key(format!("{}.close", pop.surface()));
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
                .map(|(i, (link, depth))| match link {
                    Link::Live(session) => agent_row(pop, i, session, *depth, i == picked, t),
                    Link::Dormant(seed, tender) => {
                        dormant_row(pop, i, (seed, tender), now_ms, *depth, i == picked, t)
                    }
                }),
        );
    let surface = col()
        .key(pop.surface())
        .role("dialog")
        .label("Delegation chain")
        .w(300.0)
        .padding(space::S2)
        .gap(space::S1)
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
            .on_key(chain_key)
            .modal_tx(true, |state: &mut State, _: &mut Tx| {
                header_pop::close(state)
            })
            .modal_press_through(true)
    } else {
        surface
    };
    let (anchor, placement) = match &pop.host {
        header_pop::Host::Tile(_) => (pop.anchor(), Placement::BottomStart),
        header_pop::Host::Row(row) => (row.clone(), Placement::RightStart),
    };
    Some(rune_kit::popover::anchored(
        attn_ui::kit(t),
        rune_kit::popover::raised(attn_ui::kit(t), surface, 6.0),
        &anchor,
        placement,
        4.0,
    ))
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
    let dot = crate::sidebar::state_color(AgentState::parse(&session.state), t);
    let id = session.id.clone();
    chain_row(pop, i, depth, picked, label, t)
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
        .child(el().w(7.0).h(7.0).radius(3.5).fill(dot))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| select(state, &id, tx))
}

/// The row of a closed agent that tends a seed on standby: its name, "on standby" and what it
/// waits for. Activating it opens the seed.
fn dormant_row(
    pop: &Pop,
    i: usize,
    (seed, tender): (&Seed, &StandbyTender),
    now_ms: i64,
    depth: usize,
    picked: bool,
    t: &Tokens,
) -> El {
    let name = dormant_name(tender);
    let kind = std::iter::once("on standby".to_owned())
        .chain(
            crate::garden::model::standby_of(seed, now_ms)
                .map(|s| s.waits())
                .filter(|w| !w.is_empty()),
        )
        .collect::<Vec<_>>()
        .join(" · ");
    let label = format!("{name} · {kind}");
    let opens = (seed.id.clone(), pop.session.clone());
    chain_row(pop, i, depth, picked, label, t)
        .child(
            col()
                .grow(1.0)
                .child(
                    text(name)
                        .font(type_::LABEL)
                        .color(t.text_secondary)
                        .truncate(),
                )
                .child(text(kind).font(type_::LABEL).color(t.text_muted).truncate()),
        )
        .child(el().w(7.0).h(7.0).radius(3.5).border(1.0, t.text_muted))
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open_seed(state, &opens.0, &opens.1, tx))
}

/// How attn names a closed agent on standby.
pub(crate) fn dormant_name(tender: &StandbyTender) -> String {
    if tender.label.is_empty() {
        harness_label(&tender.agent)
    } else {
        tender.label.clone()
    }
}

fn chain_row(pop: &Pop, i: usize, depth: usize, picked: bool, label: String, t: &Tokens) -> El {
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
