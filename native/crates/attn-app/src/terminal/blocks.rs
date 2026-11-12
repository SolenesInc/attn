use attn_protocol::AttachBlock;
use attn_term::blocks::{Block, Blocks};
use attn_term::selection::{Point, Selection};
use attn_term::{Mark, Overlay, OverlayKind, Rgb, Vt};

use super::{Term, Terminals};

pub(super) type TermBlocks = Blocks<Mark>;

/// The blue the Tauri terminal washes and outlines the selected block in.
const BLOCK: Rgb = [0x4d, 0x9d, 0xe0];
/// The selected block's wash, 0.08 of the blue, drawn only while its edges show.
const BLOCK_WASH: u8 = 20;
/// The selected block's outline, 0.9 of the blue.
const BLOCK_OUTLINE: u8 = 230;

/// What the terminal's context menu offers where it opened.
pub(crate) struct MenuAt {
    /// The block under the pointer, which opening the menu selected.
    pub(crate) block: Option<u64>,
    /// Whether Copy has text to take: a selection, or the block.
    pub(crate) copies: bool,
    /// Whether the block ran a command Copy command can take.
    pub(crate) command: bool,
}

/// What part of a block copying takes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Part {
    Command,
    Output,
    /// The command, then its output.
    Whole,
}

impl Terminals {
    /// Takes the command steps the shell reported in the output just written.
    pub(super) fn read_prompts(term: &mut Term) {
        for event in term.vt.take_prompt_events() {
            let vt = &mut term.vt;
            term.blocks.apply(event.step, event.at, |at| again(vt, at));
        }
    }

    /// The blocks the daemon kept for a restored screen, on the screen's rows, in place of any
    /// the screen's own bytes marked.
    pub(super) fn seed_blocks(term: &mut Term, seeded: &[AttachBlock]) {
        drop(term.vt.take_prompt_events());
        let vt = &mut term.vt;
        let mut mark = |row: Option<i64>, col: Option<i64>| {
            let row = u32::try_from(row?).ok()?;
            vt.mark(row, col.and_then(|c| u16::try_from(c).ok()).unwrap_or(0))
        };
        let mut blocks = Vec::new();
        let mut pending = None;
        for b in seeded {
            let Some(prompt) = mark(Some(b.prompt_row), None) else {
                continue;
            };
            let block = Block {
                id: b.id as u64,
                prompt,
                input: mark(b.input_row, b.input_col),
                output: mark(b.output_start_row, None),
                end: mark(b.end_row, None),
                command: b.command.clone(),
                exit_code: b.exit_code.and_then(|c| i32::try_from(c).ok()),
            };
            if b.pending {
                pending = Some(block);
            } else if block.output.is_some() && block.end.is_some() {
                blocks.push(block);
            }
        }
        term.blocks.seed(blocks, pending);
    }

    /// A click at screen row `row` with nothing selected selects the block there, or none. A
    /// click on its command's rows selects the command too, which copying then takes.
    pub(super) fn select_block_at(term: &mut Term, row: u32) {
        let found = (!term.vt.mouse_modes().alternate_screen)
            .then(|| term.blocks.block_at(row))
            .flatten();
        let Some(block) = found else {
            term.view.block = None;
            return;
        };
        term.view.block = Some(block.id);
        let Some(rows) = block.rows() else {
            return;
        };
        if let (Some((input_row, input_col)), true) = (rows.input, row < rows.output) {
            let command = block.command.clone().filter(|c| !c.is_empty());
            let last = rows.output.saturating_sub(1);
            let cols = term.vt.size().0;
            let width = term.vt.row_text(last, 0..cols).trim_end().chars().count() as u16;
            let selection = Selection {
                start: Point {
                    row: input_row,
                    col: input_col,
                },
                end: Point {
                    row: last,
                    col: width.max(input_col + 1),
                },
            };
            term.view.select_command(selection, command, &mut term.vt);
        }
    }

