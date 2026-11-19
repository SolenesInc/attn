use std::ops::RangeInclusive;

use attn_term::{Mark, Vt};
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
    screen: Screen,
    due: Option<f64>,
    /// What changes by `due` is output, not a screen an attach brought back.
    speaks: bool,
}

/// A run of keys the user typed at one place, for a screen reader to tell its echo from
/// output.
pub(super) struct Typing {
    /// The cell the cursor was on when the run began, which moves with its text.
    at: Option<Mark>,
    /// The line's text up to that cell.
    before: String,
    /// What the keys leave after it, with Backspace applied.
    text: String,
    /// A key changed the line some other way: an arrow, Tab, Ctrl+C, or Backspace past the
    /// run's start.
    edited: bool,
    /// Enter ended the run.
    entered: bool,
}

impl Typing {
    fn at_cursor(vt: &mut Vt) -> Typing {
        let (cols, _) = vt.size();
        let (col, row) = vt.cursor();
        let row = vt.history() + u32::from(row);
        let first = (0..=row).rev().find(|&r| !vt.row_continues(r)).unwrap_or(0);
        let mut before: String = (first..row).map(|r| vt.row_text(r, 0..cols)).collect();
        before.push_str(&vt.row_text(row, 0..col));
        Typing {
            at: vt.mark(row, col),
            before,
            text: String::new(),
            edited: false,
            entered: false,
        }
    }

    /// The line on `screen` this run's keys went to, when what it shows there is their echo:
    /// the text the line had before them, then what they typed.
    fn echo_on(&self, screen: &Screen) -> Option<RangeInclusive<usize>> {
        let row = screen.view_row(self.at.as_ref()?.row()?)?;
        let line = screen.line_at(row)?;
        let text = screen.line_text(line.clone());
        let after = text.strip_prefix(&self.before)?;
        (self.edited || after.trim_end() == self.text.trim_end()).then_some(line)
    }
}

/// A terminal's screen as a screen reader hears it.
struct Screen {
    cols: usize,
    /// The screen row the view shows at its top.
    top: u32,
    rows: Vec<String>,
    /// Each row continues the one above it, which wrapped into it.
    continues: Vec<bool>,
    /// The screen row of the cursor.
    cursor: u32,
    /// A mark on the cursor's cell, which moves with the text as output scrolls it.
    anchor: Option<Mark>,
    /// The view's rows that hold the shell's prompts and command lines, or None in a terminal
    /// whose shell does not mark them.
    shell: Option<Vec<bool>>,
    /// What the user typed into the terminal since the screen before.
    typed: Vec<Typing>,
}

impl Screen {
    fn read(term: &mut Term) -> Screen {
        let vt = &mut term.vt;
        let (cols, height) = vt.size();
        let top = vt.view_top();
        let rows = (0..u32::from(height))
            .map(|r| vt.row_text(top + r, 0..cols).trim_end().to_owned())
            .collect();
        let continues = (0..u32::from(height))
            .map(|r| vt.row_continues(top + r))
            .collect();
        let (col, row) = vt.cursor();
        let cursor = vt.history() + u32::from(row);
        let mut screen = Screen {
            cols: usize::from(cols),
            top,
            rows,
            continues,
            cursor,
            anchor: vt.mark(cursor, col),
            shell: None,
            typed: std::mem::take(&mut term.typed),
        };
        screen.shell = screen.shell_rows(term);
        screen
    }

    /// The view row of screen row `row`, while the view shows it.
    fn view_row(&self, row: u32) -> Option<usize> {
        let row = usize::try_from(row.checked_sub(self.top)?).ok()?;
        (row < self.rows.len()).then_some(row)
    }

