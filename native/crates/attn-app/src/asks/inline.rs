//! The words of an ask that read as one line, such as its subject, a question's prompt and a
//! choice's name and note: the inline markdown they hold, drawn on the words it marks.

use std::ops::Range;

use pulldown_cmark::{Event, Parser, Tag, TagEnd};
use rune::{Color, El, Link, Span};

use crate::markdown::ids;
use crate::terminal::Subject;

#[derive(Clone, Debug, PartialEq)]
enum Mark {
    Strong,
    Emphasis,
    Code,
    Link(String),
}

/// A line's words with its markup taken off, and what the markup marked in them. A line that
/// reads as more than one paragraph of inline markdown, such as a heading or a list item,
/// keeps its words as written.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Line {
    pub(crate) text: String,
    marks: Vec<(Range<usize>, Mark)>,
}

impl Line {
    /// `source` with its links, bare URLs and what `found` finds in its words marked as links.
    pub(crate) fn of(source: &str, found: impl Fn(&str) -> Vec<(Range<usize>, String)>) -> Line {
        let mut line = parsed(source).unwrap_or_else(|| Line {
            text: source.to_owned(),
            marks: Vec::new(),
        });
        let urls = bare_urls(&line.text);
        line.link_where_free(urls);
        let named = found(&line.text);
        line.link_where_free(named);
        line
    }

    /// `source`'s words as they read, its markup taken off.
    pub(crate) fn plain(source: &str) -> String {
        parsed(source).map_or_else(|| source.to_owned(), |line| line.text)
    }

    /// `el`, which shows this line's words, with its marks drawn and its links pressable.
    pub(crate) fn drawn(&self, el: El, link: Color, code: Color) -> El {
        if self.marks.is_empty() {
            return el;
        }
        let mut cuts: Vec<usize> = self
            .marks
            .iter()
            .flat_map(|(at, _)| [at.start, at.end])
            .collect();
        cuts.sort_unstable();
        cuts.dedup();
        let spans: Vec<Span> = cuts
            .windows(2)
            .filter_map(|run| {
                let marks: Vec<&Mark> = self
                    .marks
                    .iter()
                    .filter(|(at, _)| at.start <= run[0] && run[1] <= at.end)
                    .map(|(_, mark)| mark)
                    .collect();
                (!marks.is_empty()).then(|| {
                    marks
                        .into_iter()
                        .fold(Span::new(run[0], run[1]), |span, mark| match mark {
                            Mark::Strong => span.bold(),
                            Mark::Emphasis => span.italic(),
                            Mark::Code => attn_ui::system::code_span(span, Some(code)),
                            Mark::Link(_) => span.color(link).underline(link),
                        })
                })
            })
            .collect();
        self.links().fold(el.spans(spans), |el, (at, href)| {
            let link = Link::new(at.clone(), href);
            el.link(match ids::subject(href) {
                Some(Subject::Seed(seed)) => link.help(format!("Open {seed} in the Garden")),
                Some(Subject::Session(_)) => link,
                None => link.help(href.to_owned()),
            })
        })
    }

    pub(crate) fn has_links(&self) -> bool {
        self.links().next().is_some()
    }

    fn links(&self) -> impl Iterator<Item = (&Range<usize>, &str)> {
        self.marks.iter().filter_map(|(at, mark)| match mark {
            Mark::Link(href) => Some((at, href.as_str())),
            _ => None,
        })
    }

    fn link_where_free(&mut self, found: Vec<(Range<usize>, String)>) {
        for (at, href) in found {
            let taken = self.marks.iter().any(|(marked, mark)| {
                matches!(mark, Mark::Code | Mark::Link(_))
                    && marked.start < at.end
                    && at.start < marked.end
            });
            if !taken {
                self.marks.push((at, Mark::Link(href)));
            }
        }
    }
}

