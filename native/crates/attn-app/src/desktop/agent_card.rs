use attn_client::Facts;
use attn_protocol::session_id::short;
use attn_protocol::{
    ActorView, ClosedDispatcher, Seed, Session, SessionLedgerEntry, StandbyTender,
};
use attn_ui::blocks::{Inline, Stack};
use attn_ui::system::{
    Button, DotShape, DotSize, IconSize, Size, Type, dot, glyph, icon, keycaps, tinted_pill, words,
};
use attn_ui::{Tokens, glyph};
use rune::{Color, El, Tx};

use super::header_pop;
use super::role_icon::Mark;
use crate::State;
use crate::action::Action;
use crate::sidebar::model::{AgentState, display_name, harness_label};

/// An agent a card shows: a live session, a closed one that tends a seed on standby and
/// resumes when mail reaches it, or a closed one as the ledger holds it, with its place in a
/// chain when it closed above agents still at work.
#[derive(Clone, Copy)]
pub(crate) enum Link<'a> {
    Live(&'a Session),
    Dormant(&'a Seed, &'a StandbyTender),
    Closed(&'a SessionLedgerEntry, Option<&'a ClosedDispatcher>),
}

impl<'a> Link<'a> {
    pub(super) fn id(self) -> &'a str {
        match self {
            Link::Live(session) => &session.id,
            Link::Dormant(_, tender) => &tender.session_id,
            Link::Closed(entry, _) => &entry.id,
        }
    }

    pub(super) fn dispatcher_id(self) -> Option<&'a str> {
        match self {
            Link::Live(session) => session.dispatcher_session_id.as_deref(),
            Link::Dormant(_, tender) => tender.dispatcher_session_id.as_deref(),
            Link::Closed(_, above) => above?.dispatcher_session_id.as_deref(),
        }
    }

    pub(super) fn dispatcher(self) -> Option<&'a ActorView> {
        match self {
            Link::Live(session) => session.dispatcher.as_ref(),
            Link::Dormant(_, tender) => tender.dispatcher.as_ref(),
            Link::Closed(_, above) => above?.dispatcher.as_ref(),
        }
    }

    pub(super) fn role(self) -> Option<&'a attn_protocol::SessionDelegationRole> {
        match self {
            Link::Live(session) => session.delegation_role.as_ref(),
            Link::Dormant(..) => None,
            Link::Closed(_, above) => above?.delegation_role.as_ref(),
        }
    }
}

/// One agent as its card shows it.
pub(super) struct Card<'a> {
    pub(super) link: Link<'a>,
    pub(super) name: String,
    pub(super) needs: Option<Needs>,
}

impl<'a> Card<'a> {
    pub(super) fn of(link: Link<'a>, facts: &Facts, now_ms: i64) -> Card<'a> {
        Card {
            link,
            name: name_of(link, facts),
            needs: match link {
                Link::Live(session) => Needs::of(session, facts, now_ms),
                Link::Dormant(..) | Link::Closed(..) => None,
            },
        }
    }
}

/// Why an agent waits on the user, most pressing first.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Needs {
    Approval,
    Turn,
    Asks(usize),
}

