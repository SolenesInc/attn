use std::sync::Arc;

use attn_protocol::{Seed, SeedNoteMessage, SeedTransitionMessage, SeedTransitionResultMessage};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{
    El, Key, KeyEvent, Phase, ScrollSpec, TextEdit, Tx, col, el, memo, row, text, text_input,
};

use super::model::{self, SessionNames};
use crate::State;
use crate::sidebar::queue::epoch_ms;

pub(super) const BOARD: &str = "garden.board";
const COMPOSE_FIELD: &str = "garden.board.compose.field";
const CARD_PREFIX: &str = "garden.board.card.";
const COLUMN_PREFIX: &str = "garden.board.column.";
const ZONE_PREFIX: &str = "garden.board.zone.";
const MENU_PREFIX: &str = "garden.board.menu.";
/// Points a press on a card travels before it becomes a drag, as in the Tauri board.
const DRAG_THRESHOLD: f32 = 5.0;
const CARRY_OFFSET: f32 = 16.0;
const CARRY_WIDTH: f32 = 320.0;

/// A board column, the lifecycle state its seeds share.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Column {
    Ready,
    Growing,
    Parked,
    Closed,
}

const COLUMNS: [Column; 4] = [
    Column::Ready,
    Column::Growing,
    Column::Parked,
    Column::Closed,
];

impl Column {
    fn label(self) -> &'static str {
        match self {
            Column::Ready => "Ready",
            Column::Growing => "In progress",
            Column::Parked => "Parked",
            Column::Closed => "Closed",
        }
    }

    fn name(self) -> &'static str {
        match self {
            Column::Ready => "ready",
            Column::Growing => "growing",
            Column::Parked => "parked",
            Column::Closed => "closed",
        }
    }

    fn named(name: &str) -> Option<Column> {
        COLUMNS.into_iter().find(|c| c.name() == name)
    }
}

/// A move between lifecycle states the board offers.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Verb {
    Park,
    Harvest,
    Wither,
    Replant,
}

const VERBS: [Verb; 4] = [Verb::Park, Verb::Harvest, Verb::Wither, Verb::Replant];

impl Verb {
    fn name(self) -> &'static str {
        match self {
            Verb::Park => "park",
            Verb::Harvest => "harvest",
            Verb::Wither => "wither",
            Verb::Replant => "replant",
        }
    }

    fn named(name: &str) -> Option<Verb> {
        VERBS.into_iter().find(|v| v.name() == name)
    }

    fn label(self) -> &'static str {
        match self {
            Verb::Park => "Park",
            Verb::Harvest => "Harvest",
            Verb::Wither => "Wither",
            Verb::Replant => "Replant",
        }
    }

    /// What the compose line asks the user to write down.
    fn prompt(self) -> &'static str {
        match self {
            Verb::Harvest => "what got done",
            Verb::Wither => "why nobody should pick this up",
            Verb::Park => "what you are leaving it at",
            Verb::Replant => "why it is open again",
        }
    }

    fn required(self) -> bool {
        self == Verb::Harvest
    }

    /// Harvest and wither store the words on the seed as its reason; the other moves refuse one.
    fn reason_on_seed(self) -> bool {
        matches!(self, Verb::Harvest | Verb::Wither)
    }

    fn target(self) -> Column {
        match self {
            Verb::Harvest | Verb::Wither => Column::Closed,
            Verb::Park => Column::Parked,
            Verb::Replant => Column::Ready,
        }
    }
}

pub(crate) fn column_of(seed: &Seed) -> Column {
    match seed.status.as_str() {
        "harvested" | "withered" => Column::Closed,
        "parked" => Column::Parked,
        "growing" => Column::Growing,
        _ => Column::Ready,
    }
}

/// The verbs that move a seed in `status` into `target`: the garden's lifecycle table
/// (internal/garden/lifecycle.go) read from the board's side.
pub(crate) fn legal_verbs(status: &str, target: Column) -> Vec<Verb> {
    let open = matches!(status, "planted" | "growing" | "parked");
    match target {
        Column::Closed if open => vec![Verb::Harvest, Verb::Wither],
        Column::Parked if matches!(status, "planted" | "growing") => vec![Verb::Park],
        Column::Ready if status != "planted" => vec![Verb::Replant],
        _ => Vec::new(),
    }
}

/// The verbs a card offers on its own; the menu and the drag read the same table.
pub(crate) fn verbs_for(status: &str) -> Vec<Verb> {
    [
        Column::Growing,
        Column::Parked,
        Column::Closed,
        Column::Ready,
    ]
    .into_iter()
    .flat_map(|column| legal_verbs(status, column))
    .collect()
}

/// The board's own walk and what it is in the middle of. The list keeps its own walk.
#[derive(Default)]
pub(crate) struct Board {
    trail: Vec<String>,
    closed_open: bool,
    selected: Option<String>,
    menu_for: Option<String>,
    compose: Option<Compose>,
    words: TextEdit,
    error: Option<String>,
    busy: bool,
    drag: Option<Drag>,
}

