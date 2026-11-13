//! A document's passages as the reader draws them and as marks anchor them: the Tauri
//! reader's blocks (each top-level block, and each list item at any depth) made of the texts
//! the native reader draws, so a selection becomes an anchor and an anchor becomes the source
//! bytes to highlight.

use std::ops::Range;

use attn_protocol::MarkdownAnnotationAnchor;
use rune::TextSelection;
use rune_kit::markdown::{Block as Parsed, Drawn, DrawnStep, parse};
use rune_kit::{Kit, Markdown};

use super::anchoring::{Block, Blocks, Found, content_hash};

/// Where one drawn text sits in its block's text, and how its bytes line up with the block's
/// UTF-16 units.
#[derive(Debug)]
struct Placed {
    block: usize,
    start: usize,
    /// `(byte in the drawn text, unit from its start)` at each character written in the source
    /// (not the padding and marks the reader draws around inline code), then the end.
    units: Vec<(usize, usize)>,
}

#[derive(Debug)]
pub(crate) struct Passages {
    drawn: Drawn,
    pub(crate) blocks: Blocks,
    placed: Vec<Option<Placed>>,
}

impl Passages {
    /// `source` read as `markdown` draws it with `kit`'s style.
    pub(crate) fn read(source: &str, markdown: &Markdown, kit: &Kit) -> Passages {
        let drawn = markdown.drawn(kit);
        let lines = Lines::of(source);
        let mut blocks = Vec::new();
        let mut owners: Vec<(Vec<DrawnStep>, usize)> = Vec::new();
        let mut number = 0;
        for (i, parsed) in parse(source).iter().enumerate() {
            let Some(kind) = kind(parsed) else { continue };
            let range = drawn.blocks().get(i).cloned().unwrap_or(0..0);
            let at = vec![DrawnStep::Block(i)];
            owners.push((at.clone(), blocks.len()));
            blocks.push(Block {
                id: format!("b{number}-{kind}"),
                start_line: lines.at(range.start),
                end_line: lines.at(range.end.saturating_sub(1).max(range.start)),
                text: Vec::new(),
                parent: None,
                start_in_parent: 0,
            });
            number += 1;
            items(
                parsed,
                &at,
                blocks.len() - 1,
                &mut number,
                &mut blocks,
                &mut owners,
            );
        }
        let mut placed = Vec::with_capacity(drawn.texts().len());
        for t in drawn.texts() {
            let Some(&(_, innermost)) = owners
                .iter()
                .filter(|(at, _)| t.at().starts_with(at))
                .max_by_key(|(at, _)| at.len())
            else {
                placed.push(None);
                continue;
            };
            let chain = chain(&blocks, innermost);
            for (n, &b) in chain.iter().enumerate() {
                if !blocks[b].text.is_empty() {
                    blocks[b].text.push(u16::from(b'\n'));
                }
                if let Some(&child) = chain.get(n + 1)
                    && blocks[child].text.is_empty()
                {
                    blocks[child].start_in_parent = blocks[b].text.len();
                }
            }
            let start = blocks[innermost].text.len();
            let mut units = Vec::new();
            let mut unit = 0;
            let written = |byte: &usize| {
                t.map.is_empty() || t.map.iter().any(|(_, text)| text.contains(byte))
            };
            for (byte, c) in t.text.char_indices().filter(|(byte, _)| written(byte)) {
                units.push((byte, unit));
                let mut buf = [0; 2];
                let encoded = c.encode_utf16(&mut buf);
                for &b in &chain {
                    blocks[b].text.extend_from_slice(encoded);
                }
                unit += encoded.len();
            }
            units.push((t.text.len(), unit));
            if let Some(((first, _), (last, _))) = t.map.first().zip(t.map.last()) {
                let (first, last) = (
                    lines.at(first.start),
                    lines.at(last.end.saturating_sub(1).max(first.start)),
                );
                if start == 0 && blocks[innermost].parent.is_some() {
                    blocks[innermost].start_line = first;
                }
                for &b in &chain[1..] {
                    blocks[b].end_line = blocks[b].end_line.max(last);
                }
            }
            placed.push(Some(Placed {
                block: innermost,
                start,
                units,
            }));
        }
        Passages {
            drawn,
            blocks: Blocks {
                blocks,
                hash: content_hash(source),
            },
            placed,
        }
    }

