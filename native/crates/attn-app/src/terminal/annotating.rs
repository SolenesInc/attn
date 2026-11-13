use std::collections::HashMap;

use attn_protocol::{SessionAnnotation, SessionAnnotationsSaveMessage, SessionMessage};
use attn_term::align::{Alignment, RowRange, quotes_anchor};
use attn_term::selection::Selection;
use attn_term::{Overlay, OverlayKind, Rgb, Vt};
use rune::TextEdit;

use super::{Term, Terminals};

/// The violet the Tauri terminal marks annotated text in.
const MARKED: Rgb = [0xa7, 0x8b, 0xfa];
/// A mark's wash, 0.24 of the violet, and 0.42 while ⌥ holds the pointer over it.
const WASH: u8 = 61;
const WASH_HOVERED: u8 = 107;
/// A mark's underline, 0.75 of the violet, and all of it while hovered.
const LINE: u8 = 191;
const LINE_HOVERED: u8 = 255;

/// The newest rows searched for a message not found before, as the Tauri terminal searches.
const SEARCH_ROWS: u32 = 2000;
/// The rows searched either side of where a message was last found, at the least.
const LOCAL_MARGIN: u32 = 60;

/// One mark on an agent's message: the markdown between UTF-16 offsets `start` and `end`, the
/// label picked for it, and the comment written on it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Annotation {
    pub(crate) id: String,
    pub(crate) message_key: String,
    pub(crate) start: usize,
    pub(crate) end: usize,
    pub(crate) quote: String,
    pub(crate) label: String,
    pub(crate) comment: String,
}

impl Annotation {
    /// A stored mark, its label read from an older draft's emoji when it names no label; none
    /// when it names no mark or message.
    fn stored(wire: &SessionAnnotation) -> Option<Annotation> {
        if wire.id.is_empty() || wire.message_key.is_empty() {
            return None;
        }
        let label = match &wire.quick_label_id {
            Some(id) => id.clone(),
            None => wire
                .emoji
                .as_deref()
                .and_then(crate::annotations::label_by_emoji)
                .map(|l| l.id.to_owned())
                .unwrap_or_default(),
        };
        let offset = |n: i64| usize::try_from(n).unwrap_or(0);
        Some(Annotation {
            id: wire.id.clone(),
            message_key: wire.message_key.clone(),
            start: offset(wire.start),
            end: offset(wire.end),
            quote: wire.quote.clone(),
            label,
            comment: wire.comment.clone(),
        })
    }

    pub(crate) fn wire(&self) -> SessionAnnotation {
        let offset = |n: usize| i64::try_from(n).unwrap_or(i64::MAX);
        SessionAnnotation {
            id: self.id.clone(),
            message_key: self.message_key.clone(),
            start: offset(self.start),
            end: offset(self.end),
            quote: self.quote.clone(),
            quick_label_id: Some(self.label.clone()),
            emoji: None,
            comment: self.comment.clone(),
        }
    }
}

/// What the daemon could say about the session's transcript, which a drag that marks nothing
/// explains.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Transcript {
    Discovering,
    Ready,
    Unavailable(Option<String>),
    /// The daemon could not read it at all.
    Unreadable(String),
}

/// Why a ⌥-drag marked nothing.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Miss {
    /// The session has no message to mark.
    NoMessages,
    /// The drag covered none of the agent's messages.
    OutsideMessages,
}

/// A drag that resolved to an agent's message: where in its markdown, and what it says there.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Anchor {
    pub(crate) message_key: String,
    pub(crate) start: usize,
    pub(crate) end: usize,
    pub(crate) quote: String,
}

