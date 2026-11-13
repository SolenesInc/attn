mod artifacts;
mod authorization;
mod board;
mod checks;
mod continuation;
mod handover;
pub(crate) mod model;
pub(crate) mod search;
mod sheet;
mod spot;
mod tile;
mod view;

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use attn_protocol::{
    FsExistsMessage, FsExistsResultMessage, Seed, SeedDocument, SeedDocumentGetMessage,
    SeedDocumentGetResultMessage,
};
use rune::{El, Key, KeyEvent, TextEdit, Tx, col};

use crate::State;
use crate::shell::Drawer;

pub(crate) use tile::{Reads as TileReads, body as tile_body, climb as climb_tile};
pub(crate) use view::{frame_layer, panel};

/// How many times its size the garden draws inside a window the app's text size already
/// zooms: its own size over the app's.
pub(crate) fn zoom(state: &State) -> f32 {
    let settings = &state.facts.settings;
    crate::settings::garden_scale(settings) / crate::settings::text_scale(settings)
}

/// `content` at `zoom` times its size: laid out in a `1 / zoom` share of the box it fills and
/// drawn from the box's top-left corner, so it fills the box again.
pub(crate) fn zoomed(zoom: f32, content: El) -> El {
    col()
        .w_frac(zoom.recip())
        .h_frac(zoom.recip())
        .origin(0.0, 0.0)
        .scale(zoom)
        .child(content)
}

/// How many garden rows this thread has built, memo misses only: a row whose seed and look did
/// not change is reused and not counted.
pub fn rows_built() -> usize {
    view::ROWS_BUILT.with(std::cell::Cell::get)
}

const FRAME_PREF: &str = "attn.garden.frame";
const VIEW_PREF: &str = "attn.garden.fullscreenView";
pub(crate) const SEARCH_FIELD: &str = "garden.search.field";
pub(crate) const BODY: &str = "garden.body";

/// Where the garden shows: closed, in the dock beside the work, or holding the window.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Mode {
    Closed,
    Dock,
    Full,
}

/// The window's garden: its frame and the walk through it. The seeds are the daemon's.
#[derive(Default)]
pub(crate) struct Garden {
    /// The garden holds the window, over the dock.
    full: bool,
    /// The frame the garden opens in, as the user last chose it; read from prefs at first use.
    last: Option<Mode>,
    /// Fullscreen shows the board instead of the list; read from prefs at first use.
    board: Option<bool>,
    /// The plots walked into from the root, then the seed opened, by id.
    pub(crate) trail: Vec<String>,
    pub(crate) query: TextEdit,
    /// The plot the search stood in when it widened to the whole garden.
    pub(crate) wide_in: Option<String>,
    /// The answer the arrows reached, for the question it answers.
    pub(crate) walked: (String, usize),
    /// The folded trail is open.
    pub(crate) trail_open: bool,
    /// Each shown seed's document, by seed.
    readings: HashMap<String, Reading>,
    /// Rises with every change a seed page shows besides the seeds: a reading's answer, a
    /// check's move, a fold opened.
    pub(crate) revision: u64,
    /// What one part of a seed page is doing, and the words being written for it.
    edit: Option<spot::Edit>,
    words: TextEdit,
    handover: Option<handover::Handover>,
    /// The folds open on seed pages, by the key of the control that opens each.
    folds: HashSet<String>,
    pub(crate) board_walk: board::Board,
}

/// A seed's document as the daemon last answered it.
#[derive(Default)]
struct Reading {
    /// The garden push the latest read followed, and its request.
    asked: Option<(usize, String)>,
    document: Option<Arc<SeedDocument>>,
    error: Option<String>,
    /// The linked files asked about, and those the daemon says are not on disk.
    looked_for: Vec<String>,
    missing: HashSet<String>,
}

pub(crate) fn mode(state: &State) -> Mode {
    if state.window.garden.full {
        Mode::Full
    } else if state.window.shows(Drawer::Garden) {
        Mode::Dock
    } else {
        Mode::Closed
    }
}

/// The seeds of the selected profile's garden, newest first.
pub(crate) fn seeds(state: &State) -> &[Arc<Seed>] {
    state.facts.garden.of(&state.facts.profiles.selected)
}

fn last_frame(state: &State) -> Mode {
    state.window.garden.last.unwrap_or_else(|| {
        match crate::frame::pref_string(FRAME_PREF).as_deref() {
            Some("full") => Mode::Full,
            _ => Mode::Dock,
        }
    })
}

