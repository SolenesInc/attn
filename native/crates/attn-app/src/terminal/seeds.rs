use std::collections::HashSet;
use std::sync::Arc;

use attn_term::links::{Line, Span};
use attn_term::{LinkRow, Overlay, OverlayKind, Rgb};

use super::{Term, Terminals};

/// The app's accent, which a seed id the garden knows is washed and underlined in.
const ACCENT: Rgb = [0x3b, 0x82, 0xf6];
const WASH: u8 = 26;
const WASH_PREVIEWING: u8 = 46;
const LINE: u8 = 184;
const LINE_PREVIEWING: u8 = 255;
const ID_BODY: &str = "0123456789abcdefghjkmnpqrstvwxyz";
const ID_CELLS: usize = 8;
/// How long the pointer rests on a seed id before its card opens, and how long it may be off
/// the id and the card before the card closes, in seconds.
const CARD_OPENS: f64 = 0.16;
const CARD_CLOSES: f64 = 0.24;

/// The open seed card's terminal, seed, place and size, as bits a memo compares.
pub(crate) type CardKey = (String, String, [u32; 4], Option<[u32; 2]>);

/// The card of a seed id the pointer rests on, over the terminal that shows it: the agent the
/// terminal shows, the first cell of the id in the slot's points, and the card's own size once
/// laid out.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct SeedCard {
    pub(crate) runtime: String,
    pub(crate) session: String,
    pub(crate) seed: String,
    pub(crate) at: [f32; 4],
    pub(crate) size: Option<(f32, f32)>,
}

pub(super) enum CardTimer {
    Open(SeedCard, f64),
    Close(f64),
}

/// A seed id on screen: the seed, and the screen rows it covers with each row's first column
/// and the column after its last.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct SeedMark {
    pub(super) id: String,
    pub(super) rows: Vec<(u32, u16, u16)>,
}

impl SeedMark {
    fn covers(&self, (row, col): (u32, u16)) -> bool {
        self.rows
            .iter()
            .any(|&(r, start, end)| r == row && (start..end).contains(&col))
    }
}

impl Terminals {
    /// The garden's seed ids changed: each terminal marks the ones it shows again.
    pub(crate) fn know_seeds(&mut self, known: HashSet<String>) {
        if *self.seeds == known {
            return;
        }
        self.seeds = Arc::new(known);
        for term in self.panes.values_mut() {
            term.view.seeds = None;
        }
        self.undrawn = true;
    }

    /// The seed card open over a terminal.
    pub(crate) fn seed_card(&self, runtime: &str) -> Option<&SeedCard> {
        self.card.as_ref().filter(|c| c.runtime == runtime)
    }

    /// What the open seed card shows and where, as the tiles' view reads it.
    pub(crate) fn seed_card_key(&self) -> Option<CardKey> {
        self.card.as_ref().map(|c| {
            (
                c.runtime.clone(),
                c.seed.clone(),
                c.at.map(f32::to_bits),
                c.size.map(|(w, h)| [w.to_bits(), h.to_bits()]),
            )
        })
    }

    /// The pointer moved over terminal `runtime`, which shows `session`'s agent, or left it:
    /// resting on a seed id opens its card after a moment, and moving off both the id and the
    /// card closes it after another. The time to wake for that, when one was set.
    pub(crate) fn seed_pointer(
        &mut self,
        runtime: &str,
        session: &str,
        inside: bool,
        now: f64,
    ) -> Option<f64> {
        let under = if inside {
            self.seed_under_pointer(runtime)
        } else {
            None
        };
        let Some((seed, at)) = under.filter(|_| !self.over_card) else {
            if self.over_card {
                return None;
            }
            if matches!(&self.card_timer, Some(CardTimer::Open(c, _)) if c.runtime == runtime) {
                self.card_timer = None;
            }
            return self
                .seed_card(runtime)
                .is_some()
                .then(|| self.close_card_soon(now))
                .flatten();
        };
        if self
            .card
            .as_ref()
            .is_some_and(|c| c.runtime == runtime && c.seed == seed)
        {
            if matches!(self.card_timer, Some(CardTimer::Close(_))) {
                self.card_timer = None;
            }
            return None;
        }
        if matches!(&self.card_timer, Some(CardTimer::Open(c, _)) if c.runtime == runtime && c.seed == seed)
        {
            return None;
        }
        let due = now + CARD_OPENS;
        let card = SeedCard {
            runtime: runtime.to_owned(),
            session: session.to_owned(),
            seed,
            at,
            size: None,
        };
        self.card_timer = Some(CardTimer::Open(card, due));
        Some(due)
    }

    /// The pointer moved onto the seed card, or off it.
    pub(crate) fn seed_card_hovered(&mut self, inside: bool, now: f64) -> Option<f64> {
        self.over_card = inside && self.card.is_some();
        if inside {
            if matches!(self.card_timer, Some(CardTimer::Close(_))) {
                self.card_timer = None;
            }
            return None;
        }
        self.close_card_soon(now)
    }

    fn close_card_soon(&mut self, now: f64) -> Option<f64> {
        if self.card.is_none() || matches!(self.card_timer, Some(CardTimer::Close(_))) {
            return None;
        }
        let due = now + CARD_CLOSES;
        self.card_timer = Some(CardTimer::Close(due));
        Some(due)
    }

    /// Opens or closes the seed card whose moment came. Returns whether it did.
    pub(crate) fn expire_seed_card(&mut self, now: f64) -> bool {
        let due = match &self.card_timer {
            Some(CardTimer::Open(_, at) | CardTimer::Close(at)) => *at <= now,
            None => false,
        };
        if !due {
            return false;
        }
        self.card = match self.card_timer.take() {
            Some(CardTimer::Open(card, _)) => Some(card),
            _ => None,
        };
        self.over_card = false;
        self.undrawn = true;
        true
    }