/// The popup over a mark: its labels, and the comment box once the user writes.
pub(crate) struct Popup {
    pub(crate) mark: String,
    /// Where the pointer let go, in points from the terminal slot's top-left.
    pub(crate) at: (f32, f32),
    pub(crate) writing: bool,
    pub(crate) comment: TextEdit,
    /// The list of every other label is open.
    pub(crate) picking: bool,
    /// What the control under the pointer does.
    pub(crate) hint: Option<String>,
    /// The popup's size as last laid out, which places it.
    pub(crate) size: Option<(f32, f32)>,
    /// Where the user moved the popup to, which it keeps until it closes.
    pub(crate) moved: Option<(f32, f32)>,
}

/// How the last send of the marks went, shown in the panel.
#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Outcome {
    Sending,
    /// `count` went to the session; `kept` were made or changed meanwhile and stayed.
    Sent {
        count: usize,
        kept: usize,
        until: f64,
    },
    /// The session waits on an approval, which the sending Enter would answer.
    Skipped,
    Failed(String),
}

/// Why the last ⌥-drag marked nothing, shown where it let go until `until`.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Notice {
    pub(crate) text: String,
    pub(crate) at: (f32, f32),
    pub(crate) until: f64,
}

/// One terminal's marks as its slot shows them.
/// A point or size by its bits, which hash.
type Bits = (u32, u32);

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Shown {
    id: String,
    hovered: bool,
    notice: Option<(String, Bits)>,
    #[allow(clippy::type_complexity)]
    popup: Option<(
        String,
        Bits,
        (bool, bool),
        Option<String>,
        Option<Bits>,
        (String, usize, usize, bool),
    )>,
    marks: Vec<(String, String, String)>,
    slot: Option<Bits>,
    note: (String, usize, usize, bool),
    outcome: Option<String>,
    panel: (Option<Bits>, Option<Bits>),
    moved: Option<Bits>,
}

/// Where a message was last looked for: found on rows `first..=last`, or missed in the newest
/// `SEARCH_ROWS`, after which only rows that output can have changed are searched.
#[derive(Clone, Copy)]
enum Seen {
    At(u32, u32),
    Missed,
}

struct Cached {
    alignment: Alignment,
    writes: u64,
    geometry: u64,
    cols: u16,
    total: u32,
}

/// An agent terminal's annotations: the session's messages the marks sit on, the draft of
/// marks and note the daemon keeps for it, and where each message was found on the rows.
pub(crate) struct Marking {
    pub(crate) session: String,
    messages: Vec<SessionMessage>,
    pub(crate) transcript: Transcript,
    pub(crate) marks: Vec<Annotation>,
    pub(crate) note: TextEdit,
    /// The draft's generation as this window last wrote or read it; a save carries the next.
    pub(crate) generation: i64,
    /// The messages, or the draft, need fetching from the daemon.
    pub(crate) wants_messages: bool,
    pub(crate) wants_draft: bool,
    /// The newest messages request, whose answer alone counts.
    pub(crate) messages_asked: u64,
    aligned: HashMap<String, Cached>,
    /// Where each message was last found, kept across output to search near it first.
    spans: HashMap<String, (Seen, u16, u64)>,
    writes: u64,
    geometry: u64,
    /// The mark ⌥ holds the pointer over.
    pub(crate) hovered: Option<String>,
    pub(crate) popup: Option<Popup>,
    pub(crate) notice: Option<Notice>,
    pub(crate) outcome: Option<Outcome>,
    /// When the note, edited since the last save, is saved: once typing pauses.
    pub(crate) note_due: Option<f64>,
    /// Where the user dragged the panel to, in points from the slot's top-left.
    pub(crate) panel_at: Option<(f32, f32)>,
    /// The panel's size as last laid out.
    pub(crate) panel_size: Option<(f32, f32)>,
    /// A drag moving the panel or the popup: where the box started, and where the pointer
    /// pressed, in window points.
    pub(crate) grab: Option<((f32, f32), [f32; 2])>,
}

