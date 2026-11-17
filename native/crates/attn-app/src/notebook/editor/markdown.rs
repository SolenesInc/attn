//! How each line of a note reads, as the Tauri notebook's live preview shows it
//! (liveMarkdownPreview.ts, frontmatterCard.ts, imageWidget.ts): headings sized, inline styles
//! drawn without their marks, bullets and task boxes, rules and images as blocks, and the front
//! matter as a card. A line the caret is on shows its source, still styled.

use std::ops::Range;

use pulldown_cmark::{Event, Options, Parser, Tag, TagEnd};

/// What a line is, read with the lines around it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum LineKind {
    Text,
    /// A line of a closed front matter block; `card` when the note continues after it, so it
    /// shows as a properties card on its first line.
    FrontMatter {
        card: bool,
        first: bool,
    },
    /// A fence line of a code block, with its info string.
    Fence,
    Code,
    /// `---`, `***` or `___` alone, not the underline of a heading.
    Rule,
    /// A line of a GFM table; the first shows the whole table as a grid.
    Table {
        first: bool,
        lines: std::ops::Range<usize>,
    },
    /// An image alone on its line.
    Image {
        alt: String,
        src: String,
    },
}

/// The kind of every line of `lines`.
pub(crate) fn kinds(lines: &[String]) -> Vec<LineKind> {
    let mut kinds = vec![LineKind::Text; lines.len()];
    let mut start = 0;
    if let Some(close) = front_matter_end(lines) {
        let rest = &lines[close + 1..];
        let card = !(rest.is_empty() || rest.len() == 1 && rest[0].is_empty());
        for (i, kind) in kinds.iter_mut().enumerate().take(close + 1) {
            *kind = LineKind::FrontMatter {
                card,
                first: i == 0,
            };
        }
        start = close + 1;
    }
    let mut fence: Option<(char, usize)> = None;
    let mut i = start;
    while i < lines.len() {
        let line = &lines[i];
        if fence.is_none()
            && let Some(table) = super::table::table_at(lines, i)
        {
            for j in table.lines.clone() {
                kinds[j] = LineKind::Table {
                    first: j == table.lines.start,
                    lines: table.lines.clone(),
                };
            }
            i = table.lines.end;
            continue;
        }
        match (fence_of(line), fence) {
            (Some((c, n)), None) => {
                fence = Some((c, n));
                kinds[i] = LineKind::Fence;
            }
            (Some((c, n)), Some((open, len)))
                if c == open && n >= len && line.trim_start()[n..].trim().is_empty() =>
            {
                fence = None;
                kinds[i] = LineKind::Fence;
            }
            (_, Some(_)) => kinds[i] = LineKind::Code,
            (None, None) => {
                if is_rule(line) && !underlines(lines, i, &kinds) {
                    kinds[i] = LineKind::Rule;
                } else if let Some((alt, src)) = lone_image(line) {
                    kinds[i] = LineKind::Image { alt, src };
                }
            }
        }
        i += 1;
    }
    kinds
}

/// How far into a note its front matter may close, as the Tauri editor reads it: a fence that
/// closes later is a rule, not front matter.
const FRONT_MATTER_SCAN: usize = 4096;

/// The closing line of the front matter: line 0 is `---` and a later line is `---` or `...`.
fn front_matter_end(lines: &[String]) -> Option<usize> {
    if lines.first()?.trim() != "---" {
        return None;
    }
    let mut at = 0;
    for (i, line) in lines.iter().enumerate() {
        if at >= FRONT_MATTER_SCAN {
            return None;
        }
        if i > 0 && matches!(line.trim(), "---" | "...") {
            return Some(i);
        }
        at += line.len() + 1;
    }
    None
}

fn fence_of(line: &str) -> Option<(char, usize)> {
    let rest = line.trim_start_matches([' ', '\t']);
    let c = rest.chars().next().filter(|c| matches!(c, '`' | '~'))?;
    let n = rest.chars().take_while(|&x| x == c).count();
    (n >= 3).then_some((c, n))
}

