mod attach;
mod mentions;
mod saving;
mod view;

use std::collections::{BTreeMap, HashMap};

use attn_client::Facts;
use attn_protocol::{
    Ask, AskAnswer, AskAnswerMessage, AskDefault, AskFollowUpMessage, AskQuestion, Session,
    StandbyTender,
};
use rune::{TextEdit, Tx};

use crate::State;
use crate::shell::Layer;
use crate::sidebar::queue;

pub(crate) use saving::poll as save_drafts;
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
    /// The ask whose "Follows …" line is unfolded to show the earlier answer.
    pub(crate) follows_open: Option<String>,
    pub(crate) drafts: HashMap<String, Draft>,
    /// The follow-ups being written to replied asks, each kept while its ask stays listed.
    pub(crate) follow_ups: HashMap<String, TextEdit>,
    pub(crate) sending: Option<String>,
    /// Files are dragged over the sheet.
    pub(crate) dropping: bool,
    /// Files are dragged over the drop zone of the file question with this key.
    pub(crate) dropping_on: Option<String>,
    /// Why the daemon refused the answer to an ask, shown under that ask.
    pub(crate) error: Option<(String, String)>,
    /// The ask, and whether it was replied, that the keyboard last moved into.
    focused_on: Option<(String, bool)>,
    /// The keyboard moves into the sheet at the next follow, whatever it shows.
    take_keyboard: bool,
    mentions: Option<mentions::Mentions>,
    /// The first text of the selection in the ask, by its key path, once no press is still making
    /// the selection: the Quote button floats over it.
    pub(crate) quotable: Option<String>,
}

#[derive(Default)]
pub(crate) struct Draft {
    pub answers: BTreeMap<String, Answer>,
    pub note: TextEdit,
    /// The stop ⇥, the number keys and clicks last made active; the ask's first unanswered
    /// choice question until then.
    pub active: Option<Stop>,
    pub files: Vec<attach::File>,
    /// Send was pressed while a file still uploaded: the answer goes once every file is up.
    pub sends_after_upload: bool,
    generation: i64,
    saved: saving::Held,
    /// What was written, and when it is saved unless it changes before then.
    pending: Option<(serde_json::Value, f64)>,
}

/// Where ⇥ stops in an open ask, in order: each question, then the note.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Stop {
    Question(String),
    Note,
}

pub(crate) fn stops(ask: &Ask) -> Vec<Stop> {
    questions(ask)
        .filter(|q| Kind::of(q) != Kind::Unknown)
        .map(|q| Stop::Question(q.key.clone()))
        .chain([Stop::Note])
        .collect()
}

/// The stop the number keys act on in `ask`: the one last made active, else its first choice
/// question with no pick, else its first choice question, else its first stop.
pub(crate) fn active(state: &State, ask: &Ask) -> Stop {
    let draft = state.window.asks.drafts.get(&ask.id);
    if let Some(stop) = draft.and_then(|d| d.active.clone()) {
        return stop;
    }
    let choosing: Vec<&AskQuestion> = questions(ask).filter(|q| Kind::of(q).chooses()).collect();
    choosing
        .iter()
        .find(|q| !chosen(draft, q))
        .or(choosing.first())
        .copied()
        .map_or_else(
            || stops(ask).swap_remove(0),
            |q| Stop::Question(q.key.clone()),
        )
}

/// Files dropped on the sheet, outside a file question's drop zone, attach where
/// attach_target says.
pub(crate) fn drop_on_sheet(state: &mut State, paths: &[std::path::PathBuf], tx: &mut Tx) {
    if let Some(ask) = shown_open(state) {
        let to = attach_target(state, &ask);
        attach::dropped(state, &ask.id, &to, paths, tx);
    }
}

/// A paste while no field holds the keyboard attaches where attach_target says.
pub(crate) fn paste_on_sheet(state: &mut State, content: &rune::ClipboardContent, tx: &mut Tx) {
    if let Some(ask) = shown_open(state) {
        let to = attach_target(state, &ask);
        attach::paste(state, &ask.id, &to, content, tx);
    }
}

