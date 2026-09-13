//! Scrollback search: a plain-text snapshot of a terminal, one line per row from the top of the
//! scrollback down, and the hits a query lands on it. Rows count in the same space the viewport
//! scrolls by, so a hit's row goes straight into `ScrollViewport::Row`.
use anyhow::Result;
use libghostty_vt::fmt::{Format, Formatter, FormatterOptions};
use libghostty_vt::terminal::Terminal;

/// One match: the row from the top of the scrollback, the cell column, and the width in cells.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Hit {
    pub row: usize,
    pub col: u16,
    pub len: u16,
}

/// The terminal's text at one moment, kept beside a lowercased copy for case-insensitive queries.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Haystack {
    text: String,
    lower: String,
}

impl Haystack {
    pub fn snapshot(terminal: &Terminal<'_, '_>) -> Result<Self> {
        let options = FormatterOptions::new().with_format(Format::Plain).with_unwrap(false).with_trim(true);
        let mut formatter = Formatter::new(terminal, options)?;
        let bytes = formatter.format_alloc(None)?;
        Ok(Self::from_text(String::from_utf8_lossy(&bytes).into_owned()))
    }

    pub fn from_text(text: String) -> Self {
        let lower = text.to_lowercase();
        Self { text, lower }
    }

    pub fn rows(&self) -> usize {
        self.text.lines().count()
    }

    pub fn bytes(&self) -> usize {
        self.text.len()
    }

    /// Every place the query occurs, top to bottom. A query with no uppercase letter ignores case.
    pub fn find(&self, query: &str) -> Vec<Hit> {
        if query.is_empty() {
            return Vec::new();
        }
        let smart = !query.chars().any(char::is_uppercase);
        let (haystack, needle) = if smart { (&self.lower, query.to_lowercase()) } else { (&self.text, query.to_string()) };
        let len = query.chars().count() as u16;
        let mut hits = Vec::new();
        for (row, line) in haystack.lines().enumerate() {
            let mut from = 0;
            while let Some(offset) = line[from..].find(&needle) {
                let at = from + offset;
                hits.push(Hit { row, col: line[..at].chars().count() as u16, len });
                from = at + needle.len().max(1);
            }
        }
        hits
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hits_carry_the_row_and_column_they_landed_on() {
        let hay = Haystack::from_text("one\ntwo\n\nfour\nfive Five five\n".to_string());
        assert_eq!(hay.find("four"), vec![Hit { row: 3, col: 0, len: 4 }]);
        assert_eq!(
            hay.find("five"),
            vec![Hit { row: 4, col: 0, len: 4 }, Hit { row: 4, col: 5, len: 4 }, Hit { row: 4, col: 10, len: 4 }]
        );
        assert_eq!(hay.find("Five"), vec![Hit { row: 4, col: 5, len: 4 }]);
        assert!(hay.find("").is_empty());
        assert!(hay.find("six").is_empty());
    }

    #[test]
    fn a_snapshot_has_one_line_per_row_from_the_top_of_the_scrollback() {
        let mut terminal = Terminal::new(12, 3).unwrap();
        terminal.vt_write(b"alpha\r\nbeta\r\n\r\ndelta\r\nepsilon END\r\n");
        let hay = Haystack::snapshot(&terminal).unwrap();
        assert_eq!(terminal.scrollback_rows().unwrap(), 3, "six rows written into a three-row screen");
        assert_eq!(hay.find("delta"), vec![Hit { row: 3, col: 0, len: 5 }], "the blank row keeps its place");
        assert_eq!(hay.find("END"), vec![Hit { row: 4, col: 8, len: 3 }]);
    }
}
