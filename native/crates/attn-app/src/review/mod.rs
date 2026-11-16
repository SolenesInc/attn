//! A review document's tile body: the change an agent presents, read by keyboard. The daemon
//! sends a markdown tile its review bundle only while `code_review_enabled` is on, so a tile
//! with a bundle is a review and one without is plain markdown.

mod blocks;
mod diagram;
mod document;
mod edits;
mod file;
mod frames;
mod keys;
mod marks;
mod nav;
mod search;
pub(crate) mod setup;
mod stop;

pub use nav::places_built;

use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::ops::Range;
use std::rc::Rc;

use attn_protocol::{ReviewBundle, ReviewFlow, ReviewRow};
use attn_ui::Tokens;
use rune::tok::{radius, space, type_};
use rune::{El, Key, KeyEvent, ScrollSpec, Tx, col, memo, row, text};

use crate::State;
use document::Part;
use frames::{Frame, MapPlace, Side};

/// The reading column's width, as the markdown reader's card caps it.
const CARD_MAX_WIDTH: f32 = 832.0;
const CARD_PADDING: f32 = 20.0;
/// The frames the reader draws.
const BUILT: [Frame; 3] = [Frame::Brief, Frame::Walkthrough, Frame::Map];
/// The Walkthrough's script column is this wide, at most this share of the tile.
const SCRIPT_WIDTH: f32 = 440.0;
const SCRIPT_SHARE: f32 = 0.38;
/// The file view beside the document is this wide, at most this share of the tile.
const FILE_VIEW_WIDTH: f32 = 780.0;
const FILE_VIEW_SHARE: f32 = 0.64;
/// The Map's diagram column takes this share of the tile's width when it sits beside the step.
const DIAGRAM_SHARE: f32 = 0.44;

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
    /// An arrow of a drawn diagram that shows a stop: by its side, its diagram's place among
    /// that side's diagrams, the stop, what it says, and how many like it come before it.
    Arrow(String),
}

/// One place the cursor can stand, with the element that draws it.
pub(crate) struct Place {
    pub(crate) spot: Spot,
    pub(crate) key: String,
    pub(crate) part: usize,
    /// The stop whose row this is.
    pub(crate) stop: Option<usize>,
    /// The stop this arrow shows.
    pub(crate) arrow: Option<usize>,
    /// The first and last of its stop's rows this place covers: a row itself, a block of a
    /// document stop its lines.
    pub(crate) rows: (usize, usize),
}

/// What people did in one review tile.
#[derive(Default)]
pub(crate) struct Review {
    /// The frame picked with 1, 2 or 3; the document's own until then.
    frame: Option<Frame>,
    side: Side,
    map: MapPlace,
    cursor: Option<Spot>,
    /// The cursor's place among the places when it was last set, for when its spot leaves.
    at: usize,
    files_shown: bool,
    help: bool,
    /// The files opened from the review, the last on top.
    files: Vec<file::OpenFile>,
    /// Whether the file view fills the tile.
    filled: bool,
    next_file: u64,
    /// The rows `e` widened each stop to, by its path and first line at head.
    expansions: HashMap<(String, i64), Expansion>,
    /// The content revision the open files and expansions were last read for.
    seen: u64,
    /// The stop the Walkthrough's stage shows, by its path, its first line at head and its
    /// place among the stops, which keeps it when the document changes.
    stage: Option<(String, i64, usize)>,
    /// The other end of rows selected in a stop, which ends at the cursor.
    select_from: Option<Spot>,
    /// The send sheet, open with the verdict picked and where the review goes.
    sending: Option<(marks::Verdict, Option<crate::markdown::drafts::To>)>,
    /// Code navigation: a question being picked, asked, listed or offered.
    nav: nav::Nav,
    /// The verdict picked in the send sheet, kept until the review goes.
    verdict: Option<marks::Verdict>,
    /// Counts the reads the tile asks for, so an answer to an older read of a file or a stop
    /// never lands over a newer one.
    asked: u64,
    /// The latest read asked for each widened stop.
    widening: HashMap<(String, i64), u64>,
    /// The file and line now an edit made in the document changed first: the cursor stands on
    /// that added line once the document shows it, until the cursor moves.
    edited: Option<(String, i64)>,
    /// The files the running warm-up still means to color, and its round: a warm-up whose
    /// round has passed stops.
    warming: (u64, Vec<(String, String)>),
    sideways: file::Sideways,
}

/// A stop's rows widened by `extra` lines on each side, from its whole file, and the stop as
/// it was when they were read.
pub(crate) struct Expansion {
    rows: Rc<[ReviewRow]>,
    extra: usize,
    stop: String,
}

/// A stop as an expansion remembers it, to tell whether the stop changed since.
fn stop_seen(stop: &attn_protocol::ReviewStop) -> String {
    serde_json::to_string(stop).unwrap_or_default()
}

/// Lines of files the reader keeps colors for, past which it forgets the oldest.
const COLORED_LINES: usize = 25_000;

/// Each review tile's reading, by tile.
#[derive(Default)]
pub struct Reviews {
    tiles: HashMap<String, Review>,
    /// Each file's colors, by its path and the hash of its content, oldest first.
    colors: Vec<((String, String), Rc<stop::Colors>)>,
    /// Files too long to color, by path and hash, which are not read again to color them.
    oversize: Vec<(String, String)>,
    /// The search open over a review tile.
    search: Option<search::Search>,
    /// The note or question being written in a review tile.
    composer: Option<marks::Composer>,
    /// The text of the composer's box and of the search's box. They outlive their boxes, so
    /// an edit that lands after a box closed opens nothing.
    composer_text: rune::TextEdit,
    search_query: rune::TextEdit,
    /// The edit being written in a review tile, and its box's text.
    editing: Option<edits::Open>,
    edit_text: rune::TextEdit,
}

impl Reviews {
    /// Each widened stop, by its tile, path and first line, with the lines it was widened by.
    pub(crate) fn widenings(&self) -> Vec<(&str, &str, i64, usize)> {
        let mut all: Vec<_> = self
            .tiles
            .iter()
            .flat_map(|(tile, review)| {
                review
                    .expansions
                    .iter()
                    .map(|((path, from), e)| (tile.as_str(), path.as_str(), *from, e.extra))
            })
            .collect();
        all.sort_unstable();
        all
    }

    fn nav_mut(&mut self, tile: &str) -> Option<&mut nav::Nav> {
        self.tiles.get_mut(tile).map(|r| &mut r.nav)
    }

    /// Whether a box over a review takes the keys: an edit, a note, the search or the send.
    fn boxed(&self) -> bool {
        self.editing.is_some()
            || self.composer.is_some()
            || self.search.is_some()
            || self.tiles.values().any(|r| r.sending.is_some() || r.help)
    }

    fn colors(&self, path: &str, hash: &str) -> Option<Rc<stop::Colors>> {
        self.colors
            .iter()
            .find(|((p, h), _)| p == path && h == hash)
            .map(|(_, c)| c.clone())
    }

    /// Keeps the colors of `path` at `hash` from its `rows`, forgetting the oldest past
    /// `COLORED_LINES`; a file longer than that is noted as too long and not colored.
    fn keep_colors(&mut self, path: &str, hash: &str, rows: &[ReviewRow]) {
        let key = (path.to_owned(), hash.to_owned());
        if hash.is_empty() || self.colors(path, hash).is_some() || self.oversize.contains(&key) {
            return;
        }
        if rows.len() > COLORED_LINES {
            self.oversize.push(key);
            return;
        }
        let colors = Rc::new(stop::Colors::read(path, rows));
        self.colors
            .push(((path.to_owned(), hash.to_owned()), colors.clone()));
        while self.colors.len() > 1
            && self.colors.iter().map(|(_, c)| c.len()).sum::<usize>() > COLORED_LINES
        {
            self.colors.remove(0);
        }
    }

    /// Whether the reader has colors for `path` at `hash`, or knows it is too long for them.
    fn colored(&self, path: &str, hash: &str) -> bool {
        self.colors(path, hash).is_some()
            || self.oversize.iter().any(|(p, h)| p == path && h == hash)
    }
}

/// The review `bundle` presents in `source`, laid out for reading in its frame.
pub(crate) struct Reading<'a> {
    source: &'a str,
    bundle: &'a ReviewBundle,
    frame: Frame,
    side: Side,
    parts: Vec<Part>,
    /// The parts the frame shows in its document: all of them in the Brief, the step that holds
    /// the current arrow's stop in the Map.
    shown: Range<usize>,
    /// The Map's current arrow, among the arrows its diagrams tie to stops.
    map: Option<(usize, usize)>,
    places: Vec<Place>,
    /// The rows `e` widened each stop to, by the stop's index.
    widened: Vec<Option<Rc<[ReviewRow]>>>,
    /// In the Walkthrough, the stop the stage shows and the line its cursor stands on.
    stage: Option<(usize, Option<i64>)>,
}

impl<'a> Reading<'a> {
    pub(crate) fn new(
        tile: &str,
        source: &'a str,
        bundle: &'a ReviewBundle,
        review: &Review,
    ) -> Self {
        let frame = review
            .frame
            .filter(|f| frames::offered(bundle, &BUILT).contains(f))
            .unwrap_or_else(|| frames::opening(bundle, &BUILT));
        let side = review.side;
        let parts = document::parts(source, bundle);
        let (shown, map) = match frame {
            Frame::Map => {
                let list = frames::map_stops(bundle, side);
                let index = frames::map_index(bundle, &list, &review.map);
                let stop = list.get(index).map(|m| m.stop);
                (step(&parts, stop), Some((index, list.len())))
            }
            _ => (0..parts.len(), None),
        };
        let stage = (frame == Frame::Walkthrough)
            .then(|| {
                let stop = stage_stop(bundle, review.stage.as_ref())?;
                Some((stop, review.files.first().and_then(file::OpenFile::line)))
            })
            .flatten();
        let mut reading = Reading {
            stage,
            source,
            bundle,
            frame,
            side,
            parts,
            shown,
            map,
            places: Vec::new(),
            widened: bundle
                .stops
                .iter()
                .map(|s| {
                    review
                        .expansions
                        .get(&(s.path.clone(), s.from))
                        .map(|e| e.rows.clone())
                })
                .collect(),
        };
        reading.places = reading.places_of(tile, review.files_shown);
        reading
    }

    /// The rows stop `stop` shows: its own, or the ones `e` widened it to.
    fn rows_of(&self, stop: usize) -> &[ReviewRow] {
        self.widened[stop]
            .as_deref()
            .unwrap_or(&self.bundle.stops[stop].rows)
    }

    /// Whether part `at` shows in the frame's document.
    fn shows(&self, at: usize) -> bool {
        self.shown.contains(&at)
            && match (&self.parts[at], self.frame) {
                (Part::Summary | Part::Diagram(_), Frame::Map) => false,
                (Part::Diagram(flow), _) => {
                    frames::shows(self.bundle, &self.bundle.flows[*flow], self.side)
                }
                _ => true,
            }
    }

