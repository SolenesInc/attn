use attn_term::{Found, Mark, Overlay, OverlayKind, Rgb, Vt};
use rune::{TextEdit, Tx};

use super::{Term, Terminals};

/// The yellow the Tauri terminal washes find matches in.
const MATCH: Rgb = [0xf5, 0xc5, 0x42];
/// A match's wash, 0.28 of the yellow.
const MATCH_WASH: u8 = 71;
/// The focused match's wash, 0.6 of the yellow.
const FOCUSED_WASH: u8 = 153;

/// One terminal's find bar: what it looks for, and the matches it found, oldest first.
#[derive(Default)]
pub(crate) struct Finding {
    pub(crate) query: TextEdit,
    pub(crate) open: bool,
    pub(crate) case: bool,
    matches: Vec<Found>,
    focused: Option<usize>,
    /// The focused match's first cell, which keeps it as the history drops its oldest rows.
    focused_at: Option<Mark>,
}

impl Finding {
    /// What the bar's count reads: the focused match and how many there are.
    pub(crate) fn count(&self) -> String {
        match self.focused {
            Some(at) if !self.matches.is_empty() => format!("{}/{}", at + 1, self.matches.len()),
            _ => "0/0".into(),
        }
    }
}

/// One open find bar as the view draws it: its terminal, its query and caret, Match case and
/// the count.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Shown {
    id: String,
    query: String,
    caret: (usize, usize),
    case: bool,
    count: String,
}

/// Which way stepping through the matches goes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Toward {
    /// Toward the oldest match, as Enter steps.
    Older,
    /// Toward the newest, as Shift+Enter steps.
    Newer,
}

impl Terminals {
    /// Opens terminal `id`'s find bar with its last query selected, and finds it again.
    pub(crate) fn open_find(&mut self, id: &str, tx: &mut Tx) {
        if !self.panes.contains_key(id) {
            return;
        }
        let finding = self.finds.entry(id.to_owned()).or_default();
        finding.open = true;
        finding.query.anchor = 0;
        finding.query.focus = finding.query.text.len();
        finding.query.focused = true;
        tx.focus(find_field_key(id), false);
        self.find_again(id, true);
    }

    /// The find bar of terminal `id` while it is open.
    pub(crate) fn finding(&self, id: &str) -> Option<&Finding> {
        self.finds.get(id).filter(|f| f.open)
    }

    /// What the open find bars show, for the view to tell when one changed.
    pub(crate) fn find_bars(&self) -> Vec<Shown> {
        let mut bars: Vec<Shown> = self
            .finds
            .iter()
            .filter(|(_, f)| f.open)
            .map(|(id, f)| Shown {
                id: id.clone(),
                query: f.query.text.clone(),
                caret: (f.query.anchor, f.query.focus),
                case: f.case,
                count: f.count(),
            })
            .collect();
        bars.sort_by(|a, b| a.id.cmp(&b.id));
        bars
    }

    /// The text the find field of terminal `id` edits.
    pub(crate) fn find_query(&mut self, id: &str) -> &mut TextEdit {
        &mut self.finds.entry(id.to_owned()).or_default().query
    }

    /// The query changed: the newest match is focused and shown.
    pub(crate) fn find_edited(&mut self, id: &str) {
        if let Some(finding) = self.finds.get_mut(id) {
            finding.focused = None;
            finding.focused_at = None;
        }
        self.find_again(id, true);
    }

    /// Match case turned on or off.
    pub(crate) fn toggle_find_case(&mut self, id: &str) {
        if let Some(finding) = self.finds.get_mut(id) {
            finding.case = !finding.case;
            finding.focused = None;
            finding.focused_at = None;
        }
        self.find_again(id, true);
    }

    /// Focuses the next match `toward` older or newer output, wrapping around, and shows it.
    pub(crate) fn step_find(&mut self, id: &str, toward: Toward) {
        let (Some(finding), Some(term)) = (self.finds.get_mut(id), self.panes.get_mut(id)) else {
            return;
        };
        let len = finding.matches.len();
        if len == 0 {
            return;
        }
        let next = match (finding.focused, toward) {
            (None, _) => len - 1,
            (Some(at), Toward::Older) => (at + len - 1) % len,
            (Some(at), Toward::Newer) => (at + 1) % len,
        };
        focus(finding, &mut term.vt, Some(next));
        show(term, finding.matches[next]);
        self.undrawn = true;
    }

