use attn_term::selection::{Anchor, Point, Selection, clean_lines, word_at};
use attn_term::{Overlay, OverlayKind, Rgb, Theme};

use super::{Term, Terminals};

/// The light blue the Tauri terminal washes a selection in, by theme.
const SELECTION_DARK: Rgb = [0x26, 0x4f, 0x78];
const SELECTION_LIGHT: Rgb = [0xad, 0xd6, 0xff];

/// What the user selected in one terminal, and the scroll of its view.
#[derive(Default)]
pub(super) struct View {
    selection: Option<Selection>,
    /// The text the last finished selection copied, which ⌘C copies again.
    selected: Option<String>,
    /// The selection the program's own scrolling carries along, kept by its text.
    anchor: Option<Anchor>,
    selecting: Option<Selecting>,
}

struct Selecting {
    from: (f32, f32),
    dragged: bool,
}

impl Terminals {
    /// The primary button went down at `at` on terminal `id`'s slot, the `clicks`th press of a
    /// run, and no program heard it. A first press starts a selection; a second selects the
    /// word and a third the row, which the clipboard then holds.
    pub(crate) fn select_press(&mut self, id: &str, at: (f32, f32), clicks: u32) -> Option<String> {
        let metrics = self.metrics();
        let term = self.panes.get_mut(id)?;
        let (col, row) = selection_cell(term, &metrics, at)?;
        let row = term.vt.view_top() + u32::from(row);
        let view = &mut term.view;
        view.anchor = None;
        self.undrawn = true;
        let cols = term.vt.size().0;
        let selection = match clicks {
            1 => {
                view.selected = None;
                view.selecting = Some(Selecting {
                    from: at,
                    dragged: false,
                });
                view.selection = Some(Selection::at(row, col, col));
                return None;
            }
            2 => {
                let line = term.vt.row_text(row, 0..cols);
                let (start, end) = word_at(line.trim_end(), col)?;
                Selection::at(row, start, end)
            }
            _ => Selection::at(row, 0, cols),
        };
        view.selecting = None;
        view.selection = Some(selection);
        let text = text_of(term, selection);
        term.view.selected = (!text.is_empty()).then(|| text.clone());
        term.view.selected.clone()
    }

    /// The pointer moved to `at` while a selection is being dragged in terminal `id`. It
    /// selects once the pointer has moved half a cell from the press.
    pub(crate) fn select_drag(&mut self, id: &str, at: (f32, f32)) {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let Some(selecting) = &mut term.view.selecting else {
            return;
        };
        if !selecting.dragged {
            let (dx, dy) = (at.0 - selecting.from.0, at.1 - selecting.from.1);
            let threshold = metrics.cell_w * 0.5;
            if dx * dx + dy * dy < threshold * threshold {
                return;
            }
            selecting.dragged = true;
        }
        let Some((col, row)) = selection_cell(term, &metrics, at) else {
            return;
        };
        let row = term.vt.view_top() + u32::from(row);
        if let Some(selection) = &mut term.view.selection {
            selection.end = Point { row, col: col + 1 };
            self.undrawn = true;
        }
    }

    /// The button that started a selection in terminal `id` came up, or was cancelled. A press
    /// that never moved clears the selection; a drag leaves it, and the clipboard holds its
    /// text.
    pub(crate) fn select_release(&mut self, id: &str) -> Option<String> {
        let term = self.panes.get_mut(id)?;
        let selecting = term.view.selecting.take()?;
        if !selecting.dragged {
            term.view.selection = None;
            self.undrawn = true;
            return None;
        }
        let text = text_of(term, term.view.selection?);
        term.view.selected = (!text.is_empty()).then_some(text);
        term.view.selected.clone()
    }