    fn places_of(&self, tile: &str, files_shown: bool) -> Vec<Place> {
        if self.frame == Frame::Walkthrough {
            return Vec::new();
        }
        let prefix = prefix(tile);
        let bundle = self.bundle;
        let mut seen: HashMap<u64, usize> = HashMap::new();
        let mut arrows: HashMap<String, usize> = HashMap::new();
        let mut places = Vec::new();
        let place = |spot, key, part| Place {
            spot,
            key,
            part,
            stop: None,
            arrow: None,
            rows: (0, 0),
        };
        for (part, at) in self.parts.iter().zip(0..) {
            if !self.shows(at) {
                continue;
            }
            let key = format!("{prefix}.part.{at}");
            match part {
                Part::Prose { source: range, .. } => {
                    let text = hash(&self.source[range.clone()]);
                    let nth = seen.entry(text).or_default();
                    places.push(place(Spot::Block { text, nth: *nth }, key, at));
                    *nth += 1;
                }
                Part::Summary => {
                    places.push(place(Spot::Facts, format!("{prefix}.facts"), at));
                    if files_shown {
                        places.extend(bundle.files.iter().zip(0..).map(|(file, i)| {
                            place(
                                Spot::File(file.path.clone()),
                                format!("{prefix}.file.{i}"),
                                at,
                            )
                        }));
                    }
                }
                Part::Stop(stop) => {
                    let rows = self.rows_of(*stop);
                    let path = &bundle.stops[*stop].path;
                    if blocks::is_document(path) {
                        let read = blocks::blocks(rows);
                        places.extend(read.blocks.iter().zip(0..).map(|(b, j)| Place {
                            stop: Some(*stop),
                            rows: b.rows,
                            ..place(
                                Spot::Row {
                                    path: path.clone(),
                                    old: None,
                                    now: Some(b.from),
                                },
                                stop::row_key(&key, j),
                                at,
                            )
                        }));
                    } else {
                        places.extend(rows.iter().zip(0..).map(|(row, j)| Place {
                            stop: Some(*stop),
                            rows: (j, j),
                            ..place(row_spot(path, row), stop::row_key(&key, j), at)
                        }));
                    }
                }
                Part::NotShown => {
                    places.extend(bundle.not_shown.iter().zip(0..).map(|(entry, j)| {
                        place(
                            Spot::NotShown(entry.path.clone()),
                            format!("{key}.entry.{j}"),
                            at,
                        )
                    }));
                }
                Part::Diagram(index) => {
                    let flow = &bundle.flows[*index];
                    let diagram = bundle.flows[..*index]
                        .iter()
                        .filter(|f| f.drawn && f.side == flow.side)
                        .count();
                    for message in &flow.messages {
                        let Some(stop) = message
                            .stop_line
                            .and_then(|l| bundle.stops.iter().position(|s| s.heading_line == l))
                        else {
                            continue;
                        };
                        let s = &bundle.stops[stop];
                        let base = format!(
                            "{}:{diagram}:{}:{}:{}",
                            flow.side.as_deref().unwrap_or(""),
                            s.path,
                            s.from,
                            message.text
                        );
                        let nth = arrows.entry(base.clone()).or_default();
                        places.push(Place {
                            arrow: Some(stop),
                            ..place(
                                Spot::Arrow(format!("{base}:{nth}")),
                                arrow_key(&key, message.line),
                                at,
                            )
                        });
                        *nth += 1;
                    }
                }
            }
        }
        places
    }

    fn cursor(&self, review: &Review) -> Option<usize> {
        let last = self.places.len().checked_sub(1)?;
        if let Some((path, line)) = &review.edited {
            let edited = Spot::Row {
                path: path.clone(),
                old: None,
                now: Some(*line),
            };
            if let Some(at) = self.places.iter().position(|p| p.spot == edited) {
                return Some(at);
            }
        }
        let Some(spot) = review.cursor.as_ref() else {
            return self.map_start();
        };
        self.places
            .iter()
            .enumerate()
            .filter(|(_, p)| &p.spot == spot)
            .map(|(i, _)| i)
            .min_by_key(|i| i.abs_diff(review.at))
            .or_else(|| self.map_start())
            .or(Some(review.at.min(last)))
    }

    /// In the Map, the first row of the current arrow's stop.
    fn map_start(&self) -> Option<usize> {
        let (index, _) = self.map?;
        let stop = frames::map_stops(self.bundle, self.side).get(index)?.stop;
        self.places.iter().position(|p| p.stop == Some(stop))
    }

    /// The stop and line the cursor stands on: a row's, an arrow's stop, or the next stop after
    /// prose.
    fn place(&self, cursor: Option<usize>) -> Option<(usize, Option<i64>)> {
        if self.frame == Frame::Walkthrough {
            return self.stage;
        }
        let at = cursor?;
        let place = &self.places[at];
        if let Some(stop) = place.arrow {
            return Some((stop, None));
        }
        if let Some(stop) = place.stop {
            let now = match &place.spot {
                Spot::Row { now, .. } => *now,
                _ => None,
            };
            return Some((stop, now));
        }
        self.parts[place.part..].iter().find_map(|p| match p {
            Part::Stop(s) => Some((*s, None)),
            _ => None,
        })
    }
}

/// The stop a Walkthrough's stage named `key` shows now: the one at the same path and first
/// line, else the one at its place, else the last; the first when it named none.
fn stage_stop(bundle: &ReviewBundle, key: Option<&(String, i64, usize)>) -> Option<usize> {
    let last = bundle.stops.len().checked_sub(1)?;
    let Some((path, from, index)) = key else {
        return Some(0);
    };
    bundle
        .stops
        .iter()
        .position(|s| &s.path == path && s.from == *from)
        .or(Some((*index).min(last)))
}

/// Where the Walkthrough opens a stop's file: the place to look, else its first change, else
/// its first line.
fn stage_line(stop: &attn_protocol::ReviewStop) -> Option<i64> {
    let look = stop.notes.iter().find(|n| n.look && n.now_from > 0);
    if let Some(look) = look {
        return Some(look.now_from);
    }
    let change = stop.rows.iter().find(|r| {
        r.kind == "+"
            && r.now
                .is_some_and(|n| n >= stop.now_from && n <= stop.now_to)
    });
    change
        .and_then(|r| r.now)
        .or((stop.now_from > 0).then_some(stop.now_from))
}

/// The parts of the step that holds stop `stop`: from the `#` or `##` heading above it to the
/// next one. With no stop, none.
fn step(parts: &[Part], stop: Option<usize>) -> Range<usize> {
    let Some(at) = parts
        .iter()
        .position(|p| stop.is_some_and(|s| *p == Part::Stop(s)))
    else {
        return 0..0;
    };
    let start = (0..=at).rev().find(|&i| parts[i].opens_step()).unwrap_or(0);
    let end = (at + 1..parts.len())
        .find(|&i| parts[i].opens_step())
        .unwrap_or(parts.len());
    start..end
}

fn prefix(tile: &str) -> String {
    format!("tile.{tile}.review")
}

/// The key of review tile `tile`'s card, which its marks' toolbar and comment box sit over.
pub(crate) fn card_key(tile: &str) -> String {
    format!("{}.card", prefix(tile))
}