    /// Closes terminal `id`'s find bar, keeping its query, and gives typing back to the
    /// terminal.
    pub(crate) fn close_find(&mut self, id: &str, tx: &mut Tx) {
        if let Some(finding) = self.finds.get_mut(id) {
            finding.open = false;
            finding.matches.clear();
            finding.focused = None;
            finding.focused_at = None;
        }
        if let Some(term) = self.panes.get_mut(id) {
            term.vt.find("");
        }
        self.undrawn = true;
        tx.blur();
    }

    /// Output reached the terminals: each open find bar finds its query again, keeping its
    /// focused match where it still is. True when a count the bar shows changed.
    pub(super) fn refind(&mut self) -> bool {
        let open: Vec<String> = self
            .finds
            .iter()
            .filter(|(_, f)| f.open)
            .map(|(id, _)| id.clone())
            .collect();
        let mut changed = false;
        for id in open {
            let before = self.finds.get(&id).map(Finding::count);
            self.find_again(&id, false);
            changed |= self.finds.get(&id).map(Finding::count) != before;
        }
        changed
    }

    /// Finds terminal `id`'s query again. With `show` the focused match scrolls into view;
    /// otherwise the view stays where the user left it.
    fn find_again(&mut self, id: &str, show_focused: bool) {
        let (Some(finding), Some(term)) = (self.finds.get_mut(id), self.panes.get_mut(id)) else {
            return;
        };
        let query = finding.query.text.clone();
        term.vt.find(&query);
        let kept = finding.focused_at.as_ref().and_then(Mark::point);
        let mut matches = term.vt.found();
        matches.reverse();
        if finding.case {
            matches.retain(|m| text_of(&mut term.vt, *m) == query);
        }
        let newest = matches.len().checked_sub(1);
        let focused = kept
            .and_then(|(row, col)| {
                matches
                    .iter()
                    .position(|m| (m.start_row, m.start_col) == (row, col))
            })
            .or(newest);
        finding.matches = matches;
        focus(finding, &mut term.vt, focused);
        if show_focused && let Some(at) = finding.focused {
            show(term, finding.matches[at]);
        }
        self.undrawn = true;
    }

    /// The washes over the matches the view shows, the focused one stronger.
    pub(super) fn find_overlays(term: &mut Term, finding: Option<&Finding>) -> Vec<Overlay> {
        let Some(finding) = finding.filter(|f| f.open) else {
            return Vec::new();
        };
        let (cols, rows) = term.vt.size();
        let top = term.vt.view_top();
        let shown = top..top + u32::from(rows);
        let mut overlays = Vec::new();
        for (i, m) in finding.matches.iter().enumerate() {
            if m.end_row < shown.start || m.start_row >= shown.end {
                continue;
            }
            let alpha = if finding.focused == Some(i) {
                FOCUSED_WASH
            } else {
                MATCH_WASH
            };
            for (row, span) in spans(*m, cols) {
                if shown.contains(&row) {
                    overlays.push(Overlay {
                        row: (row - top) as u16,
                        cols: span,
                        color: MATCH,
                        alpha,
                        kind: OverlayKind::Wash,
                    });
                }
            }
        }
        overlays
    }
}

/// Focuses match `at`, marking its first cell.
fn focus(finding: &mut Finding, vt: &mut Vt, at: Option<usize>) {
    finding.focused = at;
    finding.focused_at = at
        .map(|at| finding.matches[at])
        .and_then(|m| vt.mark(m.start_row, m.start_col));
}

/// The field key of terminal `id`'s find bar.
pub(crate) fn find_field_key(id: &str) -> String {
    format!("terminal.{id}.find{}", crate::keys::FIELD_KEY_SUFFIX)
}

/// Each row a match covers, with the columns it covers there.
fn spans(m: Found, cols: u16) -> impl Iterator<Item = (u32, (u16, u16))> {
    (m.start_row..=m.end_row).map(move |row| {
        let from = if row == m.start_row { m.start_col } else { 0 };
        let to = if row == m.end_row {
            m.end_col + 1
        } else {
            cols
        };
        (row, (from, to))
    })
}

/// The text a match covers, its wrapped rows joined.
fn text_of(vt: &mut Vt, m: Found) -> String {
    let cols = vt.size().0;
    spans(m, cols)
        .map(|(row, (from, to))| vt.row_text(row, from..to))
        .collect()
}

/// Scrolls the view so the match's first row shows, unless it already does.
fn show(term: &mut Term, m: Found) {
    let rows = u32::from(term.vt.size().1);
    let top = term.vt.view_top();
    if (top..top + rows).contains(&m.start_row) {
        return;
    }
    let history = term.vt.history();
    term.vt.scroll_back_to(history.saturating_sub(m.start_row));
    Terminals::hover_moved(term);
}
