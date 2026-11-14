//! A review document's tile body: the change an agent presents, read by keyboard. The daemon
//! sends a markdown tile its review bundle only while `code_review_enabled` is on, so a tile
//! with a bundle is a review and one without is plain markdown.

mod document;
mod keys;
mod stop;

use std::collections::HashMap;
use std::hash::{Hash, Hasher};

use attn_protocol::{ReviewBundle, ReviewRow};
use attn_ui::Tokens;
use rune::tok::{radius, space};
use rune::{El, Key, KeyEvent, ScrollSpec, Tx, col, memo};

use crate::State;
use document::Part;

/// The reading column's width, as the markdown reader's card caps it.
const CARD_MAX_WIDTH: f32 = 832.0;
const CARD_PADDING: f32 = 20.0;

/// Where the cursor stands, named so it stays on the same thing when the document or the
/// change moves around it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Spot {
    /// A block of prose, by its text and how many blocks with that text come before it.
    Block { text: u64, nth: usize },
    /// The change's facts under the title.
    Facts,
    /// A changed file listed under the facts.
    File(String),
    /// A row of a stop, by its file and its lines at base and now.
    Row {
        path: String,
        old: Option<i64>,
        now: Option<i64>,
    },
    /// An entry of the Not shown list, by its path.
    NotShown(String),
}

/// One place the cursor can stand, with the element that draws it.
pub(crate) struct Place {
    pub(crate) spot: Spot,
    pub(crate) key: String,
    pub(crate) part: usize,
    pub(crate) stop: Option<usize>,
}

/// What people did in one review tile.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
pub(crate) struct Review {
    cursor: Option<Spot>,
    /// The cursor's place among the places when it was last set, for when its spot leaves.
    at: usize,
    files_shown: bool,
    help: bool,
}

/// Each review tile's reading, by tile.
#[derive(Default)]
pub struct Reviews {
    tiles: HashMap<String, Review>,
}

/// The review `bundle` presents in `source`, laid out for reading.
pub(crate) struct Reading<'a> {
    source: &'a str,
    parts: Vec<Part>,
    places: Vec<Place>,
}

impl<'a> Reading<'a> {
    pub(crate) fn new(
        tile: &str,
        source: &'a str,
        bundle: &'a ReviewBundle,
        review: &Review,
    ) -> Self {
        let parts = document::parts(source, bundle);
        let places = places(tile, source, bundle, &parts, review.files_shown);
        Reading {
            source,
            parts,
            places,
        }
    }

    fn cursor(&self, review: &Review) -> Option<usize> {
        let spot = review.cursor.as_ref()?;
        let last = self.places.len().checked_sub(1)?;
        self.places
            .iter()
            .enumerate()
            .filter(|(_, p)| &p.spot == spot)
            .map(|(i, _)| i)
            .min_by_key(|i| i.abs_diff(review.at))
            .or(Some(review.at.min(last)))
    }
}

fn prefix(tile: &str) -> String {
    format!("tile.{tile}.review")
}

fn places(
    tile: &str,
    source: &str,
    bundle: &ReviewBundle,
    parts: &[Part],
    files_shown: bool,
) -> Vec<Place> {
    let prefix = prefix(tile);
    let mut seen: HashMap<u64, usize> = HashMap::new();
    let mut places = Vec::new();
    for (part, at) in parts.iter().zip(0..) {
        let key = format!("{prefix}.part.{at}");
        match part {
            Part::Prose { source: range, .. } => {
                let text = hash(&source[range.clone()]);
                let nth = seen.entry(text).or_default();
                places.push(Place {
                    spot: Spot::Block { text, nth: *nth },
                    key,
                    part: at,
                    stop: None,
                });
                *nth += 1;
            }
            Part::Summary => {
                places.push(Place {
                    spot: Spot::Facts,
                    key: format!("{prefix}.facts"),
                    part: at,
                    stop: None,
                });
                if files_shown {
                    places.extend(bundle.files.iter().zip(0..).map(|(file, i)| Place {
                        spot: Spot::File(file.path.clone()),
                        key: format!("{prefix}.file.{i}"),
                        part: at,
                        stop: None,
                    }));
                }
            }
            Part::Stop(stop) => {
                let rows = &bundle.stops[*stop].rows;
                places.extend(rows.iter().zip(0..).map(|(row, j)| Place {
                    spot: row_spot(&bundle.stops[*stop].path, row),
                    key: stop::row_key(&key, j),
                    part: at,
                    stop: Some(*stop),
                }));
            }
            Part::NotShown => {
                places.extend(bundle.not_shown.iter().zip(0..).map(|(entry, j)| Place {
                    spot: Spot::NotShown(entry.path.clone()),
                    key: format!("{key}.entry.{j}"),
                    part: at,
                    stop: None,
                }));
            }
        }
    }
    places
}