impl Needs {
    pub(crate) fn of(session: &Session, facts: &Facts, now_ms: i64) -> Option<Needs> {
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

    pub(crate) fn said(&self) -> String {
        match self {
            Needs::Approval => "wants approval".to_owned(),
            Needs::Turn => "waiting for you".to_owned(),
            Needs::Asks(1) => "1 ask".to_owned(),
            Needs::Asks(n) => format!("{n} asks"),
        }
    }

    pub(crate) fn ink(&self, t: &Tokens) -> Color {
        match self {
            Needs::Approval => t.state_approval,
            Needs::Turn | Needs::Asks(_) => t.state_waiting,
        }
    }

    pub(super) fn badge(&self, key: String, t: &Tokens) -> El {
        tinted_pill(key, self.said(), self.ink(t), t)
    }
}

fn name_of(link: Link<'_>, facts: &Facts) -> String {
    match link {
        Link::Live(session) => display_name(session, &facts.harnesses),
        Link::Dormant(_, tender) => dormant_name(tender, &facts.harnesses),
        Link::Closed(entry, _) => crate::ledger::title(facts, entry),
    }
}

pub(super) fn role_ink(role: &attn_protocol::SessionDelegationRole, t: &Tokens) -> Color {
    if role.builtin.as_deref() == Some("orchestrator") {
        t.accent
    } else {
        t.text_tertiary
    }
}

/// How long ago `iso` was, as "3m", once a minute has passed.
pub(super) fn held(iso: &str, now_ms: i64) -> Option<String> {
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
pub(super) enum Go {
    Resume { session: String, action: String },
    Ledger(String),
    Seed(String),
    PullRequest(String),
    Ask(String),
}

impl Go {
    pub(super) fn letter(&self) -> &'static str {
        match self {
            Go::Resume { .. } => "r",
            Go::Ledger(_) => "l",
            Go::Seed(_) => "s",
            Go::PullRequest(_) => "p",
            Go::Ask(_) => "a",
        }
    }
}

/// Where the letters and the details of the agent `link` lead: its seed, its pull request and its
/// oldest open ask, or how a closed one resumes, its ledger row and its seed.
pub(super) fn goes(link: Link<'_>, state: &State) -> Vec<Go> {
    let facts = &state.facts;
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
        Link::Closed(entry, _) => {
            let action = match state.window.ledger.verdict(entry) {
                Some(verdict) => verdict.actions.first().cloned(),
                None => Some("reopen".to_owned()),
            };
            let session = entry.id.clone();
            let resume = action.map(|action| Go::Resume { session, action });
            let seed = seed_of_closed(facts, &entry.id).map(|seed| Go::Seed(seed.id.clone()));
            resume
                .into_iter()
                .chain([Go::Ledger(entry.id.clone())])
                .chain(seed)
                .collect()
        }
    }
}

/// The seed a closed session tends, or waits for on standby, or last tended.
fn seed_of_closed<'a>(facts: &'a Facts, session: &str) -> Option<&'a Seed> {
    let tends = |seed: &Seed| {
        let tender = seed.tender.as_ref().and_then(|t| t.session_id.as_deref());
        let standby = seed.standby.as_ref().and_then(|s| s.tender.as_ref());
        tender == Some(session) || standby.is_some_and(|t| t.session_id == session)
    };
    let seeds = facts.garden.of(&facts.profiles.selected);
    seeds
        .iter()
        .find(|seed| tends(seed))
        .or_else(|| {
            let last =
                |seed: &&std::sync::Arc<Seed>| seed.last_execution_id.as_deref() == Some(session);
            seeds.iter().find(last)
        })
        .map(|seed| seed.as_ref())
}

fn find_seed<'a>(facts: &'a Facts, id: &str) -> Option<&'a Seed> {
    facts
        .garden
        .of(&facts.profiles.selected)
        .iter()
        .map(|s| s.as_ref())
        .find(|s| s.id == id)
}

/// How a closed agent's card says the ledger's reopen `action`.
pub(super) fn resume_said(action: &str) -> &str {
    match action {
        "reopen" => "Resume",
        other => crate::ledger::query::action_label(other),
    }
}

/// One way out of the picked agent's details: a button with its mark, and its letter while the
/// chain has the keyboard.
pub(super) fn go_button(
    key: String,
    go: &Go,
    link: Link<'_>,
    pinned: bool,
    facts: &Facts,
    beside: Option<&str>,
    t: &Tokens,
) -> El {
    let (label, said, mark): (String, String, El) = match go {
        Go::Resume { action, .. } => {
            let said = resume_said(action);
            (
                format!("{said}: {}", name_of(link, facts)),
                said.to_owned(),
                glyph(glyph::REFRESH, IconSize::Small, t.accent),
            )
        }
        Go::Ledger(_) => (
            format!("Show {} in the ledger", name_of(link, facts)),
            "Ledger".to_owned(),
            glyph(glyph::CLOCK, IconSize::Small, t.text_tertiary),
        ),
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
                Link::Dormant(..) | Link::Closed(..) => None,
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
        Go::Resume { .. } | Go::Ledger(_) | Go::PullRequest(_) => button.whole(),
        Go::Seed(_) | Go::Ask(_) => button,
    };
    let (go, beside) = (go.clone(), beside.map(str::to_owned));
    El::from(
        button
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                follow(state, &go, beside.as_deref(), tx)
            })
            .build(t),
    )
    .key(key)
    .label(label)
}

