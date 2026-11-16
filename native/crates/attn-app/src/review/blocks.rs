//! A stop on a markdown file, read as the document it is: its blocks now, each marked when it
//! changed with the text it was, and the blocks deleted between them.

use std::collections::HashMap;
use std::rc::Rc;

use attn_protocol::{MarkdownAnnotation, ReviewRow, ReviewStop};
use attn_ui::Tokens;
use pulldown_cmark::{Event, HeadingLevel, Options, Parser, Tag, TagEnd, TextMergeStream};
use rune::tok::{radius, space, type_};
use rune::{El, Image, Link, Span, col, text};
use rune_kit::markdown::Markdown;

use crate::markdown::{Document, linked};

/// Whether a stop on `path` reads as a document rather than as code.
pub(crate) fn is_document(path: &str) -> bool {
    path.to_lowercase().ends_with(".md")
}

/// One block of a document stop: its lines now and, when it changed, the blocks it was. Blocks
/// deleted just before it show above it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Block {
    /// The first and last of the stop's rows the block holds now.
    pub(crate) rows: (usize, usize),
    pub(crate) from: i64,
    pub(crate) to: i64,
    pub(crate) source: String,
    pub(crate) kind: Kind,
    pub(crate) changed: bool,
    pub(crate) was: Option<String>,
    pub(crate) deleted_before: Option<String>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    Paragraph,
    ListItem,
    Heading(u8),
    Other,
}

impl Kind {
    /// Prose reads as words, so a light rewording of it shows its changed words.
    fn prose(self) -> bool {
        !matches!(self, Kind::Other)
    }
}

/// The blocks of a document stop, and the blocks deleted after its last one.
#[derive(Debug, PartialEq, Eq)]
pub(crate) struct Blocks {
    pub(crate) blocks: Vec<Block>,
    pub(crate) removed_after: Option<String>,
}

const OPTIONS: Options = Options::ENABLE_TABLES
    .union(Options::ENABLE_STRIKETHROUGH)
    .union(Options::ENABLE_TASKLISTS)
    .union(Options::ENABLE_YAML_STYLE_METADATA_BLOCKS)
    .union(Options::ENABLE_FOOTNOTES);

/// Each top-level block of `source`, and each item of a top-level list, by its kind and the
/// lines it spans, counted from 1.
fn block_lines(source: &str) -> Vec<(Kind, usize, usize)> {
    let breaks: Vec<usize> = source.match_indices('\n').map(|(i, _)| i).collect();
    let line_of = |byte: usize| breaks.partition_point(|&b| b < byte) + 1;
    let mut out = Vec::new();
    let mut depth = 0usize;
    let mut in_list = false;
    let mut start: Option<(Kind, usize)> = None;
    for (event, range) in Parser::new_ext(source, OPTIONS).into_offset_iter() {
        match event {
            Event::Start(tag) => {
                let top = depth == 0 || (in_list && depth == 1);
                if depth == 0 && matches!(tag, Tag::List(_)) {
                    in_list = true;
                } else if top && start.is_none() {
                    let kind = match tag {
                        Tag::Paragraph => Kind::Paragraph,
                        Tag::Item => Kind::ListItem,
                        Tag::Heading { level, .. } => Kind::Heading(heading(level)),
                        _ => Kind::Other,
                    };
                    start = Some((kind, range.start));
                }
                depth += 1;
            }
            Event::End(tag) => {
                depth -= 1;
                if depth == 0 && matches!(tag, TagEnd::List(_)) {
                    in_list = false;
                } else if (depth == 0 || (in_list && depth == 1))
                    && let Some((kind, from)) = start.take()
                {
                    let text = source[from..range.end].trim_end();
                    out.push((kind, line_of(from), line_of(from + text.len().max(1) - 1)));
                }
            }
            Event::Rule | Event::Html(_) if depth == 0 => {
                let text = source[range.clone()].trim_end();
                out.push((
                    Kind::Other,
                    line_of(range.start),
                    line_of(range.start + text.len().max(1) - 1),
                ));
            }
            _ => {}
        }
    }
    unclaimed(source, out)
}