struct Compose {
    seed: Arc<Seed>,
    verb: Verb,
}

struct Drag {
    seed: Arc<Seed>,
    start: [f32; 2],
    at: [f32; 2],
    armed: bool,
    column: Option<Column>,
    zone: Option<Verb>,
}

/// The seeds of the place the board stands in, sorted into columns.
struct Place {
    index: model::Index,
    trail: Vec<String>,
    scoped: usize,
    columns: [Vec<Arc<Seed>>; 4],
    ready: usize,
}

impl Place {
    fn of(state: &State) -> Place {
        let index = model::Index::of(super::seeds(state));
        let trail: Vec<String> = state
            .window
            .garden
            .board_walk
            .trail
            .iter()
            .take_while(|id| index.by_id.contains_key(*id))
            .cloned()
            .collect();
        let scoped = match trail.last() {
            Some(plot) => index.children_of(plot),
            None => &index.roots,
        };
        let mut columns: [Vec<Arc<Seed>>; 4] = Default::default();
        for seed in scoped {
            columns[slot(column_of(seed))].push(seed.clone());
        }
        let at = |iso: &str| epoch_ms(iso).unwrap_or(0);
        columns[0].sort_by_key(|s| (!s.ready, at(&s.created_at)));
        for column in &mut columns[1..] {
            column.sort_by_key(|s| std::cmp::Reverse(at(&s.updated_at)));
        }
        let ready = columns[0].iter().filter(|s| s.ready).count();
        let scoped = scoped.len();
        Place {
            index,
            trail,
            scoped,
            columns,
            ready,
        }
    }

    fn column(&self, column: Column) -> &[Arc<Seed>] {
        &self.columns[slot(column)]
    }

    fn count(&self, column: Column) -> usize {
        match column {
            Column::Ready => self.ready,
            other => self.column(other).len(),
        }
    }

    /// The columns the arrows walk: every one, and Closed once it is open.
    fn lanes(&self, closed_open: bool) -> Vec<Column> {
        COLUMNS
            .into_iter()
            .filter(|c| *c != Column::Closed || closed_open)
            .collect()
    }

    fn find(&self, id: &str) -> Option<(Column, usize)> {
        COLUMNS.into_iter().find_map(|column| {
            self.column(column)
                .iter()
                .position(|s| s.id == id)
                .map(|row| (column, row))
        })
    }
}

fn slot(column: Column) -> usize {
    COLUMNS.iter().position(|c| *c == column).unwrap_or(0)
}

fn card_key(id: &str) -> String {
    format!("{CARD_PREFIX}{id}")
}

fn held_by_other(seed: &Seed, names: &SessionNames) -> String {
    if seed.claimed {
        model::tender_of(seed, names)
    } else {
        String::new()
    }
}

/// How long ago `iso` was, as a card says it: now, 5m, 3h, 2d.
fn age(iso: &str, now_ms: i64) -> String {
    let Some(at) = epoch_ms(iso) else {
        return String::new();
    };
    let seconds = ((now_ms - at) as f64 / 1000.0).round() as i64;
    if seconds < 60 {
        "now".into()
    } else if seconds < 3600 {
        format!("{}m", (seconds as f64 / 60.0).round() as i64)
    } else if seconds < 86_400 {
        format!("{}h", (seconds as f64 / 3600.0).round() as i64)
    } else {
        format!("{}d", (seconds as f64 / 86_400.0).round() as i64)
    }
}

fn plot_counts(seed: &Seed) -> String {
    let Some(p) = &seed.plot_progress else {
        return String::new();
    };
    let mut parts = Vec::new();
    for (count, word) in [
        (p.growing, "in progress"),
        (p.ready, "ready"),
        (p.blocked, "blocked"),
        (p.parked, "parked"),
    ] {
        if count > 0 {
            parts.push(format!("{count} {word}"));
        }
    }
    parts.push(format!("{}/{} done", p.done, p.total));
    parts.join(" · ")
}

fn empty_line(column: Column, place: &Place) -> String {
    if place.scoped == 0 {
        return "Nothing planted here yet.".into();
    }
    match column {
        Column::Ready if !place.column(Column::Growing).is_empty() => {
            "Nothing to pick up — everything open is being worked.".into()
        }
        Column::Ready if !place.column(Column::Parked).is_empty() => format!(
            "Nothing to pick up. {} parked.",
            place.column(Column::Parked).len()
        ),
        Column::Ready => "Nothing to pick up here.".into(),
        Column::Growing => "Nobody is working on anything here.".into(),
        Column::Parked => "Nothing put down.".into(),
        Column::Closed => "Nothing has finished here yet.".into(),
    }
}

fn board(state: &mut State) -> &mut Board {
    &mut state.window.garden.board_walk
}

fn select(state: &mut State, id: &str, tx: &mut Tx) {
    board(state).selected = Some(id.to_owned());
    tx.focus(card_key(id), true);
}

fn drill(state: &mut State, id: &str, tx: &mut Tx) {
    let walk = board(state);
    walk.selected = None;
    walk.menu_for = None;
    walk.trail.push(id.to_owned());
    tx.focus(BOARD, false);
}

