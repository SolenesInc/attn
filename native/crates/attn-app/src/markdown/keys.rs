//! The keys a document reads by: Escape closes its find bar, and with vim keys on, the vim
//! reading commands move through it. A document tile reads by them while it holds the keyboard,
//! and the garden's seed page while the garden's reader has it.

use std::rc::Rc;

use attn_ui::system::Reading;
use rune::{Key, KeyEvent, Tx};

use super::find;
use crate::State;
use crate::vim::{self, Command, Step};

/// A document read with the keys: the scroll view it reads in, its source as its find bar
/// searches it, and where its headings sit.
pub(crate) struct Doc {
    pub(crate) view: String,
    pub(crate) source: find::Source,
    /// The keyed element its source's headings sit in, each keyed by its slug.
    pub(crate) headings_in: String,
    /// The keys of the headings around its source, which `]]` and `[[` stop at too.
    pub(crate) sections: Vec<String>,
}

impl Doc {
    fn headings(&self, source: &str) -> Vec<String> {
        (super::anchors::heading_keys(source).into_iter())
            .map(|(_, slug)| format!("{}/{slug}", self.headings_in))
            .chain(self.sections.iter().cloned())
            .collect()
    }
}

/// The document tile holding the keyboard, on screen with nothing over it: a markdown tile
/// showing a file, not a review or a view, or a seed tile.
fn held(state: &State) -> Option<Doc> {
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
    match leaf.tile()? {
        ("seed", seed) => Some(crate::garden::tile_doc(&holder.pane, seed)),
        ("markdown", _) => {
            let key = attn_client::TileKey {
                desktop: holder.desktop.clone(),
                tile: holder.pane.clone(),
            };
            let message = &state.facts.tile_contents.get(&key)?.message;
            (message.review.is_none() && message.view.is_none()).then(|| doc(&holder.pane))
        }
        _ => None,
    }
}

/// Forgets the find bars of document tiles that closed, and what the views they showed
/// remembered.
pub(crate) fn follow(state: &mut State) {
    let markdown: std::collections::HashSet<String> = (state.facts.desktops.iter())
        .flat_map(|d| d.leaves())
        .filter(|leaf| leaf.tile().is_some_and(|(kind, _)| kind == "markdown"))
        .map(|leaf| leaf.id.clone())
        .collect();
    state.views.keep_only(&markdown);
    let shown: std::collections::HashSet<String> = (state.facts.desktops.iter())
        .flat_map(|d| d.leaves())
        .filter_map(|leaf| match leaf.tile()? {
            ("seed", _) => Some(crate::garden::tile_view(&leaf.id)),
            ("markdown", _) => Some(view(&leaf.id)),
            _ => None,
        })
        .collect();
    (state.readings.finds).retain(|view, _| shown.contains(view) || crate::garden::reads_in(view));
}

/// The scroll view tile `tile`'s document reads in.
pub(crate) fn view(tile: &str) -> String {
    format!("tile.{tile}.document")
}

/// The document tile `tile` shows, as the keys read it.
fn doc(tile: &str) -> Doc {
    Doc {
        view: view(tile),
        source: source(tile),
        headings_in: format!("{}/{}", view(tile), super::annotating::root_key(tile)),
        sections: Vec::new(),
    }
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
    let Some(doc) = held(state) else {
        return false;
    };
    find::open(state, &doc.view, tx);
    true
}

/// A key the window heard while nothing has focus, which the document tile holding the
/// keyboard takes.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if tx.focused().is_some() {
        return false;
    }
    let Some(doc) = held(state) else {
        return false;
    };
    match step(state, &doc, ev, tx) {
        Step::Pending => true,
        Step::Command(command) => read(state, &doc, command, tx),
        Step::None => ev.key == Key::Escape && find::close(state, &doc.view, tx),
    }
}

/// What the vim keys make of `ev` on `doc`: nothing while they are off.
pub(crate) fn step(state: &mut State, doc: &Doc, ev: &KeyEvent, tx: &mut Tx) -> Step {
    if !vim::enabled(&state.facts.settings) {
        return Step::None;
    }
    vim::step(&mut state.window.vim_prefix, &doc.view, ev, tx)
}

/// Carries out `command` on `doc`: false for a command a document has no meaning for, which
/// goes on as a plain key.
pub(crate) fn read(state: &mut State, doc: &Doc, command: Command, tx: &mut Tx) -> bool {
    let view = doc.view.as_str();
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
            find::step(state, view, &doc.source, forward, tx);
        }
        Command::NextSection | Command::PrevSection => {
            let keys =
                (doc.source)(state).map_or_else(|| doc.sections.clone(), |text| doc.headings(text));
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
