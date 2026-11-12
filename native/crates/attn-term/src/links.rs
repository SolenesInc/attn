//! The links in a terminal's output, found as the Tauri terminal finds them
//! (`terminalLinks.ts`): an OSC 8 hyperlink, a URL, or a path to a file that exists, on the
//! line under the pointer, joined across the rows it wraps over.

/// The rows a line may wrap over before the hover stops joining them: a path longer than this
/// would be hundreds of characters.
pub const MAX_WRAP_JOIN_ROWS: u32 = 6;

const SCHEMES: &[&str] = &[
    "https://",
    "http://",
    "file://",
    "mailto:",
    "ftp://",
    "ssh://",
    "git://",
    "tel:",
    "magnet:",
    "gemini://",
    "gopher://",
    "news:",
];

/// One terminal line as the hover reads it: each cell's text, a wide character's spacer as
/// empty, and its OSC 8 hyperlink, over rows `first_row..first_row + row_count` of a grid
/// `cols` wide.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Line {
    pub cells: Vec<String>,
    pub uris: Vec<Option<String>>,
    pub first_row: u32,
    pub row_count: u32,
    pub cols: u16,
}

/// A run of a line's cells, from `start` up to `end`, which is not included.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Span {
    pub start: usize,
    pub end: usize,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Target {
    Url(String),
    Path {
        path: String,
        line: Option<u32>,
        column: Option<u32>,
    },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Link {
    pub target: Target,
    pub span: Span,
}

/// What opening a link does.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Open {
    Url(String),
    Path(String),
    Markdown(String),
}

/// The rows of the line through row `row`, among rows `0..rows`: back while a row continues
/// the one above, on while the next continues it, at most `MAX_WRAP_JOIN_ROWS` rows.
pub fn extent(row: u32, rows: u32, mut continues: impl FnMut(u32) -> bool) -> (u32, u32) {
    let mut first = row;
    while first > 0 && row - first < MAX_WRAP_JOIN_ROWS - 1 && continues(first) {
        first -= 1;
    }
    let mut last = row;
    while last + 1 < rows && last - first < MAX_WRAP_JOIN_ROWS - 1 && continues(last + 1) {
        last += 1;
    }
    (first, last)
}

impl Line {
    /// The line over rows `first..=last`, each read by `read`, the rows before the last
    /// padded to the grid's width so a cell's index follows from its row and column.
    pub fn over(
        first: u32,
        last: u32,
        cols: u16,
        mut read: impl FnMut(u32) -> crate::LinkRow,
    ) -> Line {
        let mut line = Line {
            first_row: first,
            row_count: last - first + 1,
            cols,
            ..Line::default()
        };
        for r in first..=last {
            let mut row = read(r);
            if r < last {
                row.cells.resize(usize::from(cols), " ".to_owned());
            }
            row.uris.resize(row.cells.len(), None);
            line.cells.extend(row.cells);
            line.uris.extend(row.uris);
        }
        line
    }

    /// The index of the cell at `row` and `col`, when the line covers it.
    pub fn index(&self, row: u32, col: u16) -> Option<usize> {
        let within = row.checked_sub(self.first_row)?;
        (within < self.row_count && col < self.cols)
            .then(|| within as usize * usize::from(self.cols) + usize::from(col))
    }

    /// The rows and columns `span` covers: each row's first column and the column after its
    /// last.
    pub fn rows(&self, span: Span) -> Vec<(u32, u16, u16)> {
        let cols = usize::from(self.cols);
        let last = span.end.max(span.start + 1) - 1;
        (span.start / cols..=last / cols)
            .map(|r| {
                let start = if r == span.start / cols {
                    span.start % cols
                } else {
                    0
                };
                let end = if r == last / cols {
                    last % cols + 1
                } else {
                    cols
                };
                (self.first_row + r as u32, start as u16, end as u16)
            })
            .collect()
    }

    fn text(&self, span: Span) -> String {
        self.cells[span.start..span.end].concat()
    }