fn parsed(source: &str) -> Option<Line> {
    let mut text = String::new();
    let mut marks = Vec::new();
    let mut open: Vec<(usize, Mark)> = Vec::new();
    let mut paragraphs = 0;
    for event in Parser::new(source) {
        match event {
            Event::Start(Tag::Paragraph) => paragraphs += 1,
            Event::End(TagEnd::Paragraph) => {}
            Event::Start(Tag::Strong) => open.push((text.len(), Mark::Strong)),
            Event::Start(Tag::Emphasis) => open.push((text.len(), Mark::Emphasis)),
            Event::Start(Tag::Link { dest_url, .. }) => {
                open.push((text.len(), Mark::Link(dest_url.into_string())))
            }
            Event::End(TagEnd::Strong | TagEnd::Emphasis | TagEnd::Link) => {
                let (start, mark) = open.pop()?;
                if start < text.len() {
                    marks.push((start..text.len(), mark));
                }
            }
            Event::Code(code) => {
                let start = text.len();
                text.push_str(&code);
                marks.push((start..text.len(), Mark::Code));
            }
            Event::Text(words) | Event::InlineHtml(words) => text.push_str(&words),
            Event::SoftBreak | Event::HardBreak => text.push(' '),
            Event::Start(Tag::Image { .. }) | Event::End(TagEnd::Image) => {}
            _ => return None,
        }
    }
    (paragraphs == 1).then_some(Line { text, marks })
}

/// The http and https URLs written bare in `text`, each ending before the punctuation that
/// closes its sentence.
fn bare_urls(text: &str) -> Vec<(Range<usize>, String)> {
    let mut found = Vec::new();
    let mut from = 0;
    while let Some(at) = text[from..].find("http").map(|at| from + at) {
        let rest = &text[at..];
        let starts_word = text[..at]
            .chars()
            .next_back()
            .is_none_or(|c| c.is_whitespace() || "(<\"'".contains(c));
        if !starts_word || !(rest.starts_with("https://") || rest.starts_with("http://")) {
            from = at + 4;
            continue;
        }
        let mut url = &rest[..rest.find(char::is_whitespace).unwrap_or(rest.len())];
        loop {
            let unbalanced =
                url.ends_with(')') && url.matches(')').count() > url.matches('(').count();
            let closes = url.ends_with(['.', ',', ';', ':', '!', '?', '\'', '"', '>']);
            if !(unbalanced || closes) {
                break;
            }
            url = &url[..url.len() - 1];
        }
        let end = at + url.len();
        if url.len() > "https://".len() {
            found.push((at..end, url.to_owned()));
        }
        from = end.max(at + 4);
    }
    found
}

#[cfg(test)]
mod tests {
    use super::*;

    type Case = (
        &'static str,
        &'static str,
        &'static [(&'static str, &'static str)],
    );

    fn read(source: &str) -> (String, Vec<(String, &'static str)>) {
        let line = Line::of(source, |_| Vec::new());
        let marks = line
            .marks
            .iter()
            .map(|(at, mark)| {
                let kind = match mark {
                    Mark::Strong => "strong",
                    Mark::Emphasis => "emphasis",
                    Mark::Code => "code",
                    Mark::Link(_) => "link",
                };
                (line.text[at.clone()].to_owned(), kind)
            })
            .collect();
        (line.text, marks)
    }

    #[test]
    fn a_line_shows_its_words_without_markup_and_marks_what_the_markup_marked() {
        let cases: &[Case] = &[
            (
                "Open https://auth.openai.com/codex/device and enter the code **Z5RI-IDLV2**, then answer here.",
                "Open https://auth.openai.com/codex/device and enter the code Z5RI-IDLV2, then answer here.",
                &[
                    ("Z5RI-IDLV2", "strong"),
                    ("https://auth.openai.com/codex/device", "link"),
                ],
            ),
            (
                "Run `attn seed show` or read [the notes](https://example.com/n).",
                "Run attn seed show or read the notes.",
                &[("attn seed show", "code"), ("the notes", "link")],
            ),
            (
                "*Postgres* — kept",
                "Postgres — kept",
                &[("Postgres", "emphasis")],
            ),
            (
                "See (https://example.com/a_(b)).",
                "See (https://example.com/a_(b)).",
                &[("https://example.com/a_(b)", "link")],
            ),
            (
                "`https://example.com` stays code",
                "https://example.com stays code",
                &[("https://example.com", "code")],
            ),
            (
                "**[bold link](https://a.example)**",
                "bold link",
                &[("bold link", "link"), ("bold link", "strong")],
            ),
            ("Plain words", "Plain words", &[]),
            ("2 \\* 3 is six", "2 * 3 is six", &[]),
            ("# Not a heading here", "# Not a heading here", &[]),
            ("- a list item", "- a list item", &[]),
        ];
        for (source, text, marks) in cases {
            let (shown, found) = read(source);
            assert_eq!(&shown, text, "{source}");
            let found: Vec<(&str, &str)> = found
                .iter()
                .map(|(at, kind)| (at.as_str(), *kind))
                .collect();
            assert_eq!(&found, marks, "{source}");
        }
    }
}