    /// The view rows of the line on view row `row`, with the rows it wrapped across.
    fn line_at(&self, row: usize) -> Option<RangeInclusive<usize>> {
        if row >= self.rows.len() {
            return None;
        }
        let first = (0..=row).rev().find(|&r| !self.continues[r]).unwrap_or(0);
        let last = (row + 1..self.rows.len())
            .take_while(|&r| self.continues[r])
            .last()
            .unwrap_or(row);
        Some(first..=last)
    }

    fn cursor_line(&self) -> Option<RangeInclusive<usize>> {
        self.line_at(self.view_row(self.cursor)?)
    }

    /// The text of the line on view rows `line`, its wrapped rows joined.
    fn line_text(&self, line: RangeInclusive<usize>) -> String {
        let last = *line.end();
        line.map(|row| match row == last {
            true => self.rows[row].clone(),
            false => format!("{:<width$}", self.rows[row], width = self.cols),
        })
        .collect()
    }

    /// The view's rows that hold a prompt and the command typed at it, from the shell's marks:
    /// a command's rows end where its output starts, and the command being typed ends with the
    /// cursor's line. The marks are the primary screen's, so a program on the alternate screen
    /// has none.
    fn shell_rows(&self, term: &mut Term) -> Option<Vec<bool>> {
        let blocks = &term.blocks;
        if blocks.pending().is_none() && blocks.blocks().is_empty() {
            return None;
        }
        let mut shell = vec![false; self.rows.len()];
        if term.vt.mouse_modes().alternate_screen {
            return Some(shell);
        }
        let mut commands: Vec<(u32, Option<u32>)> = Vec::new();
        for block in blocks.blocks().iter().rev() {
            let Some(rows) = block.rows() else { break };
            if rows.end < self.top {
                break;
            }
            commands.push((rows.prompt, rows.output.checked_sub(1)));
        }
        if let Some(pending) = blocks.pending()
            && let Some(prompt) = pending.prompt.row()
        {
            let end = match &pending.output {
                Some(output) => output.row().and_then(|row| row.checked_sub(1)),
                None => self.cursor_line().map(|line| self.top + *line.end() as u32),
            };
            commands.push((prompt, end));
        }
        for (prompt, end) in commands {
            for row in prompt..=end.unwrap_or(prompt).max(prompt) {
                if let Some(view) = self.view_row(row) {
                    shell[view] = true;
                }
            }
        }
        Some(shell)
    }
}