    fn key(&self, index: usize) -> char {
        self.cells
            .get(index)
            .and_then(|c| c.chars().next())
            .unwrap_or('\u{E000}')
    }
}

/// The OSC 8 hyperlink over cell `index` and the run of cells around it that link to the same
/// URI, since its label may hold spaces.
pub fn hyperlink_at(line: &Line, index: usize) -> Option<Link> {
    let uri = line.uris.get(index)?.as_ref()?;
    let same = |i: usize| line.uris[i].as_ref() == Some(uri);
    let mut start = index;
    while start > 0 && same(start - 1) {
        start -= 1;
    }
    let mut end = index + 1;
    while end < line.uris.len() && same(end) {
        end += 1;
    }
    Some(Link {
        target: Target::Url(uri.clone()),
        span: Span { start, end },
    })
}

/// The URL over cell `index`: a known scheme after a word boundary, up to whitespace or
/// `<>()`, without trailing punctuation.
pub fn url_at(line: &Line, index: usize) -> Option<Link> {
    let len = line.cells.len();
    let word = |c: char| c.is_ascii_alphanumeric() || c == '_';
    let mut at = 0;
    while at < len {
        let starts = (at == 0 || !word(line.key(at - 1)))
            && SCHEMES.iter().any(|s| {
                s.chars().enumerate().all(|(i, c)| line.key(at + i) == c) && at + s.len() < len
            });
        if !starts {
            at += 1;
            continue;
        }
        let mut end = at;
        while end < len
            && !(line.key(end).is_whitespace() || matches!(line.key(end), '<' | '>' | '(' | ')'))
        {
            end += 1;
        }
        let mut trimmed = end;
        while trimmed > at && matches!(line.key(trimmed - 1), '.' | ',' | ';' | ':' | '!' | '?') {
            trimmed -= 1;
        }
        let span = Span {
            start: at,
            end: trimmed,
        };
        if (span.start..span.end).contains(&index) {
            return Some(Link {
                target: Target::Url(line.text(span)),
                span,
            });
        }
        at = end.max(at + 1);
    }
    None
}

/// The run of cells around `index` that holds no whitespace.
pub fn fragment_at(line: &Line, index: usize) -> Option<Span> {
    let blank = |i: usize| line.key(i).is_whitespace();
    if index >= line.cells.len() || blank(index) {
        return None;
    }
    let mut start = index;
    while start > 0 && !blank(start - 1) {
        start -= 1;
    }
    let mut end = index + 1;
    while end < line.cells.len() && !blank(end) {
        end += 1;
    }
    Some(Span { start, end })
}

/// The paths `fragment` of `line` may name, best first, at most four: without the brackets
/// and punctuation around it, with and without a `:line:col` suffix, and from a `/` or `~`
/// the fragment reaches mid-way, as an agent's `Read(/path` line does.
pub fn path_candidates(line: &Line, fragment: Span) -> Vec<Link> {
    let mut candidates = Vec::new();
    let mut push_from = |from: usize| {
        let mut end = fragment.end;
        while end > from
            && matches!(
                line.key(end - 1),
                '\'' | '"' | ')' | ']' | '}' | '>' | ',' | ';' | '!' | '?'
            )
        {
            end -= 1;
        }
        while end > from && line.key(end - 1) == '.' {
            end -= 1;
        }
        if end == from {
            return;
        }
        let span = Span { start: from, end };
        let core = line.text(span);
        if let Some((path, row, col)) = split_line_col(&core) {
            candidates.push(Link {
                target: Target::Path {
                    path: path.to_owned(),
                    line: Some(row),
                    column: col,
                },
                span,
            });
        }
        candidates.push(Link {
            target: Target::Path {
                path: core,
                line: None,
                column: None,
            },
            span,
        });
    };
    let mut lead = fragment.start;
    while lead + 1 < fragment.end && "([{<\"'`".contains(line.key(lead)) {
        lead += 1;
    }
    push_from(lead);
    let path_char = |c: char| c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '~' | '-');
    if let Some(mid) = (lead + 1..fragment.end)
        .find(|&i| matches!(line.key(i), '/' | '~') && !path_char(line.key(i - 1)))
    {
        push_from(mid);
    }
    candidates
        .into_iter()
        .filter(|c| matches!(&c.target, Target::Path { path, .. } if looks_like_path(path)))
        .take(4)
        .collect()
}