fn arrow_key(part: &str, line: i64) -> String {
    format!("{part}.arrow.{line}")
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
/// renders the block of the document's markdown at those source bytes and draws it again when
/// `read_version` moves, `revision` moves when the content does, and `marking` sits over the
/// card.
#[allow(clippy::too_many_arguments)]
pub(crate) fn body(
    tile: &str,
    view: &str,
    title: &str,
    (source, bundle, revision): (&str, &ReviewBundle, u64),
    readings: &crate::markdown::Readings,
    (document, pictures, pictured): (
        &crate::markdown::Document,
        &Rc<HashMap<String, Option<rune::Image>>>,
        u64,
    ),
    (read, read_version): (&dyn Fn(std::ops::Range<usize>) -> El, u64),
    marking: Vec<El>,
    draft: &[attn_protocol::MarkdownAnnotation],
    (host, servers): (Option<&str>, setup::Servers),
    t: &Tokens,
) -> El {
    let reviews = &readings.reviews;
    let sent = readings.tiles.get(tile).and_then(|r| r.sent.as_ref());
    let fresh = Review::default();
    let review = reviews.tiles.get(tile).unwrap_or(&fresh);
    let reading = Reading::new(tile, source, bundle, review);
    let placed = placements(review, bundle, draft);
    let asking = stop::Asking {
        labels: review.nav.picking.as_ref().map(|(_, line, names, typed)| {
            let shown = names
                .iter()
                .filter(|n| n.label.starts_with(typed.as_str()))
                .cloned()
                .collect();
            (line.row.clone(), shown)
        }),
        asked: review.nav.asking.as_ref().map(|(_, row, _, _)| row.clone()),
    };
    let marks_on = |path: &str| -> Vec<(i64, &attn_protocol::MarkdownAnnotation)> {
        placed
            .iter()
            .filter_map(|(m, p, at)| match at {
                marks::Placed::Line(line) if p == path => Some((*line, *m)),
                _ => None,
            })
            .collect()
    };
    let marks_at = |open: &file::OpenFile| -> Vec<El> {
        let line = open.line();
        marks_on(&open.target.path)
            .into_iter()
            .filter(|(at, _)| Some(*at) == line)
            .map(|(_, m)| marks::mark_row(m, t))
            .collect()
    };
    let moved: Vec<&attn_protocol::MarkdownAnnotation> = placed
        .iter()
        .filter(|(_, _, at)| *at == marks::Placed::Moved)
        .map(|(m, _, _)| *m)
        .collect();
    let cursor = reading.cursor(review);
    let cursor_key = cursor.map(|i| reading.places[i].key.clone());
    let prefix = prefix(tile);
    let sides = frames::has_sides(bundle);
    let parts = reading.parts.iter().zip(0..).filter_map(|(part, at)| {
        if !reading.shows(at) {
            return None;
        }
        let key = format!("{prefix}.part.{at}");
        let on = |place: &Place| place.part == at;
        let current = cursor.filter(|&i| on(&reading.places[i]));
        Some(match part {
            Part::Prose { source: range, .. } => {
                let text = &reading.source[range.clone()];
                let at = (text, range.start, read_version, *t);
                let block = memo(format!("{key}.text"), at, || read(range.clone()));
                let el = place_press(col().key(key.clone()).w_full().child(block), tile, key);
                cursor_frame(el, current.is_some(), t)
            }
            Part::Summary => keys::summary(
                tile,
                bundle,
                review,
                current.map(|i| &reading.places[i].spot),
                t,
            ),
            Part::Stop(index) if reading.frame == Frame::Walkthrough => {
                let stage = reading
                    .stage
                    .filter(|(s, _)| s == index)
                    .map(|(_, line)| line);
                let script = stop::script(&key, &bundle.stops[*index], stage, t);
                if stage.is_some() {
                    script
                } else {
                    let stop = *index;
                    script.on_press_tx(pressed(tile, move |state, tile, message, tx| {
                        let Some(bundle) = message.review.as_ref() else {
                            return;
                        };
                        let parts = document::parts(&message.content, bundle);
                        apply(
                            state,
                            tile,
                            message,
                            &parts,
                            None,
                            Some(Move::Stage(stop, None)),
                            tx,
                        );
                    }))
                }
            }
            Part::Stop(index) => {
                let first = reading.places.iter().position(on).unwrap_or(0);
                let row = current.map(|i| i - first);
                let s = &bundle.stops[*index];
                let colors = reviews.colors(&s.path, &s.file_hash);
                let rows = reading.rows_of(*index);
                let on = marks_on(&s.path);
                let selected_places = current
                    .filter(|_| review.select_from.is_some())
                    .and_then(|c| selected_places(&reading, review, c))
                    .map(|(a, b)| (a - first, b - first));
                let selected = current
                    .filter(|_| review.select_from.is_some())
                    .and_then(|c| selected_rows(&reading, review, c))
                    .map(|(_, a, b)| (a, b));
                let marked: Vec<_> = on
                    .iter()
                    .map(|(l, m)| (*l, m.id.clone(), m.text.clone(), m.answer.clone()))
                    .collect();
                let version = (
                    revision,
                    row,
                    selected,
                    colors.is_some(),
                    rows.len(),
                    marked,
                    asking.clone(),
                    pictured,
                    *t,
                );
                memo(key.clone(), version, || {
                    if blocks::is_document(&s.path) {
                        let file = crate::markdown::Document {
                            path: in_repository(bundle, &s.path),
                            ..document.clone()
                        };
                        return blocks::view(
                            (tile, &key, host.is_none()),
                            s,
                            (&file, pictures),
                            (rows, &blocks::blocks(rows)),
                            (row, selected_places),
                            &on,
                            t,
                        );
                    }
                    stop::stop(
                        (tile, &key),
                        s,
                        rows,
                        (row, selected),
                        (&on, &asking),
                        colors.as_deref(),
                        t,
                    )
                })
            }
            Part::NotShown => keys::not_shown(
                (Some(tile), &key),
                bundle,
                current.map(|i| &reading.places[i].spot),
                t,
            ),
            Part::Diagram(index) => {
                let flow = &bundle.flows[*index];
                let current = current.map(|i| reading.places[i].key.clone());
                let drawn = memo(key.clone(), (revision, current.clone(), *t), || {
                    diagram::diagram(
                        &key,
                        flow,
                        &|m| {
                            let place = m.stop_line.map(|_| arrow_key(&key, m.line));
                            diagram::Mark {
                                current: place.is_some() && place == current,
                                place: place.map(|key| (tile.to_owned(), key)),
                            }
                        },
                        t,
                    )
                });
                let switch = (sides && flow.side.is_some())
                    .then(|| side_switch(&format!("{key}.side"), tile, reading.side, t));
                col().w_full().gap(space::S1).children(switch).child(drawn)
            }
        })
    });
    let card = col()
        .key(card_key(tile))
        .w_full()
        .max_w(CARD_MAX_WIDTH)
        .padding(CARD_PADDING)
        .gap(space::S3)
        .radius(radius::LG)
        .fill(t.bg_panel)
        .border(1.0, t.border.alpha(0.5))
        .children(parts)
        .children((!moved.is_empty()).then(|| {
            col()
                .key(format!("{prefix}.moved"))
                .role("region")
                .label("Moved notes")
                .w_full()
                .gap(space::S1)
                .child(text("Moved notes").font(type_::TITLE).color(t.text_primary))
                .child(
                    text("The lines these were written on changed. They still go with the review, marked as moved.")
                        .font(type_::BODY)
                        .color(t.text_secondary)
                        .wrap(),
                )
                .children(moved.iter().map(|m| marks::mark_row(m, t)))
        }))
        .children(marking);
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
        .on_layout_tx({
            let view = view.to_owned();
            move |state: &mut State, [_, _, w, _], tx: &mut Tx| {
                state.readings.measured(&view, w, tx)
            }
        })
        .child(card);
    if let Some(key) = cursor_key {
        document = document.active_descendant(key);
    }
    let frame = match reading.frame {
        Frame::Map => map(tile, &reading, document, revision, t),
        Frame::Walkthrough => {
            let width = rune::memo::layout(&prefix).map_or(0.0, |r| r[2]);
            let script = (!review.filled).then(|| {
                document
                    .w(SCRIPT_WIDTH.min(width * SCRIPT_SHARE))
                    .border_sides([0.0, 1.0, 0.0, 0.0], t.border)
            });
            let stage = match review.files.last() {
                Some(open) => {
                    let notes: Vec<&attn_protocol::ReviewNote> = bundle
                        .stops
                        .iter()
                        .filter(|s| s.path == open.target.path)
                        .flat_map(|s| &s.notes)
                        .collect();
                    let colors = reviews.colors(&open.target.path, &open.hash);
                    file::view(
                        &prefix,
                        tile,
                        open,
                        &review.sideways,
                        (review.files.len(), true),
                        (&notes, marks_at(open)),
                        (colors, &asking),
                        t,
                    )
                    .grow(1.0)
                    .min_w(0.0)
                }
                None => col().grow(1.0),
            };
            row()
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .children(script)
                .child(stage)
        }
        Frame::Brief => document,
    };
    let frame = match review
        .files
        .last()
        .filter(|_| reading.frame != Frame::Walkthrough)
    {
        None => frame,
        Some(open) => {
            let notes: Vec<&attn_protocol::ReviewNote> = bundle
                .stops
                .iter()
                .filter(|s| s.path == open.target.path)
                .flat_map(|s| &s.notes)
                .collect();
            let colors = reviews.colors(&open.target.path, &open.hash);
            let view = file::view(
                &prefix,
                tile,
                open,
                &review.sideways,
                (review.files.len(), false),
                (&notes, marks_at(open)),
                (colors, &asking),
                t,
            );
            let width = rune::memo::layout(&prefix).map_or(0.0, |r| r[2]);
            let view = if review.filled {
                view.pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
            } else {
                view.pin(None, Some(0.0), Some(0.0), Some(0.0))
                    .w(FILE_VIEW_WIDTH.min(width * FILE_VIEW_SHARE))
            };
            col().w_full().grow(1.0).min_h(0.0).child(frame).child(view)
        }
    };
    let offered = frames::offered(bundle, &BUILT);
    let read_only = reads_outside(review);
    col()
        .key(prefix.clone())
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(frame)
        .children(nav::note_line(tile, &review.nav, t).map(|n| n.px(space::S3)))
        .children(
            review.nav.panel.as_ref().map(|p| {
                nav::panel_view(tile, p, t).pin(Some(0.0), Some(space::S6), Some(0.0), None)
            }),
        )
        .child(keys::key_line(
            tile,
            reading.frame,
            &offered,
            (
                sides,
                !review.files.is_empty() && reading.frame != Frame::Walkthrough,
                !read_only,
            ),
            setup::states(tile, bundle, host, servers, t),
            t,
        ))
        .children(review.help.then(|| keys::sheet(&prefix, read_only, t)))
        .children(
            reviews
                .editing
                .as_ref()
                .filter(|e| e.tile == tile)
                .map(|e| {
                    edits::view(e, &reviews.edit_text, t).pin(
                        Some(0.0),
                        Some(space::S6),
                        Some(0.0),
                        None,
                    )
                }),
        )
        .children(
            reviews
                .composer
                .as_ref()
                .filter(|c| c.tile == tile)
                .map(|c| {
                    marks::composer_view(c, &reviews.composer_text, t).pin(
                        Some(0.0),
                        Some(space::S6),
                        Some(0.0),
                        None,
                    )
                }),
        )
        .children(review.sending.as_ref().map(|(verdict, destination)| {
            let carried: Vec<&attn_protocol::MarkdownAnnotation> = draft.iter().collect();
            marks::sheet(tile, &carried, *verdict, destination.as_ref(), sent, t).pin(
                Some(0.0),
                Some(space::S6),
                Some(0.0),
                None,
            )
        }))
        .children(reviews.search.as_ref().filter(|s| s.tile == tile).map(|s| {
            search::view(s, &reviews.search_query, bundle, t).pin(
                Some(0.0),
                Some(space::S6),
                Some(0.0),
                None,
            )
        }))
}

/// The Before and After switch over a diagram, keyed `key`, with `b` or a press on a side.
fn side_switch(key: &str, tile: &str, side: Side, t: &Tokens) -> El {
    let tab = |s: Side, name: &str| {
        let on = s == side;
        text(name.to_owned())
            .key(format!("{key}.{}", s.name()))
            .font(type_::LABEL)
            .color(if on { t.text_primary } else { t.text_tertiary })
            .role("tab")
            .label(name.to_owned())
            .focus_selected(on)
            .on_press_tx(pressed(tile, move |state, tile, _, tx| {
                pick_side(state, tile, s, tx)
            }))
    };
    row()
        .role("tablist")
        .label("Sides")
        .gap(space::S2)
        .child(tab(Side::Before, "Before"))
        .child(tab(Side::After, "After"))
        .child(text("b").font(type_::LABEL).color(t.text_tertiary))
}

/// The Map: the diagrams with the arrow it stands on, the Not shown list under them and a line
/// naming the arrow, beside the document narrowed to the step that explains the arrow, or above
/// it when the tile is too narrow for both.
fn map(tile: &str, reading: &Reading, document: El, revision: u64, t: &Tokens) -> El {
    let prefix = &prefix(tile);
    let bundle = reading.bundle;
    let list = frames::map_stops(bundle, reading.side);
    let (index, count) = reading.map.unwrap_or((0, 0));
    let current = list.get(index);
    let diagrams = bundle
        .flows
        .iter()
        .zip(0..)
        .filter(|(f, _)| frames::shows(bundle, f, reading.side))
        .map(|(flow, i)| {
            let key = format!("{prefix}.map.{i}");
            let at = current
                .filter(|c| std::ptr::eq(c.flow, flow))
                .map(|c| c.message.line);
            memo(key.clone(), (revision, at, *t), || {
                diagram::diagram(
                    &key,
                    flow,
                    &|m| diagram::Mark {
                        place: None,
                        current: Some(m.line) == at,
                    },
                    t,
                )
            })
        });
    let arrow = match current {
        Some(c) => {
            let sided = if frames::has_sides(bundle) {
                format!(" · {} the change", reading.side.name())
            } else {
                String::new()
            };
            let route = format!(
                "{} → {}",
                participant(c.flow, &c.message.from),
                participant(c.flow, &c.message.to)
            );
            col()
                .key(format!("{prefix}.map.arrow"))
                .role("region")
                .label("The arrow")
                .gap(2.0)
                .child(
                    text(format!("Arrow {} of {count}{sided}", index + 1))
                        .font(type_::LABEL)
                        .color(t.text_tertiary),
                )
                .child(
                    text(c.message.text.clone())
                        .font(type_::TITLE)
                        .color(t.text_primary)
                        .wrap(),
                )
                .child(text(route).font(type_::LABEL).color(t.text_secondary))
        }
        None => text("No arrow in this diagram is tied to a stop.")
            .font(type_::BODY)
            .color(t.text_tertiary),
    };
    let not_shown = (!bundle.not_shown.is_empty())
        .then(|| keys::not_shown((None, &format!("{prefix}.map.not-shown")), bundle, None, t));
    let sides = frames::has_sides(bundle)
        .then(|| side_switch(&format!("{prefix}.map.side"), tile, reading.side, t));
    let diagrams = col()
        .key(format!("{prefix}.map"))
        .role("region")
        .label("Map")
        .gap(space::S3)
        .padding(space::S3)
        .children(sides)
        .children(diagrams)
        .children(not_shown);
    let widest = bundle
        .flows
        .iter()
        .filter(|f| f.drawn)
        .map(diagram::width)
        .fold(0.0, f32::max);
    let width = rune::memo::layout(prefix).map_or(0.0, |r| r[2]);
    let step = col()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(col().px(space::S3).pt(space::S2).child(arrow))
        .child(document);
    if width * DIAGRAM_SHARE - 25.0 >= widest {
        row()
            .w_full()
            .grow(1.0)
            .min_h(0.0)
            .child(
                diagrams
                    .w(width * DIAGRAM_SHARE)
                    .h_full()
                    .scroll(ScrollSpec {
                        stick_bottom: false,
                    }),
            )
            .child(step)
    } else {
        col()
            .w_full()
            .grow(1.0)
            .min_h(0.0)
            .child(
                diagrams
                    .w_full()
                    .max_h(width.max(320.0) * 0.6)
                    .scroll(ScrollSpec {
                        stick_bottom: false,
                    }),
            )
            .child(step)
    }
}

fn participant(flow: &ReviewFlow, id: &str) -> String {
    let label = flow
        .participants
        .iter()
        .find(|p| p.id == id)
        .map_or(id, |p| p.label.as_str());
    let lower = label.to_ascii_lowercase();
    let end = lower.find("<br").unwrap_or(label.len());
    label[..end].trim().to_owned()
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

/// The send key on a review tile that holds the keyboard: the send sheet opens with the
/// verdict picked before, else request changes when the draft carries anything and approve
/// when it does not. False when no review tile holds the keyboard.
pub(crate) fn open_send(state: &mut State, tx: &mut Tx) -> bool {
    let Some((tile, _)) = held(state) else {
        return false;
    };
    open_sheet(state, &tile, tx)
}

/// The send of tile `tile`, from the send key or its header's Send: on a review tile the send
/// sheet opens, as `open_send` says. False when the tile shows no review.
pub(crate) fn open_sheet(state: &mut State, tile: &str, tx: &mut Tx) -> bool {
    let reviewing = state
        .facts
        .tile_contents
        .tiles
        .iter()
        .any(|(k, c)| k.tile == tile && c.message.review.is_some());
    if !reviewing {
        return false;
    }
    if state.readings.reviews.composer.is_some() {
        tx.focus(marks::FIELD, false);
        return true;
    }
    if state.readings.reviews.editing.is_some() {
        tx.focus(edits::FIELD, false);
        return true;
    }
    let Some((uri, to)) = crate::markdown::sending::destination_of(state, tile) else {
        return false;
    };
    let marked = state.drafts.get(&uri).is_some_and(|d| !d.marks.is_empty());
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.to_owned())
        .or_default();
    let verdict = review.verdict.unwrap_or(if marked {
        marks::Verdict::RequestChanges
    } else {
        marks::Verdict::Approve
    });
    review.sending = Some((verdict, to));
    state.readings.revision += 1;
    true
}

/// Review tile `tile`'s review went: its send sheet closes and its verdict is forgotten.
pub(crate) fn sent(state: &mut State, tile: &str) {
    if let Some(review) = state.readings.reviews.tiles.get_mut(tile) {
        review.sending = None;
        review.verdict = None;
    }
    state.readings.revision += 1;
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
/// by block and row, ←/→ by stop or, in the Map, by arrow, `1` to `3` switch frames, `b`
/// switches the diagrams' side, Enter shows the changed files from the facts, `?` shows the
/// keys and Escape closes them.
pub(crate) fn on_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let back_key = matches!(ev.key, Key::Command(c) if c.eq_ignore_ascii_case(&'o'))
        && ev.mods.ctrl
        && !ev.mods.cmd
        && !ev.mods.alt;
    if tx.focused().is_some() || (!back_key && (ev.mods.cmd || ev.mods.ctrl || ev.mods.alt)) {
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
    let typed = match &ev.key {
        Key::Text(typed) => typed.as_str(),
        _ => "",
    };
    if let Some((verdict, to)) = review.sending.clone() {
        let picked = marks::Verdict::of_key(typed);
        match ev.key {
            Key::Escape => review.sending = None,
            Key::Enter => {
                if let Some(to) = to {
                    let moved = moved_marks(state, &tile, &message, bundle);
                    let uri = drafts_uri(&message);
                    marks::send(state, (&tile, &uri), to, verdict, moved, tx);
                }
            }
            _ if let Some(picked) = picked => pick_verdict(review, picked),
            _ => {}
        }
        state.readings.revision += 1;
        return true;
    }
    if let Some(done) = nav_key(state, &tile, &message, &reading, ev, typed, tx) {
        state.readings.revision += 1;
        return done;
    }
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.clone())
        .or_default();
    if back_key {
        let went = go_back(review, reading.frame == Frame::Walkthrough);
        state.readings.revision += 1;
        return went;
    }
    if reading.frame == Frame::Walkthrough && !review.help && typed != "?" {
        let stage = reading.stage.map(|(s, _)| s);
        match &ev.key {
            Key::Right | Key::Left if !ev.mods.shift => {
                let Some(at) = stage else { return true };
                let next = if ev.key == Key::Right {
                    (at + 1).min(bundle.stops.len().saturating_sub(1))
                } else {
                    at.saturating_sub(1)
                };
                let to = (next != at).then_some(Move::Stage(next, None));
                apply(state, &tile, &message, &reading.parts, None, to, tx);
                return true;
            }
            Key::Text(t) if t == "z" => {
                review.filled = !review.filled;
                state.readings.revision += 1;
                return true;
            }
            Key::Escape if review.files.len() > 1 => {
                review.files.truncate(1);
                state.readings.revision += 1;
                return true;
            }
            Key::Escape if review.files.last().is_none_or(|f| f.select_from.is_none()) => {
                return false;
            }
            Key::Up
            | Key::Down
            | Key::PageUp
            | Key::PageDown
            | Key::Escape
            | Key::Right
            | Key::Left => {
                let handled = file_key(review, &prefix(&tile), ev, tx);
                state.readings.revision += 1;
                return handled;
            }
            _ => {}
        }
    }
    let viewing = !review.files.is_empty() && reading.frame != Frame::Walkthrough;
    if viewing && !review.help && typed != "?" && file_key(review, &prefix(&tile), ev, tx) {
        state.readings.revision += 1;
        return true;
    }
    let to = match (&ev.key, typed) {
        (_, "?") => {
            review.help = !review.help;
            None
        }
        (Key::Escape, _) if review.help => {
            review.help = false;
            None
        }
        (Key::Escape, _) if review.select_from.is_some() => {
            review.select_from = None;
            None
        }
        (_, "1" | "2" | "3") => {
            let Some((frame, _, _)) = Frame::ALL.iter().find(|(_, key, _)| *key == typed) else {
                return false;
            };
            to_frame(review, &reading, cursor, *frame)
        }
        (_, "b") if frames::has_sides(bundle) => {
            switch_side(review, &reading, cursor);
            Some(Move::Shown)
        }
        (Key::Down, _) => Some(Move::Line(cursor.map_or(0, |i| i + 1), ev.mods.shift)),
        (Key::Up, _) => Some(Move::Line(
            cursor.map_or(0, |i| i.saturating_sub(1)),
            ev.mods.shift,
        )),
        (Key::Right | Key::Left, _) if reading.frame == Frame::Map => {
            let (index, count) = reading.map.unwrap_or((0, 0));
            let next = if ev.key == Key::Right {
                (index + 1).min(count.saturating_sub(1))
            } else {
                index.saturating_sub(1)
            };
            let list = frames::map_stops(bundle, reading.side);
            review.map = frames::map_place(bundle, &list, next);
            review.cursor = None;
            Some(Move::Arrow)
        }
        (Key::Right, _) => Some(Move::Stop(next_stop(&reading, cursor, true))),
        (Key::Left, _) => Some(Move::Stop(next_stop(&reading, cursor, false))),
        (Key::Enter, _) if cursor.is_some_and(|i| reading.places[i].spot == Spot::Facts) => {
            cursor.and_then(|i| open_place(review, &reading, i, false))
        }
        (Key::Enter, _) | (_, "z") if !viewing => {
            let fill = typed == "z";
            let folder = cursor.is_some_and(
                |i| matches!(&reading.places[i].spot, Spot::NotShown(p) if p.ends_with('/')),
            );
            match cursor.and_then(|i| open_place(review, &reading, i, fill)) {
                Some(to) => Some(to),
                None if ev.key == Key::Enter || folder => None,
                None => return false,
            }
        }
        (_, "/") => Some(Move::Search(String::new())),
        (_, "i" | "c" | "a") if viewing && reads_outside(review) => {
            let path = review
                .files
                .last()
                .map(|f| f.target.path.clone())
                .unwrap_or_default();
            state.window.notify(
                format!("{path} is outside the repository, so it takes no edits or notes."),
                tx,
            );
            return true;
        }
        (_, "o") if !viewing => match editable_document(&message, &reading, cursor) {
            Some(path) => Some(Move::Editor(path)),
            None => return false,
        },
        (_, "i") => Some(Move::Edit),
        (_, "u") => Some(Move::Undo),
        (_, "c" | "a") => Some(Move::Mark(if typed == "a" {
            marks::Kind::Question
        } else {
            marks::Kind::Note
        })),
        (_, "e") if !viewing => {
            let Some(stop) = cursor.and_then(|i| reading.places[i].stop) else {
                return true;
            };
            let s = &bundle.stops[stop];
            let extra = review
                .expansions
                .get(&(s.path.clone(), s.from))
                .map_or(0, |e| e.extra);
            Some(Move::Expand(stop, extra + MORE_LINES))
        }
        _ => return false,
    };
    apply(
        state,
        &tile,
        &message,
        &reading.parts,
        Some(&reading),
        to,
        tx,
    );
    true
}

/// What Enter, or `z` with `fill`, does on place `at`: the facts show or hide the changed
/// files, a Not shown folder is searched, and a place with a file opens it.
fn open_place(review: &mut Review, reading: &Reading, at: usize, fill: bool) -> Option<Move> {
    match &reading.places[at].spot {
        Spot::Facts if !fill => {
            review.files_shown = !review.files_shown;
            None
        }
        Spot::NotShown(folder) if folder.ends_with('/') => {
            (!fill).then(|| Move::Search(folder.clone()))
        }
        _ => target_of(reading, at).map(|target| Move::Open(target, fill)),
    }
}

/// Switches to `frame`, when the review offers it and does not show it already.
fn to_frame(
    review: &mut Review,
    reading: &Reading,
    cursor: Option<usize>,
    frame: Frame,
) -> Option<Move> {
    if frame == reading.frame || !frames::offered(reading.bundle, &BUILT).contains(&frame) {
        return None;
    }
    Some(match switch_frame(review, reading, cursor, frame) {
        Some((stop, line)) => Move::Stage(stop, line),
        None => Move::Shown,
    })
}

/// Makes move `to` in review tile `tile`, whose `parts` were read in `reading` before the move.
fn apply(
    state: &mut State,
    tile: &str,
    message: &std::sync::Arc<attn_protocol::DesktopTileContentMessage>,
    parts: &[Part],
    reading: Option<&Reading>,
    to: Option<Move>,
    tx: &mut Tx,
) {
    let Some(bundle) = message.review.as_ref() else {
        return;
    };
    let moves = matches!(
        to,
        Some(Move::Line(..) | Move::Stop(_) | Move::Arrow | Move::Shown | Move::Stage(..))
    );
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.to_owned())
        .or_default();
    match to {
        Some(Move::Line(i, selecting)) => {
            let Some(reading) = reading.filter(|r| !r.places.is_empty()) else {
                return;
            };
            let i = i.min(reading.places.len() - 1);
            let cursor = reading.cursor(review);
            review.select_from = match (selecting, review.select_from.take()) {
                (false, _) => None,
                (true, Some(from)) => Some(from),
                (true, None) => cursor.map(|c| reading.places[c].spot.clone()),
            };
            let place = &reading.places[i];
            review.cursor = Some(place.spot.clone());
            review.at = i;
            tx.scroll_into_view(place.key.clone());
        }
        Some(Move::Stop(Some(i))) => {
            let Some(reading) = reading else { return };
            let place = &reading.places[i];
            review.cursor = Some(place.spot.clone());
            review.at = i;
            show_lead_in(tile, reading, place, tx);
        }
        Some(Move::Arrow | Move::Shown) => {
            let arrow = matches!(to, Some(Move::Arrow));
            show_moved(review, tile, &message.content, bundle, arrow, tx);
        }
        Some(Move::Open(target, fill)) => {
            open_file(state, tile, source_of(message), target, fill, tx);
        }
        Some(Move::Edit) => {
            let Some(reading) = reading else { return };
            let cursor = reading.cursor(review);
            match edit_target(state, tile, bundle, reading, cursor) {
                Ok(EditAt { path, at, doc_line }) => {
                    let uri = drafts_uri(message);
                    edits::begin(
                        state,
                        tile,
                        source_of(message),
                        &path,
                        at,
                        doc_line,
                        uri,
                        tx,
                    );
                }
                Err(why) => state.window.notify(why.to_owned(), tx),
            }
        }
        Some(Move::Editor(path)) => open_in_editor(state, tile, path, tx),
        Some(Move::Undo) => {
            let Some(reading) = reading else { return };
            let cursor = reading.cursor(review);
            if let Some(mark) = edit_under(state, tile, message, bundle, reading, cursor) {
                let uri = drafts_uri(message);
                edits::undo(state, (tile.to_owned(), source_of(message), uri), mark, tx);
            }
        }
        Some(Move::Mark(kind)) => {
            let Some(reading) = reading else { return };
            let cursor = reading.cursor(review);
            if let Some(anchor) = mark_target(state, tile, bundle, reading, cursor)
                && let Some((uri, to)) = crate::markdown::sending::destination_of(state, tile)
            {
                if kind == marks::Kind::Question && to.is_none() {
                    state.window.notify(
                        "No session takes a question: bind the document to a session first."
                            .to_owned(),
                        tx,
                    );
                    state.readings.revision += 1;
                    return;
                }
                marks::compose(
                    state,
                    marks::Composer {
                        tile: tile.to_owned(),
                        kind,
                        anchor,
                        uri,
                        to,
                    },
                    tx,
                );
            }
        }
        Some(Move::Search(query)) => {
            search::open(state, tile, source_of(message), &query, tx);
        }
        Some(Move::Stage(stop, line)) => {
            show_stage(state, tile, source_of(message), bundle, stop, line, tx);
            if let Some(at) = parts.iter().position(|p| *p == Part::Stop(stop)) {
                tx.scroll_into_view(format!("{}.part.{at}", prefix(tile)));
            }
        }
        Some(Move::Expand(stop, extra)) => {
            expand(
                state,
                tile,
                source_of(message),
                &bundle.stops[stop],
                extra,
                tx,
            );
        }
        _ => {}
    }
    if moves && let Some(review) = state.readings.reviews.tiles.get_mut(tile) {
        review.edited = None;
    }
    state.readings.revision += 1;
}

