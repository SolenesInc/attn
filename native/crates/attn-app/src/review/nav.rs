//! Code navigation in a review: the definition, uses and type of a name on a line of code,
//! answered by a language server on the machine that holds the review.

use attn_protocol::{CodeNavLocation, CodeNavMessage, CodeNavResultMessage};
use attn_ui::Tokens;
use rune::tok::{radius, space, type_};
use rune::{El, Key, KeyEvent, Tx, col, row, text};

use crate::State;

/// An answer slower than this, after the cursor moved, is offered instead of followed: a jump
/// that lands after the user moved on loses their place.
pub(crate) const LATE_ANSWER: f64 = 1.5;
/// How long an offered answer stays in the note line once it shows.
pub(crate) const OFFER: f64 = 20.0;
/// Rows a page key moves in a list of places.
const PAGE: usize = 20;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    Definition,
    Uses,
    Type,
}

impl Kind {
    pub(crate) fn of_key(key: &str) -> Option<Kind> {
        match key {
            "f" => Some(Kind::Definition),
            "r" => Some(Kind::Uses),
            "h" => Some(Kind::Type),
            _ => None,
        }
    }

    fn wire(self) -> &'static str {
        match self {
            Kind::Definition => "definition",
            Kind::Uses => "references",
            Kind::Type => "hover",
        }
    }

    fn asking(self, name: &str) -> String {
        match self {
            Kind::Definition => format!("Finding the definition of {name}…"),
            Kind::Uses => format!("Finding the uses of {name}…"),
            Kind::Type => format!("Reading the type of {name}…"),
        }
    }

    fn empty(self, name: &str) -> String {
        match self {
            Kind::Definition => format!("The language server knows no definition of {name}."),
            Kind::Uses => format!("The language server knows no uses of {name}."),
            Kind::Type => format!("The language server has no type for {name}."),
        }
    }

    fn picking(self) -> &'static str {
        match self {
            Kind::Definition => "Go to the definition of which name? Type its label.",
            Kind::Uses => "List the uses of which name? Type its label.",
            Kind::Type => "Show the type of which name? Type its label.",
        }
    }

    fn title(self, name: &str) -> String {
        match self {
            Kind::Definition => format!("Definition of {name}"),
            Kind::Uses => format!("Uses of {name}"),
            Kind::Type => format!("Type of {name}"),
        }
    }
}

/// A name on a line, where the language server counts it, and the label that picks it.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Labeled {
    pub(crate) name: String,
    /// Byte offset in the line.
    pub(crate) byte: usize,
    /// UTF-16 offset in the line, as the language server counts.
    pub(crate) character: usize,
    pub(crate) label: String,
}

/// The names on `text`: a letter or `_`, then letters, digits or `_`.
pub(crate) fn names_on(text: &str) -> Vec<(String, usize, usize)> {
    let mut out = Vec::new();
    let mut utf16 = 0;
    let mut current: Option<(usize, usize, String)> = None;
    for (byte, c) in text.char_indices() {
        let starts = c.is_alphabetic() || c == '_';
        let continues = starts || c.is_numeric();
        match &mut current {
            Some((_, _, name)) if continues => name.push(c),
            Some(_) => {
                let (b, u, name) = current.take().unwrap();
                out.push((name, b, u));
                if starts {
                    current = Some((byte, utf16, c.to_string()));
                }
            }
            None if starts => current = Some((byte, utf16, c.to_string())),
            None => {}
        }
        utf16 += c.len_utf16();
    }
    if let Some((b, u, name)) = current {
        out.push((name, b, u));
    }
    out
}

const LETTERS: &[u8] = b"asdfghjklqwertyuiopzxcvbnm";

/// One letter a name while the line has no more names than letters, then two.
pub(crate) fn label_names(names: Vec<(String, usize, usize)>) -> Vec<Labeled> {
    let one = names.len() <= LETTERS.len();
    names
        .into_iter()
        .enumerate()
        .map(|(i, (name, byte, character))| {
            let label = if one {
                (LETTERS[i] as char).to_string()
            } else {
                format!(
                    "{}{}",
                    LETTERS[i / LETTERS.len()] as char,
                    LETTERS[i % LETTERS.len()] as char
                )
            };
            Labeled {
                name,
                byte,
                character,
                label,
            }
        })
        .collect()
}

/// A line the reader can ask about: a row of code at head, in a stop or a file view.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Line {
    pub(crate) path: String,
    pub(crate) line: i64,
    pub(crate) text: String,
    /// The element the row draws as, which carries the labels and the asked mark.
    pub(crate) row: String,
}

/// An answer with places to go: a list, or a type.
#[derive(Clone, Debug)]
pub(crate) struct Answer {
    pub(crate) kind: Kind,
    pub(crate) name: String,
    pub(crate) locations: Vec<CodeNavLocation>,
    pub(crate) text: Option<String>,
    pub(crate) cursor: usize,
}

