use std::cell::RefCell;
use std::collections::BTreeMap;
use std::rc::Rc;

use attn_protocol::{Ask, session_id};
use attn_ui::blocks::{IdLabel, IdMark, IdPlace, id_list, id_scrim, lantern, name_in_place};
use attn_ui::colors::TerminalColors;
use attn_ui::{Glyph, Tokens};
use rune::{
    ChordKey, El, ElementInfo, Key, KeyEvent, Mods, Platform, Runtime, ShownLink, Transition, Tx,
    el,
};

use crate::State;
use crate::desktop::header_pop::{Front, Place};
use crate::desktop::role_icon;
use crate::keys::covers;
use crate::markdown::ids;
use crate::sidebar::model::AgentState;
use crate::terminal::{Mark, Subject};

pub(crate) const DRAWING: &str = "id_labels";
pub(crate) const LANTERN: &str = "lantern";
pub(crate) const NAME_IN_PLACE: &str = "name";

const LETTERS_NO_MENU_OR_HOLD_TAKES: &[u8; 16] = b"sdfgjkeruiopnbty";
const EVERY_LINK: &str = "";
const QUESTION: &str = "asks.ask";

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
    On(Box<Held>),
    Ended,
}

pub(crate) struct Held {
    mods: Mods,
    front: Front,
    readings: Vec<Reading>,
    targets: Vec<Target>,
    keys: usize,
    typed: String,
    shown: Shown,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct Shown {
    panes: Vec<Pane>,
    labels: Vec<Label>,
    typed: String,
    named: bool,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Pane {
    place: Place,
    /// The box of the window words show in; a terminal's slot holds its own.
    clip: Option<[u32; 4]>,
    spots: Vec<Spot>,
}

/// What a label opens: an id's card, or a link or a command of the ask Asks shows, as a click
/// on it opens it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Target {
    Id(Subject),
    Link { ask: String, href: String },
    Command { ask: String, n: usize },
}

/// A target where the window shows it: the bits of its x, y, width and height in points.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Spot {
    target: Target,
    at: [u32; 4],
}

impl Spot {
    fn new(target: Target, at: [f32; 4]) -> Spot {
        Spot {
            target,
            at: at.map(f32::to_bits),
        }
    }

    fn of(mark: Mark) -> Spot {
        Spot {
            target: Target::Id(mark.target),
            at: mark.at,
        }
    }
}

/// A box of the window that words show in, and the targets they show.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Reading {
    clip: [u32; 4],
    spots: Vec<Spot>,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
