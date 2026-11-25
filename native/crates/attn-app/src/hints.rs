use std::collections::BTreeMap;

use attn_protocol::session_id;
use attn_ui::blocks::{IdLabel, IdMark, IdPlace, id_list, id_scrim, lantern, name_in_place};
use attn_ui::colors::TerminalColors;
use attn_ui::{Glyph, Tokens};
use rune::{Chord, ChordKey, ChordMod, El, Key, KeyEvent, Mods, Platform, Tx, el};

use crate::State;
use crate::desktop::role_icon;
use crate::sidebar::model::AgentState;
use crate::terminal::{Mark, Subject};

pub(crate) const DRAWING: &str = "id_labels";
pub(crate) const LANTERN: &str = "lantern";
pub(crate) const NAME_IN_PLACE: &str = "name";

const LETTERS_NO_MENU_OR_HOLD_TAKES: &[u8; 16] = b"sdfgjkeruiopnbty";

pub(crate) fn drawing(settings: &BTreeMap<String, String>) -> &'static str {
    match settings.get(DRAWING).map(String::as_str) {
        Some(NAME_IN_PLACE) => NAME_IN_PLACE,
        _ => LANTERN,
    }
}

#[derive(Default)]
pub(crate) enum Hints {
    #[default]
    Off,
    On(Held),
    Ended,
}