/// Where a paste outside a field or a drop on the sheet attaches in the ask: the active stop
/// when it is a file question, else the note.
pub(crate) fn attach_target(state: &State, ask: &Ask) -> Stop {
    match active(state, ask) {
        Stop::Question(key)
            if questions(ask).any(|q| q.key == key && Kind::of(q) == Kind::File) =>
        {
            Stop::Question(key)
        }
        _ => Stop::Note,
    }
}

/// A choice question has a pick, or another answer written in its Other field.
fn chosen(draft: Option<&Draft>, question: &AskQuestion) -> bool {
    draft
        .and_then(|d| d.answers.get(&question.key))
        .is_some_and(|a| !a.picked.is_empty() || !a.other.text.trim().is_empty())
}

/// The stop whose control is keyed `focused`.
fn stop_of(state: &State, ask: &Ask, focused: &str) -> Option<Stop> {
    if focused == view::NOTE {
        return Some(Stop::Note);
    }
    let draft = state.window.asks.drafts.get(&ask.id);
    if let Some(file) = draft
        .into_iter()
        .flat_map(|d| &d.files)
        .find(|f| focused == view::remove_file_key(&f.id))
    {
        return Some(file.stop.clone());
    }
    questions(ask)
        .find(|q| {
            focused == view::text_key(&q.key)
                || focused == view::drop_key(&q.key)
                || focused == view::suggestion_key(&q.key)
                || focused == view::condition_key(&q.key)
                || focused == view::add_condition_key(&q.key)
                || focused == view::other_key(&q.key)
                || (0..q.choices.len()).any(|i| focused == view::choice_key(&q.key, i))
        })
        .map(|q| Stop::Question(q.key.clone()))
}

/// The control a stop puts the keyboard in: a written question's field, a choice question's
/// chosen or first choice, the note.
fn input_of(state: &State, ask: &Ask, stop: &Stop) -> String {
    let Stop::Question(key) = stop else {
        return view::NOTE.to_owned();
    };
    let Some(question) = questions(ask).find(|q| &q.key == key) else {
        return view::NOTE.to_owned();
    };
    match Kind::of(question) {
        Kind::Text => return view::text_key(key),
        Kind::File => return view::drop_key(key),
        Kind::Unknown => return view::NOTE.to_owned(),
        Kind::One | Kind::Any => {}
    }
    let picked = state
        .window
        .asks
        .drafts
        .get(&ask.id)
        .and_then(|d| d.answers.get(key))
        .and_then(|a| a.picked.first());
    match question
        .choices
        .iter()
        .position(|c| Some(&c.label) == picked)
        .or((!question.choices.is_empty()).then_some(0))
    {
        Some(i) => view::choice_key(key, i),
        None => view::other_key(key),
    }
}

/// The controls ⇥ visits in a stop, in order: its input, then a choice question's condition
/// once it has a pick, or a written question's Use the suggestion while its field is empty.
fn controls(state: &State, ask: &Ask, stop: &Stop) -> Vec<String> {
    let mut controls = vec![input_of(state, ask, stop)];
    let draft = state.window.asks.drafts.get(&ask.id);
    let removes = draft
        .into_iter()
        .flat_map(|d| d.files_on(stop))
        .map(|f| view::remove_file_key(&f.id));
    let Stop::Question(key) = stop else {
        controls.extend(removes);
        controls.push(view::ATTACH.to_owned());
        return controls;
    };
    controls.extend(removes);
    let answer = state
        .window
        .asks
        .drafts
        .get(&ask.id)
        .and_then(|d| d.answers.get(key));
    if answer.is_some_and(|a| !a.picked.is_empty()) {
        controls.push(match answer.and_then(|a| a.condition.as_ref()) {
            Some(_) => view::condition_key(key),
            None => view::add_condition_key(key),
        });
    }
    let suggested = questions(ask).any(|q| {
        &q.key == key
            && Kind::of(q) == Kind::Text
            && q.recommended.as_ref().is_some_and(|r| !r.is_empty())
    });
    let written = state
        .window
        .asks
        .drafts
        .get(&ask.id)
        .and_then(|d| d.answers.get(key))
        .is_some_and(|a| !a.text.text.trim().is_empty());
    if suggested && !written {
        controls.push(view::suggestion_key(key));
    }
    controls
}

/// Makes `stop` of the ask shown active and puts the keyboard in its control.
fn enter(state: &mut State, ask: &Ask, stop: Stop, tx: &mut Tx) {
    let input = input_of(state, ask, &stop);
    focus_in(state, ask, stop, input, tx);
}