impl Marking {
    /// The save of a note edit still waiting for typing to pause, as the marking goes: no answer
    /// can reach a marking that is gone.
    pub(crate) fn leaving(&self) -> Option<SessionAnnotationsSaveMessage> {
        self.note_due?;
        Some(SessionAnnotationsSaveMessage {
            session_id: self.session.clone(),
            annotations: self.marks.iter().map(Annotation::wire).collect(),
            note: Some(self.note.text.clone()),
            generation: self.generation + 1,
            ..SessionAnnotationsSaveMessage::new()
        })
    }

    pub(crate) fn new(session: String) -> Marking {
        Marking {
            session,
            messages: Vec::new(),
            transcript: Transcript::Discovering,
            marks: Vec::new(),
            note: TextEdit::default(),
            generation: 0,
            wants_messages: true,
            wants_draft: true,
            messages_asked: 0,
            aligned: HashMap::new(),
            spans: HashMap::new(),
            writes: 0,
            geometry: 0,
            hovered: None,
            popup: None,
            notice: None,
            outcome: None,
            note_due: None,
            panel_at: None,
            panel_size: None,
            grab: None,
        }
    }

    /// The marks, the note and what is open over them: the `annotations` of the automation
    /// server's `terminal-view` slice.
    pub(crate) fn state(&self) -> serde_json::Value {
        let marks: Vec<serde_json::Value> = self
            .marks
            .iter()
            .map(|m| {
                serde_json::json!({
                    "id": m.id,
                    "message": m.message_key,
                    "start": m.start,
                    "end": m.end,
                    "quote": m.quote,
                    "label": m.label,
                    "comment": m.comment,
                })
            })
            .collect();
        serde_json::json!({
            "session": self.session,
            "messages": self.messages.len(),
            "marks": marks,
            "note": self.note.text,
            "generation": self.generation,
            "hovered": self.hovered,
            "popup": self.popup.as_ref().map(|p| serde_json::json!({
                "mark": p.mark,
                "writing": p.writing,
                "picking": p.picking,
                "comment": p.comment.text,
            })),
            "notice": self.notice.as_ref().map(|n| n.text.clone()),
            "outcome": self.outcome.as_ref().map(|o| match o {
                Outcome::Sending => serde_json::json!({"kind": "sending"}),
                Outcome::Sent { count, kept, .. } => {
                    serde_json::json!({"kind": "sent", "count": count, "kept": kept})
                }
                Outcome::Skipped => serde_json::json!({"kind": "skipped"}),
                Outcome::Failed(why) => serde_json::json!({"kind": "error", "message": why}),
            }),
        })
    }

    /// Takes the session's messages, keeping only those with a key and text. True when they
    /// changed.
    pub(crate) fn set_messages(&mut self, messages: Vec<SessionMessage>) -> bool {
        let messages: Vec<SessionMessage> = messages
            .into_iter()
            .filter(|m| !m.key.is_empty() && !m.markdown.is_empty())
            .collect();
        let same = messages.len() == self.messages.len()
            && messages
                .iter()
                .zip(&self.messages)
                .all(|(a, b)| a.key == b.key && a.markdown == b.markdown);
        if same {
            return false;
        }
        self.messages = messages;
        self.aligned.clear();
        self.spans.clear();
        true
    }

    pub(crate) fn has_messages(&self) -> bool {
        !self.messages.is_empty()
    }

    /// Replaces the draft with the one the daemon keeps.
    pub(crate) fn hydrate(&mut self, marks: &[SessionAnnotation], note: &str, generation: i64) {
        self.marks = marks.iter().filter_map(Annotation::stored).collect();
        self.note = TextEdit::new(note);
        self.generation = generation;
    }

    /// Marks the markdown of message `key` between `start` and `end`, unlabelled; none when
    /// the message is gone or the span is not in it.
    pub(crate) fn add(&mut self, anchor: &Anchor) -> Option<&Annotation> {
        let markdown = &self
            .messages
            .iter()
            .find(|m| m.key == anchor.message_key)?
            .markdown;
        let quote = utf16_slice(markdown, anchor.start, anchor.end)?.to_owned();
        self.marks.push(Annotation {
            id: uuid::Uuid::new_v4().to_string(),
            message_key: anchor.message_key.clone(),
            start: anchor.start,
            end: anchor.end,
            quote,
            label: String::new(),
            comment: String::new(),
        });
        self.marks.last()
    }