fn climb_to(state: &mut State, depth: usize, tx: &mut Tx) {
    let walk = board(state);
    walk.selected = None;
    walk.menu_for = None;
    walk.trail.truncate(depth);
    tx.focus(BOARD, false);
}

fn toggle_menu(state: &mut State, id: &str, tx: &mut Tx) {
    let walk = board(state);
    if walk.menu_for.as_deref() == Some(id) {
        walk.menu_for = None;
        tx.focus(card_key(id), true);
        return;
    }
    walk.menu_for = Some(id.to_owned());
    walk.selected = Some(id.to_owned());
    let first = state
        .window
        .garden
        .board_walk
        .menu_for
        .as_ref()
        .and_then(|id| super::seeds(state).iter().find(|s| s.id == *id))
        .and_then(|seed| verbs_for(&seed.status).first().copied());
    match first {
        Some(verb) => tx.focus(format!("{MENU_PREFIX}{}", verb.name()), true),
        None => tx.focus(card_key(id), true),
    }
}

fn press_card(state: &mut State, id: &str, tx: &mut Tx) {
    let plot = super::seeds(state)
        .iter()
        .find(|s| s.id == id)
        .is_some_and(|s| model::is_plot(s));
    board(state).selected = Some(id.to_owned());
    if plot {
        drill(state, id, tx);
    } else {
        toggle_menu(state, id, tx);
    }
}

fn begin_verb(state: &mut State, seed: Arc<Seed>, verb: Verb, tx: &mut Tx) {
    let walk = board(state);
    walk.menu_for = None;
    walk.error = None;
    walk.drag = None;
    walk.compose = Some(Compose { seed, verb });
    walk.words = TextEdit::default();
    tx.focus(COMPOSE_FIELD, true);
}

fn cancel_compose(state: &mut State, tx: &mut Tx) {
    let walk = board(state);
    if walk.busy {
        return;
    }
    let seed = walk.compose.take().map(|c| c.seed.id.clone());
    walk.error = None;
    match seed {
        Some(id) => tx.focus(card_key(&id), true),
        None => tx.focus(BOARD, false),
    }
}

/// Sends the composed move. Harvest and wither keep the words on the seed as its reason and a
/// park keeps them as its comment; a replant writes them on the log first, as a note.
fn commit(state: &mut State, tx: &mut Tx) {
    let names = model::session_names(&state.facts.sessions);
    let walk = board(state);
    let Some(compose) = walk.compose.as_ref().filter(|_| !walk.busy) else {
        return;
    };
    let words = walk.words.text.trim().to_owned();
    let (seed, verb) = (compose.seed.clone(), compose.verb);
    if verb.required() && words.is_empty() {
        walk.error = Some(format!(
            "{}ing {} records {}",
            verb.label().to_lowercase(),
            seed.id,
            verb.prompt()
        ));
        tx.focus(COMPOSE_FIELD, true);
        return;
    }
    walk.busy = true;
    let force = !held_by_other(&seed, &names).is_empty();
    let said = (!words.is_empty()).then_some(words);
    let note = said.clone().filter(|_| verb == Verb::Replant);
    let id = seed.id.clone();
    let transition = move |state: &mut State, tx: &mut Tx| {
        let seed_id = seed.id.clone();
        state.ask(
            |request_id| SeedTransitionMessage {
                seed_id,
                verb: verb.name().into(),
                reason: said.clone().filter(|_| verb.reason_on_seed()),
                comment: said.clone().filter(|_| verb == Verb::Park),
                force: force.then_some(true),
                request_id: Some(request_id),
                ..SeedTransitionMessage::new()
            },
            move |state, answer, tx| {
                let outcome = answer.outcome().and_then(|reply| {
                    reply
                        .parse::<SeedTransitionResultMessage>()
                        .map(|_| ())
                        .ok_or_else(|| "The move was refused".to_owned())
                });
                moved(state, &seed.id, outcome, tx);
            },
            tx,
        );
    };
    let Some(body) = note else {
        return transition(state, tx);
    };
    let seed_id = id.clone();
    state.ask(
        |request_id| SeedNoteMessage {
            seed_id,
            body,
            request_id: Some(request_id),
            ..SeedNoteMessage::new()
        },
        move |state, answer, tx| match answer.outcome() {
            Ok(_) => transition(state, tx),
            Err(reason) => moved(state, &id, Err(reason), tx),
        },
        tx,
    );
}

fn moved(state: &mut State, id: &str, outcome: Result<(), String>, tx: &mut Tx) {
    let walk = board(state);
    walk.busy = false;
    if let Err(reason) = outcome {
        walk.error = Some(reason);
        tx.focus(COMPOSE_FIELD, true);
        return;
    }
    walk.compose = None;
    walk.error = None;
    walk.selected = Some(id.to_owned());
    tx.focus(card_key(id), true);
}