fn focus_in(state: &mut State, ask: &Ask, stop: Stop, control: String, tx: &mut Tx) {
    activate(state, &ask.id, stop);
    tx.focus(control, tx.by_keys() || tx.focus_visible());
}

/// Makes `stop` of `ask` active, as a click inside it does.
pub(crate) fn activate(state: &mut State, ask: &str, stop: Stop) {
    state.draft(ask).active = Some(stop);
}

fn shown_open(state: &State) -> Option<Ask> {
    let (id, replied) = state.shown_ask()?;
    if replied {
        return None;
    }
    state.facts.asks.iter().find(|a| a.id == id).cloned()
}

/// ⇥ and ⇧⇥: moves the active stop of the ask shown `by` one, from the stop the keyboard is in.
/// Past the first or last stop, and from any other control of the sheet, the key goes on to
/// move focus through the sheet's controls as Tab does.
pub(crate) fn tab(state: &mut State, by: isize, tx: &mut Tx) -> bool {
    let Some(ask) = shown_open(state) else {
        return false;
    };
    let focused = tx.focused().filter(|f| *f != SHEET).map(str::to_owned);
    let here = match &focused {
        Some(focused) => match stop_of(state, &ask, focused) {
            Some(stop) => stop,
            None => return false,
        },
        None => active(state, &ask),
    };
    let inside = controls(state, &ask, &here);
    let within = focused
        .and_then(|f| inside.iter().position(|c| *c == f))
        .and_then(|at| step_in(&inside, at, by));
    if let Some(control) = within {
        focus_in(state, &ask, here, control, tx);
        return true;
    }
    let stops = stops(&ask);
    let at = stops.iter().position(|s| *s == here).unwrap_or(0) as isize;
    let Some(next) = step_in(&stops, at as usize, by) else {
        return false;
    };
    let mut entered = controls(state, &ask, &next);
    let control = if by > 0 {
        entered.swap_remove(0)
    } else {
        entered
            .pop()
            .unwrap_or_else(|| input_of(state, &ask, &next))
    };
    focus_in(state, &ask, next, control, tx);
    true
}

/// The item `by` one from `at` in `items`, if there is one.
fn step_in<T: Clone>(items: &[T], at: usize, by: isize) -> Option<T> {
    let to = at as isize + by;
    usize::try_from(to)
        .ok()
        .and_then(|to| items.get(to))
        .cloned()
}

/// A number key `n`: picks choice `n` of the active question; the number after its last choice
/// puts the keyboard in its Other field.
pub(crate) fn number(state: &mut State, n: usize, tx: &mut Tx) -> bool {
    let Some(ask) = shown_open(state) else {
        return false;
    };
    let Stop::Question(key) = active(state, &ask) else {
        return false;
    };
    let Some(question) = questions(&ask).find(|q| q.key == key).cloned() else {
        return false;
    };
    let kind = Kind::of(&question);
    if !kind.chooses() || n == 0 {
        return false;
    }
    if n == question.choices.len() + 1 {
        activate(state, &ask.id, Stop::Question(key.clone()));
        tx.focus(view::other_key(&key), tx.by_keys() || tx.focus_visible());
        return true;
    }
    let Some(choice) = question.choices.get(n - 1) else {
        return false;
    };
    activate(state, &ask.id, Stop::Question(key.clone()));
    pick(state, &ask.id, &key, kind, &choice.label, tx);
    true
}

/// Picks `label` in a question of `ask`. A pick in a one-choice question moves the active
/// question, and the keyboard, on to the next choice question with no pick.
pub(crate) fn pick(
    state: &mut State,
    ask: &str,
    question: &str,
    kind: Kind,
    label: &str,
    tx: &mut Tx,
) {
    choose(state, ask, question, kind, label);
    if kind != Kind::One {
        return;
    }
    let Some(found) = state.facts.asks.iter().find(|a| a.id == ask).cloned() else {
        return;
    };
    let draft = state.window.asks.drafts.get(ask);
    let unpicked = |q: &&&AskQuestion| Kind::of(q).chooses() && !chosen(draft, q);
    let all: Vec<&AskQuestion> = questions(&found).collect();
    let at = all.iter().position(|q| q.key == question).unwrap_or(0);
    let next = all[at + 1..]
        .iter()
        .chain(&all[..at])
        .find(unpicked)
        .map(|q| Stop::Question(q.key.clone()));
    if let Some(next) = next {
        enter(state, &found, next, tx);
    }
}

