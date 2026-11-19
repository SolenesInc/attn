use std::cmp::Reverse;
use std::ops::RangeInclusive;

use rune::{LiveRegion, Tx};

use super::{Term, Terminals};

/// How long the terminal holding the keys stays quiet before a screen reader hears its new
/// rows. A quick command's output and its next prompt arrive with gaps of at most 60 ms
/// (`ls`, `git status`, `git log`, `date` in `zsh -i` and `zsh -f`).
const QUIET_SECONDS: f64 = 0.3;

/// The screen a screen reader last heard of the terminal that holds the keys.
pub(super) struct Heard {
    terminal: String,
    watermark: Option<u32>,
    typed: u32,
    rows: Vec<String>,
    /// Each row continues the one above it, which wrapped into it.
    continues: Vec<bool>,
    cursor: Option<Cursor>,
    due: Option<f64>,
    /// What changes by `due` is output, not a screen an attach brought back.
    speaks: bool,
}

#[derive(Clone, Copy)]
struct Cursor {
    row: usize,
    /// The shell marked a command as running, so the cursor sits in that program's output
    /// rather than in the shell's prompt or command line.
    in_program: bool,
}

impl Heard {
    fn now(terminal: &str, term: &mut Term) -> Heard {
        let top = term.vt.view_top();
        let rows = term.grid.text_lines();
        let continues = (0..rows.len())
            .map(|row| term.vt.row_continues(top + row as u32))
            .collect();
        let in_program = term
            .blocks
            .pending()
            .is_some_and(|command| command.output.is_some());
        Heard {
            terminal: terminal.to_owned(),
            watermark: term.watermark,
            typed: term.typed,
            rows,
            continues,
            cursor: term.grid.cursor.as_ref().map(|c| Cursor {
                row: usize::from(c.row),
                in_program,
            }),
            due: None,
            speaks: false,
        }
    }

    /// The rows of the line the cursor is on, with the rows it wrapped across.
    fn cursor_line(&self) -> Option<RangeInclusive<usize>> {
        let row = self.cursor?.row.min(self.rows.len().checked_sub(1)?);
        let continues = |r: usize| self.continues.get(r).copied().unwrap_or(false);
        let first = (0..=row).rev().find(|&r| !continues(r)).unwrap_or(0);
        let last = (row + 1..self.rows.len())
            .take_while(|&r| continues(r))
            .last()
            .unwrap_or(row);
        Some(first..=last)
    }
}

impl Terminals {
    /// Once output to the terminal holding the keys pauses, has a screen reader say the rows it
    /// changed, leaving out the line being typed on, which the reader echoes itself.
    pub(super) fn speak_quiet_output(&mut self, typing: Option<&str>, tx: &mut Tx) {
        let holder = typing.filter(|_| tx.assistive_technology());
        let Some((id, term)) = holder.and_then(|id| self.panes.get_mut(id).map(|t| (id, t))) else {
            self.heard = None;
            return;
        };
        let heard = match &mut self.heard {
            Some(heard) if heard.terminal == id => heard,
            _ => {
                self.heard = Some(Heard::now(id, term));
                return;
            }
        };
        let now = tx.clock();
        if term.watermark != heard.watermark {
            heard.speaks |= heard.due.is_none();
            heard.watermark = term.watermark;
            heard.due = Some(now + QUIET_SECONDS);
            tx.wake_at(now + QUIET_SECONDS);
        } else if heard.due.is_some_and(|due| now >= due) {
            let screen = Heard::now(id, term);
            let new = new_rows(heard, &screen);
            if heard.speaks && !new.is_empty() {
                tx.announce(new.join("\n"), LiveRegion::Polite);
            }
            *heard = screen;
        }
    }

    /// The screen an attach brings back to `id`, and what follows it before the quiet, is not
    /// spoken.
    pub(super) fn restoring(&mut self, id: &str, tx: &mut Tx) {
        if let Some(heard) = self.heard.as_mut().filter(|h| h.terminal == id) {
            let due = tx.clock() + QUIET_SECONDS;
            heard.speaks = false;
            heard.due = Some(due);
            tx.wake_at(due);
        }
    }
}

/// The rows of `after` that `before` did not show, matching rows across however far output
/// scrolled the screen. Blank rows are left out, and so is the line being typed on: the cursor's
/// line before, unless it sat in a running program's output and nothing was typed since, and
/// the cursor's line after, unless it sits in a program's output on a line nothing was typed on.
fn new_rows<'a>(before: &Heard, after: &'a Heard) -> Vec<&'a str> {
    let same = |row: usize, scrolled: usize| {
        before
            .rows
            .get(row + scrolled)
            .is_some_and(|was| *was == after.rows[row])
    };
    let scrolled = (0..before.rows.len().max(1))
        .max_by_key(|&k| {
            let matched = (0..after.rows.len())
                .filter(|&row| !after.rows[row].is_empty() && same(row, k))
                .count();
            (matched, Reverse(k))
        })
        .unwrap_or(0);
    let typed_since = after.typed != before.typed;
    let typed_before = before
        .cursor
        .filter(|c| !c.in_program || typed_since)
        .and_then(|_| before.cursor_line());
    let was_typed = |row: usize| {
        typed_before
            .as_ref()
            .is_some_and(|l| l.contains(&(row + scrolled)))
    };
    let typed_after = after
        .cursor_line()
        .filter(|line| after.cursor.is_some_and(|c| !c.in_program) || line.clone().any(was_typed));
    (0..after.rows.len())
        .filter(|&row| !after.rows[row].is_empty() && !same(row, scrolled))
        .filter(|&row| !was_typed(row) && !typed_after.as_ref().is_some_and(|l| l.contains(&row)))
        .map(|row| after.rows[row].as_str())
        .collect()
}
