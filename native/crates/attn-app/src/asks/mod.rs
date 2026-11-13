mod view;

use std::collections::{BTreeMap, HashMap};

use attn_client::Facts;
use attn_protocol::{Ask, AskAnswer, AskAnswerMessage, AskQuestion, OutpostInfo, Session};
use rune::{TextEdit, Tx};

use crate::State;
use crate::shell::Drawer;
use crate::sidebar::queue;

pub(crate) use view::{ChipLevel, chip, chip_widths, drawer, marker, preview, title_button};

pub(crate) const SETTING: &str = "asks_enabled";
pub(crate) const DRAWER: &str = "asks.drawer";
const CREW_DAEMON_ID: &str = "attn";

/// The drawer's own state: which ask it shows, what the person has written in each, and the
/// send in flight.
#[derive(Default)]
pub struct Asks {
    /// The ask the drawer shows; the first of its list while none is picked.
    pub(crate) selected: Option<String>,
    pub(crate) show_replied: bool,
    pub(crate) drafts: HashMap<String, Draft>,
    pub(crate) sending: Option<String>,
    /// Why the daemon refused the answer to an ask, shown under that ask.
    pub(crate) error: Option<(String, String)>,
    /// The ask, and whether it was replied, that the keyboard last moved into.
    focused_on: Option<(String, bool)>,
    /// The keyboard moves into the drawer at the next follow, whatever it shows.
    take_keyboard: bool,
}

#[derive(Default)]
pub(crate) struct Draft {
    pub answers: BTreeMap<String, Answer>,
    pub note: TextEdit,
}

#[derive(Default)]
pub(crate) struct Answer {
    pub picked: Vec<String>,
    pub other: TextEdit,
    pub text: TextEdit,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    One,
    Any,
    Text,
}

impl Kind {
    pub(crate) fn of(question: &AskQuestion) -> Kind {
        match question.kind.as_str() {
            "any" => Kind::Any,
            "text" => Kind::Text,
            _ => Kind::One,
        }
    }
}

/// One row of the drawer's list: the ask, the agent that holds it, and how the drawer names
/// that agent.
pub(crate) struct Item<'a> {
    pub ask: &'a Ask,
    pub holder: Option<&'a Session>,
    pub from: String,
    pub questions: usize,
    pub snoozed_until: Option<&'a str>,
}

/// The open asks, those of agents waiting on the person first, then a snoozed agent's asks;
/// and, newest first, the asks answered in the last day.
pub(crate) struct Lists<'a> {
    pub live: Vec<Item<'a>>,
    pub snoozed: Vec<Item<'a>>,
    pub replied: Vec<Item<'a>>,
}

impl<'a> Lists<'a> {
    pub(crate) fn of(facts: &'a Facts, now_ms: i64) -> Lists<'a> {
        let item = |ask: &'a Ask| {
            let holder = holder(ask, facts);
            Item {
                ask,
                holder,
                from: from(ask, holder),
                questions: questions(ask).count(),
                snoozed_until: None,
            }
        };
        let mut open: Vec<&Ask> = facts.asks.iter().filter(|a| a.reply.is_none()).collect();
        let waiting = |ask: &Ask| {
            holder(ask, facts)
                .filter(|s| s.turn_owed == Some(true))
                .map(|s| {
                    s.turn_opened_at
                        .clone()
                        .unwrap_or_else(|| ask.created_at.clone())
                })
        };
        open.sort_by(|a, b| {
            let (wa, wb) = (waiting(a), waiting(b));
            wb.is_some()
                .cmp(&wa.is_some())
                .then_with(|| wa.cmp(&wb))
                .then_with(|| a.created_at.cmp(&b.created_at))
                .then_with(|| a.id.cmp(&b.id))
        });
        let (mut live, mut snoozed) = (Vec::new(), Vec::new());
        for ask in open {
            let mut item = item(ask);
            let until = item.holder.and_then(|s| s.turn_snoozed_until.as_deref());
            if queue::is_snoozed(until, now_ms) {
                item.snoozed_until = until;
                snoozed.push(item);
            } else {
                live.push(item);
            }
        }
        snoozed.sort_by(|a, b| a.snoozed_until.cmp(&b.snoozed_until));
        let mut replied: Vec<&Ask> = facts.asks.iter().filter(|a| a.reply.is_some()).collect();
        replied.sort_by(|a, b| {
            let at = |ask: &Ask| ask.reply.as_ref().map(|r| r.answered_at.clone());
            at(b).cmp(&at(a)).then_with(|| a.id.cmp(&b.id))
        });
        Lists {
            live,
            snoozed,
            replied: replied.into_iter().map(item).collect(),
        }
    }

