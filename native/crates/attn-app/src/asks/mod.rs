mod mentions;
mod view;

use std::collections::{BTreeMap, HashMap};

use attn_client::Facts;
use attn_protocol::{Ask, AskAnswer, AskAnswerMessage, AskQuestion, Session, StandbyTender};
use rune::{TextEdit, Tx};

use crate::State;
use crate::shell::Layer;
use crate::sidebar::queue;

pub(crate) use view::{ChipLevel, chip, chip_widths, marker, preview, sheet, title_button};

pub(crate) const SETTING: &str = "asks_enabled";
pub(crate) const SHEET: &str = "asks.sheet";
const CREW_DAEMON_ID: &str = "attn";
const TENDER_RESUME: &str = "garden.tender_resume";

/// The closed agent on standby that tends an ask's seed, and what an answer does for it.
pub(crate) struct Standby<'a> {
    pub tender: &'a StandbyTender,
    pub answer: String,
}

/// The sheet's own state: which ask it shows, what the person has written in each, and the
/// send in flight.
#[derive(Default)]
pub struct Asks {
    /// The ask the sheet shows, kept while it is closed; the first of its list while none is
    /// picked or the one picked is gone.
    pub(crate) selected: Option<String>,
    pub(crate) show_replied: bool,
    pub(crate) drafts: HashMap<String, Draft>,
    pub(crate) sending: Option<String>,
    /// Why the daemon refused the answer to an ask, shown under that ask.
    pub(crate) error: Option<(String, String)>,
    /// The ask, and whether it was replied, that the keyboard last moved into.
    focused_on: Option<(String, bool)>,
    /// The keyboard moves into the sheet at the next follow, whatever it shows.
    take_keyboard: bool,
    mentions: Option<mentions::Mentions>,
}

#[derive(Default)]
pub(crate) struct Draft {
    pub answers: BTreeMap<String, Answer>,
    pub note: TextEdit,
}

/// `edit` holding `text` instead, at the height its grip was dragged to.
pub(crate) fn rewritten(edit: &TextEdit, text: String) -> TextEdit {
    let mut rewritten = TextEdit::new(text);
    rewritten.set_dragged_height(edit.dragged_height());
    rewritten
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

/// One row of the sheet's list: the ask, the agent that holds it or the closed agent on
/// standby that its answer resumes, and how the sheet names that agent.
pub(crate) struct Item<'a> {
    pub ask: &'a Ask,
    pub holder: Option<&'a Session>,
    pub standby: Option<Standby<'a>>,
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
            let standby = holder
                .is_none()
                .then(|| standby_tender(ask, facts))
                .flatten();
            Item {
                ask,
                holder,
                from: from(ask, holder, standby.as_ref().map(|s| s.tender)),
                standby,
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

    /// What the sheet lists: the open asks, then the replied ones when they show.
    pub(crate) fn shown(self, replied: bool) -> Vec<Item<'a>> {
        let mut items = self.live;
        items.extend(self.snoozed);
        if replied {
            items.extend(self.replied);
        }
        items
    }
}

/// Closes the sheet and opens an ask's seed in the garden, or in a tile when the garden's
/// window of seeds does not hold it.
pub(crate) fn open_seed(state: &mut State, id: &str, tx: &mut Tx) {
    close(state, tx);
    if crate::garden::seeds(state).iter().any(|s| s.id == id) {
        crate::garden::open_seed(state, id, tx);
        return;
    }
    crate::garden::open_tile(state, id, tx);
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

/// The closed agent on standby that tends the ask's seed, and whether an answer resumes it:
/// a blocked resume and the `garden.tender_resume` setting can leave the answer waiting.
fn standby_tender<'a>(ask: &Ask, facts: &'a Facts) -> Option<Standby<'a>> {
    let seed = ask.seed_id.as_ref()?;
    let standby = facts
        .garden
        .of(&facts.profiles.selected)
        .iter()
        .find(|s| &s.id == seed)?
        .standby
        .as_ref()?;
    let tender = standby.tender.as_ref()?;
    let blocked = standby.resume_blocked.as_deref().unwrap_or_default();
    let answer = if !blocked.is_empty() {
        format!("Resume blocked: {blocked}. Your answer waits for it.")
    } else {
        match facts.settings.get(TENDER_RESUME).map(String::as_str) {
            Some("off") => "Seed mail does not resume it, so your answer waits for it.".into(),
            Some("watches") => {
                "Your answer waits until a reminder or pull request update resumes it.".into()
            }
            _ => "Your answer resumes it.".into(),
        }
    };
    Some(Standby { tender, answer })
}

