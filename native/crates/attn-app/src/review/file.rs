//! The file view: a whole file of the review's repository, now, with the change from base
//! drawn in, the stop's lines marked and the agent's notes on their lines, read on the machine
//! that holds the review.

use std::rc::Rc;

use attn_protocol::{ReviewFileMessage, ReviewFileResultMessage, ReviewNote, ReviewRow};
use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::{radius, space};
use rune::{Color, El, GrowthSpec, Live, ScrollPos, ScrollSpec, TextStyle, Tx, col, row, text};

use super::stop::{Colors, code_row};
use crate::State;

/// Rows a page key moves.
pub(super) const PAGE_ROWS: usize = 20;
const ROW_HEIGHT: f32 = 20.0;

/// What a file view opens: a file, the line to put the cursor on, and the lines of the stop it
/// came from.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub(crate) struct Target {
    pub(crate) path: String,
    pub(crate) line: Option<i64>,
    pub(crate) range: Option<(i64, i64)>,
    /// A definition outside the review's repository, shown read-only.
    pub(crate) outside: bool,
}

/// A file open in the view, as the daemon last read it.
#[derive(Clone, Debug, Default)]
pub(crate) struct OpenFile {
    pub(crate) id: u64,
    pub(crate) target: Target,
    pub(crate) rows: Option<Rc<[ReviewRow]>>,
    pub(crate) hash: String,
    pub(crate) error: Option<String>,
    pub(crate) problem: Option<String>,
    pub(crate) cursor: usize,
    /// The other end of a selection that ends at the cursor.
    pub(crate) select_from: Option<usize>,
    /// The latest read asked for it; an answer to an older one is dropped.
    pub(crate) asked: u64,
    /// The line now an edit made in this view changed first, where its next read puts the
    /// cursor.
    pub(crate) edited: Option<i64>,
}

impl OpenFile {
    pub(crate) fn line(&self) -> Option<i64> {
        self.rows.as_ref()?.get(self.cursor)?.now
    }

    /// Puts the cursor on row `row`, keeping the other end of the selection when `selecting`,
    /// and shows it in the view keyed under `prefix`.
    pub(crate) fn put_cursor(&mut self, row: usize, selecting: bool, prefix: &str, tx: &mut Tx) {
        self.select_from = if selecting {
            Some(self.select_from.unwrap_or(self.cursor))
        } else {
            None
        };
        self.cursor = row;
        tx.scroll_into_view(row_key(prefix, row));
    }

    pub(crate) fn selected(&self) -> (usize, usize) {
        let from = self.select_from.unwrap_or(self.cursor);
        (from.min(self.cursor), from.max(self.cursor))
    }
}

/// How far the file view's lines are scrolled sideways and how wide their viewport is, as the
/// viewport last reported, for ⇧←/⇧→ to scroll from.
pub(crate) struct Sideways(Live<[f32; 2]>);

impl Default for Sideways {
    fn default() -> Self {
        Sideways(Live::new([0.0, 0.0]))
    }
}

impl Sideways {
    pub(super) fn offset(&self) -> f32 {
        self.0.get()[0]
    }

    pub(super) fn half_view(&self) -> f32 {
        self.0.get()[1] / 2.0
    }
}

/// The row of `rows` at line `line` now, else the first after it, else the last.
pub(crate) fn row_index_of(rows: &[ReviewRow], line: Option<i64>) -> usize {
    let Some(line) = line else { return 0 };
    rows.iter()
        .position(|r| r.now == Some(line))
        .or_else(|| rows.iter().position(|r| r.now.is_some_and(|n| n > line)))
        .unwrap_or(rows.len().saturating_sub(1))
}