    /// What the drawer lists: the open asks, then the replied ones when they show.
    pub(crate) fn shown(self, replied: bool) -> Vec<Item<'a>> {
        let mut items = self.live;
        items.extend(self.snoozed);
        if replied {
            items.extend(self.replied);
        }
        items
    }
}

/// Opens an ask's seed in the garden beside the drawer, or in a tile when the garden's window
/// of seeds does not hold it.
pub(crate) fn open_seed(state: &mut State, id: &str, tx: &mut Tx) {
    if crate::garden::seeds(state).iter().any(|s| s.id == id) {
        crate::garden::open_seed(state, id, tx);
        return;
    }
    state.act(
        |request_id| attn_protocol::OpenSeedMessage {
            seed_id: id.to_owned(),
            request_id: Some(request_id),
            ..attn_protocol::OpenSeedMessage::new()
        },
        crate::requests::Refused::Prefixed("Could not open the seed"),
        |_, _, _| {},
        tx,
    );
}

/// The agent an ask waits on: whoever the garden says tends its seed, its crew member's
/// session, or the session that asked. A seed the garden does not hold is tended by the
/// session that names it as its stake.
pub(crate) fn holder<'a>(ask: &Ask, facts: &'a Facts) -> Option<&'a Session> {
    facts.sessions.iter().find(|s| belongs_to(ask, s, facts))
}

pub(crate) fn belongs_to(ask: &Ask, session: &Session, facts: &Facts) -> bool {
    if let Some(seed) = &ask.seed_id {
        let seeds = facts.garden.of(&facts.profiles.selected);
        return match seeds.iter().find(|s| &s.id == seed) {
            Some(known) => known.tender.as_ref().is_some_and(|tender| {
                session
                    .crew_member
                    .as_ref()
                    .is_some_and(|member| tender.r#ref == format!("member:{member}"))
                    || tender.session_id.as_deref() == Some(session.id.as_str())
            }),
            None => session.seed_id.as_ref() == Some(seed),
        };
    }
    if let Some(member) = &ask.crew_member {
        return session.crew_member.as_ref() == Some(member);
    }
    ask.session_id == session.id
}

fn from(ask: &Ask, holder: Option<&Session>) -> String {
    if holder.is_some_and(|s| s.chief_of_staff == Some(true)) {
        return "Chief".into();
    }
    if let Some(member) = &ask.crew_member {
        let name = crew_display_name(member);
        return match holder {
            Some(_) => name,
            None => format!("{name} (between sessions)"),
        };
    }
    match (holder, &ask.seed_id) {
        (Some(session), _) => crate::sidebar::display_name(session),
        (None, Some(seed)) => format!(
            "waiting for the next tender of {}",
            ask.seed_title
                .as_deref()
                .filter(|t| !t.is_empty())
                .unwrap_or(seed)
        ),
        (None, None) => "Closed agent".into(),
    }
}

pub(crate) fn crew_display_name(id: &str) -> String {
    let id = id.trim();
    if id == CREW_DAEMON_ID {
        return id.into();
    }
    let mut chars = id.chars();
    match chars.next() {
        Some(first) => first.to_uppercase().chain(chars).collect(),
        None => String::new(),
    }
}

pub(crate) fn questions(ask: &Ask) -> impl Iterator<Item = &AskQuestion> {
    ask.parts.iter().filter_map(|p| p.question.as_ref())
}

/// Whether the agent runs on a machine the home cannot reach.
pub(crate) fn offline(session: &Session, outposts: &[OutpostInfo]) -> bool {
    session.host.as_deref().is_some_and(|host| {
        outposts
            .iter()
            .find(|o| o.id == host)
            .is_none_or(|o| o.reach == "unreachable")
    })
}

/// Asks show while the setting is on or while any ask exists.
pub(crate) fn available(facts: &Facts) -> bool {
    !facts.asks.is_empty() || crate::setting::on(&facts.settings, SETTING)
}

/// The open asks the drawer counts: every one but a snoozed agent's.
pub(crate) fn open_count(facts: &Facts, now_ms: i64) -> usize {
    Lists::of(facts, now_ms).live.len()
}

/// What an agent's tile header shows of its open asks: the oldest, and how many more.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Chip {
    pub ask: String,
    pub subject: String,
    pub more: usize,
}

