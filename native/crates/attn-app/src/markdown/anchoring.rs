//! Where a mark sits in a markdown document, as the Tauri reader's anchoring core
//! (`MarkdownReader/anchoring`) keeps it, so a mark made in either app finds its passage in
//! the other: the deepest block that holds the passage, the passage as rendered with up to 32
//! characters on either side, and offsets into the block's rendered text, all in UTF-16 units.
//! When the document changed, a mark finds its passage again by its words and what surrounds
//! them, and is saved where it was found.

use attn_protocol::MarkdownAnnotationAnchor;

const CONTEXT: usize = 32;
const CONFIDENCE: f64 = 0.5;
/// A near tie is two copies the context cannot tell apart; painting either would be a guess.
const AMBIGUITY_MARGIN: f64 = 0.05;

/// One block's rendered text: a top-level block, or a list item at any depth.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Block {
    pub(crate) id: String,
    pub(crate) start_line: i64,
    pub(crate) end_line: i64,
    pub(crate) text: Vec<u16>,
    pub(crate) parent: Option<usize>,
    pub(crate) start_in_parent: usize,
}

/// A document's blocks with the hash of the source they were read from.
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Blocks {
    pub(crate) blocks: Vec<Block>,
    pub(crate) hash: String,
}

/// A passage found: block `block`, UTF-16 units `start..end` of its text.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) struct Found {
    pub(crate) block: usize,
    pub(crate) start: usize,
    pub(crate) end: usize,
}

#[derive(Clone, Debug)]
pub(crate) enum Resolved {
    /// The anchor holds as saved.
    Exact(Found),
    /// Found elsewhere: `anchor` is where, to save in place of the old one.
    Moved(Found, Box<MarkdownAnnotationAnchor>),
    /// Not found, or found in places the context cannot choose between.
    Orphan,
}

/// The FNV-1a hash of `source`'s UTF-16 units, as 8 hex digits: the Tauri app's `fnv1a32`.
pub(crate) fn content_hash(source: &str) -> String {
    let mut hash: u32 = 0x811c_9dc5;
    for unit in source.encode_utf16() {
        for byte in [unit & 0xff, unit >> 8] {
            hash ^= u32::from(byte);
            hash = hash.wrapping_mul(0x0100_0193);
        }
    }
    format!("{hash:08x}")
}

impl Blocks {
    /// The anchor of units `start..end` of block `block`'s text, taken by the deepest block
    /// inside it that holds them all. None when that range is empty, out of the text or only
    /// whitespace.
    pub(crate) fn anchor(
        &self,
        block: usize,
        start: usize,
        end: usize,
    ) -> Option<MarkdownAnnotationAnchor> {
        let Found { block, start, end } = self.owner(block, start, end);
        let b = self.blocks.get(block)?;
        if end > b.text.len() || end <= start {
            return None;
        }
        let exact = text(&b.text[start..end]);
        if exact.trim().is_empty() {
            return None;
        }
        Some(MarkdownAnnotationAnchor {
            block_id: b.id.clone(),
            start_line: b.start_line,
            end_line: b.end_line,
            exact,
            prefix: text(&b.text[start.saturating_sub(CONTEXT)..start]),
            suffix: text(&b.text[end..(end + CONTEXT).min(b.text.len())]),
            start: start as i64,
            end: end as i64,
            content_hash: self.hash.clone(),
            ..MarkdownAnnotationAnchor::default()
        })
    }

    /// Where `anchor`'s passage is in this document.
    pub(crate) fn resolve(&self, anchor: &MarkdownAnnotationAnchor) -> Resolved {
        let exact = units(&anchor.exact);
        if anchor.content_hash == self.hash
            && let Some(block) = self.blocks.iter().position(|b| b.id == anchor.block_id)
            && let (Ok(start), Ok(end)) =
                (usize::try_from(anchor.start), usize::try_from(anchor.end))
            && self.blocks[block].text.get(start..end) == Some(exact.as_slice())
        {
            return Resolved::Exact(Found { block, start, end });
        }
        self.rebase(anchor, &exact)
    }

