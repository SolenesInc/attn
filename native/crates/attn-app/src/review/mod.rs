//! A review document's tile body: the change an agent presents, read by keyboard. The daemon
//! sends a markdown tile its review bundle only while `code_review_enabled` is on, so a tile
//! with a bundle is a review and one without is plain markdown.

mod diagram;
mod document;
mod frames;
mod keys;
mod stop;

use std::collections::HashMap;
use std::hash::{Hash, Hasher};
use std::ops::Range;

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
const BUILT: [Frame; 2] = [Frame::Brief, Frame::Map];
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
}

/// What people did in one review tile.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash)]
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
}

/// Each review tile's reading, by tile.
#[derive(Default)]
pub struct Reviews {
    tiles: HashMap<String, Review>,
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
        let mut reading = Reading {
            source,
            bundle,
            frame,
            side,
            parts,
            shown,
            map,
            places: Vec::new(),
        };
        reading.places = reading.places_of(tile, review.files_shown);
        reading
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
                    let rows = &bundle.stops[*stop].rows;
                    places.extend(rows.iter().zip(0..).map(|(row, j)| Place {
                        stop: Some(*stop),
                        ..place(
                            row_spot(&bundle.stops[*stop].path, row),
                            stop::row_key(&key, j),
                            at,
                        )
                    }));
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
    reviews: &Reviews,
    (read, read_version): (&dyn Fn(std::ops::Range<usize>) -> El, u64),
    marking: Vec<El>,
    t: &Tokens,
) -> El {
    let review = reviews.tiles.get(tile).cloned().unwrap_or_default();
    let reading = Reading::new(tile, source, bundle, &review);
    let cursor = reading.cursor(&review);
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
                                key: place,
                            }
                        },
                        t,
                    )
                });
                let switch = (sides && flow.side.is_some()).then(|| side_switch(reading.side, t));
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
        .child(card);
    if let Some(key) = cursor_key {
        document = document.active_descendant(key);
    }
    let frame = match reading.frame {
        Frame::Map => map(&prefix, &reading, document, revision, t),
        _ => document,
    };
    let offered = frames::offered(bundle, &BUILT);
    col()
        .key(prefix.clone())
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(frame)
        .child(keys::key_line(&prefix, reading.frame, &offered, sides, t))
        .children(review.help.then(|| keys::sheet(&prefix, t)))
}

/// The Before and After switch over a diagram, with `b`.
fn side_switch(side: Side, t: &Tokens) -> El {
    let tab = |s: Side, name: &str| {
        let on = s == side;
        text(name.to_owned())
            .font(type_::LABEL)
            .color(if on { t.text_primary } else { t.text_tertiary })
            .role("tab")
            .label(name.to_owned())
            .checked(on)
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
fn map(prefix: &str, reading: &Reading, document: El, revision: u64, t: &Tokens) -> El {
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
                        key: None,
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
        .then(|| keys::not_shown(&format!("{prefix}.map.not-shown"), bundle, None, t));
    let sides = frames::has_sides(bundle).then(|| side_switch(reading.side, t));
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
    let typed = match &ev.key {
        Key::Text(typed) => typed.as_str(),
        _ => "",
    };
    let to = match (&ev.key, typed) {
        (_, "?") => {
            review.help = !review.help;
            None
        }
        (Key::Escape, _) if review.help => {
            review.help = false;
            None
        }
        (_, "1" | "2" | "3") => {
            let Some((frame, _, _)) = Frame::ALL.iter().find(|(_, key, _)| *key == typed) else {
                return false;
            };
            if *frame == reading.frame || !frames::offered(bundle, &BUILT).contains(frame) {
                return true;
            }
            switch_frame(review, &reading, cursor, *frame);
            Some(Move::Shown)
        }
        (_, "b") if frames::has_sides(bundle) => {
            switch_side(review, &reading, cursor);
            Some(Move::Shown)
        }
        (Key::Down, _) => Some(Move::Line(cursor.map_or(0, |i| i + 1))),
        (Key::Up, _) => Some(Move::Line(cursor.map_or(0, |i| i.saturating_sub(1)))),
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
            show_lead_in(&tile, &reading, place, tx);
        }
        Some(Move::Arrow | Move::Shown) => {
            let after = Reading::new(&tile, &message.content, bundle, review);
            if let Some(i) = after.cursor(review) {
                let place = &after.places[i];
                review.cursor = Some(place.spot.clone());
                review.at = i;
                if matches!(to, Some(Move::Arrow)) || after.frame == Frame::Map {
                    show_lead_in(&tile, &after, place, tx);
                    if let Some(key) = map_arrow_key(&tile, &after) {
                        tx.scroll_into_view(key);
                    }
                } else {
                    tx.scroll_center(place.key.clone());
                }
            }
        }
        _ => {}
    }
    state.readings.revision += 1;
    true
}

enum Move {
    Line(usize),
    Stop(Option<usize>),
    /// The Map moved to another arrow: the cursor goes to its stop.
    Arrow,
    /// The frame or the side changed: the cursor stays where the new reading puts it.
    Shown,
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
fn switch_frame(review: &mut Review, reading: &Reading, cursor: Option<usize>, frame: Frame) {
    let here = reading.place(cursor);
    review.frame = Some(frame);
    if frame != Frame::Map {
        if let Some((stop, line)) = here {
            review.cursor = stop_spot(reading.bundle, stop, line);
        }
        return;
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