pub(crate) fn shows_board(state: &State) -> bool {
    mode(state) == Mode::Full
        && state
            .window
            .garden
            .board
            .unwrap_or_else(|| crate::frame::pref_string(VIEW_PREF).as_deref() == Some("board"))
}

pub(crate) fn choose_board(state: &mut State, board: bool, tx: &mut Tx) {
    state.window.garden.board = Some(board);
    crate::frame::set_pref_string(VIEW_PREF, if board { "board" } else { "list" });
    focus_frame(state, tx);
}

fn focus_frame(state: &State, tx: &mut Tx) {
    tx.focus(
        if shows_board(state) {
            board::BOARD
        } else {
            BODY
        },
        false,
    );
}

fn show(state: &mut State, frame: Mode, tx: &mut Tx) {
    let was = mode(state);
    let window = &mut state.window;
    if frame == Mode::Full {
        window.garden.full = true;
        window.close_drawer(Drawer::Garden);
    } else {
        window.garden.full = false;
        window.open_drawer(Drawer::Garden);
    }
    if was != frame {
        focus_frame(state, tx);
    }
    follow(state, tx);
}

/// ⌘⇧T and the palette: opens the garden in the frame it last had, or moves an open garden
/// between the dock and the window and remembers that frame.
pub(crate) fn toggle_frame(state: &mut State, tx: &mut Tx) {
    let next = match mode(state) {
        Mode::Closed => last_frame(state),
        Mode::Dock => Mode::Full,
        Mode::Full => Mode::Dock,
    };
    if mode(state) != Mode::Closed {
        state.window.garden.last = Some(next);
        let value = if next == Mode::Full { "full" } else { "dock" };
        crate::frame::set_pref_string(FRAME_PREF, value);
    }
    show(state, next, tx);
}

/// The sidebar icon: opens the garden in its last frame, or closes it.
pub(crate) fn toggle_from_icon(state: &mut State, tx: &mut Tx) {
    match mode(state) {
        Mode::Closed => {
            let frame = last_frame(state);
            show(state, frame, tx);
        }
        _ => close(state),
    }
}

pub(crate) fn close(state: &mut State) {
    let window = &mut state.window;
    window.garden.full = false;
    window.close_drawer(Drawer::Garden);
}

/// The trail without the plots the garden no longer holds, and what follows them.
pub(crate) fn living_trail(state: &State) -> Vec<String> {
    let seeds = seeds(state);
    state
        .window
        .garden
        .trail
        .iter()
        .take_while(|id| seeds.iter().any(|s| s.id == **id))
        .cloned()
        .collect()
}

/// The seeds on screen: the one the garden opened and each seed tile's.
fn shown_seeds(state: &State) -> HashSet<String> {
    let mut shown: HashSet<String> = state
        .facts
        .desktops
        .iter()
        .flat_map(|d| crate::sidebar::model::leaves(&d.tree_json))
        .filter_map(|leaf| match leaf {
            crate::sidebar::model::Leaf::Tile { kind, params, .. }
                if kind == "seed" && !params.is_empty() =>
            {
                Some(params)
            }
            _ => None,
        })
        .collect();
    if mode(state) != Mode::Closed {
        shown.extend(living_trail(state).pop());
    }
    shown
}

/// Reads each shown seed's document whenever the seed or the garden around it changes, as
/// the Tauri app reads it on every garden push. Only a seed's latest read counts.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let shown = shown_seeds(state);
    let push = Arc::as_ptr(&state.facts.garden) as usize;
    let readings = &mut state.window.garden.readings;
    let before = readings.len();
    readings.retain(|id, _| shown.contains(id));
    if readings.len() != before {
        state.window.garden.revision += 1;
    }
    for id in shown {
        if state
            .window
            .garden
            .readings
            .get(&id)
            .is_some_and(|r| r.asked.as_ref().is_some_and(|(at, _)| *at == push))
        {
            continue;
        }
        read(state, id, push, tx);
    }
}