impl Answer {
    /// The one line a late answer is offered as, and what Enter does with it.
    fn offer(&self) -> (String, &'static str) {
        let first = self.locations.first();
        if self.kind == Kind::Type {
            return (format!("The type of {} arrived", self.name), "show");
        }
        if let (Kind::Definition, 1, Some(first)) = (self.kind, self.locations.len(), first) {
            let file = first.path.rsplit('/').next().unwrap_or(&first.path);
            return (format!("{} is in {file}:{}", self.name, first.line), "open");
        }
        let count = self.locations.len();
        let what = match (self.kind, count) {
            (Kind::Definition, 1) => "definition",
            (Kind::Definition, _) => "definitions",
            (_, 1) => "use",
            _ => "uses",
        };
        (format!("{count} {what} of {}", self.name), "list")
    }
}

/// What the reader says above its keys about a question.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Note {
    Asking(String),
    Status(String),
    Alert(String),
}

/// The navigation of one review tile: a question being picked, asked, its answer listed or
/// offered, and what the note line says.
#[derive(Default)]
pub(crate) struct Nav {
    pub(crate) picking: Option<(Kind, Line, Vec<Labeled>, String)>,
    /// The question on its way: its number, the row asked about, and when and from where.
    pub(crate) asking: Option<(u64, String, f64, String)>,
    pub(crate) asked: u64,
    pub(crate) panel: Option<Answer>,
    /// An answer offered in the note line, and until when.
    pub(crate) offer: Option<(Answer, f64)>,
    pub(crate) note: Option<Note>,
}

/// Starts a question of `kind` on `line`: the names on it get labels to pick by, or the only
/// one is asked about at once.
pub(crate) fn start(
    nav: &mut Nav,
    kind: Kind,
    line: Result<Line, &'static str>,
) -> Option<(Kind, Line, Labeled)> {
    nav.offer = None;
    let line = match line {
        Ok(line) => line,
        Err(why) => {
            nav.note = Some(Note::Status(why.to_owned()));
            return None;
        }
    };
    let names = label_names(names_on(&line.text));
    match names.len() {
        0 => {
            nav.note = Some(Note::Status("This line has no names to ask about.".into()));
            None
        }
        1 => Some((kind, line, names.into_iter().next()?)),
        _ => {
            nav.note = None;
            nav.picking = Some((kind, line, names, String::new()));
            None
        }
    }
}

/// A key while labels show: a letter narrows them and asks once one is whole; any other key
/// drops them. Escape is left to the caller.
pub(crate) fn pick(nav: &mut Nav, typed: &str) -> Option<(Kind, Line, Labeled)> {
    let (kind, line, names, so_far) = nav.picking.take()?;
    let typed = format!("{so_far}{typed}");
    let matching: Vec<&Labeled> = names
        .iter()
        .filter(|n| n.label.starts_with(&typed))
        .collect();
    if matching.is_empty() {
        return None;
    }
    if let Some(exact) = matching.iter().find(|n| n.label == typed) {
        return Some((kind, line, (*exact).clone()));
    }
    nav.picking = Some((kind, line, names, typed));
    None
}

/// Asks tile `tile`'s language server about `name` on `line`. An answer in time follows a
/// lone definition or lists the places; one that comes late, after the cursor moved or while a
/// box is open, is offered instead. `place` names where the cursor stands, to tell.
pub(crate) fn ask(
    state: &mut State,
    tile: &str,
    source: (Option<String>, String),
    (kind, line, name): (Kind, Line, Labeled),
    place: String,
    tx: &mut Tx,
) {
    let Some(nav) = state.readings.reviews.nav_mut(tile) else {
        return;
    };
    nav.picking = None;
    nav.panel = None;
    nav.offer = None;
    nav.asked += 1;
    let number = nav.asked;
    nav.asking = Some((number, line.row.clone(), tx.clock(), place));
    nav.note = Some(Note::Asking(kind.asking(&name.name)));
    state.readings.revision += 1;
    let (host, document) = source;
    let tile = tile.to_owned();
    let (path, at, character) = (line.path.clone(), line.line, name.character as i64);
    state.ask(
        |id| CodeNavMessage {
            host,
            document,
            path,
            line: at,
            character,
            kind: kind.wire().into(),
            request_id: Some(id),
            ..CodeNavMessage::new()
        },
        move |state, answer, tx| {
            let result = match answer {
                crate::requests::Answer::NotSent => Err(
                    "The daemon is not reachable, so nothing was asked. Ask again once it is back."
                        .to_owned(),
                ),
                other => other.outcome().and_then(|r| {
                    r.parse::<CodeNavResultMessage>()
                        .ok_or_else(|| "the daemon's answer did not parse".to_owned())
                }),
            };
            answered(state, &tile, number, (kind, line, name), result, tx);
        },
        tx,
    );
}