fn row_spot(path: &str, row: &ReviewRow) -> Spot {
    Spot::Row {
        path: path.to_owned(),
        old: row.old,
        now: row.now,
    }
}

fn hash(text: &str) -> u64 {
    let mut h = std::hash::DefaultHasher::new();
    text.hash(&mut h);
    h.finish()
}

/// The body of review tile `tile`: the document `source` with the review `bundle` drawn in
/// it, read in the scroll view keyed `view` and named `title`, and the keys under it. `read`
/// renders a block of the document's markdown and draws it again when `read_version` moves,
/// and `revision` moves when the content does.
pub(crate) fn body(
    tile: &str,
    view: &str,
    title: &str,
    (source, bundle, revision): (&str, &ReviewBundle, u64),
    reviews: &Reviews,
    (read, read_version): (&dyn Fn(&str) -> El, u64),
    t: &Tokens,
) -> El {
    let review = reviews.tiles.get(tile).cloned().unwrap_or_default();
    let reading = Reading::new(tile, source, bundle, &review);
    let cursor = reading.cursor(&review);
    let cursor_key = cursor.map(|i| reading.places[i].key.clone());
    let prefix = prefix(tile);
    let parts = reading.parts.iter().zip(0..).map(|(part, at)| {
        let key = format!("{prefix}.part.{at}");
        let on = |place: &Place| place.part == at;
        let current = cursor.filter(|&i| on(&reading.places[i]));
        match part {
            Part::Prose { source: range, .. } => {
                let text = &reading.source[range.clone()];
                let block = memo(format!("{key}.text"), (text, read_version, *t), || {
                    read(text)
                });
                cursor_frame(col().key(key).w_full().child(block), current.is_some(), t)
            }
            Part::Summary => keys::summary(
                &prefix,
                bundle,
                &review,
                current.map(|i| &reading.places[i].spot),
                t,
            ),
            Part::Stop(index) => {
                let first = reading.places.iter().position(on).unwrap_or(0);
                let row = current.map(|i| i - first);
                memo(key.clone(), (revision, row, *t), || {
                    stop::stop(&key, &bundle.stops[*index], row, t)
                })
            }
            Part::NotShown => {
                keys::not_shown(&key, bundle, current.map(|i| &reading.places[i].spot), t)
            }
        }
    });
    let card = col()
        .w_full()
        .max_w(CARD_MAX_WIDTH)
        .padding(CARD_PADDING)
        .gap(space::S3)
        .radius(radius::LG)
        .fill(t.bg_panel)
        .border(1.0, t.border.alpha(0.5))
        .children(parts);
    let mut document = col()
        .key(view)
        .role("document")
        .label(title)
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .scroll(ScrollSpec {
            stick_bottom: false,
        })
        .padding(space::S3)
        .items_center()
        .child(card);
    if let Some(key) = cursor_key {
        document = document.active_descendant(key);
    }
    col()
        .key(prefix.clone())
        .w_full()
        .h_full()
        .min_h(0.0)
        .child(document)
        .child(keys::key_line(&prefix, t))
        .children(review.help.then(|| keys::sheet(&prefix, t)))
}

/// A place's element, marked when the cursor stands on it.
pub(crate) fn cursor_frame(el: El, current: bool, t: &Tokens) -> El {
    let el = el.radius(radius::SM).px(space::S2);
    if current {
        el.fill(t.bg_selected).focus_selected(true)
    } else {
        el
    }
}