/// Who an agent is: its face, its whole name, its role, harness and outpost, and its state with
/// how long it has been in it. `key` names the parts of the one shown.
pub(super) fn who(key: Option<&str>, card: &Card<'_>, state: &State, t: &Tokens) -> El {
    let facts = &state.facts;
    let now_ms = state.window.now_ms;
    let (face, meta, said, ink): (El, Vec<El>, String, Color) = match card.link {
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
                super::host::name(session, &crate::machine::Machines::of(facts)),
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
            let (said, ink) = match &card.needs {
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
        Link::Closed(entry, _) => {
            let role = card.link.role();
            let about = [
                role.map(|r| r.name.clone()),
                Some(harness_label(&facts.harnesses, &entry.agent)),
                Some(short(&entry.id).into_owned()),
                crate::machine::Machines::of(facts).away(entry.host.as_deref()),
            ]
            .into_iter()
            .flatten()
            .collect::<Vec<_>>()
            .join(" · ");
            let said = match held(entry.closed_at.as_deref().unwrap_or_default(), now_ms) {
                Some(held) => format!("closed {held}"),
                None => "closed".to_owned(),
            };
            let meta = role
                .map(|role| {
                    glyph(
                        Mark::of(Some(role)).glyph(),
                        IconSize::Small,
                        t.text_tertiary,
                    )
                })
                .into_iter()
                .chain([about_line(about, t)])
                .collect();
            (
                dot(t.text_muted, DotShape::Hollow, DotSize::Regular),
                meta,
                said,
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
                    words(card.name.clone(), Type::Name, t.text_primary).wrap_anywhere(),
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

/// What an agent is doing now, or where a closed one worked and why it cannot resume.
pub(super) fn doing(card: &Card<'_>, state: &State, t: &Tokens) -> Option<El> {
    let now_ms = state.window.now_ms;
    let (said, ink) = match card.link {
        Link::Live(session) => (
            session.activity.clone().filter(|a| !a.trim().is_empty())?,
            t.text_secondary,
        ),
        Link::Dormant(seed, _) => (
            crate::garden::model::standby_of(seed, now_ms)?.blocked_words()?,
            t.warning,
        ),
        Link::Closed(entry, _) if crate::ledger::resuming(state, entry) => {
            ("Resuming…".to_owned(), t.text_secondary)
        }
        Link::Closed(entry, _) => match crate::ledger::refused(state, entry) {
            Some(why) => (why, t.warning),
            None => (
                entry.branch.clone().filter(|b| !b.is_empty())?,
                t.text_secondary,
            ),
        },
    };
    Some(words(said, Type::Body, ink).wrap().max_lines(2))
}

/// Opens what `go` leads to and closes the chain: a seed in a tile beside `beside`'s, a pull
/// request on GitHub, an ask in the asks sheet, a closed agent in the ledger. A closed agent that
/// resumes keeps its card until it opens, so a refusal shows there.
pub(super) fn follow(state: &mut State, go: &Go, beside: Option<&str>, tx: &mut Tx) {
    match go {
        Go::Resume { session, action } => {
            if let Some(entry) = super::chain::closed_entry(session, state).cloned() {
                crate::ledger::resume(state, entry, action.clone(), tx);
            }
        }
        Go::Ledger(session) => {
            if let Some(entry) = super::chain::closed_entry(session, state).cloned() {
                header_pop::close(state);
                crate::shell::uncover_tiles(state, tx);
                crate::ledger::open_session(state, entry, tx);
            }
        }
        Go::Seed(id) => open_seed(state, id, beside, tx),
        Go::PullRequest(url) => {
            header_pop::close(state);
            super::provenance::open(state, url, tx);
        }
        Go::Ask(id) => {
            header_pop::close(state);
            let ask = Some(id.clone());
            crate::dispatch(state, Action::ShowAsks { ask }, tx);
        }
    }
}

pub(super) fn open_seed(state: &mut State, seed: &str, beside: Option<&str>, tx: &mut Tx) {
    header_pop::close(state);
    crate::shell::uncover_tiles(state, tx);
    match beside {
        Some(session) => crate::garden::open_tile_beside(state, seed, Some(session.to_owned()), tx),
        None => crate::garden::open_tile(state, seed, tx),
    }
}

pub(super) fn select(state: &mut State, session: &str, tx: &mut Tx) {
    header_pop::close(state);
    crate::shell::uncover_tiles(state, tx);
    let session = session.to_owned();
    crate::dispatch(state, Action::OpenAgent { session }, tx);
}

/// How attn names a closed agent on standby.
pub(crate) fn dormant_name(tender: &StandbyTender, harnesses: &[attn_protocol::Harness]) -> String {
    if tender.label.is_empty() {
        harness_label(harnesses, &tender.agent)
    } else {
        tender.label.clone()
    }
}