/// The row of a file's new `rows` that the cursor stood on in `before`. A row that exists at
/// head is found by its head line, one that exists at base by its base line, which an edit of
/// the file never moves. An added row is found among the added rows after the nearest base row
/// above it, by its text nearest where it stood in that run, else by its place in it.
pub(crate) fn row_index_after_refresh(rows: &[ReviewRow], before: &OpenFile) -> usize {
    let Some(old) = before.rows.as_deref() else {
        return row_index_of(rows, before.target.line);
    };
    let Some(was) = old.get(before.cursor) else {
        return row_index_of(rows, before.target.line);
    };
    if let Some(head) = was.head
        && let Some(same) = rows.iter().position(|r| r.head == Some(head))
    {
        return same;
    }
    if let Some(base) = was.old {
        return rows
            .iter()
            .position(|r| r.old == Some(base))
            .unwrap_or_else(|| row_index_of(rows, was.now));
    }
    let anchor = (0..before.cursor).rev().find(|&i| old[i].old.is_some());
    let new_anchor = match anchor {
        Some(a) => match rows.iter().position(|r| r.old == old[a].old) {
            Some(at) => Some(at),
            None => return row_index_of(rows, was.now),
        },
        None => None,
    };
    let start = new_anchor.map_or(0, |a| a + 1);
    let end = (start..rows.len())
        .find(|&i| rows[i].old.is_some())
        .unwrap_or(rows.len());
    if start >= end {
        return row_index_of(rows, was.now);
    }
    let expected = (before.cursor - anchor.map_or(0, |a| a + 1)).min(end - start - 1);
    let nearest = (start..end)
        .filter(|&i| rows[i].text == was.text)
        .min_by_key(|&i| (i - start).abs_diff(expected));
    nearest.unwrap_or(start + expected)
}

/// The first row of each run of changed rows.
pub(crate) fn change_starts(rows: &[ReviewRow]) -> Vec<usize> {
    (0..rows.len())
        .filter(|&i| rows[i].kind != " " && (i == 0 || rows[i - 1].kind == " "))
        .collect()
}

/// The lines agent notes point at, and whether any of them is the place to look.
fn noted(notes: &[&ReviewNote], line: Option<i64>) -> Option<bool> {
    let line = line?;
    notes
        .iter()
        .filter(|n| n.now_from > 0 && line >= n.now_from && line <= n.now_to)
        .map(|n| n.look)
        .reduce(|a, b| a || b)
}

/// Which of `rows` stand inside `range`, lines now: a removed row stands inside when a line
/// next to it does.
fn rows_inside(rows: &[ReviewRow], (from, to): (i64, i64)) -> Vec<bool> {
    let inside = |line: Option<i64>| line.is_some_and(|l| l >= from && l <= to);
    let mut result: Vec<bool> = rows.iter().map(|r| inside(r.now)).collect();
    let mut previous = None;
    for (i, r) in rows.iter().enumerate() {
        if r.now.is_some() {
            previous = r.now;
        } else if inside(previous) {
            result[i] = true;
        }
    }
    let mut next = None;
    for (i, r) in rows.iter().enumerate().rev() {
        if r.now.is_some() {
            next = r.now;
        } else if inside(next) {
            result[i] = true;
        }
    }
    result
}

/// How many columns `text` takes, with tabs to the next multiple of 8.
fn columns(text: &str) -> usize {
    text.chars()
        .fold(0, |at, c| if c == '\t' { at + 8 - at % 8 } else { at + 1 })
}

pub(super) fn body_key(prefix: &str) -> String {
    format!("{prefix}.view.body")
}

pub(super) fn row_key(prefix: &str, row: usize) -> String {
    format!("{prefix}.view.row.{row}")
}

/// Asks the daemon for file `path` of the review `document` on `host`, and runs `then` with
/// the rows, the file's hash and a limit the rows hit, or why there are none.
pub(super) fn read(
    state: &mut State,
    (host, document, path): (Option<String>, String, String),
    then: impl FnOnce(&mut State, Result<ReviewFileResultMessage, String>, &mut Tx) + 'static,
    tx: &mut Tx,
) {
    state.ask(
        |id| ReviewFileMessage {
            host,
            document,
            path,
            request_id: Some(id),
            ..ReviewFileMessage::new()
        },
        move |state, answer, tx| {
            let read = answer.outcome().and_then(|reply| {
                reply
                    .parse::<ReviewFileResultMessage>()
                    .ok_or_else(|| "the daemon's answer did not parse".to_owned())
            });
            then(state, read, tx)
        },
        tx,
    );
}