fn answered(
    state: &mut State,
    tile: &str,
    number: u64,
    (kind, line, name): (Kind, Line, Labeled),
    result: Result<CodeNavResultMessage, String>,
    tx: &mut Tx,
) {
    let place_now = super::place_of(state, tile);
    let boxed = state.readings.reviews.boxed();
    let Some(nav) = state.readings.reviews.nav_mut(tile) else {
        return;
    };
    if nav.asking.as_ref().map(|(n, ..)| *n) != Some(number) {
        return;
    }
    let Some((asked, _, at, from)) = nav.asking.take() else {
        return;
    };
    state.readings.revision += 1;
    let m = match result {
        Ok(m) => m,
        Err(why) => {
            nav.note = Some(Note::Alert(why));
            return;
        }
    };
    let _ = asked;
    let locations = m.locations.unwrap_or_default();
    let own = kind == Kind::Definition
        && locations.len() == 1
        && locations[0].path == line.path
        && locations[0].line == line.line
        && locations[0].end_line == line.line
        && (locations[0].character..=locations[0].end_character).contains(&(name.character as i64));
    if own {
        let source = super::source_of_tile(state, tile);
        if let Some(source) = source {
            ask(state, tile, source, (Kind::Uses, line, name), from, tx);
        }
        return;
    }
    let text = m.text.filter(|t| !t.is_empty());
    if locations.is_empty() && text.is_none() {
        nav.note = Some(Note::Status(kind.empty(&name.name)));
        return;
    }
    nav.note = None;
    let late =
        boxed || (tx.clock() - at > LATE_ANSWER && place_now.as_deref() != Some(from.as_str()));
    let shown = Answer {
        kind,
        name: name.name,
        locations,
        text,
        cursor: 0,
    };
    if kind == Kind::Definition && shown.locations.len() == 1 && !late {
        let location = shown.locations[0].clone();
        super::follow_location(state, tile, &location, tx);
        return;
    }
    if late {
        let until = tx.clock() + OFFER;
        nav.offer = Some((shown, until));
        tx.wake_at(until);
    } else {
        nav.panel = Some(shown);
    }
}

fn list_key(tile: &str) -> String {
    format!("{}.nav.list", super::prefix(tile))
}

fn place_key(i: usize) -> String {
    format!("review.nav.place.{i}")
}

/// Keys while a list of places shows: ↑/↓ and the page keys pick and bring the pick into view,
/// and Enter says to open the pick. Escape is left to the caller.
pub(crate) fn panel_key(tile: &str, panel: &mut Answer, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let last = panel.locations.len().saturating_sub(1);
    panel.cursor = match ev.key {
        Key::Down => (panel.cursor + 1).min(last),
        Key::Up => panel.cursor.saturating_sub(1),
        Key::PageDown => (panel.cursor + PAGE).min(last),
        Key::PageUp => panel.cursor.saturating_sub(PAGE),
        Key::Enter => return true,
        _ => return false,
    };
    tx.scroll_into_view_in(list_key(tile), place_key(panel.cursor));
    false
}

/// The note line above the keys: labels being picked, a question on its way, why one got no
/// answer, or an answer offered.
pub(crate) fn note_line(tile: &str, nav: &Nav, t: &Tokens) -> Option<El> {
    if let Some((kind, ..)) = &nav.picking {
        return Some(
            text(format!("{} esc cancels.", kind.picking()))
                .role("status")
                .font(type_::LABEL)
                .color(t.text_secondary),
        );
    }
    if let Some((answer, _)) = &nav.offer {
        let (said, take) = answer.offer();
        return Some(
            row()
                .key("review.nav.offer")
                .role("status")
                .label("Offered answer")
                .on_press_tx({
                    let tile = tile.to_owned();
                    move |state: &mut State, tx: &mut Tx| super::take_offer(state, &tile, tx)
                })
                .child(
                    text(format!("{said} · ↵ {take}"))
                        .font(type_::LABEL)
                        .color(t.accent),
                )
                .child(
                    text("esc")
                        .key("review.nav.offer.drop")
                        .role("button")
                        .label("Drop the offered answer")
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .on_press_tx({
                            let tile = tile.to_owned();
                            move |state: &mut State, _: &mut Tx| super::drop_offer(state, &tile)
                        }),
                ),
        );
    }
    let (said, alert) = match nav.note.as_ref()? {
        Note::Asking(s) | Note::Status(s) => (s.clone(), false),
        Note::Alert(s) => (s.clone(), true),
    };
    Some(
        text(said)
            .key("review.nav.note")
            .role(if alert { "alert" } else { "status" })
            .font(type_::LABEL)
            .color(if alert { t.error } else { t.text_secondary })
            .wrap(),
    )
}