fn from(ask: &Ask, holder: Option<&Session>, standby: Option<&StandbyTender>) -> String {
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
    if let (None, Some(tender)) = (holder, standby) {
        return format!(
            "{} · on standby",
            crate::desktop::chain::dormant_name(tender)
        );
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

/// Asks show while the setting is on or while any ask exists.
pub(crate) fn available(facts: &Facts) -> bool {
    !facts.asks.is_empty() || crate::setting::on(&facts.settings, SETTING)
}

/// The open asks the sheet counts: every one but a snoozed agent's.
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
/// asks to the counts, and, while the sheet shows them, the ages its rows and its replied
/// footer read.
pub(crate) fn next_change(state: &State, now_ms: i64) -> Option<i64> {
    let lists = Lists::of(&state.facts, now_ms);
    let wakes: Vec<i64> = lists
        .snoozed
        .iter()
        .filter_map(|item| item.snoozed_until.and_then(queue::epoch_ms))
        .collect();
    let ages: Vec<i64> = if is_open(state) {
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

    /// The ask the sheet shows: the one picked, or the first it lists.
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

pub(crate) fn is_open(state: &State) -> bool {
    state.window.layer == Some(Layer::Asks)
}

/// Opens or closes the sheet. Opening it shows the ask of the agent on screen when that agent
/// holds one, else the ask it last showed, else the first, and takes the keyboard there;
/// closing it hands the keyboard back to the agent.
pub(crate) fn toggle(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        close(state, tx);
    } else {
        show(state, None, tx);
    }
}

/// The open ask of the agent on screen: the one the sheet last showed when it is that agent's,
/// else the agent's oldest.
fn on_screen_ask(state: &State) -> Option<String> {
    let on_screen = state.window.on_screen(&state.facts)?;
    let session = state.facts.sessions.iter().find(|s| s.id == on_screen)?;
    let asks = of_session(&state.facts, session);
    let selected = state.window.asks.selected.as_deref();
    asks.iter()
        .find(|a| Some(a.id.as_str()) == selected)
        .or(asks.first())
        .map(|a| a.id.clone())
}

/// Opens the sheet on `ask`, or on the ask of the agent on screen when that agent holds one,
/// or where it was, over whatever layer is open, and takes the keyboard into it even when it
/// already shows that ask.
pub(crate) fn show(state: &mut State, ask: Option<&str>, tx: &mut Tx) {
    let ask = ask.map(str::to_owned).or_else(|| on_screen_ask(state));
    state.window.layer = Some(Layer::Asks);
    state.tick(tx);
    if let Some(ask) = ask {
        state.window.asks.selected = Some(ask);
    }
    state.window.asks.take_keyboard = true;
    follow(state, tx);
}

/// Closes the sheet, keeping the ask it shows for when it opens again.
pub(crate) fn close(state: &mut State, tx: &mut Tx) {
    if is_open(state) {
        state.window.layer = None;
    }
    state.window.asks.focused_on = None;
    tx.blur();
    crate::picker::refocus_inline(state, tx);
}

/// Keeps the sheet on the ask it shows as the lists change, forgets what was written for
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
    if !is_open(state) {
        return;
    }
    let shown = state.shown_ask();
    state.window.asks.selected = shown.as_ref().map(|(id, _)| id.clone());
    mentions::follow(state, shown.as_ref().map(|(id, _)| id.as_str()), tx);
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
        None => tx.focus(SHEET, ring),
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

/// Moves to the next or previous ask the sheet lists.
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
                answer.text = rewritten(&answer.text, value);
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

/// Sends the answers and note written for the ask shown. Once the daemon takes them the sheet
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

/// Closes the sheet and opens the agent that holds the ask shown.
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
        .filter(|s| !crate::outpost::offline(s, &state.facts.outposts))
        .cloned()
    else {
        return;
    };
    if queue::is_snoozed(holder.turn_snoozed_until.as_deref(), state.window.now_ms) {
        crate::sidebar::wake(state, &holder.id);
        return;
    }
    let name = from(ask, Some(&holder), None);
    crate::sidebar::snooze::open(state, &holder.id, name, Some(view::SNOOZE.into()), tx);
}

/// Puts the shown ask on the clipboard as the markdown its agent wrote, its subject first and
/// its id and asker last.
pub(crate) fn copy(state: &mut State, tx: &mut Tx) {
    let Some((id, _)) = state.shown_ask() else {
        return;
    };
    let items = state.asks_items();
    let Some(item) = items.iter().find(|i| i.ask.id == id) else {
        return;
    };
    tx.copy(markdown(item.ask, &item.from));
}

fn markdown(ask: &Ask, from: &str) -> String {
    let mut blocks = vec![format!("# {}", ask.subject)];
    for part in &ask.parts {
        match (&part.question, &part.markdown) {
            (Some(question), _) => blocks.push(question_block(question)),
            (None, Some(prose)) => blocks.push(prose.clone()),
            (None, None) => {}
        }
    }
    blocks.push(format!("Ask {} from {from}", ask.id));
    blocks.join("\n\n") + "\n"
}

fn question_block(question: &AskQuestion) -> String {
    let opener = match Kind::of(question) {
        Kind::One => ":::question",
        Kind::Any => ":::question-multi",
        Kind::Text => ":::question-text",
    };
    let mut lines = vec![opener.to_owned(), question.prompt.clone()];
    lines.extend(question.context.clone().filter(|c| !c.is_empty()));
    lines.extend(question.choices.iter().map(|c| match &c.description {
        Some(d) if !d.is_empty() => format!("- [ ] {} — {d}", c.label),
        _ => format!("- [ ] {}", c.label),
    }));
    lines.extend(
        question
            .recommended
            .as_ref()
            .filter(|r| !r.is_empty())
            .map(|r| format!("Recommended: {r}")),
    );
    lines.push(":::".to_owned());
    lines.join("\n")
}