/// `core` split at a `:line`, `:line:col` or `:line.col` suffix, with an optional `:` after it,
/// when a path comes before it.
fn split_line_col(core: &str) -> Option<(&str, u32, Option<u32>)> {
    let body = core.strip_suffix(':').unwrap_or(core);
    let number = |s: &str| {
        (!s.is_empty() && s.len() <= 7 && s.bytes().all(|b| b.is_ascii_digit()))
            .then(|| s.parse().ok())
            .flatten()
    };
    let digits_end = |s: &str| s.len() - s.bytes().rev().take_while(u8::is_ascii_digit).count();
    let second_at = digits_end(body);
    let first = body[..second_at].strip_suffix([':', '.']).and_then(|head| {
        let at = digits_end(head);
        let path = head[..at].strip_suffix(':')?;
        Some((
            path,
            number(&head[at..])?,
            Some(number(&body[second_at..])?),
        ))
    });
    first
        .or_else(|| {
            let path = body[..second_at].strip_suffix(':')?;
            Some((path, number(&body[second_at..])?, None))
        })
        .filter(|(path, ..)| !path.is_empty())
}

fn looks_like_path(text: &str) -> bool {
    if text.is_empty() || text.contains("://") || text.starts_with("//") {
        return false;
    }
    if text.contains('/') || text.starts_with('~') {
        return true;
    }
    text.rsplit_once('.').is_some_and(|(_, ext)| {
        (1..=8).contains(&ext.len()) && ext.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_')
    })
}

/// `path` as an absolute path, against `cwd` when relative and `home` for `~/`, with `.` and
/// `..` resolved; None when it needs one it was not given, names another user's home, or
/// climbs above the root.
pub fn resolve(path: &str, cwd: Option<&str>, home: Option<&str>) -> Option<String> {
    let joined = if path.starts_with('/') {
        path.to_owned()
    } else if path == "~" || path.starts_with("~/") {
        format!("{}{}", home?.trim_end_matches('/'), &path[1..])
    } else if path.starts_with('~') {
        return None;
    } else {
        format!("{}/{path}", cwd?.trim_end_matches('/'))
    };
    let mut segments: Vec<&str> = Vec::new();
    for segment in joined.split('/') {
        match segment {
            "" | "." => {}
            ".." => {
                segments.pop()?;
            }
            s => segments.push(s),
        }
    }
    Some(format!("/{}", segments.join("/")))
}

pub fn is_markdown(path: &str) -> bool {
    let lower = path.to_ascii_lowercase();
    lower.ends_with(".md") || lower.ends_with(".markdown")
}

impl Target {
    /// What opening the link does: a `file:` URL opens as its path, a markdown path as
    /// markdown, anything else as it is.
    pub fn open(&self) -> Open {
        let path = match self {
            Target::Url(uri) => match uri.strip_prefix("file://") {
                Some(rest) => percent_decode(&rest[rest.find('/').unwrap_or(rest.len())..]),
                None => return Open::Url(uri.clone()),
            },
            Target::Path { path, .. } => path.clone(),
        };
        if is_markdown(&path) {
            Open::Markdown(path)
        } else {
            Open::Path(path)
        }
    }
}

