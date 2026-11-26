use std::collections::{HashMap, HashSet};
use std::rc::Rc;
use std::sync::Arc;

use attn_protocol::{Seed, SeedNoteMessage, SeedTransitionMessage, SeedTransitionResultMessage};
use attn_ui::Tokens;
use attn_ui::blocks::{
    Bar, Byline, Card, Inline, Lane, Prose, Stack, Standing, Surface, Trail, board as board_body,
    lanes,
};
use attn_ui::system::{DotSize, FieldState, Link, ListRow, Type, dot, line_box, words};
use rune::{El, GrowthSpec, Key, KeyEvent, Phase, TextEdit, Tx, col, el, memo, row};
use rune_kit::TextInput;

use super::model::{self, SessionNames};
use super::v2::{self, Attention, BedFilter};
use super::{PageBucket, PageKey};
use crate::State;
use crate::sidebar::queue::epoch_ms;

pub(super) const BOARD: &str = "garden.board";
const COMPOSE_FIELD: &str = "garden.board.compose.field";
const CARD_PREFIX: &str = "garden.board.card.";
const COLUMN_PREFIX: &str = "garden.board.column.";
const ZONE_PREFIX: &str = "garden.board.zone.";
const MENU_PREFIX: &str = "garden.board.menu.";
/// Points a press on a card travels before it becomes a drag.
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
    trail_seeds: HashMap<String, Arc<Seed>>,
    pub(super) closed_open: bool,
    selected: Option<String>,
    menu_for: Option<String>,
    compose: Option<Compose>,
    words: TextEdit,
    error: Option<String>,
    busy: bool,
    drag: Option<Drag>,
}

fn living_trail(state: &State) -> Vec<String> {
    let walk = &state.window.garden.board_walk;
    if walk.trail.is_empty() {
        return Vec::new();
    }
    let seeds = super::seeds(state);
    walk.trail
        .iter()
        .take_while(|id| walk.trail_seeds.contains_key(*id) || seeds.iter().any(|s| s.id == **id))
        .cloned()
        .collect()
}

pub(super) fn current_plot(state: &State) -> Option<String> {
    living_trail(state).pop()
}

fn page_key_for(state: &State, plot: Option<&str>) -> PageKey {
    match plot {
        Some(plot_id) => PageKey::plot(state, plot_id),
        None => PageKey::root(state),
    }
}

fn find_seed(state: &State, id: &str) -> Option<Arc<Seed>> {
    if let Some(seed) = super::seeds(state).iter().find(|s| s.id == id) {
        return Some(seed.clone());
    }
    if let Some(seed) = state.window.garden.board_walk.trail_seeds.get(id) {
        return Some(seed.clone());
    }
    let key = page_key_for(state, current_plot(state).as_deref());
    state
        .window
        .garden
        .pages
        .get(&key)
        .and_then(|bucket| bucket.seeds.iter().find(|s| s.id == id).cloned())
}

impl Board {
    pub(crate) fn clear_walk(&mut self) {
        self.trail.clear();
        self.trail_seeds.clear();
        self.selected = None;
        self.menu_for = None;
        self.compose = None;
        self.error = None;
        self.drag = None;
    }
}

struct Compose {
    seed: Arc<Seed>,
    verb: Verb,
}

impl Board {
    fn composing_into(&self, column: Column) -> Option<&Compose> {
        self.compose.as_ref().filter(|c| c.verb.target() == column)
    }
}

struct Drag {
    seed: Arc<Seed>,
    start: [f32; 2],
    at: [f32; 2],
    armed: bool,
    column: Option<Column>,
    zone: Option<Verb>,
}

/// The seeds of the place the board stands in: the cards each column shows, and the closed seeds
/// with nothing open below them, which fold away.
struct Place {
    index: Rc<model::Index>,
    trail: Vec<String>,
    scoped: usize,
    columns: [Vec<Arc<Seed>>; 4],
    ready: usize,
    closed_open_below: usize,
    foldable_harvested: usize,
    foldable_withered: usize,
    folded: bool,
    page_key: PageKey,
    v2: Rc<v2::Snapshot>,
}