pub(crate) fn chip_of(facts: &Facts, session: &Session) -> Option<Chip> {
    let asks = of_session(facts, session);
    let first = asks.first()?;
    Some(Chip {
        ask: first.id.clone(),
        subject: first.subject.clone(),
        more: asks.len() - 1,
    })
}

/// An agent's open asks, oldest first.
pub(crate) fn of_session<'a>(facts: &'a Facts, session: &Session) -> Vec<&'a Ask> {
    let mut asks: Vec<&Ask> = facts
        .asks
        .iter()
        .filter(|a| a.reply.is_none() && belongs_to(a, session, facts))
        .collect();
    asks.sort_by(|a, b| a.created_at.cmp(&b.created_at));
    asks
}

impl Draft {
    /// The answers as the daemon takes them: each question with a pick, another choice or
    /// written text, in the ask's order.
    pub(crate) fn answers(&self, ask: &Ask) -> Vec<AskAnswer> {
        questions(ask)
            .filter_map(|q| {
                let answer = self.answers.get(&q.key)?;
                let picked = &answer.picked;
                let other = answer.other.text.trim();
                let text = answer.text.text.trim();
                if picked.is_empty() && other.is_empty() && text.is_empty() {
                    return None;
                }
                Some(AskAnswer {
                    key: q.key.clone(),
                    picked: picked.to_vec(),
                    other: (!other.is_empty()).then(|| other.to_owned()),
                    text: (!text.is_empty()).then(|| text.to_owned()),
                })
            })
            .collect()
    }

    pub(crate) fn sendable(&self, ask: &Ask) -> bool {
        !self.answers(ask).is_empty() || !self.note.text.trim().is_empty()
    }
}

/// The questions that recommend an answer, as the answers that pick it.
pub(crate) fn recommended(ask: &Ask) -> Vec<(String, Kind, String)> {
    questions(ask)
        .filter_map(|q| {
            let recommended = q.recommended.clone().filter(|r| !r.is_empty())?;
            Some((q.key.clone(), Kind::of(q), recommended))
        })
        .collect()
}

/// When what the asks show by the clock next changes: a snoozed asker waking, which returns its
/// asks to the counts, and, while the drawer shows them, the ages its rows and its replied
/// footer read.
pub(crate) fn next_change(state: &State, now_ms: i64) -> Option<i64> {
    let lists = Lists::of(&state.facts, now_ms);
    let wakes: Vec<i64> = lists
        .snoozed
        .iter()
        .filter_map(|item| item.snoozed_until.and_then(queue::epoch_ms))
        .collect();
    let ages: Vec<i64> = if state.window.shows(Drawer::Asks) {
        lists
            .shown(state.window.asks.show_replied)
            .iter()
            .filter_map(|item| queue::next_age_change(Some(age_from(item.ask)), now_ms))
            .collect()
    } else {
        Vec::new()
    };
    wakes.into_iter().chain(ages).min()
}

/// When an ask's age counts from: its answer once replied, else its opening.
pub(crate) fn age_from(ask: &Ask) -> &str {
    ask.reply
        .as_ref()
        .map_or(ask.created_at.as_str(), |r| r.answered_at.as_str())
}