impl Terminals {
    /// Once output to the terminal holding the keys pauses, has a screen reader say the rows it
    /// changed, leaving out the shell's prompt and command line and what the user typed, which
    /// the reader echoes itself.
    pub(super) fn speak_quiet_output(&mut self, typing: Option<&str>, tx: &mut Tx) {
        let holder = typing.filter(|_| tx.assistive_technology() && !self.in_background);
        let Some((id, term)) = holder.and_then(|id| self.panes.get_mut(id).map(|t| (id, t))) else {
            self.heard = None;
            return;
        };
        let heard = match &mut self.heard {
            Some(heard) if heard.terminal == id => heard,
            _ => {
                self.heard = Some(Heard {
                    terminal: id.to_owned(),
                    watermark: term.watermark,
                    screen: Screen::read(term),
                    due: None,
                    speaks: false,
                });
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
            if !Terminals::grid_follows_screen(term) {
                heard.due = Some(now + QUIET_SECONDS);
                tx.wake_at(now + QUIET_SECONDS);
                return;
            }
            let screen = Screen::read(term);
            let new = new_rows(&heard.screen, &screen);
            if heard.speaks && !new.is_empty() {
                tx.announce(new.join("\n"), LiveRegion::Polite);
            }
            heard.screen = screen;
            heard.due = None;
        }
    }

    /// The user typed `data` into terminal `id`, which a screen reader tells from output.
    pub(super) fn heard_typed(&mut self, id: &str, data: &[u8]) {
        if !self.heard.as_ref().is_some_and(|h| h.terminal == id) {
            return;
        }
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let text = String::from_utf8_lossy(data)
            .replace("\x1b[200~", "")
            .replace("\x1b[201~", "");
        let run = |term: &mut Term| -> usize {
            if term.typed.last().is_none_or(|run| run.entered) {
                term.typed.push(Typing::at_cursor(&mut term.vt));
            }
            term.typed.len() - 1
        };
        if text.contains('\x1b') {
            let open = run(term);
            term.typed[open].edited = true;
            return;
        }
        for c in text.chars() {
            match c {
                '\r' | '\n' => {
                    if let Some(open) = term.typed.last_mut() {
                        open.entered = true;
                    }
                }
                '\x7f' | '\x08' => {
                    let open = run(term);
                    let typing = &mut term.typed[open];
                    typing.edited |= typing.text.pop().is_none();
                }
                c if c.is_control() => {
                    let open = run(term);
                    term.typed[open].edited = true;
                }
                c => {
                    let open = run(term);
                    term.typed[open].text.push(c);
                }
            }
        }
    }

    /// An attach brought back terminal `id`'s screen, which a screen reader does not hear. When
    /// the attach `resumes`, restoring a snapshot or continuing the stream the window drew, the
    /// output after it is spoken; otherwise the screen comes back as the output that follows it,
    /// kept quiet until it pauses.
    pub(super) fn restored(&mut self, id: &str, resumes: bool, tx: &mut Tx) {
        let Some(heard) = self.heard.as_mut().filter(|h| h.terminal == id) else {
            return;
        };
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        if resumes {
            heard.screen = Screen::read(term);
            heard.watermark = term.watermark;
            heard.due = None;
        } else {
            let due = tx.clock() + QUIET_SECONDS;
            heard.speaks = false;
            heard.due = Some(due);
            tx.wake_at(due);
        }
    }

    /// The window came to the front or went behind another. A screen reader hears only the
    /// window in front, and nothing still pending of one that went behind.
    pub(super) fn window_in_front(&mut self, in_front: bool) {
        self.in_background = !in_front;
        if !in_front {
            self.heard = None;
        }
    }
}

/// The rows of `after` that `before` did not show, following the cursor's text however far
/// output scrolled it. Blank rows are left out, and so are the echo of what the user typed and
/// the shell's prompts and command lines; without shell marks, so are the lines the cursor was
/// and is on.
fn new_rows<'a>(before: &Screen, after: &'a Screen) -> Vec<&'a str> {
    let moved = before
        .anchor
        .as_ref()
        .and_then(Mark::row)
        .map(|now| i64::from(now) - i64::from(before.cursor));
    let to_after = |row: usize| -> Option<usize> {
        let screen_row = i64::from(before.top) + row as i64 + moved?;
        after.view_row(u32::try_from(screen_row).ok()?)
    };
    let mut shown = vec![None; after.rows.len()];
    for (row, text) in before.rows.iter().enumerate() {
        if let Some(now) = to_after(row) {
            shown[now] = Some(text.as_str());
        }
    }
    let mut quiet = after
        .shell
        .clone()
        .unwrap_or_else(|| vec![false; after.rows.len()]);
    let echoes = after.typed.iter().filter_map(|run| run.echo_on(after));
    echoes.flatten().for_each(|row| quiet[row] = true);
    if after.shell.is_none() {
        let was_on = before
            .cursor_line()
            .into_iter()
            .flatten()
            .filter_map(to_after);
        let grown = before
            .view_row(before.cursor)
            .and_then(to_after)
            .and_then(|row| after.line_at(row));
        was_on
            .chain(grown.into_iter().flatten())
            .chain(after.cursor_line().into_iter().flatten())
            .for_each(|row| quiet[row] = true);
    }
    (0..after.rows.len())
        .filter(|&row| !after.rows[row].is_empty() && !quiet[row])
        .filter(|&row| shown[row] != Some(after.rows[row].as_str()))
        .map(|row| after.rows[row].as_str())
        .collect()
}