    pub(crate) fn close_seed_card(&mut self) {
        self.card_timer = None;
        self.over_card = false;
        if self.card.take().is_some() {
            self.undrawn = true;
        }
    }

    /// The seed card took its size, which places it.
    pub(crate) fn seed_card_laid_out(&mut self, size: (f32, f32)) -> bool {
        match self.card.as_mut() {
            Some(card) if card.size != Some(size) => {
                card.size = Some(size);
                true
            }
            _ => false,
        }
    }

    /// The seed under the pointer resting on terminal `id`, and the cell it starts on in the
    /// slot, as x, y, width and height in points.
    fn seed_under_pointer(&mut self, id: &str) -> Option<(String, [f32; 4])> {
        let metrics = self.metrics();
        let known = self.seeds.clone();
        let term = self.panes.get_mut(id)?;
        let cell = term.view.hover.as_ref()?.cell;
        let (slot, scale) = term.slot?;
        let top = term.vt.view_top();
        let size = term.vt.size();
        let mark = marks(term, &known).iter().find(|m| m.covers(cell))?.clone();
        let &(row, start, _) = mark.rows.iter().find(|(r, ..)| *r >= top)?;
        let rect = metrics.cell_rect(size, slot, scale, (start, (row - top) as u16));
        Some((mark.id, rect))
    }

    /// Washes and underlines the seed ids on terminal `term`'s view that the garden knows,
    /// brighter on the one `previewing` names.
    pub(super) fn seed_overlays(
        term: &mut Term,
        known: &HashSet<String>,
        previewing: Option<&str>,
    ) -> Vec<Overlay> {
        if known.is_empty() {
            return Vec::new();
        }
        let rows = u32::from(term.vt.size().1);
        let top = term.vt.view_top();
        let mut overlays = Vec::new();
        for mark in marks(term, known) {
            let bright = previewing == Some(mark.id.as_str());
            for &(row, start, end) in &mark.rows {
                if !(top..top + rows).contains(&row) {
                    continue;
                }
                let row = (row - top) as u16;
                overlays.push(Overlay {
                    row,
                    cols: (start, end),
                    color: ACCENT,
                    alpha: if bright { WASH_PREVIEWING } else { WASH },
                    kind: OverlayKind::Wash,
                });
                overlays.push(Overlay {
                    row,
                    cols: (start, end),
                    color: ACCENT,
                    alpha: if bright { LINE_PREVIEWING } else { LINE },
                    kind: OverlayKind::Underline,
                });
            }
        }
        overlays
    }
}

/// The known seed ids on the rows terminal `term` shows, found once per screen of text.
fn marks<'a>(term: &'a mut Term, known: &HashSet<String>) -> &'a [SeedMark] {
    if term.view.seeds.is_none() {
        let found = if known.is_empty() {
            Vec::new()
        } else {
            find(term, known)
        };
        term.view.seeds = Some(found);
    }
    term.view.seeds.as_deref().unwrap_or_default()
}

fn find(term: &mut Term, known: &HashSet<String>) -> Vec<SeedMark> {
    let (cols, rows) = term.vt.size();
    let top = term.vt.view_top();
    let vt = &mut term.vt;
    let mut first = top;
    while first > 0 && top - first < 1 && vt.row_continues(first) {
        first -= 1;
    }
    let mut last = top + u32::from(rows) - 1;
    if vt.scrolled_back() > 0 && vt.row_continues(last + 1) {
        last += 1;
    }
    let mut marks = Vec::new();
    let mut start = first;
    while start <= last {
        let mut end = start;
        while end < last && vt.row_continues(end + 1) {
            end += 1;
        }
        let text: String = (start..=end).map(|r| vt.row_text(r, 0..cols)).collect();
        if text.contains("s-") {
            let line = Line::over(start, end, cols, |r| LinkRow {
                cells: vt.row_cells(r),
                uris: Vec::new(),
            });
            marks.extend(ids_in(&line, known));
        }
        start = end + 1;
    }
    marks
}

/// The seed ids in `line` that `known` holds, as whole words: no letter, digit or underscore
/// right before or after.
fn ids_in(line: &Line, known: &HashSet<String>) -> Vec<SeedMark> {
    let cells = &line.cells;
    let word = |c: Option<char>| c.is_some_and(|c| c.is_ascii_alphanumeric() || c == '_');
    let one = |i: usize| -> Option<char> {
        let mut chars = cells.get(i)?.chars();
        let c = chars.next()?;
        chars.next().is_none().then_some(c)
    };
    let mut found = Vec::new();
    let mut i = 0;
    while i + ID_CELLS <= cells.len() {
        let id: Option<String> = (one(i) == Some('s') && one(i + 1) == Some('-'))
            .then(|| (i + 2..i + ID_CELLS).map(one).collect::<Option<String>>())
            .flatten()
            .filter(|body| body.chars().all(|c| ID_BODY.contains(c)))
            .map(|body| format!("s-{body}"));
        let before = i.checked_sub(1).and_then(|p| cells[p].chars().last());
        let after = cells.get(i + ID_CELLS).and_then(|c| c.chars().next());
        match id {
            Some(id) if !word(before) && !word(after) && known.contains(&id) => {
                found.push(SeedMark {
                    rows: line.rows(Span {
                        start: i,
                        end: i + ID_CELLS,
                    }),
                    id,
                });
                i += ID_CELLS;
            }
            _ => i += 1,
        }
    }
    found
}