    pub(crate) fn mark(&self, id: &str) -> Option<&Annotation> {
        self.marks.iter().find(|m| m.id == id)
    }

    pub(crate) fn mark_mut(&mut self, id: &str) -> Option<&mut Annotation> {
        self.marks.iter_mut().find(|m| m.id == id)
    }

    pub(crate) fn remove(&mut self, id: &str) -> bool {
        let before = self.marks.len();
        self.marks.retain(|m| m.id != id);
        self.marks.len() != before
    }

    /// Output moved the rows: every message is found again before it is drawn.
    pub(crate) fn wrote(&mut self) {
        self.writes += 1;
    }

    /// The rows reflowed: where a message was found before says nothing about where it is.
    pub(crate) fn reflowed(&mut self) {
        self.geometry += 1;
    }

    /// The screen was replaced: nothing found on the old one holds.
    pub(crate) fn forget_rows(&mut self) {
        self.aligned.clear();
        self.spans.clear();
    }

    /// Where message `key` is on the rows now, found again only when the rows changed since.
    fn alignment(&mut self, key: &str, vt: &mut Vt) -> Option<&Alignment> {
        let markdown = &self.messages.iter().find(|m| m.key == key)?.markdown;
        let cols = vt.size().0;
        let total = vt.history() + u32::from(vt.size().1);
        let fresh = self.aligned.get(key).is_some_and(|c| {
            c.writes == self.writes
                && c.geometry == self.geometry
                && c.cols == cols
                && c.total == total
        });
        if !fresh {
            let seen = self
                .spans
                .get(key)
                .filter(|(_, at_cols, geometry)| *at_cols == cols && *geometry == self.geometry)
                .map(|(seen, ..)| *seen);
            let alignment = find(markdown, vt, seen, total);
            let seen = alignment
                .span
                .map_or(Seen::Missed, |(first, last)| Seen::At(first, last));
            self.spans
                .insert(key.to_owned(), (seen, cols, self.geometry));
            self.aligned.insert(
                key.to_owned(),
                Cached {
                    alignment,
                    writes: self.writes,
                    geometry: self.geometry,
                    cols,
                    total,
                },
            );
        }
        self.aligned.get(key).map(|c| &c.alignment)
    }

    /// Each mark the rows still show as it was quoted, with the cells it covers.
    pub(crate) fn project(&mut self, vt: &mut Vt) -> Vec<(String, Vec<RowRange>)> {
        if self.marks.is_empty() || self.messages.is_empty() {
            return Vec::new();
        }
        let marks = self.marks.clone();
        let mut washes = Vec::new();
        for mark in marks {
            let Some(rows) = self
                .alignment(&mark.message_key, vt)
                .map(|a| a.rows_for(mark.start, mark.end))
            else {
                continue;
            };
            if rows.is_empty() || !quotes_anchor(&mark.quote, &painted(vt, &rows)) {
                continue;
            }
            washes.push((mark.id, rows));
        }
        washes
    }

    /// The mark drawn over cell (`row`, `col`), the last one when marks overlap.
    pub(crate) fn mark_at(&mut self, vt: &mut Vt, row: u32, col: u16) -> Option<String> {
        self.project(vt)
            .into_iter()
            .rev()
            .find(|(_, rows)| {
                rows.iter()
                    .any(|r| r.row == row && (r.cols.0..r.cols.1).contains(&col))
            })
            .map(|(id, _)| id)
    }