/// The file view of `file`, the top of the stack of `depth` files, in tile `tile` keyed under
/// `prefix`, with the notes of every stop in its file.
#[allow(clippy::too_many_arguments)]
pub(super) fn view(
    prefix: &str,
    tile: &str,
    file: &OpenFile,
    sideways: &Sideways,
    (depth, stage): (usize, bool),
    (notes, marks): (&[&ReviewNote], Vec<El>),
    (colors, asking): (Option<Rc<Colors>>, &super::stop::Asking),
    t: &Tokens,
) -> El {
    let can_go_back = depth > 1;
    let back = row()
        .role("button")
        .label(if can_go_back {
            "← back ⌃o"
        } else {
            "← back"
        })
        .key(format!("{prefix}.view.back"))
        .gap(4.0)
        .items_center()
        .child(
            text("← back")
                .font(type_::LABEL)
                .color(t.text_muted)
                .a11y_hidden(),
        )
        .child(super::keys::caps("⌃o", t).a11y_hidden());
    let back = super::file_view_press(back, tile, stage, false);
    let lines = file
        .rows
        .as_ref()
        .map(|rows| format!("{} lines", rows.iter().filter(|r| r.now.is_some()).count()));
    let header = row()
        .w_full()
        .items_center()
        .gap(space::S3)
        .px(space::S3)
        .py(space::S1)
        .border_sides([0.0, 0.0, 1.0, 0.0], t.border.alpha(0.5))
        .child(back)
        .child(
            text(file.target.path.clone())
                .font(
                    TextStyle {
                        weight: 400.0,
                        ..type_::LABEL
                    }
                    .mono(),
                )
                .color(t.text_primary),
        )
        .children(file.target.outside.then(|| {
            text("outside the repository · read-only")
                .font(type_::LABEL)
                .color(t.warning)
        }))
        .children(lines.map(|l| text(l).font(type_::LABEL).color(t.text_muted)))
        .child(col().grow(1.0))
        .children((!stage).then(|| {
            let close = row()
                .role("button")
                .label("close esc")
                .key(format!("{prefix}.view.close"))
                .gap(4.0)
                .items_center()
                .child(
                    text("close")
                        .font(type_::LABEL)
                        .color(t.text_muted)
                        .a11y_hidden(),
                )
                .child(super::keys::caps("esc", t).a11y_hidden());
            super::file_view_press(close, tile, stage, true)
        }));
    let problem = file.problem.clone().map(|p| {
        text(p)
            .font(type_::LABEL)
            .color(t.warning)
            .wrap()
            .role("status")
    });
    let body = match (&file.rows, &file.error) {
        (_, Some(error)) => message(error, t.error, t).role("alert"),
        (None, None) => message(
            &format!("Reading {}…", file.target.path),
            t.text_tertiary,
            t,
        ),
        (Some(rows), None) => rows_view(
            prefix,
            tile,
            rows,
            (file, stage),
            notes,
            (colors, asking),
            t,
        )
        .key(body_key(prefix))
        .on_scroll_offset({
            let reported = sideways.0.clone();
            move |at: ScrollPos| {
                reported.set([at.offset[0], at.viewport[0]]);
            }
        }),
    };
    let line = file.line();
    let strip: Vec<El> = notes
        .iter()
        .filter(|n| line.is_some_and(|l| n.now_from > 0 && l >= n.now_from && l <= n.now_to))
        .map(|n| super::stop::note(n, t))
        .chain(marks)
        .collect();
    let strip = (!strip.is_empty()).then(|| {
        col()
            .key(format!("{prefix}.view.marks"))
            .role("region")
            .label("Marks on this line")
            .w_full()
            .padding(space::S2)
            .gap(space::S1)
            .border_sides([1.0, 0.0, 0.0, 0.0], t.border.alpha(0.5))
            .children(strip)
    });
    col()
        .key(format!("{prefix}.view"))
        .role("region")
        .label("File view")
        .h_full()
        .fill(t.bg_tile)
        .border_sides([0.0, 0.0, 0.0, 1.0], t.border)
        .child(header)
        .children(problem)
        .child(body)
        .children(strip)
}