/// A press on a control inside review tile `tile`, which the press took: `act` runs with what
/// the daemon last sent the tile.
fn pressed(
    tile: &str,
    act: impl Fn(&mut State, &str, &std::sync::Arc<attn_protocol::DesktopTileContentMessage>, &mut Tx)
    + 'static,
) -> impl Fn(&mut State, &mut Tx) + 'static {
    let tile = tile.to_owned();
    move |state: &mut State, tx: &mut Tx| {
        let Some((held, message)) = held(state).filter(|(held, _)| *held == tile) else {
            return;
        };
        act(state, &held, &message, tx);
    }
}

/// `el`, place `key` of review tile `tile`, where a press puts the cursor, then acts as Enter
/// does on the facts, a changed file and a Not shown entry.
pub(crate) fn place_press(el: El, tile: &str, key: String) -> El {
    el.on_press_tx(pressed(tile, move |state, tile, message, tx| {
        let Some(bundle) = message.review.as_ref() else {
            return;
        };
        let review = state
            .readings
            .reviews
            .tiles
            .entry(tile.to_owned())
            .or_default();
        let reading = Reading::new(tile, &message.content, bundle, review);
        let Some(at) = reading.places.iter().position(|p| p.key == key) else {
            return;
        };
        let acts = matches!(
            reading.places[at].spot,
            Spot::Facts | Spot::File(_) | Spot::NotShown(_)
        );
        let to = acts
            .then(|| open_place(review, &reading, at, false))
            .flatten();
        let parts = &reading.parts;
        let to_line = Some(Move::Line(at, false));
        apply(state, tile, message, parts, Some(&reading), to_line, tx);
        apply(state, tile, message, parts, Some(&reading), to, tx);
    }))
}

