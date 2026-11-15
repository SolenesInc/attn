//! A review document as the reader lays it out: its top-level blocks in order, with the lines
//! the review draws itself (each stop's heading and the notes under it, the Not shown list)
//! taken out, and the change's facts after the title.

use std::collections::HashSet;
use std::ops::Range;

use attn_protocol::ReviewBundle;
use pulldown_cmark::{Event, Options, Parser, Tag, TagEnd};

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Part {
    /// One top-level block of the document's markdown, by its source bytes, with its heading
    /// level when it is a heading.
    Prose {
        source: Range<usize>,
        heading: Option<u8>,
    },
    /// The change's facts and its changed files.
    Summary,
    /// The bundle's stop at this index, in place of its heading and note items.
    Stop(usize),
    /// The bundle's Not shown entries, in place of their list.
    NotShown,
    /// The bundle's drawn sequence diagram at this index of its flows, in place of its fence.
    Diagram(usize),
}

impl Part {
    /// A `#` or `##` heading, which opens a step of the review.
    pub(crate) fn opens_step(&self) -> bool {
        matches!(
            self,
            Part::Prose {
                heading: Some(1 | 2),
                ..
            }
        )
    }
}

struct Block {
    source: Range<usize>,
    line: i64,
    kind: Kind,
}

enum Kind {
    Heading(u8),
    List(Vec<i64>),
    Other,
}

/// `source`'s parts, in reading order.
pub(crate) fn parts(source: &str, bundle: &ReviewBundle) -> Vec<Part> {
    let stop_at = |line: i64| bundle.stops.iter().position(|s| s.heading_line == line);
    let note_lines: HashSet<i64> = bundle
        .stops
        .iter()
        .flat_map(|s| s.notes.iter().map(|n| n.line))
        .collect();
    let not_shown_lines: HashSet<i64> = bundle.not_shown.iter().map(|n| n.line).collect();
    let within = |items: &[i64], lines: &HashSet<i64>| {
        !items.is_empty() && items.iter().all(|line| lines.contains(line))
    };
    let diagram_at = |line: i64| bundle.flows.iter().position(|f| f.drawn && f.line == line);
    let mut parts = Vec::new();
    for block in blocks(source) {
        let part = match &block.kind {
            Kind::Other if let Some(flow) = diagram_at(block.line) => Part::Diagram(flow),
            Kind::Heading(3) if let Some(stop) = stop_at(block.line) => Part::Stop(stop),
            Kind::List(items) if within(items, &note_lines) => continue,
            Kind::List(items) if within(items, &not_shown_lines) => Part::NotShown,
            Kind::Heading(level) => Part::Prose {
                source: block.source,
                heading: Some(*level),
            },
            _ => Part::Prose {
                source: block.source,
                heading: None,
            },
        };
        parts.push(part);
    }
    let title = parts.iter().position(|p| {
        matches!(
            p,
            Part::Prose {
                heading: Some(1),
                ..
            }
        )
    });
    parts.insert(title.map_or(0, |at| at + 1), Part::Summary);
    parts
}

/// `source`'s top-level blocks, past its frontmatter.
fn blocks(source: &str) -> Vec<Block> {
    let options = Options::ENABLE_TABLES
        | Options::ENABLE_STRIKETHROUGH
        | Options::ENABLE_TASKLISTS
        | Options::ENABLE_FOOTNOTES
        | Options::ENABLE_YAML_STYLE_METADATA_BLOCKS;
    let line_of = |at: usize| 1 + source[..at].bytes().filter(|b| *b == b'\n').count() as i64;
    let mut blocks: Vec<Block> = Vec::new();
    let mut depth = 0usize;
    let mut skipping = false;
    for (event, range) in Parser::new_ext(source, options).into_offset_iter() {
        match event {
            Event::Start(tag) => {
                if depth == 0 {
                    let kind = match &tag {
                        Tag::MetadataBlock(_) => {
                            skipping = true;
                            Kind::Other
                        }
                        Tag::Heading { level, .. } => Kind::Heading(*level as u8),
                        Tag::List(_) => Kind::List(Vec::new()),
                        _ => Kind::Other,
                    };
                    if !skipping {
                        blocks.push(Block {
                            line: line_of(range.start),
                            source: range.clone(),
                            kind,
                        });
                    }
                } else if depth == 1
                    && matches!(tag, Tag::Item)
                    && let Some(Block {
                        kind: Kind::List(items),
                        ..
                    }) = blocks.last_mut()
                {
                    items.push(line_of(range.start));
                }
                depth += 1;
            }
            Event::End(end) => {
                depth -= 1;
                if depth == 0 && matches!(end, TagEnd::MetadataBlock(_)) {
                    skipping = false;
                }
            }
            Event::Rule | Event::Html(_) | Event::Text(_) if depth == 0 => blocks.push(Block {
                line: line_of(range.start),
                source: range,
                kind: Kind::Other,
            }),
            _ => {}
        }
    }
    blocks
}