fn message(text_: &str, color: Color, t: &Tokens) -> El {
    let _ = t;
    col()
        .w_full()
        .grow(1.0)
        .padding(space::S3)
        .child(text(text_.to_owned()).font(type_::BODY).color(color).wrap())
}

fn rows_view(
    prefix: &str,
    tile: &str,
    rows: &Rc<[ReviewRow]>,
    (file, stage): (&OpenFile, bool),
    notes: &[&ReviewNote],
    (colors, asking): (Option<Rc<Colors>>, &super::stop::Asking),
    t: &Tokens,
) -> El {
    let asking = asking.clone();
    let digits = rows
        .iter()
        .flat_map(|r| [r.old, r.now])
        .flatten()
        .max()
        .unwrap_or(1)
        .to_string()
        .len();
    let range = file.target.range;
    let inside: Option<Rc<[bool]>> = stage
        .then(|| range.map(|r| rows_inside(rows, r).into()))
        .flatten();
    let (from, to) = file.selected();
    let selecting = file.select_from.is_some();
    let cursor = file.cursor;
    let notes: Vec<ReviewNote> = notes.iter().map(|n| (*n).clone()).collect();
    let tokens = *t;
    let prefix_ = prefix.to_owned();
    let (tile_, id) = (tile.to_owned(), file.id);
    let rows_ = rows.clone();
    let widest = rows.iter().map(|r| columns(&r.text)).max().unwrap_or(0);
    let char_w = 0.6 * 13.0;
    let gutter = 2.0 * (digits as f32 * char_w + 12.0).ceil() + 14.0 + 2.0 * space::S2;
    let list = col()
        .key(format!("{prefix}.view.rows"))
        .w_full()
        .min_w(gutter + widest as f32 * char_w + space::S3)
        .growth(GrowthSpec::default())
        .virtual_rows(
            rows.len(),
            ROW_HEIGHT,
            {
                let prefix = prefix.to_owned();
                move |_: &State, i| row_key(&prefix, i).into()
            },
            move |_: &State, i| {
                let r = &rows_[i];
                let spans = colors
                    .as_ref()
                    .map(|c| c.spans(i, &tokens))
                    .unwrap_or_default();
                let in_range = range.is_some_and(|(a, b)| r.now.is_some_and(|n| n >= a && n <= b));
                let keyed = row_key(&prefix_, i);
                let marked = (
                    asking.labels_at(&keyed),
                    asking.asked.as_deref() == Some(keyed.as_str()),
                );
                let line = code_row(r, &spans, digits, in_range, false, marked, &tokens).key(keyed);
                let line = match noted(&notes.iter().collect::<Vec<_>>(), r.now) {
                    Some(true) => line.border_sides([0.0, 0.0, 0.0, 3.0], tokens.warning),
                    Some(false) => line.border_sides([0.0, 0.0, 0.0, 3.0], tokens.text_tertiary),
                    None => line,
                };
                let line = match &inside {
                    Some(inside) if !inside[i] => line.group_opacity(0.45, 0.45),
                    _ => line,
                };
                let line = if selecting && i >= from && i <= to && i != cursor {
                    line.fill(tokens.accent.alpha(0.1))
                } else {
                    line
                };
                let line = super::cursor_frame(line, i == cursor, &tokens);
                super::file_row_press(line, &tile_, id, i)
            },
        )
        .virtual_rows_version((tile.to_owned(), file.id, rows.len()));
    col()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .scroll(ScrollSpec {
            stick_bottom: false,
        })
        .scroll_xy()
        .radius(radius::SM)
        .child(list)
}