    /// The newest message the selection covers, as whole words that the rows still show.
    pub(crate) fn anchor(&mut self, vt: &mut Vt, selection: Selection) -> Option<Anchor> {
        let s = selection.ordered();
        let keys: Vec<String> = self.messages.iter().rev().map(|m| m.key.clone()).collect();
        for key in keys {
            let Some(alignment) = self.alignment(&key, vt) else {
                continue;
            };
            let Some((start, end)) =
                alignment.offsets_for((s.start.row, s.start.col), (s.end.row, s.end.col))
            else {
                continue;
            };
            let rows = alignment.rows_for(start, end);
            let markdown = &self.messages.iter().find(|m| m.key == key)?.markdown;
            let Some(quote) = utf16_slice(markdown, start, end).map(str::to_owned) else {
                continue;
            };
            if !quotes_anchor(&quote, &painted(vt, &rows)) {
                continue;
            }
            return Some(Anchor {
                message_key: key,
                start,
                end,
                quote,
            });
        }
        None
    }
}

/// Finds `markdown` near where it was `seen` last, or else in the newest `SEARCH_ROWS`: a find
/// near the old place that misses or touches its window's edge searches the newest rows instead.
/// A message the newest rows missed is looked for again only where output can have drawn it
/// since: the screen and, above it, the rows the message would take.
fn find(markdown: &str, vt: &mut Vt, seen: Option<Seen>, total: u32) -> Alignment {
    let newest = (total.saturating_sub(SEARCH_ROWS), total);
    let (base, end) = match seen {
        None => newest,
        Some(Seen::At(first, last)) => {
            let half = LOCAL_MARGIN.max(last - first + 1);
            (first.saturating_sub(half), total.min(last + half + 1))
        }
        Some(Seen::Missed) => {
            let (cols, screen) = vt.size();
            let tall = rows_tall(markdown, cols) + u32::from(screen) + LOCAL_MARGIN;
            (total.saturating_sub(tall.min(SEARCH_ROWS)), total)
        }
    };
    let alignment = align_rows(markdown, vt, base, end);
    let edge = alignment.span.is_some_and(|(first, last)| {
        (first <= base + 1 && base > 0) || (last + 2 >= end && end < total)
    });
    let retry = match seen {
        None => false,
        Some(Seen::At(..)) => alignment.span.is_none() || edge,
        Some(Seen::Missed) => edge,
    };
    if retry {
        return align_rows(markdown, vt, newest.0, newest.1);
    }
    alignment
}

/// The most rows `markdown` can take at `cols` columns: each line, wrapped.
fn rows_tall(markdown: &str, cols: u16) -> u32 {
    let cols = usize::from(cols.max(1));
    let rows: usize = markdown
        .lines()
        .map(|line| line.chars().count().div_ceil(cols).max(1))
        .sum();
    u32::try_from(rows).unwrap_or(u32::MAX)
}

fn align_rows(markdown: &str, vt: &mut Vt, base: u32, end: u32) -> Alignment {
    let rows: Vec<_> = (base..end).map(|row| vt.link_row(row)).collect();
    Alignment::of(markdown, &rows, base)
}

/// The text the cells of `rows` show now.
fn painted(vt: &mut Vt, rows: &[RowRange]) -> Vec<String> {
    rows.iter()
        .map(|r| vt.row_text(r.row, r.cols.0..r.cols.1))
        .collect()
}

/// `text` between UTF-16 offsets `start` and `end`; none when they fall outside it, inside a
/// character, or out of order.
fn utf16_slice(text: &str, start: usize, end: usize) -> Option<&str> {
    if start >= end {
        return None;
    }
    let mut at16 = 0;
    let (mut from, mut to) = (None, None);
    for (byte, c) in text.char_indices().chain([(text.len(), '\0')]) {
        if at16 == start {
            from = Some(byte);
        }
        if at16 == end {
            to = Some(byte);
            break;
        }
        at16 += c.len_utf16();
    }
    text.get(from?..to?)
}

