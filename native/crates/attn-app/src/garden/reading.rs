//! Reading a seed's page with the keys, in a seed tile and in the garden. The garden splits its
//! keys between the list and the reader: in columns the reader takes them once it has focus,
//! and in the dock once the walk stands on a seed and no search is open.

use std::rc::Rc;

use attn_ui::Tokens;
use rune::{El, Key, KeyEvent, Tx};

use super::BODY;
use super::view;
use crate::State;
use crate::markdown::find;
use crate::markdown::keys::{self as reading, Doc};
use crate::vim::{self, Command, Step};

/// The scroll view the open seed's page reads in while the garden lays its walk out in columns.
pub(crate) const READER: &str = "garden.reader";
/// The scroll view the walk reads in while it stacks in the dock.
pub(crate) const VIEWPORT: &str = "garden.viewport";
/// The surface the vim keys walk the garden's rows on.
const LIST: &str = "garden.list";

/// The scroll view the seed tile `leaf` reads in.
pub(crate) fn tile_view(leaf: &str) -> String {
    format!("seed-tile.{leaf}.viewport")
}

/// What takes the keyboard back from the find bar over the garden's view `view`: the reader in
/// columns, the garden in the dock; none for a view outside the garden.
pub(crate) fn home_of(view: &str) -> Option<&'static str> {
    match view {
        READER => Some(READER),
        VIEWPORT => Some(BODY),
        _ => None,
    }
}

/// Whether `view` is a scroll view the garden reads seeds in.
pub(crate) fn reads_in(view: &str) -> bool {
    view == READER || view == VIEWPORT
}

/// Seed `id`'s body, as its find bar reads it.
fn source(id: &str) -> find::Source {
    let id = id.to_owned();
    Rc::new(move |state: &State| {
        super::shown_seed(&state.window.garden, super::seeds(state), &id).map(|s| s.body.as_str())
    })
}

/// The seed tile `leaf` showing seed `id`, as the keys read it.
pub(crate) fn tile_doc(leaf: &str, id: &str) -> Doc {
    Doc {
        view: tile_view(leaf),
        source: source(id),
        headings_in: crate::markdown::annotating::root_key(leaf),
        sections: super::sheet::section_headings(&format!("seed-tile.{leaf}")),
    }
}

/// The seed the garden's walk stands on, as its reader reads it.
fn garden_doc(state: &State, wide: bool) -> Option<Doc> {
    let here = super::living_trail(state).pop()?;
    let view = if wide { READER } else { VIEWPORT };
    Some(Doc {
        view: view.to_owned(),
        source: source(&here),
        headings_in: super::sheet::plan_key("garden", &here),
        sections: super::sheet::section_headings("garden"),
    })
}

/// The find bar over the page of seed `id`, whose body is `body`, that `view` scrolls, while
/// it is open.
pub(super) fn find_bar(
    readings: &crate::markdown::Readings,
    view: &str,
    (id, body): (&str, &str),
    t: &Tokens,
) -> Option<El> {
    let finding = find::finding(readings, view)?;
    let count = finding.matches(body).len();
    Some(find::bar(view, finding, count, source(id), t))
}

fn finds(ev: &KeyEvent) -> bool {
    let accel = if rune::Platform::current() == rune::Platform::Mac {
        ev.mods.cmd
    } else {
        ev.mods.ctrl
    };
    accel && matches!(&ev.key, Key::Command(c) if c.eq_ignore_ascii_case(&'f'))
}

/// A key while focus is inside the garden and outside its fields, which its reader or, with
/// vim keys on, its list takes.
pub(super) fn key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let wide = view::columns(state);
    let doc = garden_doc(state, wide);
    let reads = if wide {
        tx.focused() == Some(READER)
    } else {
        !view::searching(state)
    };
    match doc {
        Some(doc) if reads => read_key(state, &doc, wide, ev, tx),
        doc => list_key(state, doc.filter(|_| wide), ev, tx),
    }
}

/// A key the reader takes: ⌘F finds in the seed, the vim keys read it, and Escape closes the
/// find bar first; in columns, Escape and `h` then hand the keyboard back to the seed's row.
fn read_key(state: &mut State, doc: &Doc, wide: bool, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if finds(ev) {
        find::open(state, &doc.view, tx);
        return true;
    }
    let back = match reading::step(state, doc, ev, tx) {
        Step::Pending => return true,
        Step::Command(Command::Left) => true,
        Step::Command(command) => return reading::read(state, doc, command, tx),
        Step::None => ev.key == Key::Escape,
    };
    if !back {
        return false;
    }
    if find::close(state, &doc.view, tx) {
        return true;
    }
    if !wide || !vim::enabled(&state.facts.settings) {
        return false;
    }
    let shown = (super::living_trail(state).pop())
        .filter(|id| view::walkable_rows(state, Some(id)).contains(id));
    tx.focus(
        shown.map_or_else(|| BODY.to_owned(), |id| view::row_key(&id)),
        false,
    );
    true
}

/// A key the list takes with vim keys on: `j`, `k`, `gg` and `G` walk its rows, `/` goes to
/// the search, and in columns `l` or Return on the open seed's row, or with no row focused,
/// hands the keyboard to the reader, `l` on another row opens it, and Ctrl-d and Ctrl-u
/// scroll the reader, `reader`.
fn list_key(state: &mut State, reader: Option<Doc>, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if !vim::enabled(&state.facts.settings) {
        return false;
    }
    let on_open_row = reader.is_some()
        && match super::focused_row(tx) {
            Some(row) => super::living_trail(state).last() == Some(&row),
            None => tx.focused() == Some(BODY),
        };
    if ev.key == Key::Enter && on_open_row {
        tx.focus(READER, false);
        return true;
    }
    let command = match vim::step(&mut state.window.vim_prefix, LIST, ev, tx) {
        Step::Pending => return true,
        Step::Command(command) => command,
        Step::None => return false,
    };
    match (command, reader) {
        (Command::LineDown, _) => super::walk_rows(state, true, tx),
        (Command::LineUp, _) => super::walk_rows(state, false, tx),
        (Command::Top, _) => super::walk_to_end(state, false, tx),
        (Command::Bottom, _) => super::walk_to_end(state, true, tx),
        (Command::Search, _) => {
            super::focus_search(state, tx);
            true
        }
        (Command::HalfPageDown | Command::HalfPageUp, Some(reader)) => {
            reading::read(state, &reader, command, tx)
        }
        (Command::Right, Some(_)) if on_open_row => {
            tx.focus(READER, false);
            true
        }
        (Command::Right, _) if view::columns(state) => match super::focused_row(tx) {
            Some(id) => {
                view::press_row(state, &id, tx);
                true
            }
            None => false,
        },
        _ => false,
    }
}