    /// The context menu opens at `at` in terminal `id`, selecting the block under it. None while
    /// a program tracks the mouse, which takes the button instead.
    pub(crate) fn menu_at(&mut self, id: &str, at: (f32, f32)) -> Option<MenuAt> {
        let metrics = self.metrics();
        let term = self.panes.get_mut(id)?;
        if term.vt.mouse_modes().tracking {
            return None;
        }
        let row = super::selection::selection_cell(term, &metrics, at)
            .map(|(_, row)| term.vt.view_top() + u32::from(row));
        let shell = !term.vt.mouse_modes().alternate_screen;
        let block = row
            .filter(|_| shell)
            .and_then(|row| term.blocks.block_at(row));
        let command = block.is_some_and(|b| b.command.as_deref().is_some_and(|c| !c.is_empty()));
        let block = block.map(|b| b.id);
        if block.is_some() && block != term.view.block {
            term.view.block = block;
            self.undrawn = true;
        }
        Some(MenuAt {
            block,
            copies: term.view.selected.is_some() || term.view.block.is_some(),
            command,
        })
    }

    /// The selected block of terminal `id`.
    pub(crate) fn block_id(&self, id: &str) -> Option<u64> {
        let term = self.panes.get(id)?;
        term.view.block.filter(|b| term.blocks.block(*b).is_some())
    }

    /// The text of `part` of block `block` in terminal `id`, as the Tauri terminal copies it:
    /// the command line the shell sent, and the output's rows with trailing blanks and blank
    /// last rows dropped.
    pub(crate) fn block_text(&mut self, id: &str, block: u64, part: Part) -> Option<String> {
        let term = self.panes.get_mut(id)?;
        let found = term.blocks.block(block)?;
        let command = found.command.clone().unwrap_or_default();
        let rows = found.rows();
        let mut output = || {
            let rows = rows?;
            let cols = term.vt.size().0;
            let total = term.vt.history() + u32::from(term.vt.size().1);
            let mut lines: Vec<String> = (rows.output..rows.end.min(total))
                .map(|row| term.vt.row_text(row, 0..cols).trim_end().to_owned())
                .collect();
            while lines.last().is_some_and(String::is_empty) {
                lines.pop();
            }
            Some(lines.join("\n"))
        };
        match part {
            Part::Command => Some(command).filter(|c| !c.is_empty()),
            Part::Output => output(),
            Part::Whole => {
                let output = output()?;
                Some(if output.is_empty() {
                    command
                } else {
                    format!("{command}\n{output}")
                })
            }
        }
    }

    /// Scrolls terminal `id`'s view to block `block`'s first row, or to its last rows.
    pub(crate) fn scroll_to_block(&mut self, id: &str, block: u64, top: bool) {
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let Some(rows) = term.blocks.block(block).and_then(Block::rows) else {
            return;
        };
        let screen = u32::from(term.vt.size().1);
        let first = if top {
            rows.prompt
        } else {
            rows.prompt.max(rows.end.saturating_sub(screen))
        };
        Terminals::scroll_to_row(term, first);
        self.undrawn = true;
    }

    /// Scrolls the view so screen row `row` is its top row, or as near as the history allows.
    pub(super) fn scroll_to_row(term: &mut Term, row: u32) {
        let history = term.vt.history();
        term.vt.scroll_back_to(history.saturating_sub(row));
        Terminals::hover_moved(term);
    }

    /// The selected block's wash and outline over the rows of it the view shows.
    pub(super) fn block_overlays(term: &mut Term) -> Vec<Overlay> {
        if term.vt.mouse_modes().alternate_screen {
            return Vec::new();
        }
        let Some(rows) = term
            .view
            .block
            .and_then(|id| term.blocks.block(id))
            .and_then(Block::rows)
        else {
            return Vec::new();
        };
        let (cols, screen) = term.vt.size();
        let top = term.vt.view_top();
        let (first, last) = (rows.prompt, rows.end.saturating_sub(1));
        let shown = top.max(first)..=(top + u32::from(screen) - 1).min(last);
        if shown.is_empty() {
            return Vec::new();
        }
        let spans = first <= top && last >= top + u32::from(screen) - 1;
        shown
            .flat_map(|row| {
                let at = (row - top) as u16;
                let wash = (!spans).then_some(Overlay {
                    row: at,
                    cols: (0, cols),
                    color: BLOCK,
                    alpha: BLOCK_WASH,
                    kind: OverlayKind::Wash,
                });
                let outline = Overlay {
                    row: at,
                    cols: (0, cols),
                    color: BLOCK,
                    alpha: BLOCK_OUTLINE,
                    kind: OverlayKind::Outline {
                        top: row == first,
                        bottom: row == last,
                    },
                };
                wash.into_iter().chain([outline])
            })
            .collect()
    }
}

/// A second mark on the cell `at` is on.
fn again(vt: &mut Vt, at: &Mark) -> Option<Mark> {
    let (row, col) = at.point()?;
    vt.mark(row, col)
}
