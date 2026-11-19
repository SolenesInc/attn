use std::cmp::Reverse;

use attn_term::Grid;
use rune::{LiveRegion, Tx};

use super::Terminals;

/// How long the terminal holding the keys stays quiet before a screen reader hears its new
/// rows. A quick command's output and its next prompt arrive with gaps of at most 60 ms
/// (`ls`, `git status`, `git log`, `date` in `zsh -i` and `zsh -f`).
const QUIET_SECONDS: f64 = 0.3;

/// The screen a screen reader last heard of the terminal that holds the keys.
pub(super) struct Heard {
    terminal: String,
    watermark: Option<u32>,
    rows: Vec<String>,
    cursor_row: Option<usize>,
    due: Option<f64>,
    /// What changes by `due` is output, not a screen an attach brought back.
    speaks: bool,
}

impl Heard {
    fn now(terminal: &str, watermark: Option<u32>, grid: &Grid) -> Heard {
        Heard {
            terminal: terminal.to_owned(),
            watermark,
            rows: grid.text_lines(),
            cursor_row: cursor_row(grid),
            due: None,
            speaks: false,
        }
    }
}

impl Terminals {
    /// Once output to the terminal holding the keys pauses, has a screen reader say the rows it
    /// changed, leaving out the row being typed on, which the reader echoes itself.
    pub(super) fn speak_quiet_output(&mut self, tx: &mut Tx) {
        let holder = self
            .focused
            .as_deref()
            .filter(|_| tx.assistive_technology());
        let Some((id, term)) = holder.and_then(|id| self.panes.get_key_value(id)) else {
            self.heard = None;
            return;
        };
        let heard = match &mut self.heard {
            Some(heard) if heard.terminal == *id => heard,
            _ => {
                self.heard = Some(Heard::now(id, term.watermark, &term.grid));
                return;
            }
        };
        let now = tx.clock();
        if term.watermark != heard.watermark {
            heard.speaks = heard.watermark.is_some() && (heard.speaks || heard.due.is_none());
            heard.watermark = term.watermark;
            heard.due = Some(now + QUIET_SECONDS);
            tx.wake_at(now + QUIET_SECONDS);
        } else if heard.due.is_some_and(|due| now >= due) {
            let screen = Heard::now(id, term.watermark, &term.grid);
            let new = new_rows(heard, &screen);
            if heard.speaks && !new.is_empty() {
                tx.announce(new.join("\n"), LiveRegion::Polite);
            }
            *heard = screen;
        }
    }
}

fn cursor_row(grid: &Grid) -> Option<usize> {
    grid.cursor.as_ref().map(|c| usize::from(c.row))
}

/// The rows of `after` that `before` did not show, matching rows across however far output
/// scrolled the screen. Blank rows and the rows the cursor was on, before and after, are left
/// out.
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
    (0..after.rows.len())
        .filter(|&row| !after.rows[row].is_empty() && !same(row, scrolled))
        .filter(|&row| Some(row) != after.cursor_row && Some(row + scrolled) != before.cursor_row)
        .map(|row| after.rows[row].as_str())
        .collect()
}