/// `el`, frame `frame`'s tab in review tile `tile`, where a press switches to the frame as its
/// key does.
pub(crate) fn frame_press(el: El, tile: &str, frame: Frame) -> El {
    el.on_press_tx(pressed(tile, move |state, tile, message, tx| {
        let Some(bundle) = message.review.as_ref() else {
            return;
        };
        let review = state
            .readings
            .reviews
            .tiles
            .entry(tile.to_owned())
            .or_default();
        let reading = Reading::new(tile, &message.content, bundle, review);
        let cursor = reading.cursor(review);
        let to = to_frame(review, &reading, cursor, frame);
        apply(state, tile, message, &reading.parts, Some(&reading), to, tx);
    }))
}

/// `el`, row `row` of open file `id` in review tile `tile`, where a press puts the file's
/// cursor.
pub(crate) fn file_row_press(el: El, tile: &str, id: u64, row: usize) -> El {
    el.on_press_tx(pressed(tile, move |state, tile, _, tx| {
        let Some(review) = state.readings.reviews.tiles.get_mut(tile) else {
            return;
        };
        let Some(open) = review.files.iter_mut().find(|f| f.id == id) else {
            return;
        };
        open.put_cursor(row, false, &prefix(tile), tx);
        state.readings.revision += 1;
    }))
}

/// `el`, verdict `verdict` in review tile `tile`'s send sheet, where a press picks it as its
/// key does.
pub(crate) fn verdict_press(el: El, tile: &str, verdict: marks::Verdict) -> El {
    el.on_press_tx(pressed(tile, move |state, tile, _, _| {
        if let Some(review) = state.readings.reviews.tiles.get_mut(tile) {
            pick_verdict(review, verdict);
        }
        state.readings.revision += 1;
    }))
}

/// Picks `verdict` in the send sheet, when it shows.
fn pick_verdict(review: &mut Review, verdict: marks::Verdict) {
    if let Some((picked, _)) = review.sending.as_mut() {
        *picked = verdict;
        review.verdict = Some(verdict);
    }
}

/// `el`, a control of review tile `tile`'s file view, where a press goes back as ⌃o does, or
/// closes the file view as Escape does. On the Walkthrough's `stage`, back stops at the stage.
pub(crate) fn file_view_press(el: El, tile: &str, stage: bool, close: bool) -> El {
    el.on_press_tx(pressed(tile, move |state, tile, _, _| {
        let Some(review) = state.readings.reviews.tiles.get_mut(tile) else {
            return;
        };
        if close {
            close_files(review);
        } else {
            go_back(review, stage);
        }
        state.readings.revision += 1;
    }))
}

/// ⌃o: back to the file opened before, or out of the file view from the first; on the
/// Walkthrough's `stage`, never past the stage itself.
fn go_back(review: &mut Review, stage: bool) -> bool {
    if stage && review.files.len() <= 1 {
        return false;
    }
    if review.files.pop().is_none() {
        return false;
    }
    if review.files.is_empty() {
        review.filled = false;
    }
    true
}

/// Puts the cursor where `review`'s reading now places it, after the frame, the side or, when
/// `arrow`, the Map's arrow changed.
fn show_moved(
    review: &mut Review,
    tile: &str,
    source: &str,
    bundle: &ReviewBundle,
    arrow: bool,
    tx: &mut Tx,
) {
    let after = Reading::new(tile, source, bundle, review);
    if let Some(i) = after.cursor(review) {
        let place = &after.places[i];
        review.cursor = Some(place.spot.clone());
        review.at = i;
        if arrow || after.frame == Frame::Map {
            show_lead_in(tile, &after, place, tx);
            if let Some(key) = map_arrow_key(tile, &after) {
                tx.scroll_into_view(key);
            }
        } else {
            tx.scroll_center(place.key.clone());
        }
    }
}

/// Shows `side` in review tile `tile` as `b` does, when it is the other side.
fn pick_side(state: &mut State, tile: &str, side: Side, tx: &mut Tx) {
    let Some(message) = state
        .facts
        .tile_contents
        .tiles
        .iter()
        .find(|(k, _)| k.tile == tile)
        .map(|(_, c)| c.message.clone())
    else {
        return;
    };
    let Some(bundle) = message.review.as_ref() else {
        return;
    };
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.to_owned())
        .or_default();
    let reading = Reading::new(tile, &message.content, bundle, review);
    if reading.side == side {
        return;
    }
    let cursor = reading.cursor(review);
    switch_side(review, &reading, cursor);
    show_moved(review, tile, &message.content, bundle, false, tx);
    state.readings.revision += 1;
}

enum Move {
    /// The cursor goes to this place, extending the selection when true.
    Line(usize, bool),
    Stop(Option<usize>),
    /// The Map moved to another arrow: the cursor goes to its stop.
    Arrow,
    /// The frame or the side changed: the cursor stays where the new reading puts it.
    Shown,
    /// Open this file in the view, filling the tile when true.
    Open(file::Target, bool),
    /// Show this many lines on each side of this stop.
    Expand(usize, usize),
    /// Put this stop's file on the Walkthrough's stage, at this line or where it opens.
    Stage(usize, Option<i64>),
    /// Open the search with this in its box.
    Search(String),
    /// Write a note or a question on what the cursor stands on.
    Mark(marks::Kind),
    /// Edit what the cursor stands on.
    Edit,
    /// Undo the newest edit on the cursor's line.
    Undo,
    /// Open this document of the repository in the editor tile.
    Editor(String),
}

/// The document of the stop the cursor stands in, when the editor tile can open it: a review on
/// this machine.
fn editable_document(
    message: &attn_protocol::DesktopTileContentMessage,
    reading: &Reading,
    cursor: Option<usize>,
) -> Option<String> {
    if source_of(message).0.is_some() {
        return None;
    }
    let stop = reading.places[cursor?].stop?;
    let path = &reading.bundle.stops[stop].path;
    blocks::is_document(path).then(|| path.clone())
}

/// Opens document `path` of tile `tile`'s review in the editor tile, rooted at the repository,
/// when the review is on this machine.
pub(crate) fn open_in_editor(state: &mut State, tile: &str, path: String, tx: &mut Tx) {
    let Some(content) = state
        .facts
        .tile_contents
        .tiles
        .iter()
        .find(|(k, c)| k.tile == tile && c.message.review.is_some())
        .map(|(_, c)| c.message.clone())
    else {
        return;
    };
    if source_of(&content).0.is_some() {
        return;
    }
    let bundle = content.review.as_ref().expect("filtered on a review");
    let params = crate::notebook::Params {
        root: Some(bundle.repository.clone()),
        path: Some(path),
    };
    crate::notebook::open_tile_at(state, params, tx);
}

/// Whether the file view shows a file outside the repository, which takes no marks or edits.
fn reads_outside(review: &Review) -> bool {
    review.files.last().is_some_and(|f| f.target.outside)
}

/// Where file `path` of `bundle`'s repository is; empty when the review names no repository.
fn in_repository(bundle: &ReviewBundle, path: &str) -> String {
    if bundle.repository.is_empty() {
        return String::new();
    }
    format!("{}/{path}", bundle.repository.trim_end_matches('/'))
}

/// The markdown files review tile `tile`'s stops show, each by where it is, with the text of
/// the rows the stop shows.
pub(crate) fn documents(
    reviews: &Reviews,
    tile: &str,
    bundle: &ReviewBundle,
) -> Vec<(String, String)> {
    let expansions = reviews.tiles.get(tile).map(|r| &r.expansions);
    bundle
        .stops
        .iter()
        .filter(|s| blocks::is_document(&s.path))
        .map(|s| {
            let rows = expansions
                .and_then(|e| e.get(&(s.path.clone(), s.from)))
                .map_or(&s.rows[..], |e| &e.rows[..]);
            let text = rows
                .iter()
                .map(|r| r.text.as_str())
                .collect::<Vec<_>>()
                .join("\n");
            (in_repository(bundle, &s.path), text)
        })
        .collect()
}

/// The key of the arrow the Map stands on, in the Map's diagrams.
fn map_arrow_key(tile: &str, reading: &Reading) -> Option<String> {
    let (index, _) = reading.map?;
    let list = frames::map_stops(reading.bundle, reading.side);
    let current = list.get(index)?;
    let flow = reading
        .bundle
        .flows
        .iter()
        .position(|f| std::ptr::eq(f, current.flow))?;
    Some(format!(
        "{}.map.{flow}.message.{}",
        prefix(tile),
        current.message.line
    ))
}

/// Puts the lead-in of the stop `place` stands in at the top of the document, then shows the
/// cursor.
fn show_lead_in(tile: &str, reading: &Reading, place: &Place, tx: &mut Tx) {
    let lead = lead_in(reading, place.part);
    if let Some(last) = reading.places.last() {
        tx.scroll_into_view(last.key.clone());
    }
    tx.scroll_into_view(format!("{}.part.{lead}", prefix(tile)));
    tx.scroll_into_view(place.key.clone());
}

/// Switches to `frame`, keeping the stop and line the cursor stands on.
fn switch_frame(
    review: &mut Review,
    reading: &Reading,
    cursor: Option<usize>,
    frame: Frame,
) -> Option<(usize, Option<i64>)> {
    let here = reading.place(cursor);
    review.frame = Some(frame);
    review.files.clear();
    review.filled = false;
    if frame == Frame::Walkthrough {
        return Some(here.unwrap_or((0, None)));
    }
    if frame != Frame::Map {
        if let Some((stop, line)) = here {
            review.cursor = stop_spot(reading.bundle, stop, line);
        }
        return None;
    }
    let bundle = reading.bundle;
    let find = |side: Side| {
        let list = frames::map_stops(bundle, side);
        let index = here.and_then(|(stop, _)| list.iter().position(|m| m.stop == stop));
        (list, index)
    };
    let (mut list, mut index) = find(review.side);
    if index.is_none() && here.is_some() && frames::has_sides(bundle) {
        let (other, at) = find(review.side.other());
        if at.is_some() {
            review.side = review.side.other();
            (list, index) = (other, at);
        }
    }
    review.map = frames::map_place(bundle, &list, index.unwrap_or(0));
    review.cursor = match (index, here) {
        (Some(_), Some((stop, line))) => stop_spot(bundle, stop, line),
        _ => None,
    };
    None
}

/// Switches the diagrams to the other side: the Map to the arrow of the same stop there, the
/// Brief's cursor to an arrow of the same stop in the other side's diagram.
fn switch_side(review: &mut Review, reading: &Reading, cursor: Option<usize>) {
    let bundle = reading.bundle;
    let next = review.side.other();
    review.side = next;
    if reading.frame == Frame::Map {
        let (index, _) = reading.map.unwrap_or((0, 0));
        let was = frames::map_stops(bundle, reading.side)
            .get(index)
            .map(|m| m.stop);
        let list = frames::map_stops(bundle, next);
        let same = list.iter().position(|m| Some(m.stop) == was).unwrap_or(0);
        review.map = frames::map_place(bundle, &list, same);
        review.cursor = None;
        return;
    }
    let Some(at) = cursor.filter(|&i| reading.places[i].arrow.is_some()) else {
        return;
    };
    let stop = reading.places[at].arrow;
    let after = Reading::new("", reading.source, bundle, review);
    let arrows: Vec<&Place> = after.places.iter().filter(|p| p.arrow.is_some()).collect();
    let target = arrows
        .iter()
        .find(|p| p.arrow == stop)
        .or(arrows.first())
        .map(|p| p.spot.clone());
    match target {
        Some(spot) => review.cursor = Some(spot),
        None => {
            let before = reading.places[..at]
                .iter()
                .rev()
                .find(|p| p.arrow.is_none())
                .map(|p| p.spot.clone());
            review.cursor = before;
        }
    }
}