impl Terminals {
    /// What lies over terminal `term`'s view for its marks: a wash and an underline on each
    /// cell a mark covers, brighter on the mark ⌥ points at.
    pub(super) fn annotation_overlays(term: &mut Term) -> Vec<Overlay> {
        let Some(marking) = term.marking.as_mut() else {
            return Vec::new();
        };
        let (_, rows) = term.vt.size();
        let top = term.vt.view_top();
        let mut overlays = Vec::new();
        for (id, ranges) in marking.project(&mut term.vt) {
            let hovered = marking.hovered.as_deref() == Some(id.as_str());
            for range in ranges {
                if !(top..top + u32::from(rows)).contains(&range.row) {
                    continue;
                }
                let row = (range.row - top) as u16;
                overlays.push(Overlay {
                    row,
                    cols: range.cols,
                    color: MARKED,
                    alpha: if hovered { WASH_HOVERED } else { WASH },
                    kind: OverlayKind::Wash,
                });
                overlays.push(Overlay {
                    row,
                    cols: range.cols,
                    color: MARKED,
                    alpha: if hovered { LINE_HOVERED } else { LINE },
                    kind: OverlayKind::Underline,
                });
            }
        }
        overlays
    }

    /// The mark at screen cell (`col`, `row`) of terminal `term`'s view.
    pub(super) fn mark_under(term: &mut Term, (col, row): (u16, u16)) -> Option<String> {
        let row = term.vt.view_top() + u32::from(row);
        term.marking.as_mut()?.mark_at(&mut term.vt, row, col)
    }