impl Place {
    fn of(state: &State) -> Place {
        let index = Rc::new(model::Index::of(super::seeds(state)));
        let v2_snap = Rc::new(v2::Snapshot::of(state, &index));
        let walk = &state.window.garden.board_walk;
        let trail: Vec<String> = walk
            .trail
            .iter()
            .take_while(|id| index.by_id.contains_key(*id) || walk.trail_seeds.contains_key(*id))
            .cloned()
            .collect();
        let page_key = page_key_for(state, trail.last().map(String::as_str));
        let scoped_seeds: Vec<Arc<Seed>> = match trail.last() {
            Some(plot) => index.children_of(plot).to_vec(),
            None if v2_snap.on => index
                .roots
                .iter()
                .filter(|s| v2::matches_bed(s, &v2_snap.bed))
                .cloned()
                .collect(),
            None => index.roots.clone(),
        };
        let mut columns: [Vec<Arc<Seed>>; 4] = Default::default();
        for seed in &scoped_seeds {
            columns[slot(column_of(seed))].push(seed.clone());
        }
        let at = |iso: &str| epoch_ms(iso).unwrap_or(0);
        columns[0].sort_by_key(|s| (!s.ready, at(&s.created_at)));
        if v2_snap.on {
            columns[1].sort_by_key(|s| {
                (
                    v2_snap.attention_of(s),
                    std::cmp::Reverse(at(&s.updated_at)),
                )
            });
            for column in &mut columns[2..] {
                column.sort_by_key(|s| std::cmp::Reverse(at(&s.updated_at)));
            }
        } else {
            for column in &mut columns[1..] {
                column.sort_by_key(|s| std::cmp::Reverse(at(&s.updated_at)));
            }
        }
        let ready = columns[0].iter().filter(|s| s.ready).count();
        let closed = &mut columns[slot(Column::Closed)];
        let local_foldable: Vec<Arc<Seed>> = closed
            .iter()
            .filter(|s| !model::has_open_work_below(s))
            .cloned()
            .collect();
        let closed_open_below = closed.len() - local_foldable.len();
        let local_harvested = local_foldable
            .iter()
            .filter(|s| s.status == "harvested")
            .count();
        let local_withered = local_foldable.len() - local_harvested;
        let bucket = state.window.garden.pages.get(&page_key);
        let (foldable_harvested, foldable_withered) = if trail.is_empty() {
            let counts = state
                .facts
                .garden
                .closed_counts_of(&state.facts.profiles.selected);
            v2::closed_tally(counts, v2_snap.on.then_some(&v2_snap.bed))
                .unwrap_or((local_harvested, local_withered))
        } else if super::history_is_capped(state)
            && let Some(b) = bucket.filter(|b| b.answered)
        {
            (b.harvested.max(0) as usize, b.withered.max(0) as usize)
        } else {
            (local_harvested, local_withered)
        };
        let folded = !walk.closed_open;
        if folded {
            closed.retain(|s| model::has_open_work_below(s));
        } else if let Some(b) = bucket
            && !b.seeds.is_empty()
        {
            let mut merged: Vec<Arc<Seed>> = closed
                .iter()
                .filter(|s| model::has_open_work_below(s))
                .cloned()
                .collect();
            let extra: Vec<Arc<Seed>> = {
                let mut seen: HashSet<&str> = merged.iter().map(|s| s.id.as_str()).collect();
                let mut out = Vec::new();
                for seed in &b.seeds {
                    if let Some(live) = index.by_id.get(&seed.id) {
                        if model::is_closed(live)
                            && !model::has_open_work_below(live)
                            && seen.insert(live.id.as_str())
                        {
                            out.push(live.clone());
                        }
                    } else if seen.insert(seed.id.as_str()) {
                        out.push(seed.clone());
                    }
                }
                for seed in &local_foldable {
                    if seen.insert(seed.id.as_str()) {
                        out.push(seed.clone());
                    }
                }
                out
            };
            merged.extend(extra);
            *closed = merged;
        }
        let open_scoped = columns[0].len() + columns[1].len() + columns[2].len();
        let scoped = open_scoped + closed_open_below + foldable_harvested + foldable_withered;
        Place {
            index,
            trail,
            scoped,
            columns,
            ready,
            closed_open_below,
            foldable_harvested,
            foldable_withered,
            folded,
            page_key,
            v2: v2_snap,
        }
    }

