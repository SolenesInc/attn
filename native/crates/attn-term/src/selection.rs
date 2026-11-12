/// A selection over screen rows (history rows first, then the screen's): from `start` up to
/// `end`, whose column is not included. Rows between the ends span the whole width.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Selection {
    pub start: Point,
    pub end: Point,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub struct Point {
    pub row: u32,
    pub col: u16,
}

impl Selection {
    pub fn at(row: u32, start: u16, end: u16) -> Selection {
        Selection {
            start: Point { row, col: start },
            end: Point { row, col: end },
        }
    }

    /// The same selection with `start` before `end`, however it was dragged.
    pub fn ordered(self) -> Selection {
        if self.start <= self.end {
            self
        } else {
            Selection {
                start: self.end,
                end: self.start,
            }
        }
    }

    /// Columns of each row the selection covers, in a grid `cols` wide.
    pub fn rows(self, cols: u16) -> impl Iterator<Item = (u32, std::ops::Range<u16>)> {
        let s = self.ordered();
        (s.start.row..=s.end.row).map(move |row| {
            let start = if row == s.start.row { s.start.col } else { 0 };
            let end = if row == s.end.row { s.end.col } else { cols };
            (row, start.min(cols)..end.min(cols))
        })
    }
}

/// The word under `col` of `line`, as the Tauri terminal picks it on a double click: letters,
/// digits, `_` and `-`.
pub fn word_at(line: &str, col: u16) -> Option<(u16, u16)> {
    let chars: Vec<char> = line.chars().collect();
    let word = |i: usize| {
        chars
            .get(i)
            .is_some_and(|c| c.is_ascii_alphanumeric() || *c == '_' || *c == '-')
    };
    let col = usize::from(col);
    if !word(col) {
        return None;
    }
    let mut start = col;
    while start > 0 && word(start - 1) {
        start -= 1;
    }
    let mut end = col + 1;
    while word(end) {
        end += 1;
    }
    Some((start as u16, end as u16))
}

/// Lines copied out of a terminal, as the Tauri terminal cleans them: trailing blanks go, and
/// the indent every line shares goes too; when the first line starts further left than the
/// rest, the indent the rest share goes from them.
pub fn clean_lines(lines: &[String]) -> Vec<String> {
    let result: Vec<&str> = lines.iter().map(|l| l.trim_end()).collect();
    let indent = |l: &&str| l.chars().take_while(|c| c.is_whitespace()).count();
    let strip = |l: &str, n: usize| -> String {
        if l.is_empty() {
            String::new()
        } else {
            l.chars().skip(n).collect()
        }
    };
    let owned = || result.iter().map(|l| (*l).to_owned()).collect();
    if result.len() <= 1 {
        return owned();
    }
    let Some(overall) = result.iter().filter(|l| !l.is_empty()).map(indent).min() else {
        return owned();
    };
    if overall > 0 {
        return result.iter().map(|l| strip(l, overall)).collect();
    }
    match result[1..]
        .iter()
        .filter(|l| !l.is_empty())
        .map(indent)
        .min()
    {
        Some(rest) if rest > 0 => std::iter::once(result[0].to_owned())
            .chain(result[1..].iter().map(|l| strip(l, rest)))
            .collect(),
        _ => owned(),
    }
}

/// A selection over a program's own screen, kept by the text it covers so it follows the
/// text when the program scrolls it: the Tauri terminal's application selection anchor.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Anchor {
    start_col: u16,
    end_col: u16,
    span: u32,
    /// Each row's text, longest first, with its row after the first and its column.
    markers: Vec<(String, u32, usize)>,
}

impl Anchor {
    /// The anchor for `selection`, whose rows read `line(row)`; None when it covers no text.
    pub fn new(selection: Selection, mut line: impl FnMut(u32) -> String) -> Option<Anchor> {
        let s = selection.ordered();
        let mut markers = Vec::new();
        for row in s.start.row..=s.end.row {
            let line: Vec<char> = line(row).chars().collect();
            let start = if row == s.start.row {
                usize::from(s.start.col)
            } else {
                0
            };
            let end = if row == s.end.row {
                usize::from(s.end.col)
            } else {
                line.len()
            };
            let segment: String = line
                .get(start.min(line.len())..end.min(line.len()).max(start.min(line.len())))
                .unwrap_or_default()
                .iter()
                .collect();
            let text = segment.trim();
            if text.is_empty() {
                continue;
            }
            let lead = segment.chars().take_while(|c| c.is_whitespace()).count();
            markers.push((text.to_owned(), row - s.start.row, start + lead));
        }
        if markers.is_empty() {
            return None;
        }
        markers.sort_by_key(|(text, ..)| std::cmp::Reverse(text.chars().count()));
        Some(Anchor {
            start_col: s.start.col,
            end_col: s.end.col,
            span: s.end.row - s.start.row,
            markers,
        })
    }