    /// The pointer moved to `at` over terminal `id` with ⌥ held, or else moved or left: the
    /// mark ⌥ points at brightens. True when that changed what is drawn.
    pub(crate) fn hover_mark(&mut self, id: &str, at: Option<(f32, f32)>) -> bool {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return false;
        };
        let cell = at.and_then(|at| super::selection::selection_cell(term, &metrics, at));
        if term.marking.as_ref().is_none_or(|m| m.marks.is_empty()) {
            return false;
        }
        let under = cell.and_then(|cell| Terminals::mark_under(term, cell));
        let marking = term.marking.as_mut().expect("checked above");
        if marking.hovered == under {
            return false;
        }
        marking.hovered = under;
        self.undrawn = true;
        true
    }

    /// Whether ⌥ points at a mark in terminal `id`, which shows the pointing hand.
    pub(crate) fn pointing_mark(&self, id: &str) -> bool {
        self.panes
            .get(id)
            .and_then(|t| t.marking.as_ref())
            .is_some_and(|m| m.hovered.is_some())
    }

    pub(crate) fn marking(&self, id: &str) -> Option<&Marking> {
        self.panes.get(id)?.marking.as_ref()
    }

    pub(crate) fn marking_mut(&mut self, id: &str) -> Option<&mut Marking> {
        self.undrawn = true;
        self.panes.get_mut(id)?.marking.as_mut()
    }

    /// The agent terminals whose session's messages or draft need fetching, by runtime id.
    pub(crate) fn markings_wanting(&self) -> Vec<(String, String, bool, bool)> {
        self.panes
            .iter()
            .filter_map(|(id, term)| {
                let m = term.marking.as_ref()?;
                (m.wants_messages || m.wants_draft).then(|| {
                    (
                        id.clone(),
                        m.session.clone(),
                        m.wants_messages,
                        m.wants_draft,
                    )
                })
            })
            .collect()
    }

    /// The session's messages changed: each terminal showing it fetches them again.
    pub(super) fn messages_changed(&mut self, session: &str) {
        for term in self.panes.values_mut() {
            if let Some(marking) = term.marking.as_mut().filter(|m| m.session == session) {
                marking.wants_messages = true;
            }
        }
    }

    /// The text terminal `id`'s comment box edits.
    pub(crate) fn popup_comment(&mut self, id: &str) -> &mut TextEdit {
        match self
            .panes
            .get_mut(id)
            .and_then(|t| t.marking.as_mut())
            .and_then(|m| m.popup.as_mut())
        {
            Some(popup) => &mut popup.comment,
            None => &mut self.unshown_comment,
        }
    }

    /// The slot's size of terminal `id`, in points.
    pub(crate) fn slot_size(&self, id: &str) -> Option<(f32, f32)> {
        self.panes.get(id)?.slot.map(|(size, _)| size)
    }

    /// What each terminal's marks put over its slot, for the view to tell when it changed.
    pub(crate) fn annotation_layers(&self) -> Vec<Shown> {
        let bits = |(x, y): (f32, f32)| (x.to_bits(), y.to_bits());
        let mut shown: Vec<Shown> = self
            .panes
            .iter()
            .filter_map(|(id, term)| {
                let m = term.marking.as_ref()?;
                Some(Shown {
                    id: id.clone(),
                    hovered: m.hovered.is_some(),
                    notice: m.notice.as_ref().map(|n| (n.text.clone(), bits(n.at))),
                    popup: m.popup.as_ref().map(|p| {
                        (
                            p.mark.clone(),
                            bits(p.at),
                            (p.writing, p.picking),
                            p.hint.clone(),
                            p.size.map(bits),
                            (
                                p.comment.text.clone(),
                                p.comment.anchor,
                                p.comment.focus,
                                p.comment.focused,
                            ),
                        )
                    }),
                    marks: m
                        .marks
                        .iter()
                        .map(|a| (a.id.clone(), a.label.clone(), a.comment.clone()))
                        .collect(),
                    slot: term.slot.map(|(size, _)| bits(size)),
                    note: (
                        m.note.text.clone(),
                        m.note.anchor,
                        m.note.focus,
                        m.note.focused,
                    ),
                    outcome: m.outcome.as_ref().map(|o| format!("{o:?}")),
                    panel: (m.panel_at.map(bits), m.panel_size.map(bits)),
                    moved: m.popup.as_ref().and_then(|p| p.moved).map(bits),
                })
            })
            .collect();
        shown.sort_by(|a, b| a.id.cmp(&b.id));
        shown
    }

    /// The terminals with a popup open.
    pub(crate) fn popups(&self) -> Vec<String> {
        self.panes
            .iter()
            .filter(|(_, t)| t.marking.as_ref().is_some_and(|m| m.popup.is_some()))
            .map(|(id, _)| id.clone())
            .collect()
    }

    /// The terminals whose note waits to be saved by `now`, and whether a panel's sent marks
    /// stopped showing.
    pub(crate) fn annotations_due(&mut self, now: f64) -> (Vec<String>, bool) {
        let mut notes = Vec::new();
        let mut sent = false;
        for (id, term) in &mut self.panes {
            let Some(marking) = term.marking.as_mut() else {
                continue;
            };
            if marking.note_due.is_some_and(|due| now >= due) {
                notes.push(id.clone());
            }
            if let Some(Outcome::Sent { until, .. }) = marking.outcome
                && now >= until
            {
                marking.outcome = None;
                sent = true;
            }
        }
        self.undrawn |= sent;
        (notes, sent)
    }

    /// The text terminal `id`'s note edits.
    pub(crate) fn note(&mut self, id: &str) -> &mut TextEdit {
        match self.panes.get_mut(id).and_then(|t| t.marking.as_mut()) {
            Some(marking) => &mut marking.note,
            None => &mut self.unshown_comment,
        }
    }

    /// Takes down each notice whose time is up: true when one went.
    pub(crate) fn expire_notices(&mut self, now: f64) -> bool {
        let mut gone = false;
        for marking in self.panes.values_mut().filter_map(|t| t.marking.as_mut()) {
            if marking.notice.as_ref().is_some_and(|n| now >= n.until) {
                marking.notice = None;
                gone = true;
            }
        }
        gone
    }

    /// A new connection: every draft and message list is fetched again, as either may have
    /// moved while the window was away.
    pub(super) fn refetch_markings(&mut self) {
        for marking in self.panes.values_mut().filter_map(|t| t.marking.as_mut()) {
            marking.wants_messages = true;
            marking.wants_draft = true;
        }
    }
}
