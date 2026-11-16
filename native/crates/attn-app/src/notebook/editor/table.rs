//! A GFM table shown as a grid, as the Tauri editor's table widget shows it while the
//! selection is outside it (tableWidget.ts): a header, aligned columns and raw cell text.

use std::ops::Range;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Align {
    None,
    Left,
    Right,
    Center,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Table {
    pub(crate) header: Vec<String>,
    pub(crate) align: Vec<Align>,
    pub(crate) rows: Vec<Vec<String>>,
    /// The lines the table spans.
    pub(crate) lines: Range<usize>,
}

/// The cells of a table line: its text between pipes, trimmed, the outer pipes optional. A
/// pipe right after a backslash is text in its cell, as pulldown-cmark reads it.
fn cells(line: &str) -> Vec<String> {
    let line = line.trim();
    let line = line.strip_prefix('|').unwrap_or(line);
    let mut cells = Vec::new();
    let mut start = 0;
    for (i, _) in line.match_indices('|') {
        if !line[..i].ends_with('\\') {
            cells.push(line[start..i].trim().to_owned());
            start = i + 1;
        }
    }
    let last = line[start..].trim();
    if cells.is_empty() || !last.is_empty() {
        cells.push(last.to_owned());
    }
    cells
}

fn delimiter(line: &str) -> Option<Vec<Align>> {
    if !line.contains('-') {
        return None;
    }
    cells(line)
        .iter()
        .map(|c| {
            let dashes = c.trim_matches(':');
            if dashes.is_empty() || !dashes.chars().all(|ch| ch == '-') {
                return None;
            }
            Some(match (c.starts_with(':'), c.ends_with(':')) {
                (true, true) => Align::Center,
                (true, false) => Align::Left,
                (false, true) => Align::Right,
                (false, false) => Align::None,
            })
        })
        .collect()
}

/// The table that starts at line `at`: a line with a pipe, then a delimiter row with as many
/// cells, then every following line with a pipe.
pub(crate) fn table_at(lines: &[String], at: usize) -> Option<Table> {
    let head = lines.get(at).filter(|l| l.contains('|'))?;
    let header = cells(head);
    let align = delimiter(lines.get(at + 1)?)?;
    if align.len() != header.len() {
        return None;
    }
    let end = (at + 2..lines.len())
        .find(|&i| !lines[i].contains('|') || lines[i].trim().is_empty())
        .unwrap_or(lines.len());
    let rows = lines[at + 2..end].iter().map(|l| cells(l)).collect();
    Some(Table {
        header,
        align,
        rows,
        lines: at..end,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn lines(doc: &str) -> Vec<String> {
        doc.split('\n').map(str::to_owned).collect()
    }

    #[test]
    fn a_table_has_its_header_alignment_raw_cells_and_lines() {
        let t = table_at(&lines("| a | b |\n| --- | :-: |\n| 1 | 2 |"), 0).unwrap();
        assert_eq!(t.header, ["a", "b"]);
        assert_eq!(t.align, [Align::None, Align::Center]);
        assert_eq!(t.rows, [["1", "2"]]);
        assert_eq!(t.lines, 0..3);
        let t = table_at(
            &lines("| a | b | c | d |\n| --- | :-- | --: | :-: |\n| 1 | 2 | 3 | 4 |"),
            0,
        )
        .unwrap();
        assert_eq!(
            t.align,
            [Align::None, Align::Left, Align::Right, Align::Center]
        );
        let t = table_at(&lines("| a |\n| --- |\n| **x** |\n\nafter"), 0).unwrap();
        assert_eq!(t.rows, [["**x**"]]);
        assert_eq!(t.lines, 0..3);
    }

    #[test]
    fn a_pipe_after_a_backslash_stays_in_its_cell_as_pulldown_cmark_reads_it() {
        for (row, want) in [
            (r"| a\|b | important |", vec![r"a\|b", "important"]),
            (r"a\|b | important", vec![r"a\|b", "important"]),
            (r"| x\\|y | z |", vec![r"x\\|y", "z"]),
            (r"| `x|y` | z |", vec!["`x", "y`", "z"]),
            (r"| a | b\|", vec!["a", r"b\|"]),
            ("| a | |", vec!["a", ""]),
        ] {
            let t = table_at(&lines(&format!("| h | i |\n| --- | --- |\n{row}")), 0).unwrap();
            assert_eq!(t.rows, [want], "{row:?}");
        }
    }

    #[test]
    fn lines_that_are_not_a_well_formed_table_are_none() {
        for doc in [
            "| a | b |\n| --- |\n| 1 | 2 |",
            "| a |\nno delimiter",
            "plain\n| --- |",
            "| a |",
        ] {
            assert_eq!(table_at(&lines(doc), 0), None, "{doc:?}");
        }
    }
}