/// Escape on the board puts away the innermost thing it is doing: the compose line, then the
/// menu, then a drag. With none, the garden closes.
fn escape(state: &mut State, tx: &mut Tx) -> bool {
    let walk = board(state);
    if walk.compose.is_some() {
        cancel_compose(state, tx);
        return true;
    }
    if let Some(id) = walk.menu_for.take() {
        tx.focus(card_key(&id), true);
        return true;
    }
    if walk.drag.take().is_some() {
        return true;
    }
    super::escape(state)
}

/// The board's keys: the arrows walk the cards down a column and across the columns, → on a
/// plot opens it, ← climbs out from the first column, and Return opens the card's menu.
fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key == Key::Escape {
        return escape(state, tx);
    }
    if ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    forget_removed(state);
    let walk = &state.window.garden.board_walk;
    if walk.compose.is_some() {
        return false;
    }
    if walk.menu_for.is_some() {
        return menu_key(state, ev, tx);
    }
    let place = Place::of(state);
    let walk = &state.window.garden.board_walk;
    let lanes = place.lanes(walk.closed_open);
    let here = walk
        .selected
        .as_deref()
        .and_then(|id| place.find(id))
        .filter(|(column, _)| lanes.contains(column));
    let first = lanes
        .iter()
        .find_map(|c| place.column(*c).first())
        .map(|s| s.id.clone());
    match ev.key {
        Key::Down | Key::Up => {
            let Some((column, row)) = here else {
                return first.is_some_and(|id| {
                    select(state, &id, tx);
                    true
                });
            };
            let cards = place.column(column);
            let next = if ev.key == Key::Down {
                (row + 1).min(cards.len() - 1)
            } else {
                row.saturating_sub(1)
            };
            let id = cards[next].id.clone();
            select(state, &id, tx);
            true
        }
        Key::Right | Key::Left => {
            let selected = walk.selected.clone();
            let plot = selected
                .as_ref()
                .and_then(|id| place.index.by_id.get(id))
                .is_some_and(|s| model::is_plot(s));
            if ev.key == Key::Right && plot {
                drill(state, &selected.unwrap_or_default(), tx);
                return true;
            }
            let Some((column, row)) = here else {
                if ev.key == Key::Left {
                    if place.trail.is_empty() {
                        return false;
                    }
                    climb_to(state, place.trail.len() - 1, tx);
                    return true;
                }
                return first.is_some_and(|id| {
                    select(state, &id, tx);
                    true
                });
            };
            let at = lanes.iter().position(|c| *c == column).unwrap_or(0);
            let toward: Vec<Column> = if ev.key == Key::Right {
                lanes[at + 1..].to_vec()
            } else {
                lanes[..at].iter().rev().copied().collect()
            };
            for lane in toward {
                let cards = place.column(lane);
                if let Some(card) = cards.get(row.min(cards.len().saturating_sub(1))) {
                    let id = card.id.clone();
                    select(state, &id, tx);
                    return true;
                }
            }
            if ev.key == Key::Left && !place.trail.is_empty() {
                climb_to(state, place.trail.len() - 1, tx);
            }
            true
        }
        Key::Enter => match walk.selected.clone() {
            Some(id) => {
                toggle_menu(state, &id, tx);
                true
            }
            None => false,
        },
        _ => false,
    }
}

/// Lets go of a selection or an open menu whose seed a garden push took away.
fn forget_removed(state: &mut State) {
    let seeds = super::seeds(state);
    let gone = |id: &Option<String>| {
        id.as_ref()
            .is_some_and(|id| !seeds.iter().any(|s| s.id == *id))
    };
    let walk = &state.window.garden.board_walk;
    let (selected, menu) = (gone(&walk.selected), gone(&walk.menu_for));
    let walk = board(state);
    if selected {
        walk.selected = None;
    }
    if menu {
        walk.menu_for = None;
    }
}

fn menu_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if !matches!(ev.key, Key::Down | Key::Up | Key::Enter) {
        return false;
    }
    let Some(seed) = state
        .window
        .garden
        .board_walk
        .menu_for
        .as_ref()
        .and_then(|id| super::seeds(state).iter().find(|s| s.id == *id))
    else {
        return false;
    };
    let verbs = verbs_for(&seed.status);
    if verbs.is_empty() {
        return true;
    }
    let at = tx
        .focused()
        .and_then(|k| k.strip_prefix(MENU_PREFIX))
        .and_then(Verb::named)
        .and_then(|v| verbs.iter().position(|x| *x == v));
    if ev.key == Key::Enter {
        if let Some(at) = at {
            begin_verb(state, seed.clone(), verbs[at], tx);
        }
        return true;
    }
    let next = match (at, ev.key == Key::Down) {
        (None, _) => 0,
        (Some(at), true) => (at + 1).min(verbs.len() - 1),
        (Some(at), false) => at.saturating_sub(1),
    };
    tx.focus(format!("{MENU_PREFIX}{}", verbs[next].name()), true);
    true
}

