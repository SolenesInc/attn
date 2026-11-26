//! The keys a plain document tile reads by while it holds the keyboard: Escape closes its find
//! bar, and with vim keys on, the vim reading commands move through it.

use std::rc::Rc;

use attn_ui::system::Reading;
use rune::{Key, KeyEvent, Tx};

use super::find;
use crate::State;
use crate::vim::{self, Command, Step};

/// The plain document tile holding the keyboard, on screen with nothing over it: a markdown
/// tile showing a file, not a review.
fn held(state: &State) -> Option<String> {
    if !state.window.tiles_in_front() {
        return None;
    }
    let holder = state.window.keyboard.as_ref()?;
    let desktop = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == holder.desktop)?;
    let leaf = desktop.leaves().into_iter().find(|l| l.id == holder.pane)?;
    if leaf.tile()?.0 != "markdown" {
        return None;
    }
    let key = attn_client::TileKey {
        desktop: holder.desktop.clone(),
        tile: holder.pane.clone(),
    };
    let message = &state.facts.tile_contents.get(&key)?.message;
    message.review.is_none().then(|| holder.pane.clone())
}

/// The scroll view tile `tile`'s document reads in.
pub(crate) fn view(tile: &str) -> String {
    format!("tile.{tile}.document")
}

/// The document tile `tile` shows, as its find bar reads it.
pub(crate) fn source(tile: &str) -> find::Source {
    let tile = tile.to_owned();
    Rc::new(move |state: &State| {
        let (_, content) =
            (state.facts.tile_contents.tiles.iter()).find(|(key, _)| key.tile == tile)?;
        Some(content.message.content.as_str())
    })
}

/// Opens the find bar of the document tile holding the keyboard: false when none does.
pub(crate) fn open_find(state: &mut State, tx: &mut Tx) -> bool {
    let Some(tile) = held(state) else {
        return false;
    };
    find::open(state, &view(&tile), tx);
    true
}

/// A key the window heard while nothing has focus, which the document tile holding the
/// keyboard takes.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if tx.focused().is_some() {
        return false;
    }
    let Some(tile) = held(state) else {
        return false;
    };
    let view = view(&tile);
    if vim::enabled(&state.facts.settings) {
        match vim::step(&mut state.window.vim_prefix, ev, tx) {
            Step::Pending => return true,
            Step::Command(command) => return read(state, &tile, &view, command, tx),
            Step::None => {}
        }
    }
    ev.key == Key::Escape && find::close(state, &view, tx)
}

/// Carries out `command` on tile `tile`'s document, scrolled in `view`: false for a command a
/// document has no meaning for, which goes on as a plain key.
fn read(state: &mut State, tile: &str, view: &str, command: Command, tx: &mut Tx) -> bool {
    match command {
        Command::LineDown => tx.scroll_by(view, Reading::Text.line_height()),
        Command::LineUp => tx.scroll_by(view, -Reading::Text.line_height()),
        Command::HalfPageDown => tx.scroll_fraction(view, 0.5),
        Command::HalfPageUp => tx.scroll_fraction(view, -0.5),
        Command::Top => tx.scroll_to_y(view, 0.0),
        Command::Bottom => tx.scroll_to_bottom_instant(view),
        Command::Search => find::open(state, view, tx),
        Command::NextMatch | Command::PrevMatch => {
            if find::finding(&state.readings, view).is_none() {
                return false;
            }
            let forward = command == Command::NextMatch;
            find::step(state, view, &source(tile), forward, tx);
        }
        Command::NextSection | Command::PrevSection => {
            let Some(text) = source(tile)(state) else {
                return false;
            };
            let root = super::annotating::root_key(tile);
            let keys = (super::anchors::heading_keys(text).into_iter())
                .map(|(_, slug)| format!("{view}/{root}/{slug}"))
                .collect();
            tx.scroll_adjacent_key(view, keys, command == Command::NextSection, 0.0);
        }
        Command::Left
        | Command::Right
        | Command::ShiftLeft
        | Command::ShiftRight
        | Command::VisualLine
        | Command::GoDefinition
        | Command::GoReferences
        | Command::Hover => return false,
    }
    true
}