/// The spot of stop `stop`'s row at line `line`, else its first row.
fn stop_spot(bundle: &ReviewBundle, stop: usize, line: Option<i64>) -> Option<Spot> {
    let s = &bundle.stops[stop];
    let row = line
        .and_then(|l| s.rows.iter().find(|r| r.now == Some(l)))
        .or(s.rows.first())?;
    Some(row_spot(&s.path, row))
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
        if matches!(part, Part::Stop(_)) || !reading.shows(i) {
            break;
        }
        start = i;
        if part.opens_step() {
            break;
        }
    }
    start
}

/// The review bundle tile `tile` of the current desktop shows.
fn bundle_of<'a>(state: &'a State, tile: &str) -> Option<&'a ReviewBundle> {
    state
        .facts
        .tile_contents
        .tiles
        .iter()
        .find(|(key, _)| key.tile == tile)?
        .1
        .message
        .review
        .as_ref()
}

/// Opens what a search found on top of tile `tile`'s files.
fn open_found(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    target: file::Target,
    tx: &mut Tx,
) {
    open_file(state, tile, source, target, false, tx);
    state.readings.revision += 1;
}

/// The rows of stop `stop` selected in the document: from the selection's other end to the
/// cursor, both in that stop, as indices of its rows. The cursor's row alone with no selection.
fn selected_rows(
    reading: &Reading,
    review: &Review,
    cursor: usize,
) -> Option<(usize, usize, usize)> {
    let stop = reading.places[cursor].stop?;
    let (a, b) = selected_places(reading, review, cursor)?;
    Some((stop, reading.places[a].rows.0, reading.places[b].rows.1))
}

/// The first and last places of the selection in the cursor's stop, which ends at the cursor.
fn selected_places(reading: &Reading, review: &Review, cursor: usize) -> Option<(usize, usize)> {
    let stop = reading.places[cursor].stop?;
    let from = review
        .select_from
        .as_ref()
        .and_then(|spot| {
            reading
                .places
                .iter()
                .enumerate()
                .filter(|(_, p)| &p.spot == spot && p.stop == Some(stop))
                .map(|(i, _)| i)
                .min_by_key(|i| i.abs_diff(cursor))
        })
        .unwrap_or(cursor);
    Some((from.min(cursor), from.max(cursor)))
}

/// The anchor of a mark on what the cursor stands on: the rows selected in the file view or
/// the stage, else the rows selected in a stop, else the cursor's row.
fn mark_target(
    state: &State,
    tile: &str,
    bundle: &ReviewBundle,
    reading: &Reading,
    cursor: Option<usize>,
) -> Option<attn_protocol::MarkdownAnnotationAnchor> {
    let review = state.readings.reviews.tiles.get(tile)?;
    if let Some(Part::Prose { source, .. }) = cursor
        .filter(|_| review.files.is_empty() && reading.frame != Frame::Walkthrough)
        .map(|at| &reading.parts[reading.places[at].part])
    {
        return prose_anchor(state, tile, reading.source, source.start);
    }
    let doc_line = |path: &str, head: Option<i64>| {
        let stops: Vec<_> = bundle.stops.iter().filter(|s| s.path == path).collect();
        stops
            .iter()
            .find(|s| head.is_some_and(|h| h >= s.from && h <= s.to))
            .or(stops.first())
            .map_or(reading.source.lines().count() as i64 + 1, |s| {
                s.heading_line
            })
    };
    let viewing = !review.files.is_empty() && reading.frame != Frame::Walkthrough;
    if viewing || reading.frame == Frame::Walkthrough {
        let open = review.files.last()?;
        let rows = open.rows.as_deref()?;
        let (from, to) = open.selected();
        let picked = &rows[from..=to.min(rows.len().checked_sub(1)?)];
        let head = picked.iter().find_map(|r| r.head);
        return marks::anchor(
            &open.target.path,
            picked,
            &rows[to + 1..],
            doc_line(&open.target.path, head),
        );
    }
    let (stop, from, to) = selected_rows(reading, review, cursor?)?;
    let rows = reading.rows_of(stop);
    let s = &bundle.stops[stop];
    marks::anchor(&s.path, &rows[from..=to], &rows[to + 1..], s.heading_line)
}

/// The anchor of the whole prose block that starts at byte `start` of the document `source`
/// shown in tile `tile`, as a selection of all its text makes it.
fn prose_anchor(
    state: &State,
    tile: &str,
    source: &str,
    start: usize,
) -> Option<attn_protocol::MarkdownAnnotationAnchor> {
    let (uri, _) = crate::markdown::sending::destination_of(state, tile)?;
    let passages = state.drafts.get(&uri)?.passages.as_ref()?.1.clone();
    let line = source[..start].matches('\n').count() as i64 + 1;
    let blocks = &passages.blocks;
    let block = blocks
        .blocks
        .iter()
        .position(|b| b.parent.is_none() && b.start_line == line)?;
    blocks.anchor(block, 0, blocks.blocks[block].text.len())
}

/// Where each of `draft`'s file marks shows: under a line of its file, or moved; a mark no
/// shown rows cover is left out.
fn placements<'m>(
    review: &Review,
    bundle: &ReviewBundle,
    draft: &'m [attn_protocol::MarkdownAnnotation],
) -> Vec<(&'m attn_protocol::MarkdownAnnotation, String, marks::Placed)> {
    draft
        .iter()
        .filter(|m| matches!(m.r#type.as_str(), "comment" | "question" | "edit"))
        .filter_map(|m| {
            let anchor = m.anchor.as_ref()?;
            let path = anchor.file.as_deref()?;
            let lists: Vec<marks::Rows> = review
                .files
                .iter()
                .filter(|f| f.target.path == path)
                .filter_map(|f| f.rows.as_deref())
                .map(|rows| marks::Rows { rows, covers: None })
                .chain(bundle.stops.iter().filter(|s| s.path == path).map(|s| {
                    match review.expansions.get(&(s.path.clone(), s.from)) {
                        Some(e) => marks::Rows {
                            rows: &e.rows,
                            covers: Some((s.from - e.extra as i64, s.to + e.extra as i64)),
                        },
                        None => marks::Rows {
                            rows: &s.rows,
                            covers: Some((s.from, s.to)),
                        },
                    }
                }))
                .collect();
            let placed = if m.r#type == "edit" {
                let stands = (
                    m.edit_after_head.unwrap_or(0),
                    m.edit_before_head.unwrap_or(0),
                );
                let now = m.edit_now.as_deref().unwrap_or_default();
                lists.iter().find_map(|r| {
                    match edits::place(r.rows, stands, now, r.covers.is_none()) {
                        edits::Stands::At(line) => Some(marks::Placed::Line(line)),
                        edits::Stands::Moved => Some(marks::Placed::Moved),
                        edits::Stands::Unknown => None,
                    }
                })?
            } else {
                marks::place(anchor, &lists)?
            };
            Some((m, path.to_owned(), placed))
        })
        .collect()
}

/// Where `i` edits: a file, the line now the cursor stands on with the lines now selected, and
/// the document line the edit is listed at.
struct EditAt {
    path: String,
    /// The line now, the lines now selected, and whether they are a document's block.
    at: (i64, Option<(i64, i64)>, bool),
    doc_line: i64,
}

/// What `i` edits: the file, the line now the cursor stands on with the lines now selected, and
/// the document line the edit is listed at; or why the cursor's place takes no edit.
fn edit_target(
    state: &State,
    tile: &str,
    bundle: &ReviewBundle,
    reading: &Reading,
    cursor: Option<usize>,
) -> Result<EditAt, &'static str> {
    const REMOVED: &str = "This line was removed. i edits the code as it is now.";
    const NOT_CODE: &str = "Put the cursor on a line of code or a document stop to edit it.";
    let review = state.readings.reviews.tiles.get(tile).ok_or(NOT_CODE)?;
    let doc_line = |path: &str| {
        bundle
            .stops
            .iter()
            .find(|s| s.path == path)
            .map_or(reading.source.lines().count() as i64 + 1, |s| {
                s.heading_line
            })
    };
    let viewing = !review.files.is_empty() && reading.frame != Frame::Walkthrough;
    if viewing || reading.frame == Frame::Walkthrough {
        let open = review.files.last().ok_or(NOT_CODE)?;
        let rows = open
            .rows
            .as_deref()
            .ok_or("The file is still being read.")?;
        let line = rows.get(open.cursor).ok_or(NOT_CODE)?.now.ok_or(REMOVED)?;
        let (a, b) = open.selected();
        let lines: Vec<i64> = rows[a..=b.min(rows.len() - 1)]
            .iter()
            .filter_map(|r| r.now)
            .collect();
        let selection = (open.select_from.is_some() && !lines.is_empty())
            .then(|| (lines[0], lines[lines.len() - 1]));
        return Ok(EditAt {
            path: open.target.path.clone(),
            at: (line, selection, false),
            doc_line: doc_line(&open.target.path),
        });
    }
    let (stop, from, to) =
        selected_rows(reading, review, cursor.ok_or(NOT_CODE)?).ok_or(NOT_CODE)?;
    let rows = reading.rows_of(stop);
    let s = &bundle.stops[stop];
    let at = cursor.ok_or(NOT_CODE)?;
    let line = match &reading.places[at].spot {
        Spot::Row { now, .. } => now.ok_or(REMOVED)?,
        _ => return Err(NOT_CODE),
    };
    let lines: Vec<i64> = rows[from..=to].iter().filter_map(|r| r.now).collect();
    let block = blocks::is_document(&s.path);
    let selection = ((block || review.select_from.is_some()) && !lines.is_empty())
        .then(|| (lines[0], lines[lines.len() - 1]));
    Ok(EditAt {
        path: s.path.clone(),
        at: (line, selection, block),
        doc_line: s.heading_line,
    })
}

/// The newest of tile `tile`'s edits whose text holds the line now the cursor stands on.
fn edit_under(
    state: &State,
    tile: &str,
    message: &attn_protocol::DesktopTileContentMessage,
    bundle: &ReviewBundle,
    reading: &Reading,
    cursor: Option<usize>,
) -> Option<attn_protocol::MarkdownAnnotation> {
    let review = state.readings.reviews.tiles.get(tile)?;
    let (path, line) = match (review.files.last(), reading.frame) {
        (Some(open), _) => (open.target.path.clone(), open.line()?),
        (None, _) => match (&reading.places[cursor?].spot, reading.places[cursor?].stop) {
            (Spot::Row { now: Some(now), .. }, Some(stop)) => {
                (bundle.stops[stop].path.clone(), *now)
            }
            _ => return None,
        },
    };
    let draft = state.drafts.get(&drafts_uri(message))?;
    placements(review, bundle, &draft.marks)
        .into_iter()
        .filter(|(m, p, _)| m.r#type == "edit" && *p == path)
        .filter(|(m, _, at)| {
            let count = m
                .edit_now
                .as_deref()
                .unwrap_or_default()
                .split('\n')
                .count() as i64;
            matches!(at, marks::Placed::Line(last) if line <= *last && line > last - count)
        })
        .map(|(m, _, _)| m.clone())
        .collect::<Vec<_>>()
        .pop()
}

/// The ids of tile `tile`'s marks whose lines changed since they were written.
fn moved_marks(
    state: &State,
    tile: &str,
    message: &attn_protocol::DesktopTileContentMessage,
    bundle: &ReviewBundle,
) -> Vec<String> {
    let Some(review) = state.readings.reviews.tiles.get(tile) else {
        return Vec::new();
    };
    let uri = drafts_uri(message);
    let Some(draft) = state.drafts.get(&uri) else {
        return Vec::new();
    };
    placements(review, bundle, &draft.marks)
        .into_iter()
        .filter(|(_, _, p)| *p == marks::Placed::Moved)
        .map(|(m, _, _)| m.id.clone())
        .collect()
}

/// The address of the annotation draft of the document `message` shows.
fn drafts_uri(message: &attn_protocol::DesktopTileContentMessage) -> String {
    crate::markdown::drafts::Source::File {
        path: message.path.clone(),
        host: message.host.clone().filter(|h| !h.is_empty()),
    }
    .uri()
}

/// Reads file `path` of tile `tile`'s review again into every view of it, after the reader
/// wrote it.
fn reread(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    path: &str,
    tx: &mut Tx,
) {
    let Some(review) = state.readings.reviews.tiles.get(tile) else {
        return;
    };
    let open: Vec<u64> = review
        .files
        .iter()
        .filter(|f| f.target.path == path)
        .map(|f| f.id)
        .collect();
    for id in open {
        load(
            state,
            tile,
            source.clone(),
            (id, path.to_owned()),
            Land::Refresh,
            tx,
        );
    }
    state.readings.revision += 1;
}

/// After an edit of file `path` in review tile `tile` that changed it first at line now `line`:
/// the file view the edit was made in, or the document when no file view shows, puts its
/// cursor on that line once it shows it.
fn land_edit(state: &mut State, tile: &str, path: &str, line: i64) {
    let Some(review) = state.readings.reviews.tiles.get_mut(tile) else {
        return;
    };
    match review.files.last_mut() {
        Some(open) if open.target.path == path => open.edited = Some(line),
        Some(_) => {}
        None => review.edited = Some((path.to_owned(), line)),
    }
}

/// Where a read puts the file view's cursor.
#[derive(Clone, Copy)]
enum Land {
    /// On this line now, or the first after it.
    Line(Option<i64>),
    /// On the row it stood on before the read.
    Refresh,
}

/// What the daemon needs to read a review's files: the machine and the document.
/// The keys of code navigation, before the review's own: while labels show, a letter picks and
/// any other key drops them; while a list of places shows, it takes the keys; Enter takes an
/// offered answer and Escape drops it; Escape drops a question on its way; `f`, `r` and `h`
/// ask about a name on the cursor's line. None when navigation leaves the key alone.
fn nav_of<'a>(state: &'a mut State, tile: &str) -> Option<&'a mut nav::Nav> {
    state.readings.revision += 1;
    state.readings.reviews.nav_mut(tile)
}

