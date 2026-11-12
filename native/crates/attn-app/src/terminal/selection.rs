use attn_term::selection::{Anchor, Point, Selection, clean_lines, word_at};
use attn_term::{Mark, Overlay, OverlayKind, Rgb, Theme, Vt};

use super::{Term, Terminals};

/// The light blue the Tauri terminal washes a selection in, by theme.
const SELECTION_DARK: Rgb = [0x26, 0x4f, 0x78];
const SELECTION_LIGHT: Rgb = [0xad, 0xd6, 0xff];

/// What the user selected in one terminal and what the pointer hovers.
#[derive(Default)]
pub(super) struct View {
    selection: Option<Selection>,
    /// The selection's first row, which keeps it on its text as the history drops old rows.
    mark: Option<Mark>,
    /// The text the last finished selection copied, which ⌘C copies again.
    pub(super) selected: Option<String>,
    /// The selection the program's own scrolling carries along, kept by its text.
    anchor: Option<Anchor>,
    pub(super) selecting: Option<Selecting>,
    /// The command block the user clicked, which copying takes when no text is selected.
    pub(super) block: Option<u64>,
    /// The cell under the pointer and the link over it, until output or a scroll moves the
    /// text under it.
    pub(super) hover: Option<super::links::Hover>,
}

impl View {
    /// Selects `selection`, marking its first row so it stays on its text.
    fn select(&mut self, selection: Option<Selection>, vt: &mut Vt) {
        self.mark = selection.and_then(|s| vt.mark(s.start.row, 0));
        self.selection = selection;
    }

    /// Selects a block's command, whose text is the command line the shell sent.
    pub(super) fn select_command(
        &mut self,
        selection: Selection,
        command: Option<String>,
        vt: &mut Vt,
    ) {
        self.select(Some(selection), vt);
        self.selected = command;
    }

    /// The selection on the rows its text is on now. It goes once its first row left the
    /// history.
    fn selection(&mut self) -> Option<Selection> {
        let selection = self.selection?;
        let Some(row) = self
            .mark
            .as_ref()
            .map_or(Some(selection.start.row), Mark::row)
        else {
            self.selection = None;
            self.mark = None;
            return None;
        };
        let moved = |p: Point| Point {
            row: (i64::from(p.row) + i64::from(row) - i64::from(selection.start.row)).max(0) as u32,
            ..p
        };
        let now = Selection {
            start: moved(selection.start),
            end: moved(selection.end),
        };
        self.selection = Some(now);
        Some(now)
    }
}

/// What letting go of a selecting button did.
pub(crate) enum Release {
    Nothing,
    /// The press selected no text: a click, which opens a link under it with ⌘ or Ctrl held.
    Click,
    Copied(String),
}

pub(super) struct Selecting {
    from: (f32, f32),
    dragged: bool,
}

impl Terminals {
    /// The primary button went down at `at` on terminal `id`'s slot, the `clicks`th press of a
    /// run, and no program heard it. A second press selects the word and a third the row, which
    /// the clipboard then holds; any other starts a selection.
    pub(crate) fn select_press(&mut self, id: &str, at: (f32, f32), clicks: u32) -> Option<String> {
        let metrics = self.metrics();
        let term = self.panes.get_mut(id)?;
        let (col, row) = selection_cell(term, &metrics, at)?;
        let row = term.vt.view_top() + u32::from(row);
        term.view.anchor = None;
        self.undrawn = true;
        let cols = term.vt.size().0;
        let selection = match clicks {
            2 => {
                let (start, end) = word_at(&term.vt.link_row(row).cells, col)?;
                Selection::at(row, start, end)
            }
            3 => Selection::at(row, 0, cols),
            _ => {
                term.view.selected = None;
                term.view.selecting = Some(Selecting {
                    from: at,
                    dragged: false,
                });
                term.view
                    .select(Some(Selection::at(row, col, col)), &mut term.vt);
                return None;
            }
        };
        term.view.selecting = None;
        term.view.select(Some(selection), &mut term.vt);
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
        if let Some(mut selection) = term.view.selection() {
            selection.end = Point { row, col: col + 1 };
            term.view.selection = Some(selection);
            self.undrawn = true;
        }
    }