/// The drag on a card: past the threshold it carries the card, a column it hovers offers the
/// moves that card may make there, and letting go on one composes it.
fn drag(state: &mut State, id: &str, d: &rune::Drag, tx: &mut Tx) {
    let at = d.window_point();
    match d.phase {
        Phase::Down => {
            let seed = super::seeds(state).iter().find(|s| s.id == id).cloned();
            board(state).drag = seed.map(|seed| Drag {
                seed,
                start: at,
                at,
                armed: false,
                column: None,
                zone: None,
            });
        }
        Phase::Move => {
            let Some(held) = board(state).drag.as_mut() else {
                return;
            };
            held.at = at;
            if !held.armed {
                if (at[0] - held.start[0]).abs() + (at[1] - held.start[1]).abs() < DRAG_THRESHOLD {
                    tx.live_only();
                    return;
                }
                held.armed = true;
                let id = held.seed.id.clone();
                let walk = board(state);
                walk.selected = Some(id);
                walk.menu_for = None;
            }
            tx.element_at_in(BOARD, at[0], at[1], |state: &mut State, picked, _| {
                if let Some(held) = board(state).drag.as_mut() {
                    let (column, zone) = under(picked.as_ref().map(rune::Picked::path));
                    held.column = column;
                    held.zone = zone;
                }
            });
        }
        Phase::Up => {
            let Some(held) = board(state).drag.take() else {
                return;
            };
            if d.cancelled() || !held.armed {
                return;
            }
            tx.element_at_in(BOARD, at[0], at[1], move |state: &mut State, picked, tx| {
                let (column, zone) = under(picked.as_ref().map(rune::Picked::path));
                if let (Some(column), Some(verb)) = (column, zone)
                    && legal_verbs(&held.seed.status, column).contains(&verb)
                {
                    begin_verb(state, held.seed, verb, tx);
                }
            });
        }
    }
}

/// The column and the move zone an element path sits in.
fn under(path: Option<&str>) -> (Option<Column>, Option<Verb>) {
    let Some(path) = path else {
        return (None, None);
    };
    let segment = |prefix: &str| {
        path.split('/')
            .find_map(|s| s.strip_prefix(prefix))
            .map(str::to_owned)
    };
    (
        segment(COLUMN_PREFIX).as_deref().and_then(Column::named),
        segment(ZONE_PREFIX).as_deref().and_then(Verb::named),
    )
}

pub(super) fn view(state: &State, t: &Tokens) -> El {
    let place = Place::of(state);
    let walk = &state.window.garden.board_walk;
    let names = model::session_names(&state.facts.sessions);
    let seeds = super::seeds(state);
    let total = state.facts.garden.total_of(&state.facts.profiles.selected);
    let mut page = col()
        .key(BOARD)
        .role("region")
        .label("The garden board")
        .focusable(true)
        .w_full()
        .h_full()
        .min_h(0.0)
        .on_key(key)
        .child(header(state, &place, t));
    if !place.trail.is_empty() {
        page = page.child(trail(&place, t));
    }
    if let Some(error) = &walk.error {
        page = page.child(
            row()
                .key("garden.board.error")
                .role("alert")
                .label(error.clone())
                .gap(space::S2)
                .padding(space::S2)
                .items_center()
                .child(
                    text(error.clone())
                        .font(type_::BODY)
                        .color(t.error)
                        .grow(1.0),
                )
                .child(
                    super::view::button("garden.board.error.dismiss".into(), "Dismiss", t)
                        .on_press(|state: &mut State| board(state).error = None),
                ),
        );
    }
    if seeds.is_empty() {
        page = page.child(
            text("The garden is empty. attn seed plant \"what this is\" puts something in it.")
                .key("garden.board.empty")
                .role("note")
                .label(
                    "The garden is empty. attn seed plant \"what this is\" puts something in it.",
                )
                .font(type_::BODY)
                .color(t.text_secondary)
                .padding(space::S3),
        );
    } else {
        let mut columns = row()
            .key("garden.board.columns")
            .w_full()
            .grow(1.0)
            .min_h(0.0)
            .gap(space::S2)
            .padding(space::S2);
        for column in COLUMNS {
            columns = columns.child(column_view(state, &place, &names, column, t));
        }
        page = page.child(columns);
    }
    if total > seeds.len() as i64 {
        let words = format!(
            "The garden holds {total} seeds; this board has the newest {}.",
            seeds.len()
        );
        page = page.child(
            text(words.clone())
                .key("garden.board.capped")
                .role("note")
                .label(words)
                .font(type_::LABEL)
                .color(t.text_muted)
                .padding(space::S2),
        );
    }
    let mut layer = el().w_full().h_full().child(page);
    if let Some(carry) = carry(walk, t) {
        layer = layer.child(carry);
    }
    layer
}

fn header(state: &State, place: &Place, t: &Tokens) -> El {
    row()
        .key("garden.board.header")
        .w_full()
        .items_center()
        .gap(space::S2)
        .padding(space::S2)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border_subtle)
        .child(
            text("The garden")
                .font(type_::LABEL)
                .color(t.text_secondary)
                .grow(1.0),
        )
        .child(super::view::view_switch(state, t))
        .child(
            text(place.scoped.to_string())
                .key("garden.board.total")
                .font(type_::LABEL)
                .color(t.text_muted),
        )
        .child(
            super::view::button("garden.board.close".into(), "Close", t)
                .on_press(|state: &mut State| super::close(state)),
        )
}

