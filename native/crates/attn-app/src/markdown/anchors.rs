//! Heading anchors: each heading's GitHub-style slug, as the Tauri reader gives its headings ids
//! (slugify.ts, rehypeHeadingSlugs.ts), so a `#fragment` link finds its heading.

use std::collections::HashMap;

use pulldown_cmark::{Event, Options, Parser, Tag, TagEnd};

/// The extensions rune-kit's Markdown parses with, so blocks found here are the blocks it draws.
pub(super) const OPTIONS: Options = Options::ENABLE_TABLES
    .union(Options::ENABLE_TASKLISTS)
    .union(Options::ENABLE_STRIKETHROUGH)
    .union(Options::ENABLE_GFM)
    .union(Options::ENABLE_YAML_STYLE_METADATA_BLOCKS);

/// `(source offset, slug)` for each top-level heading of `source`, the offset at the start of
/// its first line, as `Markdown::block_keys` takes them. Slugs count every heading, nested ones
/// too, so the second `## Setup` is `setup-1` wherever the first one sits.
pub(crate) fn heading_keys(source: &str) -> Vec<(usize, String)> {
    let mut slugs = Slugger::default();
    let mut keys = Vec::new();
    let mut depth = 0usize;
    let mut heading: Option<(usize, bool, String)> = None;
    let mut images = 0usize;
    for (event, range) in Parser::new_ext(source, OPTIONS).into_offset_iter() {
        match event {
            Event::Start(Tag::Heading { .. }) => {
                let line = source[..range.start].rfind('\n').map_or(0, |at| at + 1);
                heading = Some((line, depth == 0, String::new()));
            }
            Event::End(TagEnd::Heading(_)) => {
                if let Some((line, top, text)) = heading.take()
                    && let Some(slug) = slugs.next(&text)
                    && top
                {
                    keys.push((line, slug));
                }
            }
            Event::Start(Tag::Image { .. }) => {
                images += 1;
                depth += 1;
            }
            Event::End(TagEnd::Image) => {
                images -= 1;
                depth = depth.saturating_sub(1);
            }
            Event::Text(t) | Event::Code(t) => {
                if let Some((_, _, text)) = heading.as_mut().filter(|_| images == 0) {
                    text.push_str(&t);
                }
            }
            Event::Start(_) => depth += 1,
            Event::End(_) => depth = depth.saturating_sub(1),
            _ => {}
        }
    }
    keys
}

/// A heading's slug: lowercase, wiki links and markdown links as their labels, emphasis and
/// code markers gone, each run of other characters than letters and digits one `-`.
pub(crate) fn slug(text: &str) -> String {
    let text = labels(&text.to_lowercase());
    let mut slug = String::new();
    let mut gap = false;
    for c in text.chars().filter(|c| !matches!(c, '*' | '_' | '`' | '~')) {
        if c.is_alphanumeric() {
            if gap && !slug.is_empty() {
                slug.push('-');
            }
            gap = false;
            slug.push(c);
        } else {
            gap = true;
        }
    }
    slug
}

/// `text` with `[[target]]` as `target` and `[label](url)` as `label`.
fn labels(text: &str) -> String {
    let mut out = String::new();
    let mut rest = text;
    while let Some(open) = rest.find('[') {
        out.push_str(&rest[..open]);
        let after = &rest[open..];
        if let Some(inner) = after.strip_prefix("[[")
            && let Some(close) = inner.find("]]")
            && close > 0
            && !inner[..close].contains(']')
        {
            out.push_str(&inner[..close]);
            rest = &inner[close + 2..];
        } else if let Some(close) = after[1..].find(']')
            && close > 0
            && let Some(url) = after[1 + close + 1..].strip_prefix('(')
            && let Some(end) = url.find(')')
            && end > 0
        {
            out.push_str(&after[1..1 + close]);
            rest = &url[end + 1..];
        } else {
            out.push('[');
            rest = &after[1..];
        }
    }
    out.push_str(rest);
    out
}

/// One document's slugs: the first heading keeps its slug, later ones with the same slug add
/// `-1`, `-2`, …; a heading with no letters or digits has none.
#[derive(Default)]
struct Slugger(HashMap<String, usize>);

impl Slugger {
    fn next(&mut self, text: &str) -> Option<String> {
        let base = slug(text);
        if base.is_empty() {
            return None;
        }
        let count = self.0.entry(base.clone()).or_insert(0);
        let slug = if *count == 0 {
            base
        } else {
            format!("{base}-{count}")
        };
        *count += 1;
        Some(slug)
    }
}