    fn rebase(&self, anchor: &MarkdownAnnotationAnchor, exact: &[u16]) -> Resolved {
        for candidates in [self.exact_candidates(exact), self.spaced_candidates(exact)] {
            if candidates.is_empty() {
                continue;
            }
            let Some(found) = pick(&self.blocks, candidates, anchor) else {
                return Resolved::Orphan;
            };
            if let Some(moved) = self.anchor(found.block, found.start, found.end) {
                return Resolved::Moved(
                    self.owner(found.block, found.start, found.end),
                    Box::new(moved),
                );
            }
        }
        Resolved::Orphan
    }

    fn exact_candidates(&self, exact: &[u16]) -> Vec<Found> {
        let raw = self.blocks.iter().enumerate().flat_map(|(block, b)| {
            occurrences(&b.text, exact)
                .into_iter()
                .map(move |start| Found {
                    block,
                    start,
                    end: start + exact.len(),
                })
        });
        self.owners(raw)
    }

    /// Candidates where `exact` matches with each run of whitespace read as one space.
    fn spaced_candidates(&self, exact: &[u16]) -> Vec<Found> {
        let (needle, _) = collapsed(exact);
        if needle.iter().all(|&u| is_space(u)) {
            return Vec::new();
        }
        let ends_in_space = exact.last().is_some_and(|&u| is_space(u));
        let needle = &needle;
        let raw = self.blocks.iter().enumerate().flat_map(move |(block, b)| {
            let (spaced, map) = collapsed(&b.text);
            occurrences(&spaced, needle)
                .into_iter()
                .map(move |at| {
                    let start = map[at];
                    let mut end = map[at + needle.len()];
                    while end > start && is_space(b.text[end - 1]) && !ends_in_space {
                        end -= 1;
                    }
                    Found { block, start, end }
                })
                .collect::<Vec<_>>()
        });
        self.owners(raw)
    }

    /// Each candidate moved to the deepest block that holds it, once.
    fn owners(&self, raw: impl Iterator<Item = Found>) -> Vec<Found> {
        let mut out: Vec<Found> = Vec::new();
        for found in raw {
            let owned = self.owner(found.block, found.start, found.end);
            if !out
                .iter()
                .any(|o| o.block == owned.block && o.start == owned.start)
            {
                out.push(owned);
            }
        }
        out
    }

    /// Units `start..end` of block `block`, in the deepest block inside it that holds them.
    fn owner(&self, mut block: usize, mut start: usize, mut end: usize) -> Found {
        while let Some(child) = self.blocks.iter().position(|c| {
            c.parent == Some(block)
                && c.start_in_parent <= start
                && end <= c.start_in_parent + c.text.len()
        }) {
            start -= self.blocks[child].start_in_parent;
            end -= self.blocks[child].start_in_parent;
            block = child;
        }
        Found { block, start, end }
    }
}

/// The one candidate the context points to: the only one, or one that scores clearly above
/// the rest. None for a near tie.
fn pick(
    blocks: &[Block],
    candidates: Vec<Found>,
    anchor: &MarkdownAnnotationAnchor,
) -> Option<Found> {
    if let [only] = candidates.as_slice() {
        return Some(*only);
    }
    let (prefix, suffix) = (units(&anchor.prefix), units(&anchor.suffix));
    let score = |f: &Found| {
        let text = &blocks[f.block].text;
        let before = &text[f.start.saturating_sub(CONTEXT)..f.start];
        let after = &text[f.end..(f.end + CONTEXT).min(text.len())];
        let proximity =
            1.0 / (1.0 + (blocks[f.block].start_line - anchor.start_line).abs() as f64 / 20.0);
        0.4 * similarity(before, &prefix) + 0.4 * similarity(after, &suffix) + 0.2 * proximity
    };
    let mut scored: Vec<(f64, Found)> = candidates.into_iter().map(|f| (score(&f), f)).collect();
    scored.sort_by(|a, b| b.0.total_cmp(&a.0));
    let (best, second) = (scored[0], scored[1]);
    (best.0 >= CONFIDENCE && best.0 - second.0 >= AMBIGUITY_MARGIN).then_some(best.1)
}

fn similarity(a: &[u16], b: &[u16]) -> f64 {
    let longest = a.len().max(b.len());
    if longest == 0 {
        return 1.0;
    }
    1.0 - levenshtein(a, b) as f64 / longest as f64
}