/// `blocks` with each run of non-blank lines no block holds, such as a link's reference
/// definition, as a block of its own, in line order.
fn unclaimed(source: &str, mut blocks: Vec<(Kind, usize, usize)>) -> Vec<(Kind, usize, usize)> {
    let lines: Vec<&str> = source.split('\n').collect();
    let mut held = vec![false; lines.len() + 1];
    for (_, from, to) in &blocks {
        for h in &mut held[*from..=(*to).min(lines.len())] {
            *h = true;
        }
    }
    let mut run: Option<usize> = None;
    for (i, text) in lines.iter().enumerate() {
        let line = i + 1;
        let free = !held[line] && !text.trim().is_empty();
        match (free, run) {
            (true, None) => run = Some(line),
            (false, Some(from)) => {
                blocks.push((Kind::Other, from, line - 1));
                run = None;
            }
            _ => {}
        }
    }
    if let Some(from) = run {
        blocks.push((Kind::Other, from, lines.len()));
    }
    blocks.sort_by_key(|(_, from, _)| *from);
    blocks
}

fn heading(level: HeadingLevel) -> u8 {
    level as u8
}

/// The blocks of one side of `rows`, the rows at `side`: each with the indexes of the rows it
/// holds and its source.
fn side(rows: &[ReviewRow], side: &[usize]) -> Vec<(Kind, Vec<usize>, String)> {
    let source = side
        .iter()
        .map(|&i| rows[i].text.as_str())
        .collect::<Vec<_>>()
        .join("\n");
    block_lines(&source)
        .into_iter()
        .map(|(kind, start, end)| {
            let held = side
                .get(start - 1..end.min(side.len()))
                .unwrap_or_default()
                .to_vec();
            let source = held
                .iter()
                .map(|&i| rows[i].text.as_str())
                .collect::<Vec<_>>()
                .join("\n");
            (kind, held, source)
        })
        .collect()
}

/// The blocks of a document stop's rows: each top-level block, and each item of a list, with
/// the lines it holds now. A block now and a block before are one block when they share an
/// unchanged line, or when nothing but changed or blank lines stands between the old one and
/// the new one that replaced it. A block changed when it has an added line, or the block it
/// was has a removed line; its old text is that whole block. A block before with no block now
/// was deleted, and shows above the next block, or after the last.
pub(crate) fn blocks(rows: &[ReviewRow]) -> Blocks {
    let of = |keep: fn(&ReviewRow) -> bool| {
        (0..rows.len())
            .filter(|&i| keep(&rows[i]))
            .collect::<Vec<_>>()
    };
    let now = side(rows, &of(|r| r.now.is_some()));
    let before = side(rows, &of(|r| r.old.is_some()));
    let mut block_of: Vec<Option<usize>> = vec![None; rows.len()];
    for (b, (_, held, _)) in now.iter().enumerate() {
        for &i in held {
            block_of[i] = Some(b);
        }
    }
    let mut out: Vec<Block> = now
        .iter()
        .map(|(kind, held, source)| Block {
            rows: (
                held.first().copied().unwrap_or(0),
                held.last().copied().unwrap_or(0),
            ),
            from: held.first().and_then(|&i| rows[i].now).unwrap_or(0),
            to: held.last().and_then(|&i| rows[i].now).unwrap_or(0),
            source: source.clone(),
            kind: *kind,
            changed: held.iter().any(|&i| rows[i].kind == "+"),
            was: None,
            deleted_before: None,
        })
        .collect();
    let mut pairs: Vec<Vec<usize>> = before
        .iter()
        .map(|(_, held, _)| {
            let mut paired: Vec<usize> = Vec::new();
            for b in held.iter().filter_map(|&i| block_of[i]) {
                if !paired.contains(&b) {
                    paired.push(b);
                }
            }
            paired
        })
        .collect();
    let mut paired_now = vec![false; now.len()];
    for &b in pairs.iter().flatten() {
        paired_now[b] = true;
    }
    for (index, (_, held, _)) in now.iter().enumerate() {
        let Some(&first) = held.first() else { continue };
        if paired_now[index] {
            continue;
        }
        let replaced = before.iter().enumerate().rev().find(|(i, (_, old, _))| {
            pairs[*i].is_empty() && old.last().is_some_and(|&l| l < first)
        });
        let Some((replaced, (_, old, _))) = replaced else {
            continue;
        };
        let last = *old.last().expect("found with a last row");
        let between = &rows[last + 1..first];
        if (last + 1..first).any(|i| block_of[i].is_some())
            || between
                .iter()
                .any(|r| r.kind == " " && !r.text.trim().is_empty())
        {
            continue;
        }
        pairs[replaced] = vec![index];
    }
    let mut was: Vec<Vec<String>> = vec![Vec::new(); out.len()];
    let mut deleted: Vec<String> = Vec::new();
    for ((_, old, source), paired) in before.iter().zip(&pairs) {
        if paired.is_empty() {
            deleted.push(source.clone());
            continue;
        }
        let lost = old.iter().any(|&i| rows[i].kind == "-");
        for &b in paired {
            if lost || paired.len() > 1 {
                out[b].changed = true;
            }
            was[b].push(source.clone());
        }
        if !deleted.is_empty() {
            out[paired[0]].deleted_before = Some(deleted.join("\n\n"));
            deleted.clear();
        }
    }
    for (block, was) in out.iter_mut().zip(was) {
        if block.changed && !was.is_empty() {
            block.was = Some(was.join("\n\n"));
        }
    }
    Blocks {
        blocks: out,
        removed_after: (!deleted.is_empty()).then(|| deleted.join("\n\n")),
    }
}

