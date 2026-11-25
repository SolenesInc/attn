//! The ids attn knows in the words the window reads as markdown: each a link that opens what it
//! names, shows its card while the pointer rests on it, and takes a label while hints show.

use std::ops::Range;

use rune::{El, ShownLink, Tx};

use crate::State;
use crate::desktop::header_pop::{self, Place};
use crate::terminal::{Known, Mark, Subject};

pub(crate) const HREF: &str = "attn-";
const SEED: &str = "attn-seed:";
const SESSION: &str = "attn-session:";

pub(crate) fn href(target: &Subject) -> String {
    match target {
        Subject::Seed(id) => format!("{SEED}{id}"),
        Subject::Session(id) => format!("{SESSION}{id}"),
    }
}

pub(crate) fn subject(href: &str) -> Option<Subject> {
    let seed = || Some(Subject::Seed(href.strip_prefix(SEED)?.to_owned()));
    let session = || Some(Subject::Session(href.strip_prefix(SESSION)?.to_owned()));
    seed().or_else(session)
}

/// The links of the ids `known` names in a run of prose, as `Markdown::text_links` takes them.
pub(crate) fn links(known: &Known, run: &str) -> Vec<(Range<usize>, String)> {
    let found = known.in_text(run).into_iter();
    found.map(|(at, target)| (at, href(&target))).collect()
}

/// The readings link the ids attn knows now.
pub(crate) fn follow(state: &mut State) {
    let known = state.terminals.known_ids();
    if state.readings.ids != *known {
        state.readings.ids = known.clone();
        state.readings.revision += 1;
    }
}

/// Where the ids are in the words the pointer is over.
#[derive(Default)]
pub(crate) struct Pointed {
    inside: bool,
    links: Vec<ShownLink>,
}

/// Which ids of a reading show a card while the pointer rests on them.
#[derive(Clone, Copy, PartialEq)]
pub(crate) enum Cards {
    All,
    Sessions,
}

impl Cards {
    fn shown_for(self, target: &Subject) -> bool {
        self == Cards::All || matches!(target, Subject::Session(_))
    }
}

/// `reading`, whose ids show their `cards` while the pointer rests on them.
pub(crate) fn pointed(reading: El, cards: Cards) -> El {
    reading
        .on_pointer_move_local(move |state: &mut State, p, tx: &mut Tx| {
            let at = (!tx.pointer_held()).then_some([p.window_x, p.window_y]);
            if state.window.words.inside {
                if !point(state, at, cards, tx) {
                    tx.live_only();
                }
                return;
            }
            state.window.words.inside = true;
            tx.find_links(HREF, move |state: &mut State, links, tx| {
                state.window.words.links = links;
                point(state, at, cards, tx);
            });
        })
        .on_hover_change(move |state: &mut State, inside, tx: &mut Tx| {
            if !inside {
                state.window.words = Pointed::default();
                point(state, None, cards, tx);
            }
            tx.live_only();
        })
}

/// Returns whether that changed what the window draws.
fn point(state: &mut State, at: Option<[f32; 2]>, cards: Cards, tx: &mut Tx) -> bool {
    let links = state.window.words.links.iter();
    let mark = at.and_then(|at| {
        links
            .filter_map(|link| mark_under(link, at))
            .find(|mark| cards.shown_for(&mark.target))
    });
    header_pop::point_at_words(state, mark, tx)
}

fn mark_under(link: &ShownLink, [x, y]: [f32; 2]) -> Option<Mark> {
    let holds = |[left, top, w, h]: &&[f32; 4]| {
        (*left..left + w).contains(&x) && (*top..top + h).contains(&y)
    };
    let line = *link.lines.iter().find(holds)?;
    Some(mark(subject(&link.id)?, line))
}

fn mark(target: Subject, line: [f32; 4]) -> Mark {
    Mark {
        target,
        at: line.map(f32::to_bits),
    }
}

/// Opens `target` from a press on its link, where the card of an agent that closed hangs.
pub(crate) fn open(state: &mut State, target: Subject, tx: &mut Tx) {
    if !state.terminals.knows(&target) {
        tx.live_only();
        return;
    }
    tx.find_links(href(&target), move |state: &mut State, links, tx| {
        let pressed = links.iter().find(|link| link.hovered);
        let pressed = pressed.or_else(|| links.iter().find(|link| link.focused));
        let line = pressed
            .or(links.first())
            .and_then(|link| link.lines.first());
        let mark = mark(target, line.copied().unwrap_or_default());
        header_pop::open_mark(state, Place::words(state), mark, tx);
    });
}

/// A box of the window that words show in, and the ids they show.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Reading {
    pub(crate) clip: [u32; 4],
    pub(crate) marks: Vec<Mark>,
}

pub(crate) fn readings(state: &State, links: &[ShownLink]) -> Vec<Reading> {
    let known: Vec<(Mark, [f32; 4])> = links
        .iter()
        .filter_map(|link| {
            let target = subject(&link.id).filter(|target| state.terminals.knows(target))?;
            Some((mark(target, *link.lines.first()?), link.clip))
        })
        .collect();
    let mut boxes: Vec<[f32; 4]> = Vec::new();
    for (_, clip) in &known {
        if !boxes.iter().any(|outer| holds(*outer, *clip)) {
            boxes.retain(|inner| !holds(*clip, *inner));
            boxes.push(*clip);
        }
    }
    let reading = |clip: &[f32; 4]| {
        let inside = known.iter().filter(|(_, own)| holds(*clip, *own));
        Reading {
            clip: clip.map(f32::to_bits),
            marks: inside.map(|(mark, _)| mark.clone()).collect(),
        }
    };
    boxes.iter().map(reading).collect()
}

fn holds([x, y, w, h]: [f32; 4], [ix, iy, iw, ih]: [f32; 4]) -> bool {
    x <= ix && y <= iy && ix + iw <= x + w && iy + ih <= y + h
}

/// Whether something in the window follows where the ids in its words are.
pub(crate) fn followed(state: &State) -> bool {
    let window = &state.window;
    window.words.inside || window.hints.on() || header_pop::hovers_words(state)
}

/// Takes in where the last frame showed the ids in the window's words. Returns whether that
/// changed what the window draws.
pub(crate) fn drawn(state: &mut State, links: Vec<ShownLink>) -> bool {
    let shown = |mark: &Mark| {
        let at = mark.at.map(f32::from_bits);
        let mut same = links
            .iter()
            .filter(|link| subject(&link.id).as_ref() == Some(&mark.target));
        same.any(|link| link.lines.contains(&at))
    };
    let let_go = header_pop::words_drawn(state, shown);
    let labelled = crate::hints::words_drawn(state, &links);
    if state.window.words.inside {
        state.window.words.links = links;
    }
    let_go | labelled
}