    /// The anchor of `selection`, whose pieces' paths run through `root`, the reader's key, as
    /// the Tauri reader takes one: the block the selection starts in owns it, an end past that
    /// block stops at its end, and whitespace at either edge is left out.
    pub(crate) fn anchor(
        &self,
        selection: &TextSelection,
        root: &str,
    ) -> Option<MarkdownAnnotationAnchor> {
        let pieces: Vec<(&Placed, Range<usize>)> = selection
            .texts()
            .iter()
            .filter(|t| t.is_selected())
            .filter_map(|t| {
                let within = t.path().split_once(&format!("{root}/"))?.1;
                let placed = self.placed.get(self.drawn.at(within)?)?.as_ref()?;
                Some((placed, t.authored()))
            })
            .collect();
        let (first, first_range) = pieces.first()?;
        let owner = first.block;
        let start = first.start + first.unit(first_range.start);
        let end = pieces
            .iter()
            .rev()
            .find_map(|(p, r)| {
                self.offset_in(p.block, owner)
                    .map(|o| o + p.start + p.unit(r.end))
            })
            .unwrap_or(self.blocks.blocks[owner].text.len());
        let text = &self.blocks.blocks[owner].text;
        let space = |u: u16| char::from_u32(u32::from(u)).is_some_and(char::is_whitespace);
        let (mut start, mut end) = (start.min(text.len()), end.min(text.len()));
        while start < end && space(text[start]) {
            start += 1;
        }
        while end > start && space(text[end - 1]) {
            end -= 1;
        }
        self.blocks.anchor(owner, start, end)
    }

    /// The source bytes the passage `found` draws from, in order.
    pub(crate) fn source(&self, found: Found) -> Vec<Range<usize>> {
        let mut out = Vec::new();
        for (t, placed) in self.drawn.texts().iter().zip(&self.placed) {
            let Some(placed) = placed else { continue };
            let Some(offset) = self.offset_in(placed.block, found.block) else {
                continue;
            };
            let from = offset + placed.start;
            let to = from + placed.units.last().map_or(0, |u| u.1);
            let (a, b) = (found.start.max(from), found.end.min(to));
            if a >= b {
                continue;
            }
            out.extend(t.source(placed.byte(a - from)..placed.byte(b - from)));
        }
        out
    }

    /// Where block `block`'s text starts in block `ancestor`'s, when `ancestor` holds it.
    fn offset_in(&self, mut block: usize, ancestor: usize) -> Option<usize> {
        let mut offset = 0;
        while block != ancestor {
            let b = &self.blocks.blocks[block];
            offset += b.start_in_parent;
            block = b.parent?;
        }
        Some(offset)
    }
}

impl Placed {
    /// The unit from this text's start at byte `byte`.
    fn unit(&self, byte: usize) -> usize {
        self.units
            .iter()
            .find(|(b, _)| *b >= byte)
            .map_or(0, |u| u.1)
    }

    /// The byte of this text at unit `unit` from its start.
    fn byte(&self, unit: usize) -> usize {
        self.units
            .iter()
            .find(|(_, u)| *u >= unit)
            .map_or(0, |u| u.0)
    }
}

/// Registers each list item inside `parsed`, at any depth, in document order.
fn items(
    parsed: &Parsed,
    at: &[DrawnStep],
    parent: usize,
    number: &mut usize,
    blocks: &mut Vec<Block>,
    owners: &mut Vec<(Vec<DrawnStep>, usize)>,
) {
    let inner = |children: &[Parsed],
                 at: &[DrawnStep],
                 parent: usize,
                 number: &mut usize,
                 blocks: &mut Vec<Block>,
                 owners: &mut Vec<(Vec<DrawnStep>, usize)>| {
        for (j, child) in children.iter().enumerate() {
            let mut deeper = at.to_vec();
            deeper.push(DrawnStep::Block(j));
            items(child, &deeper, parent, number, blocks, owners);
        }
    };
    match parsed {
        Parsed::List { items: list, .. } => {
            for (k, item) in list.iter().enumerate() {
                let mut item_at = at.to_vec();
                item_at.push(DrawnStep::Item(k));
                let start_line = blocks[parent].start_line;
                owners.push((item_at.clone(), blocks.len()));
                blocks.push(Block {
                    id: format!("b{number}-list-item"),
                    start_line,
                    end_line: start_line,
                    text: Vec::new(),
                    parent: Some(parent),
                    start_in_parent: blocks[parent].text.len(),
                });
                *number += 1;
                let me = blocks.len() - 1;
                inner(item, &item_at, me, number, blocks, owners);
            }
        }
        Parsed::Quote(children)
        | Parsed::Alert(_, children)
        | Parsed::Details { body: children, .. } => {
            inner(children, at, parent, number, blocks, owners);
        }
        _ => {}
    }
}