/// The review tile that holds the keyboard, with what the daemon last sent it.
fn held(
    state: &State,
) -> Option<(
    String,
    std::sync::Arc<attn_protocol::DesktopTileContentMessage>,
)> {
    let holder = state.window.keyboard.as_ref()?;
    let key = attn_client::TileKey {
        desktop: holder.desktop.clone(),
        tile: holder.pane.clone(),
    };
    let content = state.facts.tile_contents.get(&key)?;
    content.message.review.as_ref()?;
    Some((holder.pane.clone(), content.message.clone()))
}

/// The review's keys while its tile holds the keyboard and nothing else has focus: ↑/↓ move
/// by block and row, ←/→ by stop, Enter shows the changed files from the facts, `?` shows the
/// keys and Escape closes them.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if tx.focused().is_some() || ev.mods.cmd || ev.mods.ctrl || ev.mods.alt {
        return false;
    }
    let Some((tile, message)) = held(state) else {
        return false;
    };
    let Some(bundle) = message.review.as_ref() else {
        return false;
    };
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.clone())
        .or_default();
    let reading = Reading::new(&tile, &message.content, bundle, review);
    let cursor = reading.cursor(review);
    let to = match &ev.key {
        Key::Text(typed) if typed == "?" => {
            review.help = !review.help;
            None
        }
        Key::Escape if review.help => {
            review.help = false;
            None
        }
        Key::Down => Some(Move::Line(cursor.map_or(0, |i| i + 1))),
        Key::Up => Some(Move::Line(cursor.map_or(0, |i| i.saturating_sub(1)))),
        Key::Right => Some(Move::Stop(next_stop(&reading, cursor, true))),
        Key::Left => Some(Move::Stop(next_stop(&reading, cursor, false))),
        Key::Enter if cursor.is_some_and(|i| reading.places[i].spot == Spot::Facts) => {
            review.files_shown = !review.files_shown;
            None
        }
        _ => return false,
    };
    match to {
        Some(Move::Line(i)) if !reading.places.is_empty() => {
            let i = i.min(reading.places.len() - 1);
            let place = &reading.places[i];
            review.cursor = Some(place.spot.clone());
            review.at = i;
            tx.scroll_into_view(place.key.clone());
        }
        Some(Move::Stop(Some(i))) => {
            let place = &reading.places[i];
            review.cursor = Some(place.spot.clone());
            review.at = i;
            let lead = lead_in(&reading, place.part);
            if let Some(last) = reading.places.last() {
                tx.scroll_into_view(last.key.clone());
            }
            tx.scroll_into_view(format!("{}.part.{lead}", prefix(&tile)));
            tx.scroll_into_view(place.key.clone());
        }
        _ => {}
    }
    state.readings.revision += 1;
    true
}

enum Move {
    Line(usize),
    Stop(Option<usize>),
}

/// The first place of the next stop after the cursor, or of the one before it.
fn next_stop(reading: &Reading, cursor: Option<usize>, forward: bool) -> Option<usize> {
    let places = &reading.places;
    let here = cursor.and_then(|i| places[i].stop);
    let starts = places
        .iter()
        .enumerate()
        .filter(|(i, p)| p.stop.is_some() && (*i == 0 || places[i - 1].stop != p.stop))
        .map(|(i, _)| i);
    match (cursor, forward) {
        (None, true) => starts.into_iter().next(),
        (None, false) => None,
        (Some(at), true) => starts
            .into_iter()
            .find(|&i| i > at && places[i].stop != here),
        (Some(at), false) => starts
            .collect::<Vec<_>>()
            .into_iter()
            .rfind(|&i| i < at && places[i].stop != here),
    }
}

/// The part that introduces the stop at part `at`: the step's heading for a step's first
/// stop, the prose since the previous stop for a later one, the stop itself when nothing
/// stands between.
fn lead_in(reading: &Reading, at: usize) -> usize {
    let mut start = at;
    for i in (0..at).rev() {
        let part = &reading.parts[i];
        if matches!(part, Part::Stop(_)) {
            break;
        }
        start = i;
        if part.opens_step() {
            break;
        }
    }
    start
}