/// Appends the text selected in the ask shown to its note as `> ` lines, and puts the
/// keyboard at the note's end.
pub(crate) fn quote(state: &mut State, tx: &mut Tx) -> bool {
    let Some(ask) = shown_open(state) else {
        return false;
    };
    let Some(passage) = tx
        .text_selection()
        .map(|s| s.text().trim().to_owned())
        .filter(|s| !s.is_empty())
    else {
        return false;
    };
    let quoted: Vec<String> = passage
        .lines()
        .map(|line| format!("> {}", line.trim()).trim_end().to_owned())
        .collect();
    let note = state.note(&ask.id);
    let written = note.text.trim_end();
    let text = if written.is_empty() {
        format!("{}\n", quoted.join("\n"))
    } else {
        format!("{written}\n\n{}\n", quoted.join("\n"))
    };
    *note = rewritten(note, text);
    state.window.asks.quotable = None;
    tx.clear_text_selection();
    enter(state, &ask, Stop::Note, tx);
    true
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
    /// The condition on the pick, once "Add a condition" opened its field.
    pub condition: Option<TextEdit>,
}

impl Answer {
    /// Replaces the picks. The condition qualified the picks it was written for, so it goes.
    fn replace_picks(&mut self, picked: Vec<String>) {
        if self.picked != picked {
            self.condition = None;
        }
        self.picked = picked;
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    One,
    Any,
    Text,
    File,
    /// A kind a newer daemon asks that this app cannot answer.
    Unknown,
}

impl Kind {
    pub(crate) fn of(question: &AskQuestion) -> Kind {
        match question.kind.as_str() {
            "one" => Kind::One,
            "any" => Kind::Any,
            "text" => Kind::Text,
            "file" => Kind::File,
            _ => Kind::Unknown,
        }
    }

    pub(crate) fn chooses(self) -> bool {
        matches!(self, Kind::One | Kind::Any)
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

/// The choice the agent goes with when no answer comes in time, and when.
pub(crate) fn default_of(ask: &Ask) -> Option<&AskDefault> {
    questions(ask).find_map(|q| q.default.as_ref())
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
                let stop = Stop::Question(q.key.clone());
                let files: Vec<String> = self.files_on(&stop).map(|f| f.id.clone()).collect();
                if !files.is_empty() {
                    return Some(AskAnswer {
                        key: q.key.clone(),
                        picked: Vec::new(),
                        attachments: Some(files),
                        ..AskAnswer::default()
                    });
                }
                let answer = self.answers.get(&q.key)?;
                let picked = &answer.picked;
                let other = answer.other.text.trim();
                let text = answer.text.text.trim();
                if picked.is_empty() && other.is_empty() && text.is_empty() {
                    return None;
                }
                let condition = answer
                    .condition
                    .as_ref()
                    .map(|c| c.text.trim())
                    .filter(|c| !c.is_empty() && !picked.is_empty());
                Some(AskAnswer {
                    key: q.key.clone(),
                    picked: picked.to_vec(),
                    other: (!other.is_empty()).then(|| other.to_owned()),
                    text: (!text.is_empty()).then(|| text.to_owned()),
                    condition: condition.map(str::to_owned),
                    attachments: None,
                })
            })
            .collect()
    }

    /// The files attached at a stop: a file question, or the note.
    pub(crate) fn files_on<'a>(&'a self, stop: &'a Stop) -> impl Iterator<Item = &'a attach::File> {
        self.files.iter().filter(move |f| f.stop == *stop)
    }

    pub(crate) fn sendable(&self, ask: &Ask) -> bool {
        let written = !self.answers(ask).is_empty()
            || !self.note.text.trim().is_empty()
            || !self.files.is_empty();
        written && self.files.iter().all(|f| f.failed().is_none())
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
            .flat_map(|item| std::iter::once(age_from(item.ask)).chain(amended_ats(item.ask)))
            .filter_map(|at| queue::next_age_change(Some(at), now_ms))
            .collect()
    } else {
        Vec::new()
    };
    wakes.into_iter().chain(ages).min()
}