fn is_rule(line: &str) -> bool {
    let compact: String = line.chars().filter(|c| !c.is_whitespace()).collect();
    let indent = line.len() - line.trim_start_matches(' ').len();
    indent <= 3
        && compact.len() >= 3
        && ['-', '*', '_']
            .iter()
            .any(|&c| compact.chars().all(|x| x == c))
}

/// Whether a `---` line underlines the paragraph line above it, making that a heading.
fn underlines(lines: &[String], i: usize, kinds: &[LineKind]) -> bool {
    let above = i.checked_sub(1).map(|j| (&lines[j], &kinds[j]));
    let dashes = lines[i].trim().chars().all(|c| c == '-');
    dashes
        && above.is_some_and(|(line, kind)| {
            *kind == LineKind::Text
                && !line.trim().is_empty()
                && !line.trim_start().starts_with(['#', '>', '-', '*', '+'])
        })
}

fn lone_image(line: &str) -> Option<(String, String)> {
    let trimmed = line.trim();
    if !(trimmed.starts_with("![") && trimmed.ends_with(')')) {
        return None;
    }
    let mut parser = Parser::new_ext(trimmed, Options::empty()).into_offset_iter();
    let mut image = None;
    for (event, range) in &mut parser {
        match event {
            Event::Start(Tag::Image { dest_url, .. }) if range == (0..trimmed.len()) => {
                image = Some((String::new(), dest_url.to_string()));
            }
            Event::Text(text) if image.is_some() => {
                if let Some((alt, _)) = image.as_mut() {
                    alt.push_str(&text);
                }
            }
            Event::Start(Tag::Paragraph)
            | Event::End(TagEnd::Paragraph)
            | Event::End(TagEnd::Image) => {}
            _ if image.is_none() => return None,
            _ => {}
        }
    }
    image.filter(|(_, src)| !src.is_empty())
}

/// How a line draws: its text, styles over it, and where each piece came from in the source.
#[derive(Clone, Debug, Default, PartialEq)]
pub(crate) struct Shown {
    pub(crate) text: String,
    pub(crate) styles: Vec<(Range<usize>, Style)>,
    /// `(display start, source start, length)` for each run of text kept from the source; the
    /// bytes between runs are glyphs the preview put in, such as a bullet.
    runs: Vec<(usize, usize, usize)>,
    pub(crate) links: Vec<(Range<usize>, String)>,
    /// A task box: its source range (`[ ]` or `[x]`) and whether it is checked.
    pub(crate) task: Option<(Range<usize>, bool)>,
    /// Where the task box's glyph shows.
    pub(crate) task_glyph: Option<Range<usize>>,
    pub(crate) heading: Option<u8>,
    pub(crate) quote: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Style {
    Strong,
    Emphasis,
    Code,
    Strike,
    Link,
    Mark,
    Heading(u8),
}

pub(crate) const CHECKED: &str = attn_ui::glyph::CHECKED;
pub(crate) const UNCHECKED: &str = attn_ui::glyph::UNCHECKED;
pub(crate) const BULLET: &str = "•";

impl Shown {
    /// A line shown as written, without styles: code and front matter.
    pub(crate) fn raw(line: &str) -> Shown {
        Shown {
            text: line.to_owned(),
            runs: vec![(0, 0, line.len())],
            ..Shown::default()
        }
    }

    /// The display offset of source byte `col`.
    pub(crate) fn to_display(&self, col: usize) -> usize {
        let mut last = 0;
        for &(d, s, n) in &self.runs {
            if col < s {
                return d;
            }
            if col <= s + n {
                return d + col - s;
            }
            last = d + n;
        }
        last.max(self.text.len().min(last))
    }

    /// The source byte of display offset `at`.
    pub(crate) fn to_source(&self, at: usize) -> usize {
        let mut last = 0;
        for &(d, s, n) in &self.runs {
            if at < d {
                return s;
            }
            if at < d + n {
                return s + at - d;
            }
            last = s + n;
        }
        last
    }
}

/// Builds a line's display from source pieces: kept runs and glyphs put in.
struct Builder<'a> {
    source: &'a str,
    shown: Shown,
    at: usize,
}