pub(crate) fn open_answer_place(state: &mut State, tile: &str, i: usize, tx: &mut Tx) {
    let Some(location) = nav_of(state, tile)
        .and_then(|nav| nav.panel.take())
        .and_then(|panel| panel.locations.get(i).cloned())
    else {
        return;
    };
    follow_location(state, tile, &location, tx);
}

pub(crate) fn close_panel(state: &mut State, tile: &str) {
    if let Some(nav) = nav_of(state, tile) {
        nav.panel = None;
    }
}

pub(crate) fn take_offer(state: &mut State, tile: &str, tx: &mut Tx) {
    let Some((answer, _)) = nav_of(state, tile).and_then(|nav| nav.offer.take()) else {
        return;
    };
    if answer.kind == nav::Kind::Definition && answer.locations.len() == 1 {
        follow_location(state, tile, &answer.locations[0], tx);
    } else if let Some(nav) = nav_of(state, tile) {
        nav.panel = Some(answer);
    }
}

pub(crate) fn drop_offer(state: &mut State, tile: &str) {
    if let Some(nav) = nav_of(state, tile) {
        nav.offer = None;
    }
}

fn nav_key(
    state: &mut State,
    tile: &str,
    message: &std::sync::Arc<attn_protocol::DesktopTileContentMessage>,
    reading: &Reading,
    ev: &KeyEvent,
    typed: &str,
    tx: &mut Tx,
) -> Option<bool> {
    let source = source_of(message);
    let review = state.readings.reviews.tiles.get_mut(tile)?;
    let printable = typed.chars().count() == 1 && !ev.mods.cmd && !ev.mods.ctrl && !ev.mods.alt;
    if review.nav.picking.is_some() {
        if ev.key == Key::Escape {
            review.nav.picking = None;
            return Some(true);
        }
        if !printable {
            review.nav.picking = None;
            return Some(true);
        }
        if let Some(asked) = nav::pick(&mut review.nav, typed) {
            let place = place_of(state, tile).unwrap_or_default();
            nav::ask(state, tile, source, asked, place, tx);
        }
        return Some(true);
    }
    if let Some(panel) = review.nav.panel.as_mut() {
        if ev.key == Key::Escape {
            close_panel(state, tile);
            return Some(true);
        }
        if nav::panel_key(tile, panel, ev, tx) {
            let i = panel.cursor;
            open_answer_place(state, tile, i, tx);
        }
        return Some(true);
    }
    if review.nav.offer.is_some() {
        match ev.key {
            Key::Enter => {
                take_offer(state, tile, tx);
                return Some(true);
            }
            Key::Escape => {
                drop_offer(state, tile);
                return Some(true);
            }
            _ => {}
        }
    }
    if ev.key == Key::Escape && review.nav.asking.is_some() {
        review.nav.asking = None;
        review.nav.note = None;
        return Some(true);
    }
    if let Some(Some(nav::Note::Status(_) | nav::Note::Alert(_))) = Some(&review.nav.note) {
        review.nav.note = None;
    }
    let setup = match typed {
        "s" if printable => Some(setup::start as fn(&mut State, &str, &mut Tx) -> bool),
        "x" if printable => Some(setup::toggle_offer as fn(&mut State, &str, &mut Tx) -> bool),
        _ => None,
    };
    if let Some(act) = setup
        && act(state, tile, tx)
    {
        return Some(true);
    }
    let review = state.readings.reviews.tiles.get_mut(tile)?;
    let kind = nav::Kind::of_key(typed).filter(|_| printable)?;
    let line = nav_line(review, reading, tile);
    let review = state.readings.reviews.tiles.get_mut(tile)?;
    if let Some(asked) = nav::start(&mut review.nav, kind, line) {
        let place = place_of(state, tile).unwrap_or_default();
        nav::ask(state, tile, source, asked, place, tx);
    }
    Some(true)
}

/// The line of code the cursor stands on, to ask about: a row of the file view or the stage, or
/// of a stop; or why there is none.
fn nav_line(review: &Review, reading: &Reading, tile: &str) -> Result<nav::Line, &'static str> {
    const REMOVED: &str = "This line was removed; ask about the code as it is now.";
    const NOT_CODE: &str = "Put the cursor on a line of code to ask about it.";
    let prefix = prefix(tile);
    let viewing = !review.files.is_empty() && reading.frame != Frame::Walkthrough;
    if viewing || reading.frame == Frame::Walkthrough {
        let open = review.files.last().ok_or(NOT_CODE)?;
        let row = open
            .rows
            .as_deref()
            .and_then(|r| r.get(open.cursor))
            .ok_or(NOT_CODE)?;
        return Ok(nav::Line {
            path: open.target.path.clone(),
            line: row.now.ok_or(REMOVED)?,
            text: row.text.clone(),
            row: file::row_key(&prefix, open.cursor),
        });
    }
    let at = reading.cursor(review).ok_or(NOT_CODE)?;
    let place = &reading.places[at];
    let stop = place
        .stop
        .filter(|s| !blocks::is_document(&reading.bundle.stops[*s].path))
        .ok_or(NOT_CODE)?;
    let Spot::Row { old, now, .. } = &place.spot else {
        return Err(NOT_CODE);
    };
    let line = now.ok_or(REMOVED)?;
    let text = reading
        .rows_of(stop)
        .iter()
        .find(|r| r.old == *old && r.now == Some(line))
        .map(|r| r.text.clone())
        .unwrap_or_default();
    Ok(nav::Line {
        path: reading.bundle.stops[stop].path.clone(),
        line,
        text,
        row: place.key.clone(),
    })
}

/// Where tile `tile`'s cursor stands, to tell whether it moved while a question was on its way.
fn place_of(state: &State, tile: &str) -> Option<String> {
    let review = state.readings.reviews.tiles.get(tile)?;
    let file = review
        .files
        .last()
        .map(|f| format!("{}:{}", f.target.path, f.cursor));
    Some(format!(
        "{:?}|{:?}|{:?}|{}",
        review.frame,
        review.cursor,
        file,
        review.files.len()
    ))
}

/// What the daemon needs to read tile `tile`'s review.
fn source_of_tile(state: &State, tile: &str) -> Option<(Option<String>, String)> {
    let content = state
        .facts
        .tile_contents
        .tiles
        .iter()
        .find(|(k, c)| k.tile == tile && c.message.review.is_some())?
        .1;
    Some(source_of(&content.message))
}

/// Opens where an answer points: a file of the review's repository at its line, or a file
/// outside it read-only.
fn follow_location(
    state: &mut State,
    tile: &str,
    location: &attn_protocol::CodeNavLocation,
    tx: &mut Tx,
) {
    let Some(source) = source_of_tile(state, tile) else {
        return;
    };
    if let Some(review) = state.readings.reviews.tiles.get_mut(tile) {
        review.nav.panel = None;
    }
    let target = file::Target {
        path: location.path.clone(),
        line: Some(location.line),
        range: None,
        outside: location.outside,
    };
    open_file(state, tile, source, target, false, tx);
    state.readings.revision += 1;
}

fn source_of(message: &attn_protocol::DesktopTileContentMessage) -> (Option<String>, String) {
    (
        message.host.clone().filter(|h| !h.is_empty()),
        message.path.clone(),
    )
}

/// Opens `target` in tile `tile`'s file view, on top of the files open there.
fn open_file(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    target: file::Target,
    fill: bool,
    tx: &mut Tx,
) {
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.to_owned())
        .or_default();
    review.next_file += 1;
    let id = review.next_file;
    let line = target.line;
    let path = target.path.clone();
    review.files.push(file::OpenFile {
        id,
        target,
        ..file::OpenFile::default()
    });
    if fill {
        review.filled = true;
    }
    load(state, tile, source, (id, path), Land::Line(line), tx);
}

/// Reads file `path` into open file `id` of tile `tile`, putting the cursor where `land` says.
fn load(
    state: &mut State,
    tile: &str,
    (host, document): (Option<String>, String),
    (id, path): (u64, String),
    land: Land,
    tx: &mut Tx,
) {
    let Some(review) = state.readings.reviews.tiles.get_mut(tile) else {
        return;
    };
    review.asked += 1;
    let asked = review.asked;
    let Some(open) = review.files.iter_mut().find(|f| f.id == id) else {
        return;
    };
    open.asked = asked;
    let tile = tile.to_owned();
    let p = path.clone();
    file::read(
        state,
        (host, document, path),
        move |state, read, tx| {
            if let Ok(m) = &read
                && let (Some(rows), Some(hash)) = (m.rows.as_deref(), m.hash.as_deref())
            {
                state.readings.reviews.keep_colors(&p, hash, rows);
            }
            let prefix = prefix(&tile);
            let Some(review) = state.readings.reviews.tiles.get_mut(&tile) else {
                return;
            };
            let Some(open) = review
                .files
                .iter_mut()
                .find(|f| f.id == id && f.asked == asked)
            else {
                return;
            };
            let edited = open.edited.take();
            match read {
                Ok(m) => {
                    let rows: Rc<[ReviewRow]> = m.rows.unwrap_or_default().into();
                    let edited =
                        edited.and_then(|line| rows.iter().position(|r| r.now == Some(line)));
                    open.cursor = match (edited, land) {
                        (Some(at), _) => at,
                        (None, Land::Line(line)) => file::row_index_of(&rows, line),
                        (None, Land::Refresh) => file::row_index_after_refresh(&rows, open),
                    };
                    open.select_from = None;
                    open.rows = Some(rows);
                    open.hash = m.hash.unwrap_or_default();
                    open.problem = m.problem.filter(|p| !p.is_empty());
                    open.error = None;
                    tx.scroll_center(file::row_key(&prefix, open.cursor));
                }
                Err(why) => open.error = Some(why),
            }
            state.readings.revision += 1;
        },
        tx,
    );
}

