use std::ops::Range;

use rune::{TextEdit, Tx};

use super::Terminals;
use super::blocks::Part;

/// One terminal's block filter: the block whose output it filters, what it looks for, and the
/// output lines that hold it.
#[derive(Default)]
pub(crate) struct Filtering {
    pub(crate) query: TextEdit,
    open: bool,
    pub(crate) case: bool,
    block: u64,
    lines: Vec<Filtered>,
}

/// An output line that holds the query: its place in the block's output, its text, and the
/// byte ranges of each match.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct Filtered {
    pub(crate) offset: u32,
    pub(crate) text: String,
    pub(crate) matches: Vec<Range<usize>>,
}

impl Filtering {
    /// The output lines that hold the query, or None while the query is empty.
    pub(crate) fn lines(&self) -> Option<&[Filtered]> {
        (!self.query.text.is_empty()).then_some(self.lines.as_slice())
    }

    /// What the bar's count reads: how many lines hold the query.
    pub(crate) fn count(&self) -> String {
        match self.lines.len() {
            1 => "1 line".into(),
            n => format!("{n} lines"),
        }
    }
}

/// One open filter as the view draws it, for the view to tell when one changed.
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct ShownFilter {
    id: String,
    query: String,
    caret: (usize, usize),
    focused: bool,
    case: bool,
    lines: Vec<Filtered>,
    results_height: u32,
}

/// The share of the terminal's height the filtered lines take at most before they scroll, as
/// the Tauri panel caps them at 40% of the window's.
const RESULTS_SHARE: f32 = 0.4;

impl Terminals {
    /// Opens terminal `id`'s filter over block `block`'s output, empty, in place of its find bar.
    pub(crate) fn open_filter(&mut self, id: &str, block: u64, tx: &mut Tx) {
        if !self.panes.contains_key(id) {
            return;
        }
        if self.finding(id).is_some() {
            self.close_find(id, tx);
        }
        let mut query = TextEdit::default();
        query.focused = true;
        self.filters.insert(
            id.to_owned(),
            Filtering {
                query,
                open: true,
                case: false,
                block,
                lines: Vec::new(),
            },
        );
        tx.focus(filter_field_key(id), false);
        self.undrawn = true;
    }

    /// Terminal `id`'s filter while it is open.
    pub(crate) fn filtering(&self, id: &str) -> Option<&Filtering> {
        self.filters.get(id).filter(|f| f.open)
    }

    /// What the open filters show.
    pub(crate) fn filter_panels(&self) -> Vec<ShownFilter> {
        let mut panels: Vec<ShownFilter> = self
            .filters
            .iter()
            .filter(|(_, f)| f.open)
            .map(|(id, f)| ShownFilter {
                id: id.clone(),
                query: f.query.text.clone(),
                caret: (f.query.anchor, f.query.focus),
                focused: f.query.focused,
                case: f.case,
                lines: f.lines.clone(),
                results_height: self.filter_results_height(id) as u32,
            })
            .collect();
        panels.sort_by(|a, b| a.id.cmp(&b.id));
        panels
    }

    /// How tall terminal `id`'s filtered lines grow before they scroll, in points.
    pub(crate) fn filter_results_height(&self, id: &str) -> f32 {
        let height = self
            .panes
            .get(id)
            .and_then(|term| term.slot)
            .map_or(0.0, |((_, h), _)| h);
        height * RESULTS_SHARE
    }

    /// The text the filter field of terminal `id` edits.
    pub(crate) fn filter_query(&mut self, id: &str) -> &mut TextEdit {
        &mut self.filters.entry(id.to_owned()).or_default().query
    }

    /// The query changed: the block's output is filtered again.
    pub(crate) fn filter_edited(&mut self, id: &str) {
        self.filter_again(id);
    }

    /// Match case turned on or off.
    pub(crate) fn toggle_filter_case(&mut self, id: &str) {
        if let Some(filtering) = self.filters.get_mut(id) {
            filtering.case = !filtering.case;
            filtering.query.focused = true;
        }
        self.filter_again(id);
    }

    /// Closes terminal `id`'s filter and gives typing back to the terminal.
    pub(crate) fn close_filter(&mut self, id: &str, tx: &mut Tx) {
        if self.filters.remove(id).is_some_and(|f| f.open) {
            self.undrawn = true;
            tx.blur();
        }
    }

    /// Scrolls terminal `id`'s view so the filtered output line `offset` is its top row.
    pub(crate) fn scroll_to_filtered(&mut self, id: &str, offset: u32) {
        let Some(block) = self.filtering(id).map(|f| f.block) else {
            return;
        };
        let Some(term) = self.panes.get_mut(id) else {
            return;
        };
        let Some(rows) = term.blocks.block(block).and_then(|b| b.rows()) else {
            return;
        };
        Terminals::scroll_to_row(term, rows.output + offset);
        self.undrawn = true;
    }

    fn filter_again(&mut self, id: &str) {
        let Some((block, query, case)) = self
            .filters
            .get(id)
            .map(|f| (f.block, f.query.text.clone(), f.case))
        else {
            return;
        };
        let output = (!query.is_empty())
            .then(|| self.block_text(id, block, Part::Output))
            .flatten()
            .unwrap_or_default();
        if let Some(filtering) = self.filters.get_mut(id) {
            filtering.lines = filter_lines(&output, &query, case);
        }
        self.undrawn = true;
    }
}

/// The field key of terminal `id`'s filter.
pub(crate) fn filter_field_key(id: &str) -> String {
    format!("terminal.{id}.filter.field")
}

/// The output lines that hold `query`, each with its matches, as the Tauri terminal filters a
/// block: without Match case, letters match whatever their case.
fn filter_lines(output: &str, query: &str, case: bool) -> Vec<Filtered> {
    if query.is_empty() {
        return Vec::new();
    }
    output
        .split('\n')
        .enumerate()
        .filter_map(|(offset, line)| {
            let matches = matches_in(line, query, case);
            (!matches.is_empty()).then(|| Filtered {
                offset: offset as u32,
                text: line.to_owned(),
                matches,
            })
        })
        .collect()
}

/// Each match of `query` in `line` as a byte range of `line`, matches not overlapping.
fn matches_in(line: &str, query: &str, case: bool) -> Vec<Range<usize>> {
    let fold = |text: &str| -> Vec<(char, Range<usize>)> {
        text.char_indices()
            .flat_map(|(at, c)| {
                let source = at..at + c.len_utf8();
                let folded: Vec<char> = if case {
                    vec![c]
                } else {
                    c.to_lowercase().collect()
                };
                folded.into_iter().map(move |f| (f, source.clone()))
            })
            .collect()
    };
    let hay = fold(line);
    let needle: Vec<char> = fold(query).into_iter().map(|(c, _)| c).collect();
    let mut matches = Vec::new();
    let mut from = 0;
    while from + needle.len() <= hay.len() {
        let found = hay[from..from + needle.len()]
            .iter()
            .map(|(c, _)| *c)
            .eq(needle.iter().copied());
        if found {
            let last = &hay[from + needle.len() - 1].1;
            matches.push(hay[from].1.start..last.end);
            from += needle.len();
        } else {
            from += 1;
        }
    }
    matches
}