impl<'a> Builder<'a> {
    fn keep(&mut self, to: usize) {
        if to > self.at {
            let d = self.shown.text.len();
            self.shown.text.push_str(&self.source[self.at..to]);
            self.shown.runs.push((d, self.at, to - self.at));
            self.at = to;
        }
    }

    fn skip(&mut self, to: usize) {
        self.at = self.at.max(to);
    }

    fn put(&mut self, glyph: &str) {
        self.shown.text.push_str(glyph);
    }
}

/// How `line` of kind `Text` reads: its source when `active`, else with its marks hidden.
pub(crate) fn shown(line: &str, active: bool) -> Shown {
    let mut hidden: Vec<Range<usize>> = Vec::new();
    let mut put: Vec<(usize, &'static str)> = Vec::new();
    let mut styles: Vec<(Range<usize>, Style)> = Vec::new();
    let mut links: Vec<(Range<usize>, String)> = Vec::new();
    let mut task = None;
    let mut heading = None;
    let quote_marks = quote_prefix(line);
    let quote = quote_marks > 0;
    hidden.push(0..quote_marks);
    let body = &line[quote_marks..];
    let options = Options::ENABLE_STRIKETHROUGH | Options::ENABLE_TASKLISTS;
    let mut stack: Vec<Element> = Vec::new();
    for (event, range) in Parser::new_ext(body, options).into_offset_iter() {
        let range = range.start + quote_marks..range.end + quote_marks;
        match event {
            Event::Start(tag) => stack.push((tag, range, None)),
            Event::End(_) => {
                let Some((tag, outer, inner)) = stack.pop() else {
                    continue;
                };
                let inner = inner.unwrap_or(outer.end..outer.end);
                widen(&mut stack, &outer);
                match tag {
                    Tag::Heading { level, .. } => {
                        let level = level as u8;
                        heading = Some(level);
                        let marks = line[outer.start..]
                            .chars()
                            .take_while(|c| *c == '#')
                            .count();
                        let space = line[outer.start + marks..]
                            .chars()
                            .take_while(|c| *c == ' ' || *c == '\t')
                            .count();
                        hidden.push(outer.start..outer.start + marks + space);
                        styles.push((outer.start..outer.start + marks + space, Style::Mark));
                        styles.push((inner.clone(), Style::Heading(level)));
                    }
                    Tag::Strong | Tag::Emphasis | Tag::Strikethrough => {
                        let style = match tag {
                            Tag::Strong => Style::Strong,
                            Tag::Emphasis => Style::Emphasis,
                            _ => Style::Strike,
                        };
                        hidden.push(outer.start..inner.start);
                        hidden.push(inner.end..outer.end);
                        styles.push((outer.start..inner.start, Style::Mark));
                        styles.push((inner.end..outer.end, Style::Mark));
                        styles.push((inner, style));
                    }
                    Tag::Link { dest_url, .. } => {
                        hidden.push(outer.start..inner.start);
                        hidden.push(inner.end..outer.end);
                        styles.push((outer.start..inner.start, Style::Mark));
                        styles.push((inner.end..outer.end, Style::Mark));
                        styles.push((inner.clone(), Style::Link));
                        links.push((inner, dest_url.to_string()));
                    }
                    Tag::Item => {
                        let at = outer.start + line[outer.start..].len()
                            - line[outer.start..].trim_start_matches(' ').len();
                        let marker = line[at..].chars().next();
                        let is_bullet = matches!(marker, Some('-' | '*' | '+'));
                        if is_bullet {
                            let mark = at..at + 1;
                            if let Some((boxed, _)) = &task {
                                let boxed: &Range<usize> = boxed;
                                hidden.push(at..boxed.start);
                            } else {
                                hidden.push(mark.clone());
                                put.push((mark.start, BULLET));
                            }
                            styles.push((mark, Style::Mark));
                        }
                    }
                    _ => {}
                }
            }
            Event::TaskListMarker(checked) => {
                let Some(open) = line[range.start..].find('[').map(|i| range.start + i) else {
                    continue;
                };
                let close = (open + 3).min(line.len());
                task = Some((open..close, checked));
                hidden.push(open..close);
                put.push((open, if checked { CHECKED } else { UNCHECKED }));
                widen(&mut stack, &range);
            }
            Event::Code(_) => {
                let ticks = line[range.start..]
                    .chars()
                    .take_while(|c| *c == '`')
                    .count();
                let inner = range.start + ticks..range.end - ticks;
                hidden.push(range.start..inner.start);
                hidden.push(inner.end..range.end);
                styles.push((range.start..inner.start, Style::Mark));
                styles.push((inner.end..range.end, Style::Mark));
                styles.push((inner, Style::Code));
                widen(&mut stack, &range);
            }
            _ => widen(&mut stack, &range),
        }
    }
    let mut b = Builder {
        source: line,
        shown: Shown::default(),
        at: 0,
    };
    if active {
        b.keep(line.len());
        b.shown.styles = styles;
        b.shown.links = links;
    } else {
        let mut cuts: Vec<(usize, usize, Option<&str>)> = hidden
            .iter()
            .filter(|r| !r.is_empty())
            .map(|r| (r.start, r.end, None))
            .chain(put.iter().map(|(at, glyph)| (*at, *at, Some(*glyph))))
            .collect();
        cuts.sort_by_key(|(start, end, glyph)| (*start, glyph.is_none(), *end));
        for (start, end, glyph) in cuts {
            b.keep(start);
            if let Some(glyph) = glyph {
                if glyph == CHECKED || glyph == UNCHECKED {
                    let at = b.shown.text.len();
                    b.shown.task_glyph = Some(at..at + glyph.len());
                }
                b.put(glyph);
            }
            b.skip(end);
        }
        b.keep(line.len());
        let shown = &b.shown;
        let map = |r: &Range<usize>| shown.to_display(r.start)..shown.to_display(r.end);
        let styles: Vec<_> = styles
            .iter()
            .filter(|(_, s)| *s != Style::Mark)
            .map(|(r, s)| (map(r), *s))
            .filter(|(r, _)| !r.is_empty())
            .collect();
        let links: Vec<_> = links
            .iter()
            .map(|(r, href)| (map(r), href.clone()))
            .collect();
        b.shown.styles = styles;
        b.shown.links = links;
    }
    b.shown.task = task;
    b.shown.heading = heading;
    b.shown.quote = quote;
    b.shown
}

/// An element being read: its tag, its whole range, and the range of the text inside it.
type Element<'a> = (Tag<'a>, Range<usize>, Option<Range<usize>>);