/// A mark a word is drawn in.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(crate) enum Mark {
    Code,
    Emphasis,
    Strong,
    Delete,
    Link,
}

/// A word of rendered text with the marks around it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct Word {
    pub(crate) text: String,
    pub(crate) marks: Vec<Mark>,
    pub(crate) href: Option<String>,
}

/// The words a block reads as once rendered, with their marks.
pub(crate) fn words(source: &str) -> Vec<Word> {
    let mut out = Vec::new();
    let mut marks: Vec<Mark> = Vec::new();
    let mut hrefs: Vec<String> = Vec::new();
    let mut push = |text: &str, marks: &[Mark], href: Option<&String>| {
        out.extend(text.split_whitespace().map(|w| Word {
            text: w.to_owned(),
            marks: marks.to_vec(),
            href: href.cloned(),
        }));
    };
    for event in TextMergeStream::new(Parser::new_ext(source, OPTIONS)) {
        match event {
            Event::Start(Tag::Emphasis) => marks.push(Mark::Emphasis),
            Event::Start(Tag::Strong) => marks.push(Mark::Strong),
            Event::Start(Tag::Strikethrough) => marks.push(Mark::Delete),
            Event::Start(Tag::Link { dest_url, .. }) => {
                marks.push(Mark::Link);
                hrefs.push(dest_url.to_string());
            }
            Event::End(TagEnd::Emphasis | TagEnd::Strong | TagEnd::Strikethrough) => {
                marks.pop();
            }
            Event::End(TagEnd::Link) => {
                marks.pop();
                hrefs.pop();
            }
            Event::Code(text) => {
                let mut coded = marks.clone();
                coded.push(Mark::Code);
                push(&text, &coded, hrefs.last());
            }
            Event::Text(text) | Event::Html(text) | Event::InlineHtml(text) => {
                push(&text, &marks, hrefs.last())
            }
            _ => {}
        }
    }
    out
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Op {
    Same,
    Added,
    Removed,
}

/// Cells of the word table a diff may fill; the largest prose block in attn's 332 markdown
/// files is 588 words, about 345,000 cells against itself.
const MAX_WORD_DIFF_CELLS: usize = 4_000_000;

/// The word changes from `was` to `now`, or None when the table would pass the cells budget.
pub(crate) fn diff_words(was: &[Word], now: &[Word]) -> Option<Vec<(Op, Word)>> {
    let (n, m) = (was.len(), now.len());
    if (n + 1) * (m + 1) > MAX_WORD_DIFF_CELLS {
        return None;
    }
    let width = m + 1;
    let mut table = vec![0u32; (n + 1) * width];
    for i in (0..n).rev() {
        for j in (0..m).rev() {
            table[i * width + j] = if was[i] == now[j] {
                table[(i + 1) * width + j + 1] + 1
            } else {
                table[(i + 1) * width + j].max(table[i * width + j + 1])
            };
        }
    }
    let mut ops = Vec::new();
    let (mut i, mut j) = (0, 0);
    while i < n && j < m {
        if was[i] == now[j] {
            ops.push((Op::Same, now[j].clone()));
            i += 1;
            j += 1;
        } else if table[(i + 1) * width + j] >= table[i * width + j + 1] {
            ops.push((Op::Removed, was[i].clone()));
            i += 1;
        } else {
            ops.push((Op::Added, now[j].clone()));
            j += 1;
        }
    }
    ops.extend(was[i..].iter().map(|w| (Op::Removed, w.clone())));
    ops.extend(now[j..].iter().map(|w| (Op::Added, w.clone())));
    Some(ops)
}

/// The changed words of `block` when it is prose reworded lightly: at most half its words
/// changed.
pub(crate) fn rewording(block: &Block) -> Option<Vec<(Op, Word)>> {
    let was = block.was.as_deref().filter(|_| block.kind.prose())?;
    let ops = diff_words(&words(was), &words(&block.source))?;
    let changed = ops.iter().filter(|(op, _)| *op != Op::Same).count();
    (changed * 2 <= ops.len()).then_some(ops)
}

/// A block's words as one text: removed words struck, added words tinted, code in mono,
/// strong and emphasis as they read, and the links `document` follows underlined and followed.
fn worded(ops: &[(Op, Word)], key: String, document: &Document, t: &Tokens) -> El {
    let mut said = String::new();
    let (mut spans, mut strikes) = (Vec::new(), Vec::new());
    let mut linked: Vec<(std::ops::Range<usize>, &str)> = Vec::new();
    let (mut added, mut removed) = (Vec::new(), Vec::new());
    for (op, word) in ops {
        if !said.is_empty() {
            said.push(' ');
        }
        let (start, end) = (said.len(), said.len() + word.text.len());
        said.push_str(&word.text);
        let mut span = Span::new(start, end);
        let href = word.href.as_deref().unwrap_or_default();
        for mark in &word.marks {
            span = match mark {
                Mark::Code => span.mono(),
                Mark::Strong => span.bold(),
                Mark::Emphasis => span.italic(),
                Mark::Link if !document.verdict(href).follows => span,
                Mark::Link => {
                    match linked.last_mut() {
                        Some((range, last)) if *last == href && range.end + 1 == start => {
                            range.end = end
                        }
                        _ => linked.push((start..end, href)),
                    }
                    span.underline(t.accent)
                }
                Mark::Delete => {
                    strikes.push((start, end, None));
                    span
                }
            };
        }
        match op {
            Op::Added => {
                span = span.color(t.success).bg(t.success.alpha(0.15), 1.0);
                added.push(word.text.as_str());
            }
            Op::Removed => {
                span = span.color(t.error);
                strikes.push((start, end, Some(t.error)));
                removed.push(word.text.as_str());
            }
            Op::Same => {}
        }
        spans.push(span);
    }
    let document = document.clone();
    linked
        .into_iter()
        .map(|(range, href)| Link::new(range, href))
        .fold(text(said), El::link)
        .on_link(
            move |state: &mut crate::State, href: &str, tx: &mut rune::Tx| {
                crate::markdown::links::follow(state, &document, href, tx)
            },
        )
        .key(key)
        .description(format!(
            "added: {}; removed: {}",
            added.join(" "),
            removed.join(" ")
        ))
        .font(type_::BODY)
        .color(t.text_primary)
        .spans(spans)
        .strikes(strikes)
        .span_radius(3.0)
        .wrap()
}

/// The file a stop is on, which its blocks' links and images resolve from, and the pictures
/// its images show.
type File<'a> = (&'a Document, &'a Rc<HashMap<String, Option<Image>>>);

/// `source` drawn as the file at `file` draws it.
fn drawn(source: &str, (document, pictures): File, t: &Tokens) -> El {
    linked(
        Markdown::new(source.to_owned()).smart_punctuation(true),
        document.clone(),
        pictures.clone(),
        t,
    )
    .build(attn_ui::kit(t))
}

/// Markdown a block was, drawn as the document drew it, dimmed in a removed frame.
fn was(source: &str, key: String, file: File, t: &Tokens) -> El {
    col()
        .key(key)
        .role("group")
        .description("removed")
        .w_full()
        .px(space::S2)
        .border_sides([0.0, 0.0, 0.0, 2.0], t.error)
        .fill(t.error.alpha(0.06))
        .opacity(0.7)
        .child(drawn(source, file, t))
}

/// A stop on a markdown file keyed `key`, drawn as its blocks, with the cursor on block
/// `cursor`, the agent's notes under the blocks they are on and the user's marks under theirs.
pub(super) fn view(
    (tile, key, editor): (&str, &str, bool),
    stop: &ReviewStop,
    file: File,
    (rows, read): (&[ReviewRow], &Blocks),
    (cursor, selected): (Option<usize>, Option<(usize, usize)>),
    marks: &[(i64, &MarkdownAnnotation)],
    t: &Tokens,
) -> El {
    let within = |b: &Block, line: i64| line >= b.from && line <= b.to;
    let unplaced = stop
        .notes
        .iter()
        .filter(|n| {
            !read
                .blocks
                .iter()
                .any(|b| n.now_from > 0 && within(b, n.now_to))
        })
        .map(|n| super::stop::note(n, t));
    let shown = read.blocks.iter().enumerate().flat_map(|(i, b)| {
        let keyed = super::stop::row_key(key, i);
        let before = b
            .deleted_before
            .as_deref()
            .map(|d| was(d, format!("{keyed}.deleted"), file, t));
        let body = match rewording(b) {
            Some(ops) => col().child(worded(&ops, format!("{keyed}.words"), file.0, t)),
            None => col()
                .gap(space::S1)
                .children(
                    b.was
                        .as_deref()
                        .map(|w| was(w, format!("{keyed}.was"), file, t)),
                )
                .child(drawn(&b.source, file, t)),
        };
        let in_range = b.from <= stop.now_to && b.to >= stop.now_from;
        let block = body
            .key(keyed.clone())
            .role("group")
            .label(format!(
                "{} block, line{} {}",
                if b.changed { "changed" } else { "unchanged" },
                if b.to > b.from { "s" } else { "" },
                if b.to > b.from {
                    format!("{}–{}", b.from, b.to)
                } else {
                    b.from.to_string()
                }
            ))
            .w_full()
            .px(space::S2)
            .py(space::S1)
            .radius(radius::SM)
            .border_sides(
                [0.0, 0.0, 0.0, 3.0],
                if b.changed {
                    t.success
                } else if in_range {
                    t.border
                } else {
                    t.border.alpha(0.0)
                },
            );
        let block = match selected {
            Some((a, b)) if i >= a && i <= b && cursor != Some(i) => {
                block.fill(t.accent.alpha(0.1))
            }
            _ => block,
        };
        let block = super::place_press(
            super::cursor_frame(block, cursor == Some(i), t),
            tile,
            keyed,
        );
        let notes = stop
            .notes
            .iter()
            .filter(move |n| n.now_from > 0 && within(b, n.now_to))
            .map(|n| super::stop::note(n, t));
        let marked = marks
            .iter()
            .filter(move |(at, _)| within(b, *at))
            .map(|(_, m)| super::marks::mark_row(m, t));
        before
            .into_iter()
            .chain(std::iter::once(block))
            .chain(notes)
            .chain(marked)
            .collect::<Vec<_>>()
    });
    let after = read
        .removed_after
        .as_deref()
        .map(|d| was(d, format!("{key}.removed"), file, t));
    let empty = (read.blocks.is_empty() && read.removed_after.is_none()).then(|| {
        text("The file is gone or empty now.")
            .font(type_::BODY)
            .color(t.text_tertiary)
    });
    col()
        .key(key.to_owned())
        .role("group")
        .label(format!("{} {}", stop.path, super::stop::range(stop, rows)))
        .w_full()
        .radius(radius::MD)
        .border(1.0, t.border.alpha(0.6))
        .fill(t.bg_tile)
        .padding(space::S2)
        .gap(space::S1)
        .child(super::stop::header(stop, rows, t))
        .children(editor.then(|| {
            let (tile, path) = (tile.to_owned(), stop.path.clone());
            text("o editor")
                .key(format!("{key}.editor"))
                .role("button")
                .label("Open in the editor")
                .font(type_::LABEL)
                .color(t.text_secondary)
                .on_press_tx(move |state: &mut crate::State, tx: &mut rune::Tx| {
                    super::open_in_editor(state, &tile, path.clone(), tx)
                })
        }))
        .children(unplaced)
        .children(shown)
        .children(after)
        .children(empty)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn row(kind: &str, old: Option<i64>, now: Option<i64>, text: &str) -> ReviewRow {
        ReviewRow {
            kind: kind.into(),
            old,
            now,
            text: text.into(),
            ..Default::default()
        }
    }

    /// Rows from a diff written one line each: a leading ' ', '+' or '-'.
    fn rows(diff: &str) -> Vec<ReviewRow> {
        let (mut old, mut now) = (0, 0);
        diff.lines()
            .map(|l| {
                let (kind, text) = l.split_at(1);
                match kind {
                    "+" => {
                        now += 1;
                        row("+", None, Some(now), text)
                    }
                    "-" => {
                        old += 1;
                        row("-", Some(old), None, text)
                    }
                    _ => {
                        old += 1;
                        now += 1;
                        row(" ", Some(old), Some(now), text)
                    }
                }
            })
            .collect()
    }

    type Seen = (
        i64,
        i64,
        Kind,
        bool,
        Option<&'static str>,
        Option<&'static str>,
    );

    #[test]
    fn the_blocks_of_a_document_stop() {
        let cases: &[(&str, &[Seen], Option<&str>)] = &[
            (
                " # Title\n \n Same text.",
                &[
                    (1, 1, Kind::Heading(1), false, None, None),
                    (3, 3, Kind::Paragraph, false, None, None),
                ],
                None,
            ),
            (
                " # Title\n \n-Old words here.\n+New words here.",
                &[
                    (1, 1, Kind::Heading(1), false, None, None),
                    (3, 3, Kind::Paragraph, true, Some("Old words here."), None),
                ],
                None,
            ),
            (
                " One.\n \n-Gone.\n-\n Two.",
                &[
                    (1, 1, Kind::Paragraph, false, None, None),
                    (3, 3, Kind::Paragraph, false, None, Some("Gone.")),
                ],
                None,
            ),
            (
                " One.\n \n-Gone at the end.",
                &[(1, 1, Kind::Paragraph, false, None, None)],
                Some("Gone at the end."),
            ),
            (
                " - first\n-- second\n+- second, now longer\n - third",
                &[
                    (1, 1, Kind::ListItem, false, None, None),
                    (2, 2, Kind::ListItem, true, Some("- second"), None),
                    (3, 3, Kind::ListItem, false, None, None),
                ],
                None,
            ),
            (
                " A line\n+that grew\n end.",
                &[(1, 3, Kind::Paragraph, true, Some("A line\nend."), None)],
                None,
            ),
            (
                " ```go\n-x := 1\n+x := 2\n ```",
                &[(1, 3, Kind::Other, true, Some("```go\nx := 1\n```"), None)],
                None,
            ),
            ("-All of it.", &[], Some("All of it.")),
            (
                " [ref]: http://x\n \n-Old.\n+New.",
                &[
                    (1, 1, Kind::Other, false, None, None),
                    (3, 3, Kind::Paragraph, true, Some("Old."), None),
                ],
                None,
            ),
            (
                " A note[^1].\n \n-[^1]: was\n+[^1]: now",
                &[
                    (1, 1, Kind::Paragraph, false, None, None),
                    (3, 3, Kind::Other, true, Some("[^1]: was"), None),
                ],
                None,
            ),
        ];
        for (diff, want, after) in cases {
            let got = blocks(&rows(diff));
            let seen: Vec<_> = got
                .blocks
                .iter()
                .map(|b| {
                    (
                        b.from,
                        b.to,
                        b.kind,
                        b.changed,
                        b.was.clone(),
                        b.deleted_before.clone(),
                    )
                })
                .collect();
            let want: Vec<_> = want
                .iter()
                .map(|(f, t, k, c, w, d)| {
                    (*f, *t, *k, *c, w.map(str::to_owned), d.map(str::to_owned))
                })
                .collect();
            assert_eq!(seen, want, "{diff}");
            assert_eq!(got.removed_after.as_deref(), *after, "{diff}");
        }
    }

    /// The guide of the reader's document-stop tests: a heading, a reworded paragraph, a
    /// rewritten one, a list with a reworded item, an added paragraph, a hard-wrapped paragraph
    /// with one word changed, and a paragraph deleted before one kept as it was.
    fn guide() -> Vec<ReviewRow> {
        let mut rows = vec![
            row(" ", Some(1), Some(1), "# Reviews"),
            row(" ", Some(2), Some(2), ""),
            row("-", Some(3), None, "Press `c` to write a note on a line."),
            row("+", None, Some(3), "Press `c` to leave a note on the line."),
            row(" ", Some(4), Some(4), ""),
            row(
                "-",
                Some(5),
                None,
                "Nothing else is sent until you send the review.",
            ),
            row(
                "+",
                None,
                Some(5),
                "Questions reach the agent at once and come back answered.",
            ),
        ];
        let rest = [
            "",
            "- one",
            "- two",
            "- three",
            "",
            "A paragraph added at the end.",
            "",
            "Hard-wrapped lines",
            "keep their words",
            "when one of them changes.",
            "",
            "A paragraph kept as it was.",
        ];
        for (i, text) in rest.iter().enumerate() {
            let n = 6 + i as i64;
            match *text {
                "A paragraph kept as it was." => rows.extend([
                    row("-", Some(n), None, "A paragraph that was deleted."),
                    row("-", Some(n), None, ""),
                    row(" ", Some(n), Some(n), text),
                ]),
                "keep their words" => rows.extend([
                    row("-", Some(n), None, "keep all words"),
                    row("+", None, Some(n), text),
                ]),
                "- two" => rows.extend([
                    row("-", Some(n), None, "- to"),
                    row("+", None, Some(n), text),
                ]),
                t if t.starts_with("A paragraph added") => rows.push(row("+", None, Some(n), text)),
                _ => rows.push(row(" ", Some(n), Some(n), text)),
            }
        }
        rows
    }

    #[test]
    fn the_guide_reads_as_its_blocks_with_their_rewordings() {
        let read = blocks(&guide());
        let seen: Vec<(i64, bool)> = read.blocks.iter().map(|b| (b.from, b.changed)).collect();
        assert_eq!(
            seen,
            [
                (1, false),
                (3, true),
                (5, true),
                (7, false),
                (8, true),
                (9, false),
                (11, true),
                (13, true),
                (17, false)
            ]
        );
        let changed = |b: &Block| {
            rewording(b).map(|ops| {
                let of = |want: Op| {
                    ops.iter()
                        .filter(|(op, _)| *op == want)
                        .map(|(_, w)| w.text.clone())
                        .collect::<Vec<_>>()
                };
                (of(Op::Added), of(Op::Removed))
            })
        };
        let b = &read.blocks;
        assert_eq!(
            changed(&b[1]),
            Some((
                vec!["leave".into(), "the".into()],
                vec!["write".into(), "a".into()]
            ))
        );
        assert_eq!(changed(&b[2]), None);
        assert_eq!(
            b[2].was.as_deref(),
            Some("Nothing else is sent until you send the review.")
        );
        assert_eq!((changed(&b[4]), b[4].was.as_deref()), (None, Some("- to")));
        assert_eq!((b[6].was.as_deref(), changed(&b[6])), (None, None));
        assert_eq!(
            changed(&b[7]),
            Some((vec!["their".into()], vec!["all".into()]))
        );
        assert_eq!(
            b[8].deleted_before.as_deref(),
            Some("A paragraph that was deleted.")
        );
        assert_eq!(read.removed_after, None);
    }

    #[test]
    fn a_light_rewording_shows_its_changed_words_and_a_rewrite_does_not() {
        let block = |was: &str, now: &str| Block {
            rows: (0, 0),
            from: 1,
            to: 1,
            source: now.into(),
            kind: Kind::Paragraph,
            changed: true,
            was: Some(was.into()),
            deleted_before: None,
        };
        let ops = rewording(&block("The quick fox jumps.", "The quick brown fox jumps.")).unwrap();
        let said: Vec<_> = ops.iter().map(|(op, w)| (*op, w.text.as_str())).collect();
        assert_eq!(
            said,
            [
                (Op::Same, "The"),
                (Op::Same, "quick"),
                (Op::Added, "brown"),
                (Op::Same, "fox"),
                (Op::Same, "jumps.")
            ]
        );
        assert!(rewording(&block("Alpha beta gamma.", "Totally new sentence here.")).is_none());
        let joined: Vec<String> = words("foo\\*bar a &amp; b")
            .into_iter()
            .map(|w| w.text)
            .collect();
        assert_eq!(joined, ["foo*bar", "a", "&", "b"]);
        let marked = words("A `code` and *em* [link](https://x)");
        assert_eq!(marked[1].marks, [Mark::Code]);
        assert_eq!(marked[3].marks, [Mark::Emphasis]);
        assert_eq!(
            (marked[4].marks.clone(), marked[4].href.as_deref()),
            (vec![Mark::Link], Some("https://x"))
        );
    }
}