fn levenshtein(a: &[u16], b: &[u16]) -> usize {
    if a == b {
        return 0;
    }
    let mut prev: Vec<usize> = (0..=b.len()).collect();
    let mut curr = vec![0; b.len() + 1];
    for (i, &x) in a.iter().enumerate() {
        curr[0] = i + 1;
        for (j, &y) in b.iter().enumerate() {
            curr[j + 1] = (curr[j] + 1)
                .min(prev[j + 1] + 1)
                .min(prev[j] + usize::from(x != y));
        }
        std::mem::swap(&mut prev, &mut curr);
    }
    prev[b.len()]
}

/// Every start of `needle` in `haystack`, overlapping ones included.
fn occurrences(haystack: &[u16], needle: &[u16]) -> Vec<usize> {
    if needle.is_empty() || needle.len() > haystack.len() {
        return Vec::new();
    }
    haystack
        .windows(needle.len())
        .enumerate()
        .filter(|(_, w)| *w == needle)
        .map(|(i, _)| i)
        .collect()
}

/// `text` with each run of whitespace one space, and where each of its units came from, with
/// the end of `text` last.
fn collapsed(text: &[u16]) -> (Vec<u16>, Vec<usize>) {
    let (mut out, mut map) = (
        Vec::with_capacity(text.len()),
        Vec::with_capacity(text.len() + 1),
    );
    let mut in_space = false;
    for (i, &u) in text.iter().enumerate() {
        if is_space(u) {
            if !in_space {
                map.push(i);
                out.push(u16::from(b' '));
            }
            in_space = true;
        } else {
            map.push(i);
            out.push(u);
            in_space = false;
        }
    }
    map.push(text.len());
    (out, map)
}

fn is_space(u: u16) -> bool {
    char::from_u32(u32::from(u)).is_some_and(char::is_whitespace)
}

fn units(s: &str) -> Vec<u16> {
    s.encode_utf16().collect()
}