impl State {
    fn asks_items(&self) -> Vec<Item<'_>> {
        Lists::of(&self.facts, self.window.now_ms).shown(self.window.asks.show_replied)
    }

    /// The ask the drawer shows: the one picked, or the first it lists.
    pub(crate) fn shown_ask(&self) -> Option<(String, bool)> {
        let items = self.asks_items();
        let picked = self.window.asks.selected.as_deref();
        items
            .iter()
            .find(|i| Some(i.ask.id.as_str()) == picked)
            .or(items.first())
            .map(|i| (i.ask.id.clone(), i.ask.reply.is_some()))
    }

    fn draft(&mut self, ask: &str) -> &mut Draft {
        self.window.asks.drafts.entry(ask.to_owned()).or_default()
    }

    pub(crate) fn answer(&mut self, ask: &str, question: &str) -> &mut Answer {
        self.draft(ask)
            .answers
            .entry(question.to_owned())
            .or_default()
    }

    pub(crate) fn note(&mut self, ask: &str) -> &mut TextEdit {
        &mut self.draft(ask).note
    }
}

/// Opens or closes the drawer. Opening it shows the ask it last showed, or the first, and
/// takes the keyboard there; closing it hands the keyboard back to the agent.
pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if state.window.shows(Drawer::Asks) {
        close(state, tx);
    } else {
        show(state, None, tx);
    }
}

/// Opens the drawer on `ask`, or where it was, and takes the keyboard into it even when it
/// already shows that ask.
pub(crate) fn show(state: &mut State, ask: Option<&str>, tx: &mut Tx) {
    state.window.open_drawer(Drawer::Asks);
    state.tick(tx);
    if let Some(ask) = ask {
        state.window.asks.selected = Some(ask.to_owned());
    }
    state.window.asks.take_keyboard = true;
    follow(state, tx);
}

pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    state.window.close_drawer(Drawer::Asks);
    state.window.asks.selected = None;
    state.window.asks.focused_on = None;
    tx.blur();
}

/// Keeps the drawer on the ask it shows as the lists change, forgets what was written for
/// asks that closed, and moves the keyboard into an ask newly shown.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let open: Vec<String> = state
        .facts
        .asks
        .iter()
        .filter(|a| a.reply.is_none())
        .map(|a| a.id.clone())
        .collect();
    state.window.asks.drafts.retain(|id, _| open.contains(id));
    if !state.window.shows(Drawer::Asks) {
        return;
    }
    let shown = state.shown_ask();
    state.window.asks.selected = shown.as_ref().map(|(id, _)| id.clone());
    let take_keyboard = std::mem::take(&mut state.window.asks.take_keyboard);
    if shown == state.window.asks.focused_on && !take_keyboard {
        return;
    }
    if let Some((ask, _)) = &state.window.asks.focused_on
        && shown.as_ref().is_none_or(|(id, _)| id != ask)
    {
        state.window.asks.error = None;
    }
    state.window.asks.focused_on = shown.clone();
    let ring = tx.by_keys() || tx.focus_visible();
    match shown.and_then(|(id, _)| first_input(state, &id)) {
        Some(input) => tx.focus(input, ring),
        None => tx.focus(DRAWER, ring),
    }
}

/// The first control an open ask takes the keyboard in: its first choice or written answer.
fn first_input(state: &State, ask: &str) -> Option<String> {
    let ask = state.facts.asks.iter().find(|a| a.id == ask)?;
    if ask.reply.is_some() {
        return None;
    }
    let question = questions(ask).next()?;
    Some(match Kind::of(question) {
        Kind::Text => view::text_key(&question.key),
        Kind::One | Kind::Any if question.choices.is_empty() => view::other_key(&question.key),
        Kind::One | Kind::Any => view::choice_key(&question.key, 0),
    })
}

/// Moves to the next or previous ask the drawer lists.
pub(crate) fn step(state: &mut State, by: isize, tx: &mut Tx) {
    let items = state.asks_items();
    let Some((shown, _)) = state.shown_ask() else {
        return;
    };
    let at = items.iter().position(|i| i.ask.id == shown).unwrap_or(0) as isize;
    let Some(next) = items
        .get((at + by).max(0) as usize)
        .filter(|_| at + by >= 0)
    else {
        return;
    };
    let next = next.ask.id.clone();
    state.window.asks.selected = Some(next);
    follow(state, tx);
}

pub(crate) fn pick(state: &mut State, ask: &str, question: &str, kind: Kind, label: &str) {
    let answer = state.answer(ask, question);
    match kind {
        Kind::One => {
            answer.picked = vec![label.to_owned()];
            answer.other = TextEdit::default();
        }
        Kind::Any => {
            if let Some(at) = answer.picked.iter().position(|p| p == label) {
                answer.picked.remove(at);
            } else {
                answer.picked.push(label.to_owned());
            }
        }
        Kind::Text => {}
    }
}