/// The blocks from the outermost down to `block`.
fn chain(blocks: &[Block], block: usize) -> Vec<usize> {
    let mut chain = vec![block];
    while let Some(parent) = blocks[*chain.last().expect("never empty")].parent {
        chain.push(parent);
    }
    chain.reverse();
    chain
}

/// The Tauri reader's name for the element a top-level block renders as; none for front
/// matter, which it draws outside the document.
fn kind(parsed: &Parsed) -> Option<&'static str> {
    Some(match parsed {
        Parsed::FrontMatter(_) => return None,
        Parsed::Heading(..) => "heading",
        Parsed::Code(_) | Parsed::Diff(_) | Parsed::Mermaid(_) => "code",
        Parsed::List { .. } => "list",
        Parsed::Quote(_) | Parsed::Alert(..) => "blockquote",
        Parsed::Rule => "thematic-break",
        Parsed::Table(_) => "table",
        Parsed::Details { .. } => "details",
        _ => "paragraph",
    })
}

/// The 1-based line of each source byte.
struct Lines(Vec<usize>);

impl Lines {
    fn of(source: &str) -> Lines {
        Lines(
            std::iter::once(0)
                .chain(source.match_indices('\n').map(|(i, _)| i + 1))
                .collect(),
        )
    }

    fn at(&self, byte: usize) -> i64 {
        self.0.partition_point(|&start| start <= byte) as i64
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const DOC: &str = "---\ntitle: x\n---\n# Plan\n\nCache the `tile` layout.\n\n- first\n- second\n  - deep one\n\n> quoted\n";

    fn read(source: &str) -> Passages {
        let kit = Kit::neutral(true);
        Passages::read(
            source,
            &Markdown::new(source.to_owned()).smart_punctuation(true),
            &kit,
        )
    }

    fn found(p: &Passages, id: &str, word: &str) -> Found {
        let block = p
            .blocks
            .blocks
            .iter()
            .position(|b| b.id == id)
            .unwrap_or_else(|| panic!("no {id}"));
        let text = String::from_utf16(&p.blocks.blocks[block].text).unwrap();
        let start = text
            .find(word)
            .unwrap_or_else(|| panic!("{word} not in {text:?}"));
        let start = text[..start].encode_utf16().count();
        Found {
            block,
            start,
            end: start + word.encode_utf16().count(),
        }
    }

    #[test]
    fn blocks_are_the_tauri_readers_top_level_blocks_and_list_items_in_document_order() {
        let p = read(DOC);
        let summary: Vec<(&str, i64, i64, String)> = p
            .blocks
            .blocks
            .iter()
            .map(|b| {
                (
                    b.id.as_str(),
                    b.start_line,
                    b.end_line,
                    String::from_utf16(&b.text).unwrap(),
                )
            })
            .collect();
        assert_eq!(
            summary,
            [
                ("b0-heading", 4, 4, "Plan".to_owned()),
                ("b1-paragraph", 6, 6, "Cache the tile layout.".to_owned()),
                ("b2-list", 8, 10, "first\nsecond\ndeep one".to_owned()),
                ("b3-list-item", 8, 8, "first".to_owned()),
                ("b4-list-item", 9, 10, "second\ndeep one".to_owned()),
                ("b5-list-item", 10, 10, "deep one".to_owned()),
                ("b6-blockquote", 12, 12, "quoted".to_owned()),
            ]
        );
    }

    #[test]
    fn a_passage_maps_back_to_the_source_it_was_written_in() {
        let p = read(DOC);
        let cases = [
            ("b1-paragraph", "tile layout", "tile` layout"),
            ("b5-list-item", "deep", "deep"),
            ("b2-list", "second\ndeep", "second\n  - deep"),
        ];
        for (id, word, written) in cases {
            let ranges = p.source(found(&p, id, word));
            let (first, last) = (ranges.first().unwrap().start, ranges.last().unwrap().end);
            assert_eq!(&DOC[first..last], written, "{id} {word}");
        }
    }
}