fn text(units: &[u16]) -> String {
    String::from_utf16_lossy(units)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn block(id: &str, line: i64, text: &str) -> Block {
        Block {
            id: id.into(),
            start_line: line,
            end_line: line,
            text: units(text),
            parent: None,
            start_in_parent: 0,
        }
    }

    fn doc(blocks: Vec<Block>, source: &str) -> Blocks {
        Blocks {
            blocks,
            hash: content_hash(source),
        }
    }

    fn at(d: &Blocks, block: usize, word: &str) -> MarkdownAnnotationAnchor {
        let start = text(&d.blocks[block].text).find(word).unwrap();
        let start = units(&text(&d.blocks[block].text)[..start]).len();
        d.anchor(block, start, start + units(word).len())
            .expect("an anchor")
    }

    #[test]
    fn the_hash_is_the_tauri_apps_fnv1a_over_utf16_units() {
        let cases = [
            ("", "811c9dc5"),
            ("a", "2b24d044"),
            ("hello", "c3457ef7"),
            ("é", "59fb6f0c"),
            ("\u{1F642}", "73ed99d8"),
        ];
        for (source, hash) in cases {
            assert_eq!(content_hash(source), hash, "{source:?}");
        }
    }

    #[test]
    fn an_anchor_quotes_its_passage_with_up_to_32_units_of_context_and_skips_whitespace_only() {
        let d = doc(
            vec![block(
                "b0-paragraph",
                3,
                "Cache the tile layout before the first paint, then measure it again.",
            )],
            "src",
        );
        let a = at(&d, 0, "first paint");
        assert_eq!(
            (a.exact.as_str(), a.prefix.as_str(), a.suffix.as_str()),
            (
                "first paint",
                "ache the tile layout before the ",
                ", then measure it again."
            )
        );
        assert_eq!(
            (
                a.block_id.as_str(),
                a.start_line,
                a.start,
                a.end,
                a.content_hash.as_str()
            ),
            ("b0-paragraph", 3, 33, 44, content_hash("src").as_str())
        );
        assert!(d.anchor(0, 5, 6).is_none(), "a space alone");
        assert!(d.anchor(0, 6, 6).is_none(), "nothing");
        assert!(d.anchor(0, 60, 99).is_none(), "past the text");
    }

    #[test]
    fn an_anchor_belongs_to_the_deepest_block_that_holds_it() {
        let mut item = block("b1-list-item", 2, "second item");
        (item.parent, item.start_in_parent) = (Some(0), 6);
        let d = doc(vec![block("b0-list", 1, "first\nsecond item"), item], "src");
        let a = d.anchor(0, 13, 17).unwrap();
        assert_eq!(
            (a.block_id.as_str(), a.start, a.end, a.exact.as_str()),
            ("b1-list-item", 7, 11, "item")
        );
        assert_eq!(
            d.anchor(0, 0, 10).unwrap().block_id,
            "b0-list",
            "across items stays with the list"
        );
    }

    #[test]
    fn a_mark_resolves_where_it_was_or_moves_with_its_words_or_is_orphaned() {
        let before = doc(
            vec![
                block("b0-paragraph", 1, "Intro."),
                block(
                    "b1-paragraph",
                    3,
                    "Cache the tile layout before the first paint.",
                ),
            ],
            "v1",
        );
        let mark = at(&before, 1, "tile layout");
        assert!(matches!(
            before.resolve(&mark),
            Resolved::Exact(Found {
                block: 1,
                start: 10,
                end: 21
            })
        ));

        type Case = (&'static str, Blocks, Option<(usize, usize)>);
        let cases: [Case; 5] = [
            (
                "same block, shifted",
                doc(
                    vec![
                        block("b0-paragraph", 1, "Intro."),
                        block(
                            "b1-paragraph",
                            3,
                            "Now cache the tile layout before the first paint.",
                        ),
                    ],
                    "v2",
                ),
                Some((1, 14)),
            ),
            (
                "another block",
                doc(
                    vec![
                        block("b0-heading", 1, "Plan"),
                        block("b1-paragraph", 2, "Intro."),
                        block(
                            "b2-paragraph",
                            4,
                            "Cache the tile layout before the first paint.",
                        ),
                    ],
                    "v2",
                ),
                Some((2, 10)),
            ),
            (
                "rewrapped",
                doc(
                    vec![
                        block("b0-paragraph", 1, "Intro."),
                        block(
                            "b1-paragraph",
                            3,
                            "Cache the tile\n  layout before the first paint.",
                        ),
                    ],
                    "v2",
                ),
                Some((1, 10)),
            ),
            (
                "gone",
                doc(
                    vec![
                        block("b0-paragraph", 1, "Intro."),
                        block("b1-paragraph", 3, "Paint first."),
                    ],
                    "v2",
                ),
                None,
            ),
            (
                "two copies alike",
                doc(
                    vec![
                        block(
                            "b0-paragraph",
                            3,
                            "Cache the tile layout before the first paint.",
                        ),
                        block(
                            "b1-paragraph",
                            3,
                            "Cache the tile layout before the first paint.",
                        ),
                    ],
                    "v2",
                ),
                None,
            ),
        ];
        for (name, after, want) in cases {
            match (after.resolve(&mark), want) {
                (Resolved::Moved(found, moved), Some((block, start))) => {
                    assert_eq!((found.block, found.start), (block, start), "{name}");
                    assert!(
                        matches!(after.resolve(&moved), Resolved::Exact(f) if f == found),
                        "{name}: the moved anchor holds"
                    );
                }
                (Resolved::Orphan, None) => {}
                (got, _) => panic!("{name}: {got:?}"),
            }
        }
    }

    #[test]
    fn of_two_copies_the_one_whose_surroundings_match_wins() {
        let before = doc(
            vec![
                block("b0-paragraph", 1, "Before the first paint, measure."),
                block("b1-paragraph", 9, "After the first paint, cache."),
            ],
            "v1",
        );
        let mark = at(&before, 1, "the first paint");
        let after = doc(
            vec![
                block("b0-paragraph", 1, "Before the first paint, measure."),
                block("b1-paragraph", 2, "New."),
                block("b2-paragraph", 10, "After the first paint, cache."),
            ],
            "v2",
        );
        let Resolved::Moved(found, _) = after.resolve(&mark) else {
            panic!("moved")
        };
        assert_eq!(found.block, 2);
    }
}