    /// Where the cursor and what lies over terminal `id`'s view as the window last drew it,
    /// how far the view is scrolled back, and what ⌘C copies: the automation server's
    /// `terminal-view` slice.
    pub fn view_state(&self, id: &str) -> Option<serde_json::Value> {
        let term = self.panes.get(id)?;
        let overlays: Vec<serde_json::Value> = term
            .grid
            .overlays()
            .iter()
            .map(|o| {
                serde_json::json!({
                    "row": o.row,
                    "cols": [o.cols.0, o.cols.1],
                    "kind": format!("{:?}", o.kind),
                    "color": attn_term::Theme::hex(o.color),
                    "alpha": o.alpha,
                })
            })
            .collect();
        Some(serde_json::json!({
            "cursor": term.grid.cursor.map(|c| [c.col, c.row]),
            "overlays": overlays,
            "scrolled_back": term.scrolled_back,
            "copies": term.view.selected,
        }))
    }

    /// What ⌘C copies from terminal `id`: the text of its last selection.
    pub(crate) fn copy_text(&self, id: &str) -> Option<String> {
        self.panes.get(id)?.view.selected.clone()
    }

    /// Scrolls terminal `id`'s view by `lines`, positive toward the screen: true when it moved.
    pub(super) fn scroll_view(term: &mut Term, lines: i32) -> bool {
        let back = term.vt.scrolled_back();
        let history = term.vt.history();
        let next = (i64::from(back) - i64::from(lines)).clamp(0, i64::from(history)) as u32;
        if (next == 0 && lines > 0) || (next == history && lines < 0) {
            term.pointer.wheel = Default::default();
        }
        if next == back {
            return false;
        }
        term.vt.scroll_back_to(next);
        true
    }

    /// The wheel is about to scroll the program's own screen: a selection on it follows its
    /// text from now on.
    pub(super) fn anchor_selection(term: &mut Term) {
        let view = &term.view;
        let (Some(selection), None) = (view.selection, &view.anchor) else {
            return;
        };
        let text = text_of(term, selection);
        if text.is_empty() {
            return;
        }
        term.view.selected = Some(text);
        let cols = term.vt.size().0;
        let vt = &mut term.vt;
        term.view.anchor = Anchor::new(selection, |row| {
            vt.row_text(row, 0..cols).trim_end().to_owned()
        });
    }

    /// Output arrived: a selection that follows its text finds it again on the screen.
    pub(super) fn follow_anchor(term: &mut Term) {
        let Some(anchor) = &term.view.anchor else {
            return;
        };
        let (cols, rows) = term.vt.size();
        let top = term.vt.view_top();
        let lines: Vec<String> = (0..u32::from(rows))
            .map(|row| term.vt.row_text(top + row, 0..cols).trim_end().to_owned())
            .collect();
        term.view.selection = anchor.find(&lines, top, cols);
    }

    /// The screen was replaced: what the user selected on the old one goes with it, and so
    /// does a clipboard write the old stream left unfinished.
    pub(super) fn forget_screen(term: &mut Term) {
        term.view = View::default();
        term.osc52 = Default::default();
    }

    /// What lies over terminal `id`'s view: its selection.
    pub(super) fn overlays(term: &mut Term, theme: &Theme) -> Vec<Overlay> {
        let Some(selection) = term.view.selection else {
            return Vec::new();
        };
        let (cols, rows) = term.vt.size();
        let top = term.vt.view_top();
        let color = if *theme == Theme::of(true) {
            SELECTION_DARK
        } else {
            SELECTION_LIGHT
        };
        selection
            .rows(cols)
            .filter(|(row, _)| (top..top + u32::from(rows)).contains(row))
            .map(|(row, span)| Overlay {
                row: (row - top) as u16,
                cols: (span.start, span.end),
                color,
                alpha: 255,
                kind: OverlayKind::Wash,
            })
            .collect()
    }
}

fn selection_cell(
    term: &mut Term,
    metrics: &attn_term::Metrics,
    at: (f32, f32),
) -> Option<(u16, u16)> {
    let (slot, scale) = term.slot?;
    metrics.cell_in(term.vt.size(), slot, scale, at)
}

/// A selection's text as the Tauri terminal copies it: each row's cells, cleaned.
fn text_of(term: &mut Term, selection: Selection) -> String {
    let cols = term.vt.size().0;
    let lines: Vec<String> = selection
        .rows(cols)
        .map(|(row, span)| term.vt.row_text(row, span))
        .collect();
    clean_lines(&lines).join("\n")
}