/// Puts stop `stop`'s file on tile `tile`'s Walkthrough stage, with its cursor on `line`, or
/// where the Walkthrough opens the stop, and the stop's lines marked. The files opened over
/// the stage go. The stage keeps the rows it shows while it reads the same file again.
fn show_stage(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    bundle: &ReviewBundle,
    stop: usize,
    line: Option<i64>,
    tx: &mut Tx,
) {
    let Some(s) = bundle.stops.get(stop) else {
        return;
    };
    let range = (s.now_from > 0).then_some((s.now_from, s.now_to));
    let target = file::Target {
        path: s.path.clone(),
        line: line.or_else(|| stage_line(s)),
        range,
        outside: false,
    };
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.to_owned())
        .or_default();
    review.stage = Some((s.path.clone(), s.from, stop));
    let kept = review
        .files
        .first()
        .filter(|f| f.target.path == s.path)
        .map(|f| (f.rows.clone(), f.hash.clone()));
    review.files.clear();
    review.next_file += 1;
    let id = review.next_file;
    let (rows, hash) = kept.unwrap_or_default();
    let cursor = rows
        .as_deref()
        .map_or(0, |r| file::row_index_of(r, target.line));
    let land = Land::Line(target.line);
    review.files.push(file::OpenFile {
        id,
        target,
        rows,
        hash,
        cursor,
        ..file::OpenFile::default()
    });
    load(state, tile, source, (id, s.path.clone()), land, tx);
}

/// `shown`, a stop's rows, widened by `lines` on each side from `file`, the whole file.
fn widen(shown: &[ReviewRow], file: &[ReviewRow], lines: usize) -> Option<Rc<[ReviewRow]>> {
    let at = |r: &ReviewRow| file.iter().position(|f| f.old == r.old && f.now == r.now);
    let first = at(shown.first()?)?;
    let last = at(shown.last()?)?;
    Some(file[first.saturating_sub(lines)..(last + lines + 1).min(file.len())].into())
}

/// Shows `extra` lines on each side of stop `stop` of tile `tile`, read from its file now.
fn expand(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    stop: &attn_protocol::ReviewStop,
    extra: usize,
    tx: &mut Tx,
) {
    let key = (stop.path.clone(), stop.from);
    let review = state
        .readings
        .reviews
        .tiles
        .entry(tile.to_owned())
        .or_default();
    review.asked += 1;
    let asked = review.asked;
    review.widening.insert(key.clone(), asked);
    let (tile, shown, seen) = (tile.to_owned(), stop.rows.clone(), stop_seen(stop));
    let stop_path = stop.path.clone();
    let (host, document) = source;
    file::read(
        state,
        (host, document, stop.path.clone()),
        move |state, read, _| {
            if let Ok(m) = &read
                && let (Some(rows), Some(hash)) = (m.rows.as_deref(), m.hash.as_deref())
            {
                state.readings.reviews.keep_colors(&stop_path, hash, rows);
            }
            let Some(review) = state.readings.reviews.tiles.get_mut(&tile) else {
                return;
            };
            if review.widening.get(&key) != Some(&asked) {
                return;
            }
            review.widening.remove(&key);
            let Ok(m) = read else { return };
            let Some(rows) = widen(&shown, m.rows.as_deref().unwrap_or_default(), extra) else {
                return;
            };
            review.expansions.insert(
                key,
                Expansion {
                    rows,
                    extra,
                    stop: seen,
                },
            );
            state.readings.revision += 1;
        },
        tx,
    );
}

/// The lines `e` shows around a stop.
const MORE_LINES: usize = 10;

/// Keeps each shown review current: when its content changed, the open files are read again
/// with the cursor on its row, and the widened stops that changed are widened again; a stop's
/// file that has no colors yet is read once to color it, one file at a time.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) -> bool {
    let now = tx.clock();
    let mut expired = false;
    for review in state.readings.reviews.tiles.values_mut() {
        if review
            .nav
            .offer
            .as_ref()
            .is_some_and(|(_, until)| *until <= now)
        {
            review.nav.offer = None;
            expired = true;
        }
    }
    if expired {
        state.readings.revision += 1;
    }
    let shown: Vec<(
        String,
        u64,
        std::sync::Arc<attn_protocol::DesktopTileContentMessage>,
    )> = state
        .facts
        .tile_contents
        .tiles
        .iter()
        .filter(|(_, c)| c.message.review.is_some())
        .map(|(key, c)| (key.tile.clone(), c.revision, c.message.clone()))
        .collect();
    for (tile, revision, message) in shown {
        let Some(bundle) = message.review.as_ref() else {
            continue;
        };
        let review = state
            .readings
            .reviews
            .tiles
            .entry(tile.clone())
            .or_default();
        let walkthrough = review
            .frame
            .filter(|f| frames::offered(bundle, &BUILT).contains(f))
            .unwrap_or_else(|| frames::opening(bundle, &BUILT))
            == Frame::Walkthrough;
        if walkthrough && review.files.is_empty() {
            if let Some(stop) = stage_stop(bundle, review.stage.as_ref()) {
                review.seen = revision;
                show_stage(state, &tile, source_of(&message), bundle, stop, None, tx);
                warm(state, &tile, &message, bundle, tx);
            }
            continue;
        }
        if review.seen == revision {
            continue;
        }
        review.seen = revision;
        if walkthrough && let Some(stop) = stage_stop(bundle, review.stage.as_ref()) {
            let s = &bundle.stops[stop];
            if review
                .files
                .first()
                .is_some_and(|f| f.target.path != s.path)
            {
                show_stage(state, &tile, source_of(&message), bundle, stop, None, tx);
                warm(state, &tile, &message, bundle, tx);
                continue;
            }
            review.stage = Some((s.path.clone(), s.from, stop));
            if let Some(stage) = review.files.first_mut() {
                stage.target.range = (s.now_from > 0).then_some((s.now_from, s.now_to));
            }
        }
        let files: Vec<(u64, String)> = review
            .files
            .iter()
            .map(|f| (f.id, f.target.path.clone()))
            .collect();
        review.expansions.retain(|(path, from), _| {
            bundle
                .stops
                .iter()
                .any(|s| &s.path == path && s.from == *from)
        });
        let widened: Vec<(attn_protocol::ReviewStop, usize)> = bundle
            .stops
            .iter()
            .filter_map(|s| {
                let e = review.expansions.get(&(s.path.clone(), s.from))?;
                (e.stop != stop_seen(s)).then(|| (s.clone(), e.extra))
            })
            .collect();
        for (id, path) in files {
            load(
                state,
                &tile,
                source_of(&message),
                (id, path),
                Land::Refresh,
                tx,
            );
        }
        for (stop, extra) in widened {
            expand(state, &tile, source_of(&message), &stop, extra, tx);
        }
        warm(state, &tile, &message, bundle, tx);
    }
    expired
}

/// Reads the stops' files the reader has no colors for, one after another, to color them.
/// A file a widened stop is reading again is colored by that read. A warm-up already running
/// for those files goes on; one for other files gives way.
fn warm(
    state: &mut State,
    tile: &str,
    message: &std::sync::Arc<attn_protocol::DesktopTileContentMessage>,
    bundle: &ReviewBundle,
    tx: &mut Tx,
) {
    let reviews = &state.readings.reviews;
    let widening = |path: &str| {
        reviews
            .tiles
            .get(tile)
            .is_some_and(|r| r.widening.keys().any(|(p, _)| p == path))
    };
    let mut wanted: Vec<(String, String)> = Vec::new();
    for stop in &bundle.stops {
        let key = (stop.path.clone(), stop.file_hash.clone());
        if !stop.file_hash.is_empty()
            && !reviews.colored(&key.0, &key.1)
            && !widening(&stop.path)
            && !wanted.contains(&key)
        {
            wanted.push(key);
        }
    }
    let Some(review) = state.readings.reviews.tiles.get_mut(tile) else {
        return;
    };
    if wanted.iter().all(|w| review.warming.1.contains(w)) {
        return;
    }
    review.warming = (review.warming.0 + 1, wanted.clone());
    let round = review.warming.0;
    warm_next(state, tile.to_owned(), round, source_of(message), tx);
}

fn warm_next(
    state: &mut State,
    tile: String,
    round: u64,
    source: (Option<String>, String),
    tx: &mut Tx,
) {
    let reviews = &mut state.readings.reviews;
    let Some(review) = reviews
        .tiles
        .get_mut(&tile)
        .filter(|r| r.warming.0 == round)
    else {
        return;
    };
    let known = |(path, hash): &(String, String)| {
        reviews
            .colors
            .iter()
            .any(|((p, h), _)| p == path && h == hash)
            || reviews.oversize.iter().any(|(p, h)| p == path && h == hash)
    };
    let pending: Vec<(String, String)> = review
        .warming
        .1
        .iter()
        .filter(|w| !known(w))
        .cloned()
        .collect();
    review.warming.1 = pending;
    let Some((path, wanted)) = review.warming.1.first().cloned() else {
        return;
    };
    let (host, document) = source.clone();
    file::read(
        state,
        (host, document, path.clone()),
        move |state, read, tx| {
            if let Ok(m) = read
                && let (Some(rows), Some(hash)) = (m.rows.as_deref(), m.hash.as_deref())
            {
                state.readings.reviews.keep_colors(&path, hash, rows);
                state.readings.revision += 1;
            }
            if let Some(review) = state.readings.reviews.tiles.get_mut(&tile)
                && review.warming.0 == round
            {
                review.warming.1.retain(|w| w.0 != path || w.1 != wanted);
            }
            warm_next(state, tile, round, source, tx);
        },
        tx,
    );
}

/// The file a place opens: a stop's row at its line with the stop's lines marked, an arrow's
/// stop, a changed file or a Not shown file.
fn target_of(reading: &Reading, at: usize) -> Option<file::Target> {
    let place = &reading.places[at];
    let stop_target = |stop: usize, line: Option<i64>| {
        let s = &reading.bundle.stops[stop];
        let range = (s.now_from > 0).then_some((s.now_from, s.now_to));
        file::Target {
            path: s.path.clone(),
            line: line.or(range.map(|r| r.0)),
            range,
            outside: false,
        }
    };
    match &place.spot {
        Spot::Row { now, .. } => place.stop.map(|s| stop_target(s, *now)),
        Spot::Arrow(_) => place.arrow.map(|s| stop_target(s, None)),
        Spot::File(path) => Some(file::Target {
            path: path.clone(),
            ..file::Target::default()
        }),
        Spot::NotShown(path) if !path.ends_with('/') => Some(file::Target {
            path: path.clone(),
            ..file::Target::default()
        }),
        _ => None,
    }
}

/// The file view's keys: ↑/↓ by row, with ⇧ to select, PageUp/PageDown by a page, ←/→ by
/// change, ⇧←/⇧→ sideways by half the view, `z` fills the tile, ⌃o goes back and Escape drops
/// the selection, then closes.
fn file_key(review: &mut Review, prefix: &str, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(open) = review.files.last_mut() else {
        return false;
    };
    let rows = open.rows.clone();
    let count = rows.as_ref().map_or(0, |r| r.len());
    let to = |n: isize| n.clamp(0, count.saturating_sub(1) as isize) as usize;
    let cursor = open.cursor as isize;
    let next = match &ev.key {
        Key::Right | Key::Left if ev.mods.shift => {
            let half = review.sideways.half_view() * if ev.key == Key::Right { 1.0 } else { -1.0 };
            tx.scroll_to_x(file::body_key(prefix), review.sideways.offset() + half);
            return true;
        }
        Key::Down => Some(to(cursor + 1)),
        Key::Up => Some(to(cursor - 1)),
        Key::PageDown => Some(to(cursor + file::PAGE_ROWS as isize)),
        Key::PageUp => Some(to(cursor - file::PAGE_ROWS as isize)),
        Key::Right | Key::Left => {
            let starts = rows.as_deref().map(file::change_starts).unwrap_or_default();
            let at = open.cursor;
            Some(if ev.key == Key::Right {
                starts.iter().copied().find(|&s| s > at).unwrap_or(at)
            } else {
                starts.iter().copied().rev().find(|&s| s < at).unwrap_or(at)
            })
        }
        Key::Text(t) if t == "z" => {
            review.filled = !review.filled;
            return true;
        }
        Key::Escape if open.select_from.is_some() => {
            open.select_from = None;
            return true;
        }
        Key::Escape => {
            close_files(review);
            return true;
        }
        _ => return false,
    };
    if let Some(next) = next.filter(|_| count > 0) {
        let selecting = ev.mods.shift && matches!(ev.key, Key::Up | Key::Down);
        open.put_cursor(next, selecting, prefix, tx);
    }
    true
}

/// Escape with nothing selected: the file view closes.
fn close_files(review: &mut Review) {
    review.files.clear();
    review.filled = false;
}