/// A one-choice question's written answer replaces its pick.
pub(crate) fn other_edited(state: &mut State, ask: &str, question: &str, kind: Kind) {
    if kind == Kind::One {
        state.answer(ask, question).picked.clear();
    }
}

/// Picks every recommended answer, when several questions recommend one.
pub(crate) fn accept_recommended(state: &mut State, ask: &str) {
    let Some(found) = state.facts.asks.iter().find(|a| a.id == ask).cloned() else {
        return;
    };
    let recommended = recommended(&found);
    if found.reply.is_some() || recommended.len() < 2 || state.window.asks.sending.is_some() {
        return;
    }
    for (key, kind, value) in recommended {
        let answer = state.answer(ask, &key);
        match kind {
            Kind::Text => {
                answer.text = TextEdit::new(value);
                answer.picked.clear();
                answer.other = TextEdit::default();
            }
            Kind::One | Kind::Any => {
                answer.picked = vec![value];
                answer.other = TextEdit::default();
                answer.text = TextEdit::default();
            }
        }
    }
}

/// Sends the answers and note written for the ask shown. Once the daemon takes them the drawer
/// moves to the ask after it in its section, or before it, or stays to show the reply; a
/// refusal keeps what was written and says why.
pub(crate) fn send(state: &mut State, tx: &mut Tx) {
    let Some((id, false)) = state.shown_ask() else {
        return;
    };
    if state.window.asks.sending.is_some() {
        return;
    }
    let Some(ask) = state.facts.asks.iter().find(|a| a.id == id).cloned() else {
        return;
    };
    let draft = state.draft(&id);
    if !draft.sendable(&ask) {
        return;
    }
    let answers = draft.answers(&ask);
    let note = draft.note.text.trim().to_owned();
    let after = {
        let lists = Lists::of(&state.facts, state.window.now_ms);
        let section = if lists.snoozed.iter().any(|i| i.ask.id == id) {
            lists.snoozed
        } else {
            lists.live
        };
        let at = section.iter().position(|i| i.ask.id == id).unwrap_or(0);
        section
            .get(at + 1)
            .or(at.checked_sub(1).and_then(|before| section.get(before)))
            .map(|i| i.ask.id.clone())
    };
    state.window.asks.sending = Some(id.clone());
    state.window.asks.error = None;
    state.ask(
        |request_id| AskAnswerMessage {
            request_id,
            ask_id: id.clone(),
            answers,
            note: (!note.is_empty()).then_some(note),
            ..AskAnswerMessage::new()
        },
        move |state, answer, tx| {
            state.window.asks.sending = None;
            match answer.outcome() {
                Ok(_) => {
                    state.window.asks.selected = after.or_else(|| Some(ask.id.clone()));
                    follow(state, tx);
                }
                Err(why) => state.window.asks.error = Some((ask.id.clone(), why)),
            }
        },
        tx,
    );
}

/// Opens the agent that holds the ask shown, and closes the drawer.
pub(crate) fn open_agent(state: &mut State, session: &str, tx: &mut Tx) {
    close(state, tx);
    crate::sidebar::open_agent(state, session, tx);
}

/// Snoozes or wakes the agent that holds the ask shown.
pub(crate) fn toggle_snooze(state: &mut State, tx: &mut Tx) {
    let Some((id, _)) = state.shown_ask() else {
        return;
    };
    let Some(ask) = state.facts.asks.iter().find(|a| a.id == id) else {
        return;
    };
    let Some(holder) = holder(ask, &state.facts)
        .filter(|s| !offline(s, &state.facts.outposts))
        .cloned()
    else {
        return;
    };
    if queue::is_snoozed(holder.turn_snoozed_until.as_deref(), state.window.now_ms) {
        crate::sidebar::wake(state, &holder.id);
        return;
    }
    let name = from(ask, Some(&holder));
    crate::sidebar::snooze::open(state, &holder.id, name, Some(view::SNOOZE.into()), true, tx);
}