fn read(state: &mut State, id: String, push: usize, tx: &mut Tx) {
    let request_id = state.next_request_id();
    let reading = state.window.garden.readings.entry(id.clone()).or_default();
    reading.asked = Some((push, request_id.clone()));
    let command = SeedDocumentGetMessage {
        seed_id: id.clone(),
        request_id: request_id.clone(),
        ..SeedDocumentGetMessage::new()
    };
    state.ask_as(
        request_id.clone(),
        &command,
        move |state, answer, tx| {
            let garden = &mut state.window.garden;
            let Some(reading) = garden.readings.get_mut(&id).filter(|r| {
                r.asked
                    .as_ref()
                    .is_some_and(|(_, asked)| *asked == request_id)
            }) else {
                return;
            };
            match answer.parse::<SeedDocumentGetResultMessage>() {
                Some(reply) if reply.success => {
                    reading.document = reply.document.map(Arc::new);
                    reading.error = None;
                    let linked = linked_files(reading.document.as_deref());
                    if linked != reading.looked_for {
                        reading.looked_for = linked.clone();
                        reading.missing.clear();
                        garden.revision += 1;
                        look_for(state, &id, linked, tx);
                        return;
                    }
                }
                Some(reply) => {
                    reading.error = Some(
                        reply
                            .error
                            .unwrap_or_else(|| format!("Could not read {id}")),
                    );
                }
                None => reading.error = Some(format!("Could not read {id}")),
            }
            garden.revision += 1;
        },
        tx,
    );
}

/// The absolute paths of the files `document` links to.
fn linked_files(document: Option<&SeedDocument>) -> Vec<String> {
    document
        .into_iter()
        .flat_map(|d| &d.references)
        .filter_map(|r| r.path.clone())
        .filter(|p| p.starts_with('/'))
        .collect()
}

/// Asks the daemon whether each linked file of seed `id` is on disk.
fn look_for(state: &mut State, id: &str, paths: Vec<String>, tx: &mut Tx) {
    for path in paths {
        let Some((dir, name)) = path.rsplit_once('/').filter(|(dir, _)| !dir.is_empty()) else {
            continue;
        };
        let command = FsExistsMessage {
            path: name.to_owned(),
            root: Some(dir.to_owned()),
            ..FsExistsMessage::new()
        };
        let (id, path) = (id.to_owned(), path.clone());
        state.ask(
            |request_id| FsExistsMessage {
                request_id: Some(request_id),
                ..command
            },
            move |state, answer, _| {
                let gone = answer
                    .parse::<FsExistsResultMessage>()
                    .and_then(|r| r.result)
                    .is_some_and(|r| !r.exists);
                let garden = &mut state.window.garden;
                if let Some(reading) = garden
                    .readings
                    .get_mut(&id)
                    .filter(|r| gone && r.looked_for.contains(&path))
                {
                    reading.missing.insert(path);
                    garden.revision += 1;
                }
            },
            tx,
        );
    }
}

/// The linked files of seed `id` the daemon says are not on disk.
pub(super) fn missing<'a>(garden: &'a Garden, id: &str) -> Option<&'a HashSet<String>> {
    garden.readings.get(id).map(|r| &r.missing)
}

/// The shown seed's document as the daemon last answered it, and the error of its last read.
pub(crate) fn reading<'a>(
    garden: &'a Garden,
    id: &str,
) -> (Option<&'a SeedDocument>, Option<&'a str>) {
    let Some(reading) = garden.readings.get(id) else {
        return (None, None);
    };
    (
        reading.document.as_deref().filter(|d| d.seed.id == id),
        reading.error.as_deref(),
    )
}

/// Drops the walk into a plot or seed, and reads it.
pub(crate) fn drill(state: &mut State, id: &str, tx: &mut Tx) {
    let mut trail = living_trail(state);
    trail.push(id.to_owned());
    state.window.garden.trail = trail;
    tx.focus(BODY, false);
    follow(state, tx);
}

/// Climbs to `depth` steps from the root, focusing the row it came out of.
pub(crate) fn climb_to(state: &mut State, depth: usize, tx: &mut Tx) {
    let mut trail = living_trail(state);
    let from = trail.get(depth).cloned();
    trail.truncate(depth);
    state.window.garden.trail = trail;
    match from {
        Some(row) => tx.focus(view::row_key(&row), false),
        None => tx.focus(BODY, false),
    }
    follow(state, tx);
}

/// A row of the column `level` steps down picks that seed there, dropping what followed.
pub(crate) fn select_at(state: &mut State, level: usize, id: &str, tx: &mut Tx) {
    let mut trail = living_trail(state);
    trail.truncate(level);
    trail.push(id.to_owned());
    state.window.garden.trail = trail;
    tx.focus(BODY, false);
    follow(state, tx);
}

/// Opens a seed in the garden, docking the garden when it is closed.
pub(crate) fn open_seed(state: &mut State, id: &str, tx: &mut Tx) {
    if mode(state) == Mode::Closed {
        show(state, Mode::Dock, tx);
    }
    open_found(state, id, tx);
}

/// Opens a found seed under the whole path of plots above it, and ends the search.
pub(crate) fn open_found(state: &mut State, id: &str, tx: &mut Tx) {
    let path = view::index(state).path_to(id);
    let garden = &mut state.window.garden;
    garden.trail = path;
    garden.query = TextEdit::default();
    garden.wide_in = None;
    tx.focus(BODY, false);
    follow(state, tx);
}