struct Label {
    target: Target,
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
    Reference,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
enum Icon {
    Role(role_icon::Mark),
    Seed(String),
    Closed,
    Web,
    File,
    Command,
}

impl Ink {
    fn color(&self, t: &Tokens) -> rune::Color {
        match self {
            Ink::Needs(needs) => needs.ink(t),
            Ink::Agent(state) => crate::sidebar::state_color(*state, t),
            Ink::Seed(status) => crate::desktop::seed_chip::state_color(status, t),
            Ink::Closed => t.text_muted,
            Ink::Reference => t.accent,
        }
    }
}

impl Icon {
    fn mark(&self) -> IdMark {
        match self {
            Icon::Role(role) => IdMark::Glyph(role.glyph()),
            Icon::Seed(status) => IdMark::Glyph(Glyph::Icon(attn_ui::icon::seed(status))),
            Icon::Closed => IdMark::Hollow,
            Icon::Web => IdMark::Glyph(Glyph::Icon(&attn_ui::icon::BROWSER)),
            Icon::File => IdMark::Glyph(Glyph::Icon(&attn_ui::icon::MARKDOWN)),
            Icon::Command => IdMark::Glyph(Glyph::Icon(&attn_ui::icon::TERMINAL)),
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
    if mods == Mods::default() {
        return false;
    }
    crate::desktop::header_pop::close(state);
    let found = Rc::new(RefCell::new(Vec::new()));
    let keys = elements(state);
    let links = links_read(&keys);
    if !keys.is_empty() {
        let into = found.clone();
        tx.find_elements(keys, move |_: &mut State, elements, _| {
            *into.borrow_mut() = elements;
        });
    }
    tx.find_links(links, move |state: &mut State, links, tx| {
        if !covers(tx.mods(), mods) {
            state.window.hints = Hints::Ended;
            return;
        }
        let elements = found.take();
        let mut held = Held {
            mods,
            front: Front::of(state),
            readings: readings(state, &links, &elements),
            targets: Vec::new(),
            keys: 0,
            typed: String::new(),
            shown: Shown::default(),
        };
        let panes = panes(state, &held.readings);
        if panes.is_empty() {
            return;
        }
        held.take_in(&panes);
        held.shown = draw(state, &held, panes);
        state.window.hints = Hints::On(Box::new(held));
    });
    true
}

pub(crate) fn follow(state: &mut State) -> bool {
    match &state.window.hints {
        Hints::On(held) => label_again(state, held.readings.clone()),
        Hints::Off | Hints::Ended => false,
    }
}

/// The links the last frame showed that labels may take, and the elements of the ask Asks shows,
/// while labels show.
pub(crate) fn shown(rt: &Runtime<State>) -> Option<(Vec<ShownLink>, Vec<Option<ElementInfo>>)> {
    if !rt.state().window.hints.on() {
        return None;
    }
    let keys = elements(rt.state());
    let all = if keys.is_empty() {
        Vec::new()
    } else {
        rt.elements()
    };
    let element = |key: &String| {
        let named = |e: &&ElementInfo| !e.exiting && e.key.as_ref() == Some(key);
        all.iter().find(named).cloned()
    };
    Some((
        rt.links(links_read(&keys)),
        keys.iter().map(element).collect(),
    ))
}

/// The links labels read: every link while Asks shows an ask, else the ids' alone.
fn links_read(elements: &[String]) -> &'static str {
    match elements.is_empty() {
        true => ids::HREF,
        false => EVERY_LINK,
    }
}

/// Labels what the window's words and the ask Asks shows show now. Returns whether that changed
/// what it draws.
pub(crate) fn words_drawn(
    state: &mut State,
    links: &[ShownLink],
    elements: &[Option<ElementInfo>],
) -> bool {
    let Hints::On(held) = &state.window.hints else {
        return false;
    };
    let readings = readings(state, links, elements);
    held.readings != readings && label_again(state, readings)
}

/// The ask Asks shows.
fn asked(state: &State) -> Option<&Ask> {
    if !crate::asks::is_open(state) {
        return None;
    }
    let (id, _) = state.shown_ask()?;
    state.facts.asks.iter().find(|ask| ask.id == id)
}

/// The keys of the elements labels read while Asks shows an ask: its question, then the button
/// of each of its commands.
fn elements(state: &State) -> Vec<String> {
    let Some(ask) = asked(state) else {
        return Vec::new();
    };
    let commands = crate::asks::commands::of(ask).len();
    let buttons = (0..commands).map(crate::asks::open_command_key);
    std::iter::once(QUESTION.to_owned())
        .chain(buttons)
        .collect()
}

fn readings(state: &State, links: &[ShownLink], elements: &[Option<ElementInfo>]) -> Vec<Reading> {
    let known = links.iter().filter_map(|link| {
        let target = ids::subject(&link.id).filter(|target| state.terminals.knows(target))?;
        Some((
            Spot::new(Target::Id(target), *link.lines.first()?),
            link.clip,
        ))
    });
    let found: Vec<(Spot, [f32; 4])> = known.chain(in_question(state, links, elements)).collect();
    let mut boxes: Vec<[f32; 4]> = Vec::new();
    for (_, clip) in &found {
        if !boxes.iter().any(|outer| holds(*outer, *clip)) {
            boxes.retain(|inner| !holds(*clip, *inner));
            boxes.push(*clip);
        }
    }
    let reading = |clip: &[f32; 4]| {
        let inside = found.iter().filter(|(_, own)| holds(*clip, *own));
        Reading {
            clip: clip.map(f32::to_bits),
            spots: inside.map(|(spot, _)| spot.clone()).collect(),
        }
    };
    boxes.iter().map(reading).collect()
}

/// The links the question of the ask Asks shows follows and the buttons of its commands, each
/// with the box it shows in.
fn in_question(
    state: &State,
    links: &[ShownLink],
    elements: &[Option<ElementInfo>],
) -> Vec<(Spot, [f32; 4])> {
    let (Some(ask), Some(Some(question))) = (asked(state), elements.first()) else {
        return Vec::new();
    };
    let question = question.visible;
    let document = crate::asks::document(state, ask);
    let followed = links.iter().filter_map(|link| {
        let line = *link.lines.first()?;
        let follows = ids::subject(&link.id).is_none() && document.verdict(&link.id).follows;
        let target = Target::Link {
            ask: ask.id.clone(),
            href: link.id.clone(),
        };
        (follows && holds(question, line)).then(|| (Spot::new(target, line), link.clip))
    });
    let buttons = elements[1..].iter().enumerate().filter_map(|(n, button)| {
        let at = button.as_ref()?.visible;
        let target = Target::Command {
            ask: ask.id.clone(),
            n,
        };
        (at[2] > 0.0 && at[3] > 0.0).then(|| (Spot::new(target, at), question))
    });
    followed.chain(buttons).collect()
}

fn holds([x, y, w, h]: [f32; 4], [ix, iy, iw, ih]: [f32; 4]) -> bool {
    x <= ix && y <= iy && ix + iw <= x + w && iy + ih <= y + h
}

fn label_again(state: &mut State, readings: Vec<Reading>) -> bool {
    let Hints::On(mut held) = std::mem::replace(&mut state.window.hints, Hints::Ended) else {
        return false;
    };
    let panes = panes(state, &readings);
    if held.front != Front::of(state) || panes.is_empty() {
        return true;
    }
    held.readings = readings;
    held.take_in(&panes);
    let shown = draw(state, &held, panes);
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

fn open(state: &mut State, target: &Target, tx: &mut Tx) {
    let Hints::On(held) = std::mem::replace(&mut state.window.hints, Hints::Ended) else {
        return;
    };
    match target {
        Target::Id(subject) => {
            let at = held.shown.panes.into_iter().find_map(|pane| {
                let spot = pane.spots.into_iter().find(|s| s.target == *target)?;
                Some((pane.place, spot.at))
            });
            if let Some((place, at)) = at {
                let mark = Mark {
                    target: subject.clone(),
                    at,
                };
                crate::desktop::header_pop::pin_mark(state, place, mark, tx);
            }
        }
        Target::Link { ask, href } => crate::asks::follow_href(state, ask, href, tx),
        Target::Command { ask, n } => crate::asks::panel::open_command(state, ask, *n, tx),
    }
}

impl Held {
    fn take_in(&mut self, panes: &[Pane]) {
        for spot in panes.iter().flat_map(|pane| &pane.spots) {
            if !self.targets.contains(&spot.target) {
                self.targets.push(spot.target.clone());
            }
        }
        let keys = keys_for(self.targets.len());
        if keys > self.keys {
            self.keys = keys;
            self.typed.clear();
        }
    }
}

fn letter(ev: &KeyEvent, tx: &Tx) -> Option<char> {
    LETTERS_NO_MENU_OR_HOLD_TAKES
        .iter()
        .map(|l| char::from(*l))
        .find(|l| {
            crate::keys::chord_of(ev.mods, ChordKey::Char(*l)).matches_tx_on(
                ev,
                tx,
                Platform::current(),
            )
        })
}

fn keys_for(targets: usize) -> usize {
    let mut keys = 1;
    while LETTERS_NO_MENU_OR_HOLD_TAKES.len().pow(keys as u32) < targets {
        keys += 1;
    }
    keys
}

fn keys_of(target: usize, keys: usize) -> String {
    let base = LETTERS_NO_MENU_OR_HOLD_TAKES.len();
    let letter =
        |place: usize| LETTERS_NO_MENU_OR_HOLD_TAKES[target / base.pow(place as u32) % base];
    (0..keys).rev().map(|p| char::from(letter(p))).collect()
}

/// The terminals on screen, and the boxes of words that hold ids: the terminal that has the
/// keyboard first, or the words when none has it.
fn panes(state: &mut State, readings: &[Reading]) -> Vec<Pane> {
    let words = readings.iter().map(|reading| Pane {
        place: Place::words(state),
        clip: Some(reading.clip),
        spots: reading.spots.clone(),
    });
    let mut panes: Vec<Pane> = words.collect();
    panes.extend(terminals(state));
    let keyboard = state.window.typing_terminal(&state.facts);
    let has_keyboard = |pane: &Pane| {
        let runtime = pane.place.runtime();
        runtime.is_some() && runtime == keyboard.as_deref()
    };
    panes.sort_by_key(|pane| !has_keyboard(pane));
    panes
}

fn terminals(state: &mut State) -> Vec<Pane> {
    let covered = crate::garden::mode(state) == crate::garden::Mode::Full;
    if covered || !state.window.tiles_in_front() {
        return Vec::new();
    }
    let shown: Vec<(String, String)> = crate::desktop::shown_panes(state)
        .into_iter()
        .filter(|pane| state.terminals.draws(&pane.runtime_id))
        .map(|pane| (pane.runtime_id.clone(), pane.session_id.clone()))
        .collect();
    let pane = |(runtime, reader): (String, String)| Pane {
        spots: (state.terminals.marks_on_screen(&runtime).into_iter())
            .map(Spot::of)
            .collect(),
        place: Place::Terminal { runtime, reader },
        clip: None,
    };
    shown.into_iter().map(pane).collect()
}

fn draw(state: &State, held: &Held, panes: Vec<Pane>) -> Shown {
    let on_screen = |target: &Target| {
        panes
            .iter()
            .any(|pane| pane.spots.iter().any(|spot| spot.target == *target))
    };
    let labels = held
        .targets
        .iter()
        .enumerate()
        .filter(|(_, target)| on_screen(target))
        .filter_map(|(i, target)| label(state, target, keys_of(i, held.keys)))
        .collect();
    let named = drawing(&state.facts.settings) == NAME_IN_PLACE;
    Shown {
        panes,
        labels,
        typed: held.typed.clone(),
        named,
    }
}

fn label(state: &State, target: &Target, keys: String) -> Option<Label> {
    let (id, name, said, ink, icon) = match target {
        Target::Id(subject) => id_label(state, subject)?,
        Target::Link { href, .. } => link_label(href),
        Target::Command { ask, n } => command_label(state, ask, *n)?,
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

type Said = (String, String, String, Ink, Icon);

fn link_label(href: &str) -> Said {
    let starts = |scheme: &str| {
        href.get(..scheme.len())
            .is_some_and(|s| s.eq_ignore_ascii_case(scheme))
    };
    let (name, said, icon) = if starts("http://") || starts("https://") {
        (href, "web page", Icon::Web)
    } else if starts("mailto:") {
        (&href["mailto:".len()..], "email", Icon::Web)
    } else if href.starts_with('#') {
        (href, "section", Icon::File)
    } else {
        (file_name(href), "file", Icon::File)
    };
    (
        String::new(),
        name.to_owned(),
        said.to_owned(),
        Ink::Reference,
        icon,
    )
}

fn file_name(href: &str) -> &str {
    let path = href.split(['?', '#']).next().unwrap_or(href);
    match path.trim_end_matches('/').rsplit('/').next() {
        Some(name) if !name.is_empty() => name,
        _ => href,
    }
}

fn command_label(state: &State, ask: &str, n: usize) -> Option<Said> {
    let ask = state.facts.asks.iter().find(|a| a.id == ask)?;
    let command = crate::asks::commands::of(ask).into_iter().nth(n)?;
    let first = command.text.lines().next().unwrap_or_default().to_owned();
    Some((
        String::new(),
        first,
        "command".to_owned(),
        Ink::Reference,
        Icon::Command,
    ))
}

fn id_label(state: &State, target: &Subject) -> Option<Said> {
    let facts = &state.facts;
    Some(match target {
        Subject::Seed(id) => {
            let seed = crate::garden::preview_seed(state, id)?;
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
    })
}

pub(crate) fn layer(
    shown: &Shown,
    runtime: &str,
    size: (f32, f32),
    terminal: &TerminalColors,
    t: &Tokens,
) -> Option<El> {
    let on = |pane: &Pane| pane.place.runtime() == Some(runtime);
    let at = shown.panes.iter().position(on)?;
    let ground = attn_ui::colors::rgb(terminal.bg);
    let labelled = shown.labelled(
        at,
        &format!("terminal.{runtime}"),
        [0.0, 0.0],
        size,
        ground,
        t,
    );
    Some(labelled.absolute(0.0, 0.0).w_full().h_full())
}

/// The labels over each box of the window that words show in.
pub(crate) fn words_layers(state: &State, t: &Tokens) -> Vec<El> {
    let Some(shown) = state.window.hints.shown() else {
        return Vec::new();
    };
    let panes = shown.panes.iter().enumerate();
    panes
        .filter_map(|(at, pane)| {
            let [x, y, w, h] = pane.clip?.map(f32::from_bits);
            let labelled = shown.labelled(at, &format!("words.{at}"), [x, y], (w, h), t.bg_app, t);
            Some(labelled.absolute(x, y).w(w).h(h))
        })
        .collect()
}

impl Shown {
    /// The labels of pane `at`, whose marks are placed from `origin`, over it at `size`.
    fn labelled(
        &self,
        at: usize,
        key: &str,
        [ox, oy]: [f32; 2],
        size: (f32, f32),
        ground: rune::Color,
        t: &Tokens,
    ) -> El {
        let typed = self.typed.len();
        let drawing = if self.named { name_in_place } else { lantern };
        let marks: Vec<(&Target, [f32; 4])> = (self.panes[at].spots.iter())
            .map(|spot| {
                let [x, y, w, h] = spot.at.map(f32::from_bits);
                (&spot.target, [x - ox, y - oy, w, h])
            })
            .collect();
        let labels = marks
            .iter()
            .enumerate()
            .filter_map(|(i, (target, spot_at))| {
                let label = self.labels.iter().find(|l| l.target == **target)?;
                let spot = spot(*spot_at, &marks, size);
                (label.keys.starts_with(&self.typed))
                    .then(|| drawing(format!("{key}.label.{i}"), &label.drawn(typed, t), spot, t))
            });
        let listed = at == 0 && (!self.named || self.labels.is_empty());
        let list = listed.then(|| {
            let labels: Vec<IdLabel> = (self.labels.iter())
                .filter(|label| label.keys.starts_with(&self.typed))
                .map(|label| label.drawn(typed, t))
                .collect();
            id_list(format!("{key}.labels"), &labels, size, t)
        });
        el().key(format!("{key}.hints"))
            .inert()
            .transition(Transition::none())
            .instant_changes()
            .child(id_scrim(ground, self.named))
            .children(labels)
            .children(list)
    }
}

fn spot(at: [f32; 4], marks: &[(&Target, [f32; 4])], pane: (f32, f32)) -> IdPlace {
    let next = marks
        .iter()
        .map(|(_, other)| other)
        .filter(|[x, y, ..]| *y == at[1] && *x > at[0])
        .map(|[x, ..]| *x)
        .fold(pane.0, f32::min);
    IdPlace {
        at,
        room: next - at[0],
        pane,
    }
}