/// Each element in `stack` covers at least `range` with its inner text.
fn widen(stack: &mut [Element], range: &Range<usize>) {
    if let Some((_, _, inner)) = stack.last_mut() {
        *inner = Some(match inner.take() {
            Some(r) => r.start.min(range.start)..r.end.max(range.end),
            None => range.clone(),
        });
    }
}

/// The bytes of `>` marks, each with one space after it, that open a quote line.
fn quote_prefix(line: &str) -> usize {
    let mut at = line.len() - line.trim_start_matches(' ').len();
    if at > 3 || !line[at..].starts_with('>') {
        return 0;
    }
    while line[at..].starts_with('>') {
        at += 1;
        if line[at..].starts_with(' ') {
            at += 1;
        }
    }
    at
}

#[cfg(test)]
mod tests {
    use super::*;

    /// What each line reads as away from the caret, as the Tauri test lists it.
    fn reads(text: &str) -> Vec<String> {
        let lines: Vec<String> = text.split('\n').map(str::to_owned).collect();
        let kinds = kinds(&lines);
        let mut out = Vec::new();
        for (line, kind) in lines.iter().zip(&kinds) {
            match kind {
                LineKind::FrontMatter {
                    card: true,
                    first: true,
                } => out.push("[properties]".to_owned()),
                LineKind::FrontMatter {
                    card: true,
                    first: false,
                } => {}
                LineKind::Rule => out.push(String::new()),
                LineKind::Image { .. } => out.push("[image]".to_owned()),
                LineKind::Text => out.push(
                    shown(line, false)
                        .text
                        .replace(CHECKED, "\u{2611}")
                        .replace(UNCHECKED, "\u{2610}"),
                ),
                _ => out.push(line.clone()),
            }
        }
        out
    }

