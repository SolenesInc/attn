//! Markdown as the blocks a reader lays out: pulldown-cmark's events folded into a small tree.
use pulldown_cmark::{CodeBlockKind, Event, Options, Parser, Tag, TagEnd};

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Span {
    pub text: String,
    pub bold: bool,
    pub italic: bool,
    pub strike: bool,
    pub code: bool,
    pub link: Option<String>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Item {
    pub checked: Option<bool>,
    pub blocks: Vec<Block>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Block {
    Heading { level: u8, spans: Vec<Span> },
    Paragraph(Vec<Span>),
    Code { lang: Option<String>, text: String },
    Mermaid(String),
    Quote(Vec<Block>),
    List { start: Option<u64>, items: Vec<Item> },
    Table { header: Vec<Vec<Span>>, rows: Vec<Vec<Vec<Span>>> },
    Image { url: String, alt: String },
    Rule,
}

pub fn parse(source: &str) -> Vec<Block> {
    let options = Options::ENABLE_TABLES
        | Options::ENABLE_STRIKETHROUGH
        | Options::ENABLE_TASKLISTS
        | Options::ENABLE_YAML_STYLE_METADATA_BLOCKS;
    let mut builder = Builder::new();
    for event in Parser::new_ext(source, options) {
        builder.event(event);
    }
    builder.finish()
}

/// Plain text of the spans, for places that cannot style (the switcher, a title).
pub fn plain(spans: &[Span]) -> String {
    spans.iter().map(|span| span.text.as_str()).collect()
}

enum Container {
    Blocks(Vec<Block>),
    List { start: Option<u64>, items: Vec<Item> },
    Item { checked: Option<bool>, blocks: Vec<Block> },
    Table { header: Vec<Vec<Span>>, rows: Vec<Vec<Vec<Span>>>, row: Vec<Vec<Span>> },
}

#[derive(Default)]
struct Style {
    bold: u8,
    italic: u8,
    strike: u8,
    link: Option<String>,
}

struct Builder {
    stack: Vec<Container>,
    spans: Vec<Span>,
    style: Style,
    heading: Option<u8>,
    code: Option<(Option<String>, String)>,
    image: Option<(String, String)>,
    metadata: usize,
}

impl Builder {
    fn new() -> Self {
        Self {
            stack: vec![Container::Blocks(Vec::new())],
            spans: Vec::new(),
            style: Style::default(),
            heading: None,
            code: None,
            image: None,
            metadata: 0,
        }
    }

    fn event(&mut self, event: Event<'_>) {
        if let Event::Start(Tag::MetadataBlock(_)) = event {
            self.metadata += 1;
            return;
        }
        if let Event::End(TagEnd::MetadataBlock(_)) = event {
            self.metadata -= 1;
            return;
        }
        if self.metadata > 0 {
            return;
        }
        match event {
            Event::Start(tag) => self.start(tag),
            Event::End(tag) => self.end(tag),
            Event::Text(text) => {
                if let Some((_, code)) = &mut self.code {
                    code.push_str(&text);
                } else if let Some((_, alt)) = &mut self.image {
                    alt.push_str(&text);
                } else {
                    self.push_text(&text);
                }
            }
            Event::Code(text) => self.spans.push(Span { text: text.to_string(), code: true, ..self.span() }),
            Event::SoftBreak => self.push_text(" "),
            Event::HardBreak => self.push_text("\n"),
            Event::Rule => {
                self.flush();
                self.push_block(Block::Rule);
            }
            Event::Html(html) => {
                self.flush();
                self.push_block(Block::Code { lang: Some("html".to_string()), text: html.to_string() });
            }
            Event::InlineHtml(html) => self.push_text(&html),
            Event::FootnoteReference(name) => self.push_text(&format!("[{name}]")),
            Event::TaskListMarker(checked) => {
                if let Some(Container::Item { checked: slot, .. }) = self.stack.last_mut() {
                    *slot = Some(checked);
                }
            }
            Event::InlineMath(text) | Event::DisplayMath(text) => self.push_text(&text),
        }
    }

    fn start(&mut self, tag: Tag<'_>) {
        match tag {
            Tag::Paragraph => self.flush(),
            Tag::Heading { level, .. } => {
                self.flush();
                self.heading = Some(level as u8);
            }
            Tag::BlockQuote(_) => {
                self.flush();
                self.stack.push(Container::Blocks(Vec::new()));
            }
            Tag::CodeBlock(kind) => {
                self.flush();
                let lang = match kind {
                    CodeBlockKind::Fenced(lang) if !lang.trim().is_empty() => {
                        Some(lang.split_whitespace().next().unwrap_or_default().to_string())
                    }
                    _ => None,
                };
                self.code = Some((lang, String::new()));
            }
            Tag::List(start) => {
                self.flush();
                self.stack.push(Container::List { start, items: Vec::new() });
            }
            Tag::Item => self.stack.push(Container::Item { checked: None, blocks: Vec::new() }),
            Tag::Table(_) => {
                self.flush();
                self.stack.push(Container::Table { header: Vec::new(), rows: Vec::new(), row: Vec::new() });
            }
            Tag::TableRow | Tag::TableHead => {
                if let Some(Container::Table { row, .. }) = self.stack.last_mut() {
                    row.clear();
                }
            }
            Tag::TableCell => self.spans.clear(),
            Tag::Emphasis => self.style.italic += 1,
            Tag::Strong => self.style.bold += 1,
            Tag::Strikethrough => self.style.strike += 1,
            Tag::Link { dest_url, .. } => self.style.link = Some(dest_url.to_string()),
            Tag::Image { dest_url, .. } => self.image = Some((dest_url.to_string(), String::new())),
            _ => {}
        }
    }

    fn end(&mut self, tag: TagEnd) {
        match tag {
            TagEnd::Paragraph => self.flush(),
            TagEnd::Heading(_) => {
                let level = self.heading.take().unwrap_or(1);
                let spans = std::mem::take(&mut self.spans);
                self.push_block(Block::Heading { level, spans });
            }
            TagEnd::BlockQuote(_) => {
                self.flush();
                if let Some(Container::Blocks(blocks)) = self.stack.pop() {
                    self.push_block(Block::Quote(blocks));
                }
            }
            TagEnd::CodeBlock => {
                if let Some((lang, text)) = self.code.take() {
                    let block = if lang.as_deref() == Some("mermaid") {
                        Block::Mermaid(text)
                    } else {
                        Block::Code { lang, text }
                    };
                    self.push_block(block);
                }
            }
            TagEnd::Item => {
                self.flush();
                if let Some(Container::Item { checked, blocks }) = self.stack.pop()
                    && let Some(Container::List { items, .. }) = self.stack.last_mut()
                {
                    items.push(Item { checked, blocks });
                }
            }
            TagEnd::List(_) => {
                if let Some(Container::List { start, items }) = self.stack.pop() {
                    self.push_block(Block::List { start, items });
                }
            }
            TagEnd::TableCell => {
                let spans = std::mem::take(&mut self.spans);
                if let Some(Container::Table { row, .. }) = self.stack.last_mut() {
                    row.push(spans);
                }
            }
            TagEnd::TableHead => {
                if let Some(Container::Table { header, row, .. }) = self.stack.last_mut() {
                    *header = std::mem::take(row);
                }
            }
            TagEnd::TableRow => {
                if let Some(Container::Table { rows, row, .. }) = self.stack.last_mut() {
                    rows.push(std::mem::take(row));
                }
            }
            TagEnd::Table => {
                if let Some(Container::Table { header, rows, .. }) = self.stack.pop() {
                    self.push_block(Block::Table { header, rows });
                }
            }
            TagEnd::Emphasis => self.style.italic = self.style.italic.saturating_sub(1),
            TagEnd::Strong => self.style.bold = self.style.bold.saturating_sub(1),
            TagEnd::Strikethrough => self.style.strike = self.style.strike.saturating_sub(1),
            TagEnd::Link => self.style.link = None,
            TagEnd::Image => {
                if let Some((url, alt)) = self.image.take() {
                    if self.spans.is_empty() {
                        self.push_block(Block::Image { url, alt });
                    } else {
                        self.spans.push(Span { text: alt, link: Some(url), ..self.span() });
                    }
                }
            }
            _ => {}
        }
    }

    fn span(&self) -> Span {
        Span {
            text: String::new(),
            bold: self.style.bold > 0,
            italic: self.style.italic > 0,
            strike: self.style.strike > 0,
            code: false,
            link: self.style.link.clone(),
        }
    }

    fn push_text(&mut self, text: &str) {
        let span = self.span();
        if let Some(last) = self.spans.last_mut()
            && !last.code
            && last.bold == span.bold
            && last.italic == span.italic
            && last.strike == span.strike
            && last.link == span.link
        {
            last.text.push_str(text);
            return;
        }
        self.spans.push(Span { text: text.to_string(), ..span });
    }

    fn flush(&mut self) {
        if self.spans.is_empty() {
            return;
        }
        let spans = std::mem::take(&mut self.spans);
        self.push_block(Block::Paragraph(spans));
    }

    fn push_block(&mut self, block: Block) {
        match self.stack.last_mut() {
            Some(Container::Blocks(blocks)) | Some(Container::Item { blocks, .. }) => blocks.push(block),
            Some(Container::List { items, .. }) => {
                if let Some(item) = items.last_mut() {
                    item.blocks.push(block);
                }
            }
            Some(Container::Table { .. }) | None => {}
        }
    }

    fn finish(mut self) -> Vec<Block> {
        self.flush();
        match self.stack.into_iter().next() {
            Some(Container::Blocks(blocks)) => blocks,
            _ => Vec::new(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const DOC: &str = "---\ntitle: front matter\n---\n# Title\n\nSome *emphasis* and **strong** with `code` and a [link](https://example.com).\n\n- one\n- [x] done\n  - nested\n\n1. first\n2. second\n\n> quoted\n\n```rust\nfn main() {}\n```\n\n```mermaid\nflowchart LR\n  a --> b\n```\n\n| h1 | h2 |\n|----|----|\n| a | b |\n\n![alt text](img.png)\n\n---\n";

    fn text(text: &str) -> Span {
        Span { text: text.to_string(), ..Span::default() }
    }

    #[test]
    fn every_block_kind_comes_through_and_front_matter_does_not() {
        let blocks = parse(DOC);
        assert_eq!(blocks[0], Block::Heading { level: 1, spans: vec![text("Title")] });
        assert_eq!(
            blocks[1],
            Block::Paragraph(vec![
                text("Some "),
                Span { italic: true, ..text("emphasis") },
                text(" and "),
                Span { bold: true, ..text("strong") },
                text(" with "),
                Span { code: true, ..text("code") },
                text(" and a "),
                Span { link: Some("https://example.com".to_string()), ..text("link") },
                text("."),
            ])
        );
        let Block::List { start: None, items } = &blocks[2] else {
            panic!("expected a bullet list, got {:?}", blocks[2]);
        };
        assert_eq!(items[0], Item { checked: None, blocks: vec![Block::Paragraph(vec![text("one")])] });
        assert_eq!(items[1].checked, Some(true));
        assert_eq!(items[1].blocks[0], Block::Paragraph(vec![text("done")]));
        assert!(matches!(&items[1].blocks[1], Block::List { items, .. } if items.len() == 1));
        assert!(matches!(&blocks[3], Block::List { start: Some(1), items } if items.len() == 2));
        assert_eq!(blocks[4], Block::Quote(vec![Block::Paragraph(vec![text("quoted")])]));
        assert_eq!(blocks[5], Block::Code { lang: Some("rust".to_string()), text: "fn main() {}\n".to_string() });
        assert_eq!(blocks[6], Block::Mermaid("flowchart LR\n  a --> b\n".to_string()));
        assert_eq!(
            blocks[7],
            Block::Table { header: vec![vec![text("h1")], vec![text("h2")]], rows: vec![vec![vec![text("a")], vec![text("b")]]] }
        );
        assert_eq!(blocks[8], Block::Image { url: "img.png".to_string(), alt: "alt text".to_string() });
        assert_eq!(blocks[9], Block::Rule);
        assert_eq!(blocks.len(), 10);
    }

    #[test]
    fn soft_breaks_join_lines_and_hard_breaks_keep_them() {
        let blocks = parse("one\ntwo  \nthree");
        assert_eq!(blocks, vec![Block::Paragraph(vec![text("one two\nthree")])]);
    }
}