fn trail(place: &Place, t: &Tokens) -> El {
    let mut nav = row()
        .key("garden.board.trail")
        .role("navigation")
        .label("Where you are")
        .items_center()
        .gap(space::S1)
        .padding(space::S2)
        .child(
            super::view::button("garden.board.trail.0".into(), "Garden", t)
                .on_press_tx(|state: &mut State, tx: &mut Tx| climb_to(state, 0, tx)),
        );
    let last = place.trail.len() - 1;
    for (depth, id) in place.trail.iter().enumerate() {
        let title = place
            .index
            .by_id
            .get(id)
            .map_or_else(|| id.clone(), |s| s.title.clone());
        nav = nav.child(
            text("›")
                .font(type_::LABEL)
                .color(t.text_muted)
                .a11y_hidden(),
        );
        let step = super::view::button(format!("garden.board.trail.{}", depth + 1), title, t);
        nav = nav.child(if depth == last {
            step.disabled(true)
        } else {
            step.on_press_tx(move |state: &mut State, tx: &mut Tx| climb_to(state, depth + 1, tx))
        });
    }
    nav.child(
        text(place.trail[last].clone())
            .font(type_::LABEL)
            .color(t.text_muted),
    )
}

fn column_view(
    state: &State,
    place: &Place,
    names: &SessionNames,
    column: Column,
    t: &Tokens,
) -> El {
    let walk = &state.window.garden.board_walk;
    let cards = place.column(column);
    let composing = walk.compose.as_ref().filter(|c| c.verb.target() == column);
    let collapsed = column == Column::Closed && !walk.closed_open && composing.is_none();
    let dragging = walk.drag.as_ref().filter(|d| d.armed);
    let zones = dragging.map_or_else(Vec::new, |d| legal_verbs(&d.seed.status, column));
    let hovered = dragging.is_some_and(|d| d.column == Some(column)) && !zones.is_empty();
    let count = place.count(column);
    let mut head = row()
        .items_center()
        .gap(space::S2)
        .child(
            text(column.label())
                .role("heading")
                .label(column.label())
                .font(type_::SECTION)
                .color(t.text_primary),
        )
        .child(
            text(count.to_string())
                .font(type_::LABEL)
                .color(t.text_muted),
        );
    if column == Column::Closed && !cards.is_empty() {
        head = head.child(
            super::view::button(
                "garden.board.closed.reveal".into(),
                if walk.closed_open { "hide" } else { "show" },
                t,
            )
            .expanded(walk.closed_open)
            .on_press(|state: &mut State| {
                let walk = board(state);
                walk.closed_open = !walk.closed_open;
            }),
        );
    }
    let mut body = col().w_full().gap(space::S1);
    if let Some(compose) = composing {
        body = body.child(composer(compose, &walk.words, names, t));
    }
    if collapsed {
        let harvested = cards.iter().filter(|s| s.status == "harvested").count();
        let withered = cards.len() - harvested;
        let words = if cards.is_empty() {
            "Nothing has finished here yet.".to_owned()
        } else if withered > 0 {
            format!("{harvested} harvested · {withered} withered")
        } else {
            format!("{harvested} harvested")
        };
        body = body.child(note("garden.board.closed.summary", words, t));
    } else if cards.is_empty() {
        body = body.child(note(
            &format!("garden.board.empty.{}", column.name()),
            empty_line(column, place),
            t,
        ));
    } else {
        for (row_at, seed) in cards.iter().enumerate() {
            if column == Column::Ready && row_at == place.ready && place.ready < cards.len() {
                body = body.child(
                    text(format!("{} not ready yet", cards.len() - place.ready))
                        .font(type_::LABEL)
                        .color(t.text_muted)
                        .a11y_hidden(),
                );
            }
            body = body.child(card(state, place, names, column, seed, t));
        }
    }
    if hovered && let Some(held) = dragging {
        for verb in &zones {
            body = body.child(
                row()
                    .key(format!("{ZONE_PREFIX}{}", verb.name()))
                    .role("button")
                    .label(format!("{} {}", verb.label(), held.seed.id))
                    .padding(space::S2)
                    .gap(space::S2)
                    .radius(6.0)
                    .border(
                        1.0,
                        if held.zone == Some(*verb) {
                            t.accent
                        } else {
                            t.border
                        },
                    )
                    .fill(if held.zone == Some(*verb) {
                        t.bg_selected
                    } else {
                        t.bg_panel
                    })
                    .child(text(verb.label()).font(type_::BODY).color(t.text_primary))
                    .child(
                        text(held.seed.id.clone())
                            .font(type_::LABEL)
                            .color(t.text_muted),
                    ),
            );
        }
    }
    col()
        .key(format!("{COLUMN_PREFIX}{}", column.name()))
        .role("region")
        .label(format!("{}, {count}", column.label()))
        .w(0.0)
        .grow(1.0)
        .h_full()
        .min_h(0.0)
        .gap(space::S2)
        .padding(space::S2)
        .radius(6.0)
        .fill(if hovered { t.bg_selected } else { t.bg_app })
        .opacity(if dragging.is_some() && zones.is_empty() {
            0.5
        } else {
            1.0
        })
        .child(head)
        .child(
            col()
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .scroll(ScrollSpec {
                    stick_bottom: false,
                })
                .child(body),
        )
}