fn amended_ats(ask: &Ask) -> impl Iterator<Item = &str> {
    ask.parts.iter().filter_map(|p| p.amended_at.as_deref())
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
    let listed: Vec<String> = state.facts.asks.iter().map(|a| a.id.clone()).collect();
    state
        .window
        .asks
        .follow_ups
        .retain(|id, _| listed.contains(id));
    saving::restore(state);
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
    state.window.asks.quotable = None;
    let ring = tx.by_keys() || tx.focus_visible();
    match shown_open(state) {
        Some(ask) => {
            let input = input_of(state, &ask, &active(state, &ask));
            tx.focus(input, ring)
        }
        None => tx.focus(SHEET, ring),
    }
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

fn choose(state: &mut State, ask: &str, question: &str, kind: Kind, label: &str) {
    let answer = state.answer(ask, question);
    match kind {
        Kind::One => {
            answer.replace_picks(vec![label.to_owned()]);
            answer.other = TextEdit::default();
        }
        Kind::Any => {
            if let Some(at) = answer.picked.iter().position(|p| p == label) {
                answer.picked.remove(at);
            } else {
                answer.picked.push(label.to_owned());
            }
            if answer.picked.is_empty() {
                answer.condition = None;
            }
        }
        Kind::Text | Kind::File | Kind::Unknown => {}
    }
}

/// A one-choice question's written answer replaces its pick.
pub(crate) fn other_edited(state: &mut State, ask: &str, question: &str, kind: Kind) {
    if kind == Kind::One {
        state.answer(ask, question).replace_picks(Vec::new());
    }
}

/// Opens the field for a condition on the question's pick and moves the keyboard into it.
pub(crate) fn add_condition(state: &mut State, ask: &str, question: &str, tx: &mut Tx) {
    state
        .answer(ask, question)
        .condition
        .get_or_insert_with(TextEdit::default);
    activate(state, ask, Stop::Question(question.to_owned()));
    let ring = tx.by_keys() || tx.focus_visible();
    tx.focus(view::condition_key(question), ring);
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
                answer.replace_picks(Vec::new());
                answer.other = TextEdit::default();
            }
            Kind::One | Kind::Any => {
                answer.replace_picks(vec![value]);
                answer.other = TextEdit::default();
                answer.text = TextEdit::default();
            }
            Kind::File | Kind::Unknown => {}
        }
    }
}

/// Sends the answers and note written for the ask shown, or the follow-up written to it once
/// replied. Once the daemon takes an answer the sheet moves to the ask after it in its section,
/// or before it, or stays to show the reply; a refusal keeps what was written and says why.
pub(crate) fn send(state: &mut State, tx: &mut Tx) {
    match state.shown_ask() {
        Some((id, false)) => send_ask(state, id, tx),
        Some((id, true)) => send_follow_up(state, id, tx),
        None => {}
    }
}

fn send_ask(state: &mut State, id: String, tx: &mut Tx) {
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
    if draft.files.iter().any(attach::File::uploading) {
        draft.sends_after_upload = true;
        state.window.asks.error = None;
        return;
    }
    let answers = draft.answers(&ask);
    let note = draft.note.text.trim().to_owned();
    let attachments: Vec<String> = draft.files_on(&Stop::Note).map(|f| f.id.clone()).collect();
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
            attachments: (!attachments.is_empty()).then_some(attachments),
            ..AskAnswerMessage::new()
        },
        move |state, answer, tx| {
            state.window.asks.sending = None;
            match answer.outcome() {
                Ok(_) if state.shown_ask().is_some_and(|(shown, _)| shown == ask.id) => {
                    state.window.asks.selected = after.or_else(|| Some(ask.id.clone()));
                    follow(state, tx);
                }
                Ok(_) => {}
                Err(why) => state.window.asks.error = Some((ask.id.clone(), why)),
            }
        },
        tx,
    );
}

/// A file of `ask`'s answer finished uploading, failed or was removed: a send that waited on
/// its files goes once none is uploading, and stops when one failed.
fn uploaded(state: &mut State, ask: &str, tx: &mut Tx) {
    let Some(draft) = state.window.asks.drafts.get_mut(ask) else {
        return;
    };
    if !draft.sends_after_upload || draft.files.iter().any(attach::File::uploading) {
        return;
    }
    draft.sends_after_upload = false;
    if draft.files.iter().all(|f| f.failed().is_none()) {
        send_ask(state, ask.to_owned(), tx);
    }
}