    fn foldable_total(&self) -> usize {
        self.foldable_harvested + self.foldable_withered
    }

    fn column(&self, column: Column) -> &[Arc<Seed>] {
        &self.columns[slot(column)]
    }

    fn count(&self, column: Column) -> usize {
        match column {
            Column::Ready => self.ready,
            Column::Closed => self.closed_open_below + self.foldable_total(),
            other => self.column(other).len(),
        }
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

fn standby_marker(standby: &model::Standby, t: &Tokens) -> El {
    let quiet = |said: String| words(said, Type::Small, t.text_secondary);
    let line = Byline::new()
        .wrap()
        .push(words("standby", Type::Small, t.warning))
        .push_all(standby.pull_requests.iter().map(|pr| {
            let ready = pr.word == "ready";
            words(
                format!("{} {}", pr.number, pr.word),
                if ready { Type::Name } else { Type::Small },
                if ready { t.success } else { t.text_secondary },
            )
            .help(pr.title.clone())
        }))
        .push_all(standby.reminder_words().map(quiet))
        .build(t);
    Stack::tight()
        .child(line)
        .children((!standby.note.is_empty()).then(|| {
            words(standby.note.clone(), Type::Small, t.text_secondary)
                .help(standby.note.clone())
                .single_line()
                .truncate()
        }))
        .children(standby.blocked_words().map(|said| {
            words(said, Type::Small, t.warning)
                .help(standby.resume_blocked.clone())
                .single_line()
                .truncate()
        }))
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

fn plot_counts(seed: &Seed) -> Vec<String> {
    let Some(p) = &seed.plot_progress else {
        return Vec::new();
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
    parts
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
    let mut trail = living_trail(state);
    let seed = find_seed(state, id);
    let walk = board(state);
    walk.selected = None;
    walk.menu_for = None;
    trail.push(id.to_owned());
    walk.trail = trail;
    if let Some(seed) = seed {
        walk.trail_seeds.insert(id.to_owned(), seed);
    }
    walk.trail_seeds.retain(|k, _| walk.trail.contains(k));
    tx.instant();
    tx.focus(BOARD, false);
    super::follow(state, tx);
}

fn climb_to(state: &mut State, depth: usize, tx: &mut Tx) {
    let mut trail = living_trail(state);
    trail.truncate(depth);
    let walk = board(state);
    walk.selected = None;
    walk.menu_for = None;
    walk.trail = trail;
    walk.trail_seeds.retain(|k, _| walk.trail.contains(k));
    tx.instant();
    tx.focus(BOARD, false);
    super::follow(state, tx);
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
        .clone()
        .and_then(|id| find_seed(state, &id))
        .and_then(|seed| verbs_for(&seed.status).first().copied());
    match first {
        Some(verb) => tx.focus(format!("{MENU_PREFIX}{}", verb.name()), true),
        None => tx.focus(card_key(id), true),
    }
}

fn press_card(state: &mut State, id: &str, tx: &mut Tx) {
    let plot = find_seed(state, id).is_some_and(|s| model::is_plot(&s));
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
    let names = model::session_names(&state.facts.sessions, &state.facts.harnesses);
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
    crate::shell::escape_drawers(state, tx)
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
    if v2::enabled(state)
        && let Key::Text(ref t) = ev.key
        && t == "b"
        && let Some(selected) = walk.selected.clone()
    {
        let back = tx.focused().map(str::to_owned);
        v2::open_bed_picker(state, &selected, back, tx);
        return true;
    }
    let place = Place::of(state);
    let walk = &state.window.garden.board_walk;
    let here = walk.selected.as_deref().and_then(|id| place.find(id));
    let first = COLUMNS
        .iter()
        .find_map(|c| place.column(*c).first())
        .map(|s| s.id.clone());
    let down = ev.key == Key::Down || (place.v2.on && matches!(&ev.key, Key::Text(t) if t == "j"));
    let up = ev.key == Key::Up || (place.v2.on && matches!(&ev.key, Key::Text(t) if t == "k"));
    if down || up {
        let Some((column, row)) = here else {
            return first.is_some_and(|id| {
                select(state, &id, tx);
                true
            });
        };
        let cards = place.column(column);
        let next = if down {
            (row + 1).min(cards.len() - 1)
        } else {
            row.saturating_sub(1)
        };
        let id = cards[next].id.clone();
        select(state, &id, tx);
        if down && column == Column::Closed && next + 5 >= cards.len() {
            super::load_more_page(state, place.page_key.clone(), tx);
        }
        return true;
    }
    match ev.key {
        Key::Right | Key::Left => {
            let selected = walk.selected.clone();
            let plot = selected
                .as_deref()
                .and_then(|id| find_seed(state, id))
                .is_some_and(|s| model::is_plot(&s));
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
            let at = slot(column);
            let toward: Vec<Column> = if ev.key == Key::Right {
                COLUMNS[at + 1..].to_vec()
            } else {
                COLUMNS[..at].iter().rev().copied().collect()
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
    let walk = &state.window.garden.board_walk;
    let selected = walk
        .selected
        .as_deref()
        .is_some_and(|id| find_seed(state, id).is_none());
    let menu = walk
        .menu_for
        .as_deref()
        .is_some_and(|id| find_seed(state, id).is_none());
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
        .clone()
        .and_then(|id| find_seed(state, &id))
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
            let seed = find_seed(state, id);
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
    let names = Rc::new(model::session_names(
        &state.facts.sessions,
        &state.facts.harnesses,
    ));
    let seeds = super::seeds(state);
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
    if place.v2.on && !super::view::columns(state) {
        page = page.child(v2::dock_bed_chips(state, &place.v2.counts, t));
    }
    if !place.trail.is_empty() {
        page = page.child(
            Bar::new("garden.board.where")
                .lead(trail(state, &place, t))
                .build(t),
        );
    }
    if let Some(error) = &walk.error {
        page = page.child(
            Bar::new("garden.board.error")
                .lead(words(error.clone(), Type::Body, t.error).wrap())
                .push(
                    super::view::quiet("Dismiss")
                        .on_press(|state: &mut State| board(state).error = None)
                        .build(t)
                        .key("garden.board.error.dismiss"),
                )
                .build(t)
                .role("alert")
                .label(error.clone()),
        );
    }
    let mut parts = Vec::new();
    if seeds.is_empty() && place.scoped == 0 && place.trail.is_empty() {
        parts.push(
            Prose::empty(
                "garden.board.empty",
                "The garden is empty. `attn seed plant \"what this is\"` puts something in it.",
            )
            .el(t),
        );
    } else {
        parts.push(lanes(
            "garden.board.columns",
            COLUMNS.map(|column| column_view(state, &place, &names, column, t)),
        ));
    }
    let body = board_body("garden.board.page", parts);
    if place.v2.on && super::view::columns(state) {
        page = page.child(
            row()
                .key("garden.board.layout")
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .child(v2::beds_rail(state, &place.v2.counts, t))
                .child(body),
        );
    } else {
        page = page.child(body);
    }
    let mut layer = el().w_full().h_full().child(page);
    if let Some(carry) = carry(walk, t) {
        layer = layer.child(carry);
    }
    layer
}

fn header(state: &State, place: &Place, t: &Tokens) -> El {
    let lead = if place.v2.on && place.v2.bed != BedFilter::All {
        let root: El = Link::new("garden.board.bed.root", "Garden").build(t).into();
        let root = root.on_press_tx(|state: &mut State, tx: &mut Tx| {
            v2::select_bed(state, BedFilter::All, tx)
        });
        Trail::new("garden.board.bed.trail", "Selected bed")
            .step(root)
            .here(
                "garden.board.bed.here",
                v2::bed_label(state, &place.v2.bed),
                t,
            )
            .build(t)
    } else {
        words("The garden", Type::Small, t.text_secondary)
    };
    Bar::new("garden.board.header")
        .lead(lead)
        .push(super::view::view_switch(state, t))
        .push(
            words(place.scoped.to_string(), Type::Small, t.text_tertiary).key("garden.board.total"),
        )
        .push(
            super::view::quiet("Close")
                .on_press(|state: &mut State| super::close(state))
                .build(t)
                .key("garden.board.close"),
        )
        .build(t)
}

fn trail(state: &State, place: &Place, t: &Tokens) -> El {
    let step = |key: String, said: String, depth: usize| -> El {
        let link: El = Link::new(key, said).build(t).into();
        link.on_press_tx(move |state: &mut State, tx: &mut Tx| climb_to(state, depth, tx))
    };
    let root_label = if place.v2.on && place.v2.bed != BedFilter::All {
        v2::bed_label(state, &place.v2.bed)
    } else {
        "Garden".into()
    };
    let mut nav = Trail::new("garden.board.trail", "Where you are").step(step(
        "garden.board.trail.0".into(),
        root_label,
        0,
    ));
    let last = place.trail.len() - 1;
    for (depth, id) in place.trail.iter().enumerate() {
        let title = place
            .index
            .by_id
            .get(id)
            .or_else(|| state.window.garden.board_walk.trail_seeds.get(id))
            .map_or_else(|| id.clone(), |s| s.title.clone());
        let key = format!("garden.board.trail.{}", depth + 1);
        nav = nav.step(if depth == last {
            El::from(Link::new(key, title).ink(t.text_primary).build(t)).disabled(true)
        } else {
            step(key, title, depth + 1)
        });
    }
    nav.build(t)
}

struct ClosedCardCtx {
    selected: Option<String>,
    menu_for: Option<String>,
    now_ms: i64,
    names: Rc<SessionNames>,
    index: Rc<model::Index>,
    v2: Rc<v2::Snapshot>,
}

fn column_view(
    state: &State,
    place: &Place,
    names: &Rc<SessionNames>,
    column: Column,
    t: &Tokens,
) -> El {
    let walk = &state.window.garden.board_walk;
    let cards = place.column(column);
    let composing = walk.composing_into(column);
    let folded = column == Column::Closed && place.folded;
    let dragging = walk.drag.as_ref().filter(|d| d.armed);
    let zones = dragging.map_or_else(Vec::new, |d| legal_verbs(&d.seed.status, column));
    let hovered = dragging.is_some_and(|d| d.column == Some(column)) && !zones.is_empty();
    let count = place.count(column);
    let mut lane = Lane::new(
        format!("{COLUMN_PREFIX}{}", column.name()),
        format!("garden.board.body.{}", column.name()),
        column.label(),
        count,
    )
    .lit(hovered)
    .faded(dragging.is_some() && zones.is_empty());
    let bucket: Option<&PageBucket> = (column == Column::Closed)
        .then(|| state.window.garden.pages.get(&place.page_key))
        .flatten();
    if column == Column::Closed && (place.foldable_total() > 0 || walk.closed_open) {
        lane = lane.aside(
            super::view::quiet(if walk.closed_open { "hide" } else { "show" })
                .expanded(walk.closed_open)
                .on_press_tx(|state: &mut State, tx: &mut Tx| {
                    let walk = board(state);
                    walk.closed_open = !walk.closed_open;
                    super::follow(state, tx);
                })
                .build(t)
                .key("garden.board.closed.reveal"),
        );
    }
    if let Some(compose) = composing {
        lane = lane.push(composer(compose, &walk.words, names, t));
    }
    if cards.is_empty() && !folded {
        if !bucket.is_some_and(|b| b.loading || b.error.is_some()) {
            lane = lane.push(note(
                &format!("garden.board.empty.{}", column.name()),
                empty_line(column, place),
                t,
            ));
        }
    } else if column == Column::Closed && !folded {
        let cards_rc: Rc<[Arc<Seed>]> = cards.to_vec().into();
        let keys = cards_rc.clone();
        let ctx = Rc::new(ClosedCardCtx {
            selected: walk.selected.clone(),
            menu_for: walk.menu_for.clone(),
            now_ms: state.window.now_ms,
            names: names.clone(),
            index: place.index.clone(),
            v2: place.v2.clone(),
        });
        let tokens = *t;
        let list_el = Lane::cards_column()
            .key("garden.board.closed.list")
            .self_stretch()
            .growth(GrowthSpec::default())
            .virtual_rows(
                cards_rc.len(),
                96.0,
                move |_: &State, i| format!("garden.board.slot.{}", keys[i].id).into(),
                move |_: &State, i| {
                    let seed = &cards_rc[i];
                    let look = CardLook {
                        seed: Arc::as_ptr(seed) as usize,
                        column: Column::Closed,
                        selected: ctx.selected.as_deref() == Some(seed.id.as_str()),
                        menu_open: ctx.menu_for.as_deref() == Some(seed.id.as_str()),
                        blockers: ctx.index.blockers(&seed.id),
                        tender: model::tender_of(seed, &ctx.names),
                        age: model::short_age(
                            if seed.updated_at.is_empty() {
                                &seed.created_at
                            } else {
                                &seed.updated_at
                            },
                            ctx.now_ms,
                        ),
                        standby: model::standby_of(seed, ctx.now_ms),
                        v2: ctx.v2.on,
                        attention: ctx.v2.attention_of(seed),
                        show_bed: ctx.v2.on && ctx.v2.bed == BedFilter::All,
                    };
                    memo(
                        format!("garden.board.slot.{}", seed.id),
                        (look.clone(), tokens),
                        || {
                            super::view::ROWS_BUILT.with(|n| n.set(n.get() + 1));
                            card_face(seed, &look, &tokens)
                        },
                    )
                },
            );
        lane = lane.push(list_el);
    } else {
        for (row_at, seed) in cards.iter().enumerate() {
            if column == Column::Ready && row_at == place.ready && place.ready < cards.len() {
                lane = lane.push(
                    words(
                        format!("{} not ready yet", cards.len() - place.ready),
                        Type::Small,
                        t.text_tertiary,
                    )
                    .a11y_hidden(),
                );
            }
            lane = lane.push(card(state, place, names, column, seed, t));
        }
    }
    if column == Column::Closed
        && (!folded || bucket.is_some_and(|b| b.error.is_some() || (b.loading && !b.answered)))
        && let Some(tail) = super::view::page_tail(place.page_key.clone(), bucket, t)
    {
        lane = lane.push(tail);
    }
    if folded
        && (cards.is_empty() || place.foldable_total() > 0)
        && !bucket.is_some_and(|b| !b.answered && (b.loading || b.error.is_some()))
    {
        let harvested = place.foldable_harvested;
        let withered = place.foldable_withered;
        let said = if cards.is_empty() && place.foldable_total() == 0 {
            "Nothing has finished here yet.".to_owned()
        } else if withered > 0 {
            format!("{harvested} harvested · {withered} withered")
        } else {
            format!("{harvested} harvested")
        };
        lane = lane.push(note("garden.board.closed.summary", said, t));
    }
    if hovered && let Some(held) = dragging {
        for verb in &zones {
            lane = lane.landing(
                Card::new(format!("{ZONE_PREFIX}{}", verb.name()))
                    .control("button", format!("{} {}", verb.label(), held.seed.id))
                    .standing(if held.zone == Some(*verb) {
                        Standing::Picked
                    } else {
                        Standing::Resting
                    })
                    .push(words(verb.label(), Type::Body, t.text_primary))
                    .push(words(held.seed.id.clone(), Type::Mono, t.text_tertiary))
                    .build(t),
            );
        }
    }
    lane.build(t)
}

fn note(key: &str, said: impl Into<String>, t: &Tokens) -> El {
    let said = said.into();
    words(said.clone(), Type::Small, t.text_tertiary)
        .key(key.to_owned())
        .role("note")
        .label(said)
        .wrap()
}

fn card_look(
    state: &State,
    place: &Place,
    names: &SessionNames,
    column: Column,
    seed: &Arc<Seed>,
) -> CardLook {
    let walk = &state.window.garden.board_walk;
    CardLook {
        seed: Arc::as_ptr(seed) as usize,
        column,
        selected: walk.selected.as_deref() == Some(seed.id.as_str()),
        menu_open: walk.menu_for.as_deref() == Some(seed.id.as_str()),
        blockers: place.index.blockers(&seed.id),
        tender: model::tender_of(seed, names),
        age: model::short_age(
            if seed.updated_at.is_empty() {
                &seed.created_at
            } else {
                &seed.updated_at
            },
            state.window.now_ms,
        ),
        standby: model::standby_of(seed, state.window.now_ms),
        v2: place.v2.on,
        attention: place.v2.attention_of(seed),
        show_bed: place.v2.on && place.v2.bed == BedFilter::All,
    }
}

fn card(
    state: &State,
    place: &Place,
    names: &SessionNames,
    column: Column,
    seed: &Arc<Seed>,
    t: &Tokens,
) -> El {
    let look = card_look(state, place, names, column, seed);
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
    standby: Option<model::Standby>,
    v2: bool,
    attention: Attention,
    show_bed: bool,
}

fn card_face(seed: &Arc<Seed>, look: &CardLook, t: &Tokens) -> El {
    let (selected, menu_open) = (look.selected, look.menu_open);
    let verbs = verbs_for(&seed.status);
    let id = seed.id.clone();
    let pressed = seed.id.clone();
    let dragged = seed.id.clone();
    let standing = if selected {
        Standing::Picked
    } else {
        Standing::Resting
    };
    let face = if look.v2 {
        let (dot_color, dot_shape) = look.attention.dot_look(t);
        let head = Inline::joined()
            .w_full()
            .child(dot(dot_color, dot_shape, DotSize::Small).a11y_hidden())
            .child(
                words(seed.title.clone(), Type::Body, t.text_primary)
                    .single_line()
                    .truncate()
                    .grow(1.0)
                    .min_w(0.0),
            );
        let pulse = seed
            .pulse
            .as_deref()
            .map(str::trim)
            .filter(|p| !p.is_empty())
            .map(|p| {
                words(p.to_owned(), Type::Small, t.text_tertiary)
                    .key(format!("garden.board.pulse.{}", seed.id))
                    .single_line()
                    .truncate()
            });
        let left_tag = if look.show_bed {
            seed.bed
                .as_deref()
                .filter(|b| !b.is_empty())
                .map_or_else(|| seed.id.clone(), str::to_owned)
        } else {
            seed.id.clone()
        };
        let left_meta = match &seed.plot_progress {
            Some(p) => format!("{left_tag} · {}/{}", p.done, p.total),
            None => left_tag,
        };
        let foot = Inline::joined()
            .w_full()
            .justify_between()
            .child(
                words(left_meta, Type::Mono, t.text_muted)
                    .single_line()
                    .truncate()
                    .shrink()
                    .min_w(0.0),
            )
            .child(words(look.age.clone(), Type::Mono, t.text_muted));
        let facts = match look.column {
            Column::Ready if !seed.ready => card_meta(look, seed, t),
            Column::Ready | Column::Parked => harvest_marker(seed, t).into_iter().collect(),
            _ => Vec::new(),
        };
        let readiness = (!facts.is_empty()).then(|| Byline::new().wrap().push_all(facts).build(t));
        Card::new(card_key(&seed.id))
            .control("button", seed.title.clone())
            .standing(standing)
            .push(head)
            .push_all(pulse)
            .push_all(readiness)
            .push_all(look.standby.as_ref().map(|s| standby_marker(s, t)))
            .push(foot)
            .build(t)
    } else {
        let mut facts = Byline::new().wrap().push_all(card_meta(look, seed, t));
        if let Some(tally) = model::check_tally(seed) {
            facts = facts.push(words(
                model::tally_words(&tally),
                Type::Small,
                t.text_secondary,
            ));
        }
        let facts = facts
            .id(seed.id.clone(), t.text_tertiary)
            .said(look.age.clone(), t);
        Card::new(card_key(&seed.id))
            .control("button", seed.title.clone())
            .standing(standing)
            .push(words(seed.title.clone(), Type::Body, t.text_primary).wrap())
            .push_all(look.standby.as_ref().map(|s| standby_marker(s, t)))
            .push(facts.build(t))
            .build(t)
    };
    let face = face
        .expanded(menu_open)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| press_card(state, &pressed, tx))
        .on_drag(move |state: &mut State, d: &mut rune::Drag, tx: &mut Tx| {
            drag(state, &dragged, d, tx)
        });
    let mut card = Stack::tight().w_full().child(face);
    let mut actions = Inline::tight();
    if model::is_plot(seed) {
        let drilled = id.clone();
        actions = actions.child(
            El::from(
                super::view::quiet("Open the plot")
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| drill(state, &drilled, tx))
                    .build(t)
                    .key(format!("garden.board.drill.{id}")),
            )
            .label(format!("Open the plot under {}", seed.title)),
        );
    }
    if !menu_open && selected && !verbs.is_empty() {
        let shown = id.clone();
        actions = actions.child(
            El::from(
                super::view::quiet("Move")
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        toggle_menu(state, &shown, tx)
                    })
                    .build(t)
                    .key(format!("garden.board.more.{id}")),
            )
            .label(format!("Move {}", seed.title)),
        );
    }
    card = card.child(actions);
    if menu_open {
        card = card.child(menu(seed, &verbs, t));
    }
    card
}

fn harvest_marker(seed: &Seed, t: &Tokens) -> Option<El> {
    model::harvest_when(seed)
        .map(|(sentence, marker)| words(marker, Type::Small, t.text_secondary).help(sentence))
}

fn card_meta(look: &CardLook, seed: &Seed, t: &Tokens) -> Vec<El> {
    let word = |said: String, color| words(said, Type::Small, color);
    if model::is_plot(seed) {
        return plot_counts(seed)
            .into_iter()
            .map(|count| word(count, t.text_secondary))
            .collect();
    }
    let marker = harvest_marker(seed, t);
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
    let menu = ListRow::column()
        .key(format!("garden.board.menu-of.{}", seed.id))
        .role("menu")
        .label(format!("Move {}", seed.title));
    if verbs.is_empty() {
        let said = format!("Nothing moves a {} seed from here.", seed.status);
        return Surface::raised(menu.child(note("garden.board.menu.empty", said, t)), t);
    }
    let menu = menu.children(verbs.iter().map(|verb| {
        let (verb, chosen) = (*verb, seed.clone());
        ListRow::new(format!("{MENU_PREFIX}{}", verb.name()), verb.label())
            .title(format!("{}…", verb.label()))
            .role("menuitem")
            .on(t.bg_elevated)
            .build(t)
            .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                begin_verb(state, chosen.clone(), verb, tx)
            })
    }));
    Surface::raised(menu, t)
}

fn composer(compose: &Compose, written: &TextEdit, names: &SessionNames, t: &Tokens) -> El {
    let verb = compose.verb;
    let placeholder = if verb.required() {
        verb.prompt().to_owned()
    } else {
        format!("{} — optional", verb.prompt())
    };
    let field: El = line_box(
        TextInput::new(written)
            .key(COMPOSE_FIELD)
            .label(format!(
                "{} {}: {}",
                verb.label(),
                compose.seed.id,
                verb.prompt()
            ))
            .placeholder(placeholder)
            .on_edit(|state: &mut State| &mut board(state).words)
            .on_key(compose_key),
        FieldState::Ready,
        t,
    )
    .into();
    let taken = held_by_other(&compose.seed, names);
    Card::new("garden.board.compose")
        .standing(Standing::Lifted)
        .push(
            Byline::new()
                .push(words(verb.label().to_lowercase(), Type::Small, t.accent))
                .id(compose.seed.id.clone(), t.text_tertiary)
                .build(t),
        )
        .push(field)
        .push_all(
            (!verb.reason_on_seed())
                .then(|| words("goes on the log", Type::Small, t.text_tertiary)),
        )
        .push_all(
            (!taken.is_empty())
                .then(|| words(format!("takes it from {taken}"), Type::Small, t.warning)),
        )
        .build(t)
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
        Card::new("garden.board.carry")
            .standing(Standing::Lifted)
            .push(words(held.seed.title.clone(), Type::Body, t.text_primary))
            .push(words(held.seed.id.clone(), Type::Mono, t.text_tertiary))
            .build(t)
            .absolute(held.at[0] + CARRY_OFFSET, held.at[1] + CARRY_OFFSET)
            .w(CARRY_WIDTH)
            .a11y_hidden(),
    )
}
