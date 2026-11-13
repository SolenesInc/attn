pub(crate) mod model;
pub(crate) mod search;
mod view;

use std::sync::Arc;

use attn_protocol::{Seed, SeedDocument, SeedDocumentGetMessage, SeedDocumentGetResultMessage};
use rune::{Key, KeyEvent, TextEdit, Tx};

use crate::State;
use crate::shell::Drawer;

pub(crate) use view::{frame_layer, panel};

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
    pub(crate) reading: Reading,
}

/// The opened seed's document as the daemon last answered it.
#[derive(Default)]
pub(crate) struct Reading {
    /// What the latest read asked for: the seed, the garden push it followed, and its request.
    asked: Option<(String, usize, String)>,
    pub(crate) document: Option<Arc<SeedDocument>>,
    pub(crate) error: Option<String>,
}

pub(crate) fn mode(state: &State) -> Mode {
    if state.window.garden.full {
        Mode::Full
    } else if state.window.drawer == Some(Drawer::Garden) {
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

pub(crate) fn choose_board(state: &mut State, board: bool) {
    state.window.garden.board = Some(board);
    crate::frame::set_pref_string(VIEW_PREF, if board { "board" } else { "list" });
}

fn show(state: &mut State, frame: Mode, tx: &mut Tx) {
    let was = mode(state);
    let window = &mut state.window;
    if frame == Mode::Full {
        window.garden.full = true;
        if window.drawer == Some(Drawer::Garden) {
            window.drawer = None;
        }
    } else {
        window.garden.full = false;
        window.drawer = Some(Drawer::Garden);
    }
    if was != frame {
        tx.focus(BODY, false);
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
    if window.drawer == Some(Drawer::Garden) {
        window.drawer = None;
    }
}

/// Escape while the garden is open closes it, keeping the walk and the question.
pub(crate) fn escape(state: &mut State) -> bool {
    if mode(state) == Mode::Closed {
        return false;
    }
    close(state);
    true
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

/// Reads the opened seed's document whenever the seed or the garden around it changes, as
/// the Tauri panel reads it on every garden push. Only the latest read's answer counts.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let open = mode(state) != Mode::Closed && !state.window.garden.trail.is_empty();
    let here = if open {
        living_trail(state).pop()
    } else {
        None
    };
    let push = Arc::as_ptr(&state.facts.garden) as usize;
    let reading = &mut state.window.garden.reading;
    let Some(here) = here else {
        *reading = Reading::default();
        return;
    };
    if reading
        .asked
        .as_ref()
        .is_some_and(|(seed, at, _)| *seed == here && *at == push)
    {
        return;
    }
    if reading.document.as_ref().is_some_and(|d| d.seed.id != here) {
        reading.document = None;
    }
    reading.error = None;
    let request_id = state.next_request_id();
    let reading = &mut state.window.garden.reading;
    reading.asked = Some((here.clone(), push, request_id.clone()));
    let command = SeedDocumentGetMessage {
        seed_id: here.clone(),
        request_id: request_id.clone(),
        ..SeedDocumentGetMessage::new()
    };
    state.ask_as(
        request_id.clone(),
        &command,
        move |state, answer, _| {
            let reading = &mut state.window.garden.reading;
            if reading
                .asked
                .as_ref()
                .is_none_or(|(_, _, id)| *id != request_id)
            {
                return;
            }
            match answer.parse::<SeedDocumentGetResultMessage>() {
                Some(reply) if reply.success => {
                    reading.document = reply.document.map(Arc::new);
                    reading.error = None;
                }
                Some(reply) => {
                    reading.error = Some(
                        reply
                            .error
                            .unwrap_or_else(|| format!("Could not read {here}")),
                    );
                }
                None => reading.error = Some(format!("Could not read {here}")),
            }
        },
        tx,
    );
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
        Key::Escape => escape(state),
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
        return escape(state);
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