fn percent_decode(text: &str) -> String {
    let bytes = text.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        let hex = |b: u8| (b as char).to_digit(16);
        if bytes[i] == b'%'
            && let (Some(a), Some(b)) = (
                bytes.get(i + 1).and_then(|&b| hex(b)),
                bytes.get(i + 2).and_then(|&b| hex(b)),
            )
        {
            out.push((a * 16 + b) as u8);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// `path` as a `file:` URL, each byte outside the unreserved set and `/` percent-encoded.
pub fn file_url(path: &str) -> String {
    let mut url = String::from("file://");
    for b in path.bytes() {
        if b.is_ascii_alphanumeric() || matches!(b, b'/' | b'-' | b'_' | b'.' | b'~') {
            url.push(b as char);
        } else {
            url.push_str(&format!("%{b:02X}"));
        }
    }
    url
}

#[cfg(test)]
mod tests {
    use super::*;

    /// A link's text and the cells it spans, its first and the one after its last.
    type Spanned<'a> = Option<(&'a str, usize, usize)>;
    /// A path candidate: its path, line and column, and the cells it spans.
    type Candidate<'a> = (&'a str, Option<u32>, Option<u32>, usize, usize);
    /// A path, the cwd and home it resolves against, and where it resolves to.
    type Resolving<'a> = (&'a str, Option<&'a str>, Option<&'a str>, Option<&'a str>);

    fn line(text: &str) -> Line {
        let cells: Vec<String> = text.chars().map(String::from).collect();
        Line {
            uris: vec![None; cells.len()],
            cols: cells.len().max(1) as u16,
            cells,
            first_row: 0,
            row_count: 1,
        }
    }

    fn path(link: &Link) -> &str {
        match &link.target {
            Target::Path { path, .. } => path,
            Target::Url(uri) => uri,
        }
    }

    #[test]
    fn a_url_covers_its_scheme_to_its_end_without_trailing_punctuation() {
        let cases: &[(&str, usize, Spanned)] = &[
            (
                "see https://example.test/page. then more",
                10,
                Some(("https://example.test/page", 4, 29)),
            ),
            ("see https://example.test/page", 2, None),
            ("(ssh://host/repo)", 3, Some(("ssh://host/repo", 1, 16))),
            ("xhttps://a.test", 3, None),
            ("mailto:a@b.test, then", 2, Some(("mailto:a@b.test", 0, 15))),
        ];
        for (text, index, want) in cases {
            let got = url_at(&line(text), *index)
                .map(|l| (path(&l).to_owned(), l.span.start, l.span.end));
            assert_eq!(
                got,
                want.map(|(u, s, e)| (u.to_owned(), s, e)),
                "{text:?} at {index}"
            );
        }
    }

    #[test]
    fn a_hyperlink_spans_the_cells_that_share_its_uri() {
        let x = Some("https://x.test");
        let cases: &[(&[Option<&str>], usize, Spanned)] = &[
            (
                &[
                    None, None, None, None, x, x, x, x, x, x, x, x, x, x, None, None,
                ],
                8,
                Some(("https://x.test", 4, 14)),
            ),
            (
                &[
                    Some("https://a.test"),
                    Some("https://a.test"),
                    Some("https://b.test"),
                    Some("https://b.test"),
                ],
                1,
                Some(("https://a.test", 0, 2)),
            ),
            (
                &[
                    Some("https://a.test"),
                    Some("https://a.test"),
                    Some("https://b.test"),
                    Some("https://b.test"),
                ],
                2,
                Some(("https://b.test", 2, 4)),
            ),
            (&[x, x, x], 0, Some(("https://x.test", 0, 3))),
            (&[x, x, x], 2, Some(("https://x.test", 0, 3))),
            (&[None, x, None], 0, None),
            (&[None, x, None], 2, None),
        ];
        for (uris, index, want) in cases {
            let l = Line {
                cells: vec![" ".to_owned(); uris.len()],
                uris: uris.iter().map(|u| u.map(str::to_owned)).collect(),
                ..line("")
            };
            let got =
                hyperlink_at(&l, *index).map(|l| (path(&l).to_owned(), l.span.start, l.span.end));
            assert_eq!(
                got,
                want.map(|(u, s, e)| (u.to_owned(), s, e)),
                "{uris:?} at {index}"
            );
        }
    }

    #[test]
    fn a_fragment_is_the_run_without_whitespace() {
        assert_eq!(
            fragment_at(&line("a (src/x.go:1) b"), 5),
            Some(Span { start: 2, end: 14 })
        );
        assert_eq!(fragment_at(&line("a b"), 1), None);
        assert_eq!(fragment_at(&line("ab"), 7), None);
    }

    #[test]
    fn path_candidates_match_the_tauri_terminal() {
        let cases: &[(&str, &[Candidate])] = &[
            (
                "src/main.go:12:3",
                &[
                    ("src/main.go", Some(12), Some(3), 0, 16),
                    ("src/main.go:12:3", None, None, 0, 16),
                ],
            ),
            (
                "(internal/store/db.go:88),",
                &[
                    ("internal/store/db.go", Some(88), None, 1, 24),
                    ("internal/store/db.go:88", None, None, 1, 24),
                ],
            ),
            (
                "x.go:12.5",
                &[
                    ("x.go", Some(12), Some(5), 0, 9),
                    ("x.go:12.5", None, None, 0, 9),
                ],
            ),
            ("Makefile.am", &[("Makefile.am", None, None, 0, 11)]),
            ("~/notes.md", &[("~/notes.md", None, None, 0, 10)]),
            ("error:", &[]),
            ("https://example.test/x.go", &[]),
            ("word", &[]),
            (
                "a/b:9",
                &[("a/b", Some(9), None, 0, 5), ("a/b:9", None, None, 0, 5)],
            ),
            ("src/main.go", &[("src/main.go", None, None, 0, 11)]),
            (
                "--file=~/notes/todo.md",
                &[
                    ("--file=~/notes/todo.md", None, None, 0, 22),
                    ("~/notes/todo.md", None, None, 7, 22),
                ],
            ),
            (
                "Read(/Users/me/attn/AGENTS.md",
                &[
                    ("Read(/Users/me/attn/AGENTS.md", None, None, 0, 29),
                    ("/Users/me/attn/AGENTS.md", None, None, 5, 29),
                ],
            ),
            ("v1.2:3", &[("v1.2", Some(3), None, 0, 6)]),
        ];
        for (text, want) in cases {
            let l = line(text);
            let got: Vec<_> = path_candidates(
                &l,
                Span {
                    start: 0,
                    end: l.cells.len(),
                },
            )
            .into_iter()
            .map(|c| match c.target {
                Target::Path { path, line, column } => {
                    (path, line, column, c.span.start, c.span.end)
                }
                Target::Url(_) => unreachable!(),
            })
            .collect();
            let want: Vec<_> = want
                .iter()
                .map(|(p, l, c, s, e)| ((*p).to_owned(), *l, *c, *s, *e))
                .collect();
            assert_eq!(got, want, "{text:?}");
        }
    }

    #[test]
    fn a_line_joins_the_rows_it_wraps_over_within_the_view() {
        let read = |texts: &'static [&'static str]| {
            move |r: u32| crate::LinkRow {
                cells: texts[r as usize].chars().map(String::from).collect(),
                uris: Vec::new(),
            }
        };
        let joined = |row, rows, continues: fn(u32) -> bool, texts: &'static [&'static str]| {
            let (first, last) = extent(row, rows, continues);
            let l = Line::over(first, last, 10, read(texts));
            (l.cells.concat(), l.first_row, l.row_count)
        };
        assert_eq!(
            joined(1, 2, |_| false, &["hello", "world"]),
            ("world".into(), 1, 1)
        );
        for hovered in 0..3 {
            assert_eq!(
                joined(hovered, 3, |r| r > 0, &["Read(/tmp/", "abc/def.md", ")"]),
                ("Read(/tmp/abc/def.md)".into(), 0, 3)
            );
        }
        assert_eq!(joined(0, 2, |r| r > 0, &["ab", "cd"]).0, "ab        cd");
        assert_eq!(
            extent(11, 12, |r| r > 0),
            (11 - (MAX_WRAP_JOIN_ROWS - 1), 11)
        );
        assert_eq!(extent(0, 1, |_| true), (0, 0));
    }

    #[test]
    fn a_lines_cells_map_to_indexes_and_spans_back_to_rows() {
        let l = Line {
            cells: vec!["x".to_owned(); 25],
            uris: vec![None; 25],
            first_row: 3,
            row_count: 3,
            cols: 10,
        };
        let cases = [
            ((3, 0), Some(0)),
            ((4, 2), Some(12)),
            ((5, 9), Some(29)),
            ((2, 0), None),
            ((6, 0), None),
            ((4, 10), None),
        ];
        for ((row, col), want) in cases {
            assert_eq!(l.index(row, col), want, "{row},{col}");
        }
        assert_eq!(
            l.rows(Span { start: 5, end: 25 }),
            [(3, 5, 10), (4, 0, 10), (5, 0, 5)]
        );
        assert_eq!(l.rows(Span { start: 12, end: 18 }), [(4, 2, 8)]);
    }

    #[test]
    fn a_path_resolves_against_the_cwd_and_home() {
        let cases: &[Resolving] = &[
            ("/a/./b/../c", None, None, Some("/a/c")),
            ("src/x.go", Some("/repo/"), None, Some("/repo/src/x.go")),
            ("./src/x.go", Some("/repo"), None, Some("/repo/src/x.go")),
            ("../x.go", Some("/repo/sub"), None, Some("/repo/x.go")),
            (
                "~/notes.md",
                Some("/repo"),
                Some("/Users/me"),
                Some("/Users/me/notes.md"),
            ),
            ("~/notes.md", Some("/repo"), None, None),
            ("~other/notes.md", Some("/repo"), Some("/Users/me"), None),
            ("src/x.go", None, None, None),
            ("../../../../x", Some("/a"), None, None),
        ];
        for (p, cwd, home, want) in cases {
            assert_eq!(resolve(p, *cwd, *home).as_deref(), *want, "{p:?}");
        }
    }

    #[test]
    fn opening_routes_markdown_paths_and_file_urls_as_the_tauri_terminal_does() {
        let path = |p: &str| Target::Path {
            path: p.to_owned(),
            line: None,
            column: None,
        };
        let url = |u: &str| Target::Url(u.to_owned());
        let cases = [
            (
                path("/repo/README.md"),
                Open::Markdown("/repo/README.md".into()),
            ),
            (
                path("/repo/plan.MARKDOWN"),
                Open::Markdown("/repo/plan.MARKDOWN".into()),
            ),
            (
                path("/docs/Spec.Markdown"),
                Open::Markdown("/docs/Spec.Markdown".into()),
            ),
            (path("/repo/main.go"), Open::Path("/repo/main.go".into())),
            (
                path("/notes/file.mdx"),
                Open::Path("/notes/file.mdx".into()),
            ),
            (path("/notes/md"), Open::Path("/notes/md".into())),
            (
                path("/notes/file.md.bak"),
                Open::Path("/notes/file.md.bak".into()),
            ),
            (
                url("https://example.test/x.md"),
                Open::Url("https://example.test/x.md".into()),
            ),
            (
                url("file:///repo/main.go"),
                Open::Path("/repo/main.go".into()),
            ),
            (
                url("file://localhost/repo/README.md"),
                Open::Markdown("/repo/README.md".into()),
            ),
            (
                url("file:///repo/my%20notes.md"),
                Open::Markdown("/repo/my notes.md".into()),
            ),
        ];
        for (target, want) in cases {
            assert_eq!(target.open(), want, "{target:?}");
        }
        assert_eq!(file_url("/repo/my notes.md"), "file:///repo/my%20notes.md");
    }
}