fn note(key: &str, words: impl Into<String>, t: &Tokens) -> El {
    let words = words.into();
    text(words.clone())
        .key(key.to_owned())
        .role("note")
        .label(words)
        .font(type_::LABEL)
        .color(t.text_muted)
}

fn card(
    state: &State,
    place: &Place,
    names: &SessionNames,
    column: Column,
    seed: &Arc<Seed>,
    t: &Tokens,
) -> El {
    let walk = &state.window.garden.board_walk;
    let look = CardLook {
        seed: Arc::as_ptr(seed) as usize,
        column,
        selected: walk.selected.as_deref() == Some(seed.id.as_str()),
        menu_open: walk.menu_for.as_deref() == Some(seed.id.as_str()),
        blockers: place.index.blockers(&seed.id),
        tender: model::tender_of(seed, names),
        age: age(
            if seed.updated_at.is_empty() {
                &seed.created_at
            } else {
                &seed.updated_at
            },
            state.window.now_ms,
        ),
    };
    memo(
        format!("garden.board.slot.{}", seed.id),
        (look.clone(), *t),
        || {
            super::view::ROWS_BUILT.with(|n| n.set(n.get() + 1));
            card_face(seed, &look, t)
        },
    )
}

/// What a card shows, beside its seed: a card whose look and seed did not change is reused.
#[derive(Clone, PartialEq, Eq, Hash)]
struct CardLook {
    seed: usize,
    column: Column,
    selected: bool,
    menu_open: bool,
    blockers: usize,
    tender: String,
    age: String,
}

fn card_face(seed: &Arc<Seed>, look: &CardLook, t: &Tokens) -> El {
    let (selected, menu_open) = (look.selected, look.menu_open);
    let verbs = verbs_for(&seed.status);
    let id = seed.id.clone();
    let pressed = seed.id.clone();
    let dragged = seed.id.clone();
    let mut meta = row()
        .items_center()
        .gap(space::S2)
        .wrap()
        .children(card_meta(look, seed, t));
    if let Some(tally) = model::check_tally(seed) {
        meta = meta.child(
            text(model::tally_words(&tally))
                .font(type_::LABEL)
                .color(t.text_secondary),
        );
    }
    meta = meta
        .child(text(seed.id.clone()).font(type_::LABEL).color(t.text_muted))
        .child(
            text(look.age.clone())
                .font(type_::LABEL)
                .color(t.text_muted),
        );
    let mut face = col()
        .key(card_key(&seed.id))
        .role("button")
        .label(seed.title.clone())
        .focusable(true)
        .expanded(menu_open)
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(6.0)
        .fill(if selected { t.bg_selected } else { t.bg_panel })
        .border(1.0, if selected { t.accent } else { t.border_subtle })
        .hover(t.bg_elevated)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| press_card(state, &pressed, tx))
        .on_drag(move |state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            drag(state, &dragged, d, tx)
        })
        .child(
            text(seed.title.clone())
                .font(type_::BODY)
                .color(t.text_primary),
        );
    if let Some(standby) = &seed.standby {
        face = face.child(
            text(if standby.resume_blocked.is_some() {
                "standby · resume blocked"
            } else {
                "standby"
            })
            .font(type_::LABEL)
            .color(t.warning),
        );
    }
    face = face.child(meta);
    let mut card = col().w_full().gap(space::S1).child(face);
    let mut actions = row().gap(space::S1);
    if model::is_plot(seed) {
        let drilled = id.clone();
        actions = actions.child(
            super::view::button(
                format!("garden.board.drill.{id}"),
                format!("Open the plot under {}", seed.title),
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| drill(state, &drilled, tx)),
        );
    }
    if !menu_open && selected && !verbs.is_empty() {
        let shown = id.clone();
        actions = actions.child(
            super::view::button(
                format!("garden.board.more.{id}"),
                format!("Move {}", seed.title),
                t,
            )
            .on_press_tx(move |state: &mut State, tx: &mut Tx| toggle_menu(state, &shown, tx)),
        );
    }
    card = card.child(actions);
    if menu_open {
        card = card.child(menu(seed, &verbs, t));
    }
    card
}