/// Opens the follow-up field of the replied ask shown and takes the keyboard into it.
pub(crate) fn follow_up(state: &mut State, tx: &mut Tx) {
    let Some((id, true)) = state.shown_ask() else {
        return;
    };
    state.window.asks.follow_ups.entry(id).or_default();
    let ring = tx.by_keys() || tx.focus_visible();
    tx.focus(view::FOLLOW_UP, ring);
}

pub(crate) fn follow_up_edit<'a>(state: &'a mut State, ask: &str) -> &'a mut TextEdit {
    state
        .window
        .asks
        .follow_ups
        .entry(ask.to_owned())
        .or_default()
}

/// Sends what was written to follow up on a replied ask to whoever holds it now. Once the
/// daemon takes it the field closes and a notice says who it went to; a refusal keeps it and
/// says why.
fn send_follow_up(state: &mut State, id: String, tx: &mut Tx) {
    if state.window.asks.sending.is_some() {
        return;
    }
    let Some(text) = state
        .window
        .asks
        .follow_ups
        .get(&id)
        .map(|edit| edit.text.trim().to_owned())
        .filter(|text| !text.is_empty())
    else {
        return;
    };
    let to = state
        .asks_items()
        .iter()
        .find(|i| i.ask.id == id)
        .map(|i| i.from.clone())
        .unwrap_or_default();
    state.window.asks.sending = Some(id.clone());
    state.window.asks.error = None;
    state.ask(
        {
            let id = id.clone();
            |request_id| AskFollowUpMessage {
                request_id,
                ask_id: id,
                text,
                ..AskFollowUpMessage::new()
            }
        },
        move |state, answer, tx| {
            state.window.asks.sending = None;
            match answer.outcome() {
                Ok(_) => {
                    state.window.asks.follow_ups.remove(&id);
                    state.window.notify(format!("Follow-up sent to {to}"), tx);
                    if is_open(state) {
                        tx.focus(SHEET, tx.by_keys() || tx.focus_visible());
                    }
                }
                Err(why) => state.window.asks.error = Some((id, why)),
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
            (Some(question), _) => blocks.push(question_block(question, &ask.created_at)),
            (None, Some(prose)) => blocks.push(match &part.amended_at {
                Some(at) => format!("Updated {at}:\n\n{}", prose.trim_end()),
                None => prose.clone(),
            }),
            (None, None) => {}
        }
    }
    blocks.push(format!("Ask {} from {from}", ask.id));
    blocks.join("\n\n") + "\n"
}

fn question_block(question: &AskQuestion, opened_at: &str) -> String {
    let opener = match Kind::of(question) {
        Kind::One => ":::question".to_owned(),
        Kind::Any => ":::question-multi".to_owned(),
        Kind::Text => ":::question-text".to_owned(),
        Kind::File => ":::question-file".to_owned(),
        Kind::Unknown => format!(":::question-{}", question.kind),
    };
    let mut lines = vec![opener, question.prompt.clone()];
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
    lines.extend(question.default.as_ref().and_then(|d| {
        let waited = queue::epoch_ms(&d.due_at)? - queue::epoch_ms(opened_at)?;
        Some(format!("Default: {} after {}", d.choice, duration(waited)))
    }));
    lines.push(":::".to_owned());
    lines.join("\n")
}

/// A wait as the agent wrote it after `Default: <choice> after`: whole hours, minutes and
/// seconds, such as 2h, 1h30m or 45s. The due time drops the fraction of a second the ask
/// opened at, so the wait rounds to the nearest second.
fn duration(waited_ms: i64) -> String {
    let seconds = (waited_ms + 500) / 1000;
    let (h, m, s) = (seconds / 3600, seconds / 60 % 60, seconds % 60);
    let mut out = String::new();
    for (n, unit) in [(h, "h"), (m, "m"), (s, "s")] {
        if n > 0 {
            out.push_str(&format!("{n}{unit}"));
        }
    }
    if out.is_empty() { "0s".into() } else { out }
}