    #[test]
    fn a_note_reads_without_its_marks_with_blocks_for_rules_images_and_front_matter() {
        for (name, content, lines) in [
            (
                "a heading without its marker",
                "# Title\n\nbody",
                vec!["Title", "", "body"],
            ),
            (
                "inline styles and a link as text alone",
                "x **b** *i* `c` ~~s~~ [the note](/k/foo.md)",
                vec!["x b i c s the note"],
            ),
            (
                "bullets and task boxes, and an ordered list as written",
                "- item\n- [ ] todo\n- [x] done\n\n1. first",
                vec!["• item", "\u{2610} todo", "\u{2611} done", "", "1. first"],
            ),
            (
                "a fenced code block with its fences",
                "```ts\ncode\n```",
                vec!["```ts", "code", "```"],
            ),
            (
                "every line of a blockquote, nested ones too, without their marks",
                "> one\n> two\n\n> > nested",
                vec!["one", "two", "", "nested"],
            ),
            (
                "a rule, but not the underline of a heading",
                "para\n\n---\n\ntitle\n---",
                vec!["para", "", "", "", "title", "---"],
            ),
            (
                "an image on its own line",
                "Before.\n\n![a cat](a.png)\n\nAfter.",
                vec!["Before.", "", "[image]", "", "After."],
            ),
            (
                "frontmatter as a properties card",
                "---\ntype: area\n---\n# Body",
                vec!["[properties]", "Body"],
            ),
            (
                "frontmatter with no body line as raw YAML",
                "---\ntype: area\n---\n",
                vec!["---", "type: area", "---", ""],
            ),
            (
                "an unclosed frontmatter fence as markdown",
                "---\ntags:\n  - one\n- item",
                vec!["", "tags:", "  • one", "• item"],
            ),
        ] {
            assert_eq!(reads(content), lines, "{name}");
        }
    }

    #[test]
    fn a_fence_that_closes_far_from_the_top_is_not_front_matter() {
        let body = (0..400)
            .map(|i| format!("line {i} of the note"))
            .collect::<Vec<_>>()
            .join("\n");
        let lines: Vec<String> = format!("---\n{body}\n---\nafter")
            .split('\n')
            .map(str::to_owned)
            .collect();
        assert_eq!(kinds(&lines)[0], LineKind::Rule);
        assert!(
            !kinds(&lines)
                .iter()
                .any(|k| matches!(k, LineKind::FrontMatter { .. }))
        );
    }

    #[test]
    fn an_image_among_text_stays_in_the_text() {
        for content in [
            "Look ![a cat](a.png) here",
            "![a cat](a.png) is cute",
            "![one](a.png)![two](b.png)",
        ] {
            assert_eq!(kinds(&[content.to_owned()]), [LineKind::Text], "{content}");
        }
    }

    #[test]
    fn the_caret_s_line_shows_its_source_styled_and_offsets_map_both_ways() {
        let line = "x **bold** [a](/b.md)";
        let active = shown(line, true);
        assert_eq!(active.text, line);
        assert!(
            active
                .styles
                .iter()
                .any(|(r, s)| *s == Style::Strong && &line[r.clone()] == "bold")
        );
        let quiet = shown(line, false);
        assert_eq!(quiet.text, "x bold a");
        assert_eq!(quiet.links, [(7..8, "/b.md".to_owned())]);
        assert_eq!(quiet.to_display(line.find("bold").unwrap()), 2);
        assert_eq!(quiet.to_source(2), line.find("bold").unwrap());
        assert_eq!(
            quiet.to_source(quiet.text.len()),
            line.len() - "](/b.md)".len()
        );
        let task = shown("- [ ] todo", false);
        assert_eq!(task.task, Some((2..5, false)));
        assert_eq!(shown("## Title", true).heading, Some(2));
    }
}