fn card_meta(look: &CardLook, seed: &Seed, t: &Tokens) -> Vec<El> {
    let word = |words: String, color| text(words).font(type_::LABEL).color(color);
    if model::is_plot(seed) {
        return vec![word(plot_counts(seed), t.text_secondary)];
    }
    let marker = model::harvest_when(seed)
        .map(|(sentence, marker)| word(marker, t.text_secondary).help(sentence));
    let blockers = look.blockers;
    match look.column {
        Column::Ready if seed.ready => std::iter::once(word("ready".into(), t.accent))
            .chain(marker)
            .collect(),
        Column::Ready if blockers > 0 => {
            vec![word(format!("blocked by {blockers}"), t.warning)]
        }
        Column::Ready => match marker {
            Some(marker) => vec![marker],
            None if seed.template => vec![word("packet".into(), t.text_muted)],
            None if seed.gate => vec![word("gate".into(), t.text_muted)],
            None => vec![word("not ready".into(), t.text_muted)],
        },
        Column::Growing => {
            let tender = look.tender.clone();
            let live = seed.tender.is_none() || seed.claimed;
            let mut words = if tender.is_empty() {
                "held".to_owned()
            } else {
                tender
            };
            if !live {
                words.push_str(" · session gone");
            }
            vec![word(
                words,
                if live { t.text_secondary } else { t.text_muted },
            )]
        }
        Column::Parked => {
            vec![marker.unwrap_or_else(|| word("parked".into(), t.text_muted))]
        }
        Column::Closed => {
            let words = seed
                .reason
                .clone()
                .filter(|r| !r.is_empty())
                .unwrap_or_else(|| seed.status.clone());
            vec![word(words, t.text_muted)]
        }
    }
}

fn menu(seed: &Arc<Seed>, verbs: &[Verb], t: &Tokens) -> El {
    let mut menu = col()
        .key(format!("garden.board.menu-of.{}", seed.id))
        .role("menu")
        .label(format!("Move {}", seed.title))
        .gap(space::S1)
        .padding(space::S1)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border);
    if verbs.is_empty() {
        let words = format!("Nothing moves a {} seed from here.", seed.status);
        return menu.child(note("garden.board.menu.empty", words, t));
    }
    for verb in verbs {
        let (verb, chosen) = (*verb, seed.clone());
        menu = menu.child(
            row()
                .key(format!("{MENU_PREFIX}{}", verb.name()))
                .role("menuitem")
                .label(verb.label())
                .focusable(true)
                .padding(space::S1)
                .radius(4.0)
                .hover(t.bg_selected)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    begin_verb(state, chosen.clone(), verb, tx)
                })
                .child(
                    text(format!("{}…", verb.label()))
                        .font(type_::BODY)
                        .color(t.text_primary),
                ),
        );
    }
    menu
}

fn composer(compose: &Compose, words: &TextEdit, names: &SessionNames, t: &Tokens) -> El {
    let verb = compose.verb;
    let placeholder = if verb.required() {
        verb.prompt().to_owned()
    } else {
        format!("{} — optional", verb.prompt())
    };
    let mut line = col()
        .key("garden.board.compose")
        .w_full()
        .gap(space::S1)
        .padding(space::S2)
        .radius(6.0)
        .fill(t.bg_elevated)
        .border(1.0, t.accent)
        .child(
            row()
                .gap(space::S2)
                .items_center()
                .child(
                    text(verb.label().to_lowercase())
                        .font(type_::LABEL)
                        .color(t.accent),
                )
                .child(
                    text(compose.seed.id.clone())
                        .font(type_::LABEL)
                        .color(t.text_muted),
                ),
        )
        .child(
            text_input(words, type_::BODY)
                .key(COMPOSE_FIELD)
                .single_line()
                .label(format!(
                    "{} {}: {}",
                    verb.label(),
                    compose.seed.id,
                    verb.prompt()
                ))
                .placeholder(placeholder, t.text_muted)
                .on_edit(|state: &mut State| &mut board(state).words)
                .on_input_key(compose_key)
                .w_full()
                .color(t.text_primary),
        );
    if !verb.reason_on_seed() {
        line = line.child(
            text("goes on the log")
                .font(type_::LABEL)
                .color(t.text_muted),
        );
    }
    let taken = held_by_other(&compose.seed, names);
    if !taken.is_empty() {
        line = line.child(
            text(format!("takes it from {taken}"))
                .font(type_::LABEL)
                .color(t.warning),
        );
    }
    line
}

fn compose_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    match ev.key {
        Key::Enter => {
            commit(state, tx);
            true
        }
        Key::Escape => {
            cancel_compose(state, tx);
            true
        }
        _ => false,
    }
}

/// The card the hand carries while a drag is armed.
fn carry(walk: &Board, t: &Tokens) -> Option<El> {
    let held = walk.drag.as_ref().filter(|d| d.armed)?;
    Some(
        col()
            .key("garden.board.carry")
            .absolute(held.at[0] + CARRY_OFFSET, held.at[1] + CARRY_OFFSET)
            .w(CARRY_WIDTH)
            .padding(space::S2)
            .radius(6.0)
            .fill(t.bg_elevated)
            .border(1.0, t.accent)
            .a11y_hidden()
            .child(
                text(held.seed.title.clone())
                    .font(type_::BODY)
                    .color(t.text_primary),
            )
            .child(
                text(held.seed.id.clone())
                    .font(type_::LABEL)
                    .color(t.text_muted),
            ),
    )
}