pub(crate) struct Held {
    mods: Mods,
    place: Option<String>,
    targets: Vec<Subject>,
    keys: usize,
    typed: String,
    shown: Shown,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct Shown {
    panes: Vec<Pane>,
    labels: Vec<Label>,
    typed: String,
    list_in: Option<String>,
    named: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Pane {
    runtime: String,
    reader: String,
    marks: Vec<Mark>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Label {
    target: Subject,
    keys: String,
    id: String,
    name: String,
    state: String,
    ink: Ink,
    icon: Icon,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Ink {
    Needs(crate::desktop::agent_card::Needs),
    Agent(AgentState),
    Seed(String),
    Closed,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Icon {
    Role(role_icon::Mark),
    Seed(String),
    Closed,
}

impl Ink {
    fn color(&self, t: &Tokens) -> rune::Color {
        match self {
            Ink::Needs(needs) => needs.ink(t),
            Ink::Agent(state) => crate::sidebar::state_color(*state, t),
            Ink::Seed(status) => crate::desktop::seed_chip::state_color(status, t),
            Ink::Closed => t.text_muted,
        }
    }
}

impl Icon {
    fn mark(&self) -> IdMark {
        match self {
            Icon::Role(role) => IdMark::Glyph(role.glyph()),
            Icon::Seed(status) => IdMark::Glyph(Glyph::Icon(attn_ui::icon::seed(status))),
            Icon::Closed => IdMark::Hollow,
        }
    }
}

impl Label {
    fn drawn(&self, typed: usize, t: &Tokens) -> IdLabel<'_> {
        IdLabel {
            keys: &self.keys,
            typed,
            mark: self.icon.mark(),
            id: &self.id,
            name: &self.name,
            state: &self.state,
            ink: self.ink.color(t),
        }
    }
}

impl Hints {
    pub(crate) fn on(&self) -> bool {
        matches!(self, Hints::On(_))
    }

    pub(crate) fn shown(&self) -> Option<&Shown> {
        match self {
            Hints::On(held) => Some(&held.shown),
            Hints::Off | Hints::Ended => None,
        }
    }
}

pub(crate) fn start(state: &mut State, tx: &mut Tx) -> bool {
    let mods = tx.mods();
    let panes = panes(state);
    if mods == Mods::default() || panes.is_empty() {
        return false;
    }
    crate::desktop::header_pop::close(state);
    let found = find(state, panes);
    let mut held = Held {
        mods,
        place: place(state),
        targets: Vec::new(),
        keys: 0,
        typed: String::new(),
        shown: Shown::default(),
    };
    for pane in &found {
        held.take_in(pane);
    }
    held.keys = keys_for(held.targets.len());
    held.shown = draw(state, &held, found);
    state.window.hints = Hints::On(held);
    true
}

pub(crate) fn follow(state: &mut State) -> bool {
    let Hints::On(held) = &state.window.hints else {
        return false;
    };
    let panes = panes(state);
    if held.place != place(state) || panes.is_empty() {
        state.window.hints = Hints::Ended;
        return true;
    }
    let found = find(state, panes);
    let Hints::On(mut held) = std::mem::take(&mut state.window.hints) else {
        return false;
    };
    for pane in &found {
        held.take_in(pane);
    }
    let shown = draw(state, &held, found);
    let changed = shown != held.shown;
    held.shown = shown;
    state.window.hints = Hints::On(held);
    changed
}

pub(crate) fn on_modifiers(state: &mut State, mods: Mods) -> bool {
    match &state.window.hints {
        Hints::On(held) if !covers(mods, held.mods) => {
            state.window.hints = Hints::Ended;
            true
        }
        _ => false,
    }
}

pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if matches!(ev.key, Key::Modifier { .. }) {
        return false;
    }
    let held = match &mut state.window.hints {
        Hints::Off => return false,
        Hints::Ended if tx.key_repeat() => return true,
        Hints::Ended => {
            state.window.hints = Hints::Off;
            return false;
        }
        Hints::On(held) => held,
    };
    if tx.key_repeat() {
        return true;
    }
    if ev.key == Key::Escape {
        state.window.hints = Hints::Ended;
        return true;
    }
    let Some(letter) = letter(ev, tx) else {
        return true;
    };
    let typed = format!("{}{letter}", held.typed);
    let picked = held
        .shown
        .labels
        .iter()
        .find(|label| label.keys.starts_with(&typed));
    match picked.filter(|label| label.keys == typed) {
        Some(label) => {
            let target = label.target.clone();
            open(state, &target, tx);
        }
        None if picked.is_some() => {
            held.shown.typed.clone_from(&typed);
            held.typed = typed;
        }
        None => {}
    }
    true
}

fn open(state: &mut State, target: &Subject, tx: &mut Tx) {
    let Hints::On(held) = std::mem::replace(&mut state.window.hints, Hints::Ended) else {
        return;
    };
    let at = held.shown.panes.into_iter().find_map(|pane| {
        let mark = pane.marks.into_iter().find(|m| m.target == *target)?;
        Some((pane.runtime, pane.reader, mark))
    });
    if let Some((runtime, reader, mark)) = at {
        crate::desktop::header_pop::pin_mark(state, &runtime, &reader, mark, tx);
    }
}

impl Held {
    fn take_in(&mut self, pane: &Pane) {
        for mark in &pane.marks {
            if !self.targets.contains(&mark.target) {
                self.targets.push(mark.target.clone());
            }
        }
    }
}

fn covers(now: Mods, held: Mods) -> bool {
    let kept = |held: bool, now: bool| !held || now;
    kept(held.shift, now.shift)
        && kept(held.cmd, now.cmd)
        && kept(held.alt, now.alt)
        && kept(held.ctrl, now.ctrl)
        && kept(held.logo, now.logo)
}

fn letter(ev: &KeyEvent, tx: &Tx) -> Option<char> {
    let mods = [
        (ev.mods.cmd, ChordMod::Primary),
        (ev.mods.ctrl, ChordMod::Ctrl),
        (ev.mods.alt, ChordMod::Alt),
        (ev.mods.shift, ChordMod::Shift),
        (ev.mods.logo, ChordMod::Super),
    ];
    LETTERS_NO_MENU_OR_HOLD_TAKES
        .iter()
        .map(|l| char::from(*l))
        .find(|l| {
            mods.iter()
                .filter(|(down, _)| *down)
                .fold(Chord::new(ChordKey::Char(*l)), |chord, (_, m)| {
                    chord.with(*m)
                })
                .matches_tx_on(ev, tx, Platform::current())
        })
}

fn keys_for(targets: usize) -> usize {
    let mut keys = 1;
    while LETTERS_NO_MENU_OR_HOLD_TAKES.len().pow(keys as u32) < targets {
        keys += 1;
    }
    keys
}

fn keys_of(target: usize, keys: usize) -> Option<String> {
    let base = LETTERS_NO_MENU_OR_HOLD_TAKES.len();
    let letter =
        |place: usize| LETTERS_NO_MENU_OR_HOLD_TAKES[target / base.pow(place as u32) % base];
    (target < base.pow(keys as u32))
        .then(|| (0..keys).rev().map(|p| char::from(letter(p))).collect())
}

fn place(state: &State) -> Option<String> {
    state
        .window
        .acting(&state.facts)
        .map(|(desktop, _)| desktop.id.clone())
}

fn panes(state: &State) -> Vec<(String, String)> {
    if !state.window.tiles_in_front() {
        return Vec::new();
    }
    let keyboard = state.window.typing_terminal(&state.facts);
    let mut panes: Vec<(String, String)> = crate::desktop::shown_panes(state)
        .into_iter()
        .filter(|pane| state.terminals.draws(&pane.runtime_id))
        .map(|pane| (pane.runtime_id.clone(), pane.session_id.clone()))
        .collect();
    panes.sort_by_key(|(runtime, _)| Some(runtime) != keyboard.as_ref());
    panes
}

fn find(state: &mut State, panes: Vec<(String, String)>) -> Vec<Pane> {
    panes
        .into_iter()
        .map(|(runtime, reader)| Pane {
            marks: state.terminals.marks_on_screen(&runtime),
            runtime,
            reader,
        })
        .collect()
}

fn draw(state: &State, held: &Held, panes: Vec<Pane>) -> Shown {
    let on_screen = |target: &Subject| {
        panes
            .iter()
            .any(|pane| pane.marks.iter().any(|mark| mark.target == *target))
    };
    let labels = held
        .targets
        .iter()
        .enumerate()
        .filter(|(_, target)| on_screen(target))
        .filter_map(|(i, target)| label(state, target, keys_of(i, held.keys)?))
        .collect();
    let named = drawing(&state.facts.settings) == NAME_IN_PLACE;
    Shown {
        list_in: panes.first().map(|pane| pane.runtime.clone()),
        panes,
        labels,
        typed: held.typed.clone(),
        named,
    }
}

fn label(state: &State, target: &Subject, keys: String) -> Option<Label> {
    let facts = &state.facts;
    let (id, name, said, ink, icon) = match target {
        Subject::Seed(id) => {
            let seeds = facts.garden.of(&facts.profiles.selected);
            let seed = seeds.iter().find(|seed| seed.id == *id)?;
            (
                id.trim_start_matches("s-").to_owned(),
                seed.title.clone(),
                crate::desktop::seed_chip::state_label(&seed.status).to_lowercase(),
                Ink::Seed(seed.status.clone()),
                Icon::Seed(seed.status.clone()),
            )
        }
        Subject::Session(id) => {
            let short = session_id::short(id);
            let short = short.trim_start_matches("ag-").to_owned();
            if let Some(session) = facts.sessions.iter().find(|s| s.id == *id) {
                let needs =
                    crate::desktop::agent_card::Needs::of(session, facts, state.window.now_ms);
                let standing = AgentState::parse(&session.state);
                let (said, ink) = match needs {
                    Some(needs) => (needs.said(), Ink::Needs(needs)),
                    None => (standing.spoken().to_owned(), Ink::Agent(standing)),
                };
                (
                    short,
                    crate::sidebar::model::display_name(session, &facts.harnesses),
                    said,
                    ink,
                    Icon::Role(role_icon::Mark::of(session.delegation_role.as_ref())),
                )
            } else {
                let entry = state.terminals.closed_session(id)?;
                (
                    short,
                    crate::ledger::title(facts, entry),
                    "closed".to_owned(),
                    Ink::Closed,
                    Icon::Closed,
                )
            }
        }
    };
    Some(Label {
        target: target.clone(),
        keys,
        id,
        name,
        state: said,
        ink,
        icon,
    })
}

pub(crate) fn layer(
    shown: &Shown,
    runtime: &str,
    size: (f32, f32),
    terminal: &TerminalColors,
    t: &Tokens,
) -> Option<El> {
    let pane = shown.panes.iter().find(|pane| pane.runtime == runtime)?;
    let typed = shown.typed.len();
    let drawing = if shown.named { name_in_place } else { lantern };
    let labels = pane.marks.iter().enumerate().filter_map(|(i, mark)| {
        let label = shown.labels.iter().find(|l| l.target == mark.target)?;
        label.keys.starts_with(&shown.typed).then(|| {
            let key = format!("terminal.{runtime}.label.{i}");
            drawing(
                key,
                &label.drawn(typed, t),
                spot(mark, &pane.marks, size),
                t,
            )
        })
    });
    let listed =
        shown.list_in.as_deref() == Some(runtime) && (!shown.named || shown.labels.is_empty());
    let list = listed.then(|| {
        let labels: Vec<IdLabel> = shown
            .labels
            .iter()
            .filter(|label| label.keys.starts_with(&shown.typed))
            .map(|label| label.drawn(typed, t))
            .collect();
        id_list(format!("terminal.{runtime}.labels"), &labels, size, t)
    });
    Some(
        el().key(format!("terminal.{runtime}.hints"))
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .inert()
            .child(id_scrim(attn_ui::colors::rgb(terminal.bg)))
            .children(labels)
            .children(list),
    )
}

fn spot(mark: &Mark, marks: &[Mark], pane: (f32, f32)) -> IdPlace {
    let at = mark.at.map(f32::from_bits);
    let next = marks
        .iter()
        .map(|other| other.at.map(f32::from_bits))
        .filter(|[x, y, ..]| *y == at[1] && *x > at[0])
        .map(|[x, ..]| x)
        .fold(pane.0, f32::min);
    IdPlace {
        at,
        room: next - at[0],
        pane,
    }
}