    /// Where the anchored text shows among `lines`, the rows from screen row `top`, in a grid
    /// `cols` wide; None when it shows nowhere.
    pub fn find(&self, lines: &[String], top: u32, cols: u16) -> Option<Selection> {
        let lines: Vec<Vec<char>> = lines.iter().map(|l| l.chars().collect()).collect();
        let (text, offset, col) = &self.markers[0];
        let primary: Vec<char> = text.chars().collect();
        for (row, line) in lines.iter().enumerate() {
            let mut from = 0;
            while let Some(at) = index_of(line, &primary, from) {
                from = at + 1;
                let Some(start_row) = (row as u32).checked_sub(*offset) else {
                    continue;
                };
                let end_row = start_row + self.span;
                let delta = at as i64 - *col as i64;
                let matches = (end_row as usize) < lines.len()
                    && self.markers.iter().all(|(text, offset, col)| {
                        let want = *col as i64 + delta;
                        let text: Vec<char> = text.chars().collect();
                        want >= 0
                            && lines
                                .get((start_row + offset) as usize)
                                .and_then(|l| index_of(l, &text, want as usize))
                                == Some(want as usize)
                    });
                if matches {
                    let shift = |c: u16| (i64::from(c) + delta).clamp(0, i64::from(cols)) as u16;
                    return Some(Selection {
                        start: Point {
                            row: top + start_row,
                            col: shift(self.start_col),
                        },
                        end: Point {
                            row: top + end_row,
                            col: shift(self.end_col),
                        },
                    });
                }
            }
        }
        None
    }
}

fn index_of(line: &[char], needle: &[char], from: usize) -> Option<usize> {
    if needle.is_empty() || from > line.len() {
        return None;
    }
    (from..=line.len().saturating_sub(needle.len()))
        .find(|&i| line.get(i..i + needle.len()) == Some(needle))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn lines(text: &[&str]) -> Vec<String> {
        text.iter().map(|l| (*l).to_owned()).collect()
    }

    #[test]
    fn clean_lines_matches_the_tauri_terminal() {
        let cases: &[(&[&str], &[&str])] = &[
            (&["  one  "], &["  one"]),
            (&["  a", "    b", "", "  c"], &["a", "  b", "", "c"]),
            (&["$ ls", "    a", "    b"], &["$ ls", "a", "b"]),
            (&["x", "y"], &["x", "y"]),
            (&["", "  "], &["", ""]),
        ];
        for (input, want) in cases {
            assert_eq!(clean_lines(&lines(input)), lines(want), "{input:?}");
        }
    }

    #[test]
    fn a_double_click_takes_the_word_of_letters_digits_underscores_and_dashes() {
        assert_eq!(word_at("git push --force origin", 6), Some((4, 8)));
        assert_eq!(word_at("git push --force origin", 10), Some((9, 16)));
        assert_eq!(word_at("a.b", 1), None);
        assert_eq!(word_at("short", 9), None);
    }

    #[test]
    fn an_anchor_finds_its_text_where_the_program_moved_it() {
        let before = lines(&["header", "  alpha beta", "  gamma"]);
        let selection = Selection {
            start: Point { row: 11, col: 2 },
            end: Point { row: 12, col: 7 },
        };
        let anchor = Anchor::new(selection, |row| before[(row - 10) as usize].clone()).unwrap();
        let after = lines(&["x", "y", "    alpha beta", "    gamma"]);
        assert_eq!(
            anchor.find(&after, 20, 40),
            Some(Selection {
                start: Point { row: 22, col: 4 },
                end: Point { row: 23, col: 9 },
            })
        );
        assert_eq!(anchor.find(&lines(&["nothing"]), 0, 40), None);
    }
}
