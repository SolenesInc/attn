use std::collections::{HashMap, HashSet};

use attn_client::Facts;
use attn_protocol::{ActorView, Seed, Session, StandbyTender};
use attn_ui::blocks::{Block, HeaderPiece, Inline, Popover, Prose, Stack};
use attn_ui::system::{
    Button, DotShape, DotSize, IconSize, ListRow, Size, Tone, TreeLines, Type, dot, glyph, icon,
    keycaps, keys_hint, tag, tinted_pill, words,
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

/// The live agent on row `i` of the chain of `session`.
pub(crate) fn agent_at(session: &str, i: usize, facts: &Facts) -> Option<String> {
    let (rows, _) = tree(session, facts);
    match rows.get(i.min(rows.len().checked_sub(1)?))?.0 {
        Link::Live(session) => Some(session.id.clone()),
        Link::Dormant(..) => None,
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
    link: Link<'a>,
    depth: usize,
    name: String,
    /// The name less the start it shares with its dispatcher's, as "-terms".
    folded: Option<String>,
    lines: TreeLines,
    needs: Option<Needs>,
}

impl Agent<'_> {
    fn shown_name(&self) -> String {
        self.folded.clone().unwrap_or_else(|| self.name.clone())
    }
}

/// Why an agent waits on the user, most pressing first.
enum Needs {
    Approval,
    Turn,
    Asks(usize),
}

impl Needs {
    fn of(session: &Session, facts: &Facts, now_ms: i64) -> Option<Needs> {
        let snoozed =
            crate::sidebar::queue::is_snoozed(session.turn_snoozed_until.as_deref(), now_ms);
        if AgentState::parse(&session.state) == AgentState::PendingApproval {
            Some(Needs::Approval)
        } else if session.turn_owed == Some(true) && !snoozed {
            Some(Needs::Turn)
        } else {
            let asks = crate::asks::of_session(facts, session).len();
            (asks > 0).then_some(Needs::Asks(asks))
        }
    }

    fn said(&self) -> String {
        match self {
            Needs::Approval => "wants approval".to_owned(),
            Needs::Turn => "waiting for you".to_owned(),
            Needs::Asks(1) => "1 ask".to_owned(),
            Needs::Asks(n) => format!("{n} asks"),
        }
    }

    fn ink(&self, t: &Tokens) -> Color {
        match self {
            Needs::Approval => t.state_approval,
            Needs::Turn | Needs::Asks(_) => t.state_waiting,
        }
    }

    fn badge(&self, key: String, t: &Tokens) -> El {
        tinted_pill(key, self.said(), self.ink(t), t)
    }
}

fn name_of(link: Link<'_>, harnesses: &[attn_protocol::Harness]) -> String {
    match link {
        Link::Live(session) => display_name(session, harnesses),
        Link::Dormant(_, tender) => dormant_name(tender, harnesses),
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
    let names: Vec<String> = rows
        .iter()
        .map(|(link, _)| name_of(*link, &facts.harnesses))
        .collect();
    let depths: Vec<usize> = rows.iter().map(|(_, depth)| *depth).collect();
    rows.iter()
        .enumerate()
        .map(|(i, (link, depth))| Agent {
            link: *link,
            depth: *depth,
            folded: dispatcher_row(&depths, i).and_then(|up| folded(&names[i], &names[up])),
            name: names[i].clone(),
            lines: lines_of(&depths, i),
            needs: match link {
                Link::Live(session) => Needs::of(session, facts, now_ms),
                Link::Dormant(..) => None,
            },
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
    state.facts.sessions.iter().find(|s| s.id == pop.session)?;
    let (rows, earlier) = tree(&pop.session, &state.facts);
    let now_ms = state.window.now_ms;
    let agents = agents(&rows, &state.facts, now_ms);
    let picked = state
        .window
        .desk
        .header_pop
        .picked
        .min(agents.len().checked_sub(1)?);
    let surface_key = pop.surface();
    let tree_el = col()
        .key(agents_key(pop))
        .role("tree")
        .label("Agents in delegation chain")
        .w_full()
        .children(
            agents
                .iter()
                .enumerate()
                .map(|(i, agent)| agent_row(pop, i, agent, &state.facts, now_ms, i == picked, t)),
        );
    let mut blocks = Vec::new();
    blocks.extend(earlier.map(|name| {
        Prose::hint(
            format!("{surface_key}.earlier"),
            format!("Started by {name}, which attn cannot show."),
        )
        .build(t)
    }));
    blocks.push(Block::custom(format!("{surface_key}.tree"), tree_el));
    let need = agents.iter().filter(|a| a.needs.is_some()).count();
    let need = (need > 0).then(|| {
        words(format!("{need} need you"), Type::Small, t.state_waiting)
            .single_line()
            .key(format!("{surface_key}.needs"))
    });
    let moves = [
        keys_hint(["↑", "↓", "←", "→"], "move", t),
        keys_hint(["↵"], "open", t),
    ];
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

fn role_ink(role: &attn_protocol::SessionDelegationRole, t: &Tokens) -> Color {
    if role.builtin.as_deref() == Some("orchestrator") {
        t.accent
    } else {
        t.text_tertiary
    }
}

/// The role's mark at the end of a row, and the room of one where the agent has no role, so the
/// badges before it line up.
fn role_slot(role: Option<&attn_protocol::SessionDelegationRole>, t: &Tokens) -> El {
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
    let (kind, ink) = match (picked, agent.link) {
        (true, _) => (Type::Name, t.text_primary),
        (false, Link::Live(_)) => (Type::Body, t.text_primary),
        (false, Link::Dormant(..)) => (Type::Body, t.text_secondary),
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
    let row = match agent.link {
        Link::Live(session) => {
            let kind = session
                .delegation_role
                .as_ref()
                .map(|r| r.name.clone())
                .unwrap_or_else(|| harness_label(&facts.harnesses, &session.agent));
            let current = session.id == pop.session;
            let label = format!(
                "{} · {kind}{}",
                agent.name,
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
            let row = match &agent.needs {
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
            let opens = (seed.id.clone(), pop.session.clone());
            ListRow::new(key.clone(), format!("{} · {kind}", agent.name))
                .lead(dot(t.text_muted, DotShape::Hollow, DotSize::Regular))
                .title_el(row_name(&key, agent, picked, t))
                .aside(said.single_line())
                .aside(role_slot(None, t))
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    open_seed(state, &opens.0, &opens.1, tx)
                })
        }
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

/// How long ago `iso` was, as "3m", once a minute has passed.
fn held(iso: &str, now_ms: i64) -> Option<String> {
    let at = crate::sidebar::queue::epoch_ms(iso)?;
    match (now_ms - at).max(0) / 1000 {
        s if s < 60 => None,
        s if s < 3600 => Some(format!("{}m", s / 60)),
        s if s < 86_400 => Some(format!("{}h", s / 3600)),
        s => Some(format!("{}d", s / 86_400)),
    }
}

/// What the picked agent's details lead to, each by its letter.
#[derive(Clone)]
enum Go {
    Seed(String),
    PullRequest(String),
    Ask(String),
}

impl Go {
    fn letter(&self) -> &'static str {
        match self {
            Go::Seed(_) => "s",
            Go::PullRequest(_) => "p",
            Go::Ask(_) => "a",
        }
    }
}

/// Where the letters and the details of the agent `link` lead: its seed, its pull request and its
/// oldest open ask.
fn goes(link: Link<'_>, facts: &Facts) -> Vec<Go> {
    match link {
        Link::Live(session) => session
            .seed_id
            .clone()
            .filter(|s| !s.is_empty())
            .map(Go::Seed)
            .into_iter()
            .chain(
                super::provenance::pull_request(session).map(|pr| Go::PullRequest(pr.url.clone())),
            )
            .chain(
                crate::asks::of_session(facts, session)
                    .first()
                    .map(|ask| Go::Ask(ask.id.clone())),
            )
            .collect(),
        Link::Dormant(seed, _) => vec![Go::Seed(seed.id.clone())],
    }
}

fn find_seed<'a>(facts: &'a Facts, id: &str) -> Option<&'a Seed> {
    facts
        .garden
        .of(&facts.profiles.selected)
        .iter()
        .map(|s| s.as_ref())
        .find(|s| s.id == id)
}

/// One way out of the picked agent's details: a button with its mark, and its letter while the
/// chain has the keyboard.
fn go_button(
    key: String,
    go: &Go,
    link: Link<'_>,
    pinned: bool,
    facts: &Facts,
    session: &str,
    t: &Tokens,
) -> El {
    let (label, said, mark): (String, String, El) = match go {
        Go::Seed(id) => {
            let seed = find_seed(facts, id);
            let named = match seed {
                Some(s) if !s.step_slug.is_empty() => s.step_slug.clone(),
                Some(s) if !s.title.is_empty() => s.title.clone(),
                _ => id.clone(),
            };
            let mark = attn_ui::icon::seed(seed.map_or("", |s| s.status.as_str()));
            (
                format!("Open seed {named}"),
                named,
                glyph(mark, IconSize::Small, t.text_tertiary),
            )
        }
        Go::PullRequest(url) => {
            let pr = match link {
                Link::Live(agent) => super::provenance::pull_request(agent),
                Link::Dormant(..) => None,
            };
            match pr {
                Some(pr) => (
                    format!(
                        "Open pull request {} on GitHub, {}",
                        super::provenance::name(pr),
                        super::provenance::status_said(pr),
                    ),
                    format!("#{}", pr.number),
                    super::provenance::status_dot(pr, t),
                ),
                None => (
                    format!("Open {url}"),
                    url.clone(),
                    dot(t.text_tertiary, DotShape::Hollow, DotSize::Small),
                ),
            }
        }
        Go::Ask(id) => {
            let subject = facts
                .asks
                .iter()
                .find(|a| &a.id == id)
                .map(|a| a.subject.clone())
                .unwrap_or_default();
            (
                format!("Answer ask: {subject}"),
                subject,
                glyph(glyph::COMMENT, IconSize::Small, t.state_waiting),
            )
        }
    };
    let letter = go.letter().to_uppercase();
    let leading = Inline::joined()
        .children(pinned.then(|| keycaps([letter.as_str()], t).a11y_hidden()))
        .child(mark.a11y_hidden());
    let button = Button::new(said).size(Size::Compact).leading(leading);
    let button = match go {
        Go::PullRequest(_) => button.whole(),
        Go::Seed(_) | Go::Ask(_) => button,
    };
    let (go, session) = (go.clone(), session.to_owned());
    El::from(
        button
            .on_press_tx(move |state: &mut State, tx: &mut Tx| follow(state, &go, &session, tx))
            .build(t),
    )
    .key(key)
    .label(label)
}

/// Who an agent is: its face, its whole name, its role, harness and outpost, and its state with
/// how long it has been in it. `key` names the parts of the one shown.
fn who(key: Option<&str>, agent: &Agent<'_>, state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let now_ms = state.window.now_ms;
    let (face, meta, said, ink): (El, Vec<El>, String, Color) = match agent.link {
        Link::Live(session) => {
            let standing = AgentState::parse(&session.state);
            let state_ink = crate::sidebar::state_color(standing, t);
            let face = if crate::sidebar::logos(state) {
                icon(
                    crate::harness::logo::of(state, &session.agent),
                    IconSize::Large,
                    state_ink,
                )
            } else {
                dot(state_ink, DotShape::Filled, DotSize::Regular)
            };
            let role = session.delegation_role.as_ref();
            let about = [
                role.map(|r| r.name.clone()),
                Some(harness_label(&facts.harnesses, &session.agent)),
                session.host.clone().filter(|h| !h.is_empty()),
            ]
            .into_iter()
            .flatten()
            .collect::<Vec<_>>()
            .join(" · ");
            let meta = role
                .map(|role| {
                    glyph(
                        Mark::of(Some(role)).glyph(),
                        IconSize::Small,
                        role_ink(role, t),
                    )
                })
                .into_iter()
                .chain([about_line(about, t)])
                .collect();
            let (said, ink) = match &agent.needs {
                Some(needs @ (Needs::Approval | Needs::Turn)) => (needs.said(), needs.ink(t)),
                Some(Needs::Asks(_)) | None => (standing.spoken().to_owned(), state_ink),
            };
            let said = match held(&session.state_since, now_ms) {
                Some(held) => format!("{said} {held}"),
                None => said,
            };
            (face, meta, said, ink)
        }
        Link::Dormant(seed, tender) => {
            let waits = crate::garden::model::standby_of(seed, now_ms)
                .map(|s| s.waits())
                .filter(|w| !w.is_empty())
                .unwrap_or_else(|| harness_label(&facts.harnesses, &tender.agent));
            (
                dot(t.text_muted, DotShape::Hollow, DotSize::Regular),
                vec![about_line(waits, t)],
                "on standby".to_owned(),
                t.text_tertiary,
            )
        }
    };
    let keyed = |part: El, name: &str| match key {
        Some(key) => part.key(format!("{key}.{name}")),
        None => part,
    };
    Inline::top()
        .min_w(0.0)
        .child(face.a11y_hidden())
        .child(
            Stack::tight()
                .grow(1.0)
                .min_w(0.0)
                .child(keyed(
                    words(agent.name.clone(), Type::Name, t.text_primary).wrap_anywhere(),
                    "name",
                ))
                .child(keyed(Inline::joined().children(meta), "about")),
        )
        .child(keyed(words(said, Type::Small, ink).single_line(), "state"))
}

fn about_line(said: String, t: &Tokens) -> El {
    words(said, Type::Small, t.text_tertiary)
        .single_line()
        .truncate()
        .shrink()
}

/// What an agent is doing now, or why a closed one cannot resume.
fn doing(agent: &Agent<'_>, now_ms: i64, t: &Tokens) -> Option<El> {
    let (said, ink) = match agent.link {
        Link::Live(session) => (
            session.activity.clone().filter(|a| !a.trim().is_empty())?,
            t.text_secondary,
        ),
        Link::Dormant(seed, _) => (
            crate::garden::model::standby_of(seed, now_ms)?.blocked_words()?,
            t.warning,
        ),
    };
    Some(words(said, Type::Body, ink).wrap().max_lines(2))
}

/// The picked agent: who it is, what it is doing, and a button for each of its seed, its pull
/// request and its oldest open ask. Each part keeps the room the chain's agents need at most, so
/// the tree under it holds still while the pick moves.
fn details(pop: &Pop, agents: &[Agent<'_>], picked: usize, state: &State, t: &Tokens) -> El {
    let key = details_key(pop);
    let facts = &state.facts;
    let now_ms = state.window.now_ms;
    let agent = &agents[picked];
    let others = || agents.iter().enumerate().filter(move |(i, _)| *i != picked);
    let goes_of = |agent: &Agent<'_>| goes(agent.link, facts);
    let buttons = goes_of(agent)
        .into_iter()
        .map(|go| {
            go_button(
                format!("{key}.{}", go.letter()),
                &go,
                agent.link,
                pop.pinned,
                facts,
                &pop.session,
                t,
            )
        })
        .collect::<Vec<_>>();
    let shown_doing = doing(agent, now_ms, t);
    let room_doing: Vec<El> = others().filter_map(|(_, a)| doing(a, now_ms, t)).collect();
    let acts = shown_doing.is_some() || !room_doing.is_empty();
    let leads = agents.iter().any(|a| !goes_of(a).is_empty());
    Stack::loose()
        .w_full()
        .min_w(0.0)
        .child(Stack::steady(
            who(Some(&key), agent, state, t),
            others().map(|(_, a)| who(None, a, state, t)),
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
    opens: Pick,
    goes: Vec<Go>,
}

fn pinned_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if header_pop::switch_key(state, ev, tx) {
        return true;
    }
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
    let targets: Vec<Target> = tree(&session, &state.facts)
        .0
        .iter()
        .map(|(link, depth)| Target {
            depth: *depth,
            opens: match link {
                Link::Live(s) => Pick::Agent(s.id.clone()),
                Link::Dormant(seed, _) => Pick::Seed(seed.id.clone()),
            },
            goes: goes(*link, &state.facts),
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
            match targets.get(at).map(|t| t.opens.clone()) {
                Some(Pick::Agent(id)) => select(state, &id, tx),
                Some(Pick::Seed(id)) => open_seed(state, &id, &session, tx),
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
            follow(state, &go, &session, tx);
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
}

/// Opens what `go` leads to and closes the chain: a seed in a tile beside `session`, a pull
/// request on GitHub, an ask in the asks sheet.
fn follow(state: &mut State, go: &Go, session: &str, tx: &mut Tx) {
    match go {
        Go::Seed(id) => open_seed(state, id, session, tx),
        Go::PullRequest(url) => {
            header_pop::close(state);
            super::provenance::open(state, url, tx);
        }
        Go::Ask(id) => {
            header_pop::close(state);
            crate::asks::show(state, Some(id), tx);
        }
    }
}

fn open_seed(state: &mut State, seed: &str, session: &str, tx: &mut Tx) {
    header_pop::close(state);
    crate::garden::open_tile_beside(state, seed, Some(session.to_owned()), tx);
}

fn select(state: &mut State, session: &str, tx: &mut Tx) {
    header_pop::close(state);
    crate::sidebar::open_agent(state, session, tx);
}

/// How attn names a closed agent on standby.
pub(crate) fn dormant_name(tender: &StandbyTender, harnesses: &[attn_protocol::Harness]) -> String {
    if tender.label.is_empty() {
        harness_label(harnesses, &tender.agent)
    } else {
        tender.label.clone()
    }
}