fn focus_search(state: &mut State, tx: &mut Tx) {
    let query = &mut state.window.garden.query;
    let end = query.text.len();
    query.set_selection(0, end);
    tx.focus(SEARCH_FIELD, true);
}

pub(crate) fn toggle_wide(state: &mut State, tx: &mut Tx) {
    let plot = living_trail(state).pop();
    let garden = &mut state.window.garden;
    garden.wide_in = match garden.wide_in {
        None => plot,
        Some(_) => None,
    };
    focus_search(state, tx);
}

pub(crate) fn toggle_closed(state: &mut State, tx: &mut Tx) {
    let query = &mut state.window.garden.query;
    *query = TextEdit::new(search::toggle_token(&query.text, "is:any"));
    focus_search(state, tx);
}

pub(crate) fn set_query(state: &mut State, text: String, tx: &mut Tx) {
    state.window.garden.query = TextEdit::new(text);
    focus_search(state, tx);
}

/// The search field's keys: the arrows walk the answers while there are some, Return opens
/// the one reached, and Alt+Return widens the scope out of the plot or back.
pub(crate) fn field_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let answers = view::answers(state);
    match ev.key {
        Key::Down | Key::Up if !answers.ids.is_empty() => {
            let last = answers.ids.len() - 1;
            let at = answers.active.min(last);
            let next = if ev.key == Key::Down {
                (at + 1).min(last)
            } else {
                at.saturating_sub(1)
            };
            state.window.garden.walked = (answers.question, next);
            true
        }
        Key::Down | Key::Up => walk_rows(state, ev.key == Key::Down, tx),
        Key::Enter if ev.mods.alt => {
            if answers.can_widen {
                toggle_wide(state, tx);
            }
            true
        }
        Key::Enter => match answers.ids.get(answers.active) {
            Some(id) => {
                let id = id.clone();
                open_found(state, &id, tx);
                true
            }
            None => false,
        },
        Key::Escape => crate::shell::escape_drawers(state, tx),
        _ => false,
    }
}

fn typing(tx: &Tx) -> bool {
    tx.focused()
        .is_some_and(|key| key.ends_with(crate::keys::FIELD_KEY_SUFFIX))
}

/// The garden's keys while focus is inside it: `/` or ⌘F goes to the search field, the arrows
/// walk the rows of the place it stands in, → drills into the focused row in columns, ← climbs
/// out, and Escape closes the garden.
pub(crate) fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let accel = if rune::KeyPlatform::current() == rune::KeyPlatform::Mac {
        ev.mods.cmd
    } else {
        ev.mods.ctrl
    };
    let slash = matches!(&ev.key, Key::Text(t) if t == "/");
    let find = matches!(&ev.key, Key::Command(c) if c.eq_ignore_ascii_case(&'f')) && accel;
    if find || (slash && !typing(tx)) {
        focus_search(state, tx);
        return true;
    }
    if ev.key == Key::Escape {
        return crate::shell::escape_drawers(state, tx);
    }
    if typing(tx) || ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    match ev.key {
        Key::Left => {
            let trail = living_trail(state);
            if trail.is_empty() {
                return false;
            }
            climb_to(state, trail.len() - 1, tx);
            true
        }
        Key::Right if view::columns(state) => match focused_row(tx) {
            Some(id) => {
                view::press_row(state, &id, tx);
                true
            }
            None => false,
        },
        Key::Down | Key::Up => walk_rows(state, ev.key == Key::Down, tx),
        _ => false,
    }
}

fn focused_row(tx: &Tx) -> Option<String> {
    tx.focused()?
        .strip_prefix(view::ROW_PREFIX)
        .map(str::to_owned)
}

/// Moves focus one row down or up the rows of the focused row's column, or of the place the
/// garden stands in, from the top or the bottom when no row has it.
fn walk_rows(state: &mut State, down: bool, tx: &mut Tx) -> bool {
    let focused = focused_row(tx);
    let rows = view::walkable_rows(state, focused.as_deref());
    if rows.is_empty() {
        return false;
    }
    let at = focused.and_then(|id| rows.iter().position(|r| *r == id));
    let next = match (at, down) {
        (None, true) => 0,
        (None, false) => rows.len() - 1,
        (Some(at), true) => (at + 1).min(rows.len() - 1),
        (Some(at), false) => at.saturating_sub(1),
    };
    tx.focus(view::row_key(&rows[next]), true);
    true
}