/// A row of the list of places is about this tall.
const PLACE_HEIGHT: f32 = 15.0;

thread_local! {
    static PLACES_BUILT: std::cell::Cell<usize> = const { std::cell::Cell::new(0) };
}

/// How many places of answers' lists this thread has built.
pub fn places_built() -> usize {
    PLACES_BUILT.with(std::cell::Cell::get)
}

/// Place `i` of a list, `l`, which a press opens.
fn place_row(tile: &str, l: &CodeNavLocation, i: usize, picked: bool, t: &Tokens) -> El {
    PLACES_BUILT.with(|n| n.set(n.get() + 1));
    let outside = if l.outside { " outside" } else { "" };
    let el = row()
        .key(place_key(i))
        .role("option")
        .label(format!("{}:{}{outside} {}", l.path, l.line, l.text.trim()))
        .w_full()
        .gap(space::S2)
        .px(space::S2)
        .child(
            text(format!("{}:{}", l.path, l.line))
                .font(type_::LABEL)
                .color(t.text_secondary),
        )
        .children(
            l.outside
                .then(|| text("outside").font(type_::LABEL).color(t.warning)),
        )
        .child(
            text(l.text.trim().to_owned())
                .font(type_::MONO_CAPTION)
                .color(t.text_primary),
        )
        .on_press_tx({
            let tile = tile.to_owned();
            move |state: &mut State, tx: &mut Tx| super::open_answer_place(state, &tile, i, tx)
        });
    if picked {
        el.fill(t.bg_selected).focus_selected(true)
    } else {
        el
    }
}

/// The list of places an answer gives, or the type it reads.
pub(crate) fn panel_view(tile: &str, answer: &Answer, t: &Tokens) -> El {
    let title = answer.kind.title(&answer.name);
    let panel = col()
        .key("review.nav.panel")
        .role("dialog")
        .label(title.clone())
        .w_full()
        .max_w(720.0)
        .padding(space::S3)
        .gap(space::S2)
        .radius(radius::LG)
        .child(
            row()
                .gap(space::S3)
                .child(text(title.clone()).font(type_::TITLE).color(t.text_primary))
                .children((answer.kind != Kind::Type).then(|| {
                    text(answer.locations.len().to_string())
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                }))
                .child(col().grow(1.0))
                .children(
                    (!answer.locations.is_empty())
                        .then(|| text("↵ open").font(type_::LABEL).color(t.text_tertiary)),
                )
                .child(
                    text("esc close")
                        .key("review.nav.panel.close")
                        .role("button")
                        .label("Close")
                        .font(type_::LABEL)
                        .color(t.text_tertiary)
                        .on_press_tx({
                            let tile = tile.to_owned();
                            move |state: &mut State, _: &mut Tx| super::close_panel(state, &tile)
                        }),
                ),
        )
        .children(answer.text.clone().map(|t_| {
            text(t_)
                .font(type_::MONO_CAPTION)
                .color(t.text_primary)
                .wrap()
        }))
        .children((!answer.locations.is_empty()).then(|| {
            let shown = tile.to_owned();
            let tokens = *t;
            let places = col()
                .w_full()
                .growth(rune::GrowthSpec::default())
                .virtual_rows(
                    answer.locations.len(),
                    PLACE_HEIGHT,
                    |_: &State, i| place_key(i).into(),
                    move |state: &State, i| {
                        let answer = state
                            .readings
                            .reviews
                            .tiles
                            .get(&shown)
                            .and_then(|r| r.nav.panel.as_ref());
                        match answer.and_then(|a| Some((a, a.locations.get(i)?))) {
                            Some((answer, l)) => {
                                place_row(&shown, l, i, i == answer.cursor, &tokens)
                            }
                            None => col().key(place_key(i)),
                        }
                    },
                );
            col()
                .key(list_key(tile))
                .role("listbox")
                .label(title)
                .w_full()
                .max_h(360.0)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .child(places)
        }));
    rune_kit::popover::raised(attn_ui::kit(t), panel, 8.0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn names_and_their_labels() {
        let names = names_on("\tsum := añb + _c2(9x)");
        let picked: Vec<(&str, usize, usize)> =
            names.iter().map(|(n, b, c)| (n.as_str(), *b, *c)).collect();
        assert_eq!(
            picked,
            [("sum", 1, 1), ("añb", 8, 8), ("_c2", 15, 14), ("x", 20, 19)]
        );
        let labels: Vec<String> = label_names(names).into_iter().map(|l| l.label).collect();
        assert_eq!(labels, ["a", "s", "d", "f"]);
        let many: Vec<String> = label_names((0..30).map(|i| (format!("n{i}"), i, i)).collect())
            .into_iter()
            .map(|l| l.label)
            .collect();
        assert_eq!((many[0].as_str(), many[26].as_str()), ("aa", "sa"));
    }
}