    /// The button that started a selection in terminal `id` came up, or was cancelled. A press
    /// that never moved was a click, which clears the selection; a drag leaves it, and the
    /// clipboard holds its text.
    pub(crate) fn select_release(&mut self, id: &str) -> Release {
        let Some(term) = self.panes.get_mut(id) else {
            return Release::Nothing;
        };
        let Some(selecting) = term.view.selecting.take() else {
            return Release::Nothing;
        };
        if !selecting.dragged {
            term.view.select(None, &mut term.vt);
            self.undrawn = true;
            return Release::Click;
        }
        let Some(selection) = term.view.selection() else {
            return Release::Nothing;
        };
        let text = text_of(term, selection);
        term.view.selected = (!text.is_empty()).then(|| text.clone());
        if text.is_empty() {
            Release::Click
        } else {
            Release::Copied(text)
        }
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
            "block": term.view.block,
        }))
    }

    /// What ⌘C copies from terminal `id`: the text of its last selection, or else the whole
    /// selected block.
    pub(crate) fn copy_text(&mut self, id: &str) -> Option<String> {
        if let Some(text) = self.panes.get(id)?.view.selected.clone() {
            return Some(text);
        }
        let block = self.block_id(id)?;
        self.block_text(id, block, super::blocks::Part::Whole)
    }

    /// A click ended at `at` in terminal `id` with nothing selected: it selects the block there.
    pub(crate) fn click(&mut self, id: &str, at: (f32, f32)) {
        let metrics = self.metrics();
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let Some((_, row)) = selection_cell(term, &metrics, at) else {
            return;
        };
        let row = term.vt.view_top() + u32::from(row);
        let before = term.view.block;
        Terminals::select_block_at(term, row);
        self.undrawn |= before != term.view.block || term.view.selection.is_some();
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
        Terminals::hover_moved(term);
        true
    }

    /// The wheel is about to scroll the program's own screen: a selection on it follows its
    /// text from now on.
    pub(super) fn anchor_selection(term: &mut Term) {
        if term.view.anchor.is_some() {
            return;
        }
        let Some(selection) = term.view.selection() else {
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

    /// Output moved the text: the hover reads it again, the shell's command steps make
    /// blocks, and a selection that follows its text finds it again on the screen.
    pub(super) fn wrote(term: &mut Term) {
        Terminals::hover_moved(term);
        Terminals::read_prompts(term);
        let Some(anchor) = &term.view.anchor else {
            return;
        };
        let (cols, rows) = term.vt.size();
        let top = term.vt.view_top();
        let lines: Vec<String> = (0..u32::from(rows))
            .map(|row| term.vt.row_text(top + row, 0..cols).trim_end().to_owned())
            .collect();
        let found = anchor.find(&lines, top, cols);
        term.view.select(found, &mut term.vt);
    }

    /// The screen was replaced: what the user selected on the old one goes with it, and so
    /// do its blocks.
    pub(super) fn forget_screen(term: &mut Term) {
        term.view = View::default();
        term.blocks = Default::default();
    }

    /// What lies over terminal `id`'s view: its selection, and the hovered link's underline.
    pub(super) fn overlays(
        term: &mut Term,
        theme: &Theme,
        finding: Option<&super::find::Finding>,
    ) -> Vec<Overlay> {
        let mut overlays = Terminals::block_overlays(term);
        overlays.extend(Terminals::find_overlays(term, finding));
        overlays.extend(Terminals::selection_overlays(term, theme));
        overlays.extend(Terminals::link_overlays(term, theme));
        overlays
    }

    fn selection_overlays(term: &mut Term, theme: &Theme) -> Vec<Overlay> {
        let Some(selection) = term.view.selection() else {
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

pub(super) fn selection_cell(
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
