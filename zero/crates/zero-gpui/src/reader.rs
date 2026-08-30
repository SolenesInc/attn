//! The read face of a document tile: markdown blocks laid out as proportional text, with
//! pictures and mermaid diagrams drawn as pictures rather than fences.
use std::collections::HashMap;
use std::collections::hash_map::DefaultHasher;
use std::hash::{Hash, Hasher};
use std::ops::Range;
use std::path::PathBuf;

use gpui::{
    AnyElement, Div, Font, FontStyle, FontWeight, Hsla, InteractiveText, Pixels, SharedString,
    StrikethroughStyle, StyledText, TextRun, UnderlineStyle, div, font, img, prelude::*, px,
    relative,
};
use zero::markdown::{Block, Item, Span};
use zero::mermaid;

use crate::theme;

/// A rendered diagram: the SVG on disk for `img`, and its natural size in points.
pub enum Diagram {
    Ready { path: PathBuf, width: f32, height: f32 },
    Failed(String),
}

pub struct Reader<'a> {
    dir: PathBuf,
    diagrams: &'a mut HashMap<u64, Diagram>,
    width: Pixels,
    next_id: u64,
}

impl<'a> Reader<'a> {
    /// `dir` resolves relative links and pictures; `width` caps what a picture may take.
    pub fn new(dir: PathBuf, diagrams: &'a mut HashMap<u64, Diagram>, width: Pixels) -> Self {
        Self { dir, diagrams, width, next_id: 0 }
    }

    pub fn render(&mut self, blocks: &[Block]) -> Vec<AnyElement> {
        blocks.iter().map(|block| self.block(block)).collect()
    }

    fn block(&mut self, block: &Block) -> AnyElement {
        match block {
            Block::Heading { level, spans } => {
                let (size, weight, top) = match level {
                    1 => (26., FontWeight::BOLD, 8.),
                    2 => (21., FontWeight::SEMIBOLD, 14.),
                    3 => (17., FontWeight::SEMIBOLD, 8.),
                    _ => (15., FontWeight::SEMIBOLD, 4.),
                };
                let text = self.inline(spans, weight, theme::fg());
                div()
                    .mt(px(top))
                    .text_size(px(size))
                    .line_height(relative(1.25))
                    .when(*level == 2, |heading| {
                        heading.pb(px(6.)).border_b_1().border_color(theme::gutter().alpha(0.5))
                    })
                    .child(text)
                    .into_any_element()
            }
            Block::Paragraph(spans) => self.inline(spans, FontWeight::NORMAL, theme::fg()),
            Block::Code { lang, text } => self.code(lang.as_deref(), text).into_any_element(),
            Block::Mermaid(source) => self.mermaid(source),
            Block::Quote(blocks) => {
                let inner = self.render(blocks);
                div()
                    .border_l_2()
                    .border_color(theme::purple().alpha(0.5))
                    .pl(px(14.))
                    .text_color(theme::fg_dark())
                    .flex()
                    .flex_col()
                    .gap(px(8.))
                    .children(inner)
                    .into_any_element()
            }
            Block::List { start, items } => self.list(*start, items),
            Block::Table { header, rows } => self.table(header, rows),
            Block::Image { url, alt } => self.image(url, alt),
            Block::Rule => div().h(px(1.)).my(px(6.)).bg(theme::gutter().alpha(0.7)).into_any_element(),
        }
    }

    fn inline(&mut self, spans: &[Span], weight: FontWeight, color: Hsla) -> AnyElement {
        let mut text = String::new();
        let mut runs = Vec::new();
        let mut links: Vec<(Range<usize>, String)> = Vec::new();
        for span in spans {
            if span.text.is_empty() {
                continue;
            }
            let start = text.len();
            text.push_str(&span.text);
            let run_font = Font {
                weight: if span.bold { FontWeight::BOLD } else { weight },
                style: if span.italic { FontStyle::Italic } else { FontStyle::Normal },
                ..font(if span.code { "JetBrains Mono" } else { ".SystemUIFont" })
            };
            let run_color = if span.link.is_some() {
                theme::blue()
            } else if span.code {
                theme::cyan()
            } else {
                color
            };
            runs.push(TextRun {
                len: span.text.len(),
                font: run_font,
                color: run_color,
                background_color: span.code.then(|| theme::bg_highlight().alpha(0.55)),
                underline: span.link.as_ref().map(|_| UnderlineStyle {
                    thickness: px(1.),
                    color: Some(theme::blue().alpha(0.5)),
                    wavy: false,
                }),
                strikethrough: span
                    .strike
                    .then(|| StrikethroughStyle { thickness: px(1.), color: Some(color.alpha(0.8)) }),
            });
            if let Some(url) = &span.link {
                links.push((start..text.len(), self.resolve(url)));
            }
        }
        if text.is_empty() {
            return div().into_any_element();
        }
        let styled = StyledText::new(SharedString::from(text)).with_runs(runs);
        if links.is_empty() {
            return styled.into_any_element();
        }
        let (ranges, urls): (Vec<Range<usize>>, Vec<String>) = links.into_iter().unzip();
        InteractiveText::new(("link", self.id()), styled)
            .on_click(ranges, move |index, _, cx| {
                if let Some(url) = urls.get(index) {
                    cx.open_url(url);
                }
            })
            .into_any_element()
    }

    fn resolve(&self, url: &str) -> String {
        if url.contains("://") || url.starts_with("mailto:") || url.starts_with('#') {
            return url.to_string();
        }
        format!("file://{}", self.dir.join(url).display())
    }

    fn code(&mut self, lang: Option<&str>, text: &str) -> Div {
        div()
            .relative()
            .rounded(px(8.))
            .bg(theme::bg_dark())
            .border_1()
            .border_color(theme::gutter().alpha(0.5))
            .px(px(14.))
            .py(px(11.))
            .font_family("JetBrains Mono")
            .text_size(px(12.5))
            .line_height(relative(1.5))
            .text_color(theme::fg_dark())
            .child(text.trim_end_matches('\n').to_string())
            .when_some(lang, |block, lang| {
                block.child(
                    div()
                        .absolute()
                        .top(px(6.))
                        .right(px(10.))
                        .font_family(".SystemUIFont")
                        .text_size(px(10.))
                        .text_color(theme::comment())
                        .child(lang.to_string()),
                )
            })
    }

    fn list(&mut self, start: Option<u64>, items: &[Item]) -> AnyElement {
        let mut list = div().flex().flex_col().gap(px(5.));
        for (index, item) in items.iter().enumerate() {
            let marker: AnyElement = match (item.checked, start) {
                (Some(checked), _) => div()
                    .mt(px(4.))
                    .size(px(14.))
                    .rounded(px(3.))
                    .border_1()
                    .border_color(theme::gutter())
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_size(px(10.))
                    .when(checked, |tick| {
                        tick.bg(theme::blue()).border_color(theme::blue()).text_color(theme::bg_dark()).child("✓")
                    })
                    .into_any_element(),
                (None, Some(start)) => div()
                    .font_family("JetBrains Mono")
                    .text_size(px(12.5))
                    .text_color(theme::comment())
                    .child(format!("{}.", start + index as u64))
                    .into_any_element(),
                (None, None) => div().text_color(theme::comment()).child("•").into_any_element(),
            };
            let body = self.render(&item.blocks);
            list = list.child(
                div()
                    .flex()
                    .items_start()
                    .gap(px(8.))
                    .child(div().flex_none().min_w(px(18.)).flex().justify_end().child(marker))
                    .child(div().flex_1().min_w(px(0.)).flex().flex_col().gap(px(5.)).children(body)),
            );
        }
        list.into_any_element()
    }

    fn table(&mut self, header: &[Vec<Span>], rows: &[Vec<Vec<Span>>]) -> AnyElement {
        let mut head = div().flex().bg(theme::bg_highlight().alpha(0.5));
        for cell in header {
            let text = self.inline(cell, FontWeight::SEMIBOLD, theme::fg());
            head = head.child(div().flex_1().px(px(10.)).py(px(6.)).child(text));
        }
        let mut table = div()
            .rounded(px(8.))
            .border_1()
            .border_color(theme::gutter().alpha(0.5))
            .overflow_hidden()
            .flex()
            .flex_col()
            .text_size(px(13.5))
            .child(head);
        for row in rows {
            let mut line = div().flex().border_t_1().border_color(theme::gutter().alpha(0.4));
            for cell in row {
                let text = self.inline(cell, FontWeight::NORMAL, theme::fg_dark());
                line = line.child(div().flex_1().px(px(10.)).py(px(6.)).child(text));
            }
            table = table.child(line);
        }
        table.into_any_element()
    }

    fn image(&mut self, url: &str, alt: &str) -> AnyElement {
        if url.contains("://") {
            // The prototype has no HTTP client; a remote picture stays a link.
            let text = if alt.is_empty() { url } else { alt };
            let span = Span { text: text.to_string(), link: Some(url.to_string()), ..Span::default() };
            return self.inline(&[span], FontWeight::NORMAL, theme::fg());
        }
        let path = self.dir.join(url);
        if !path.exists() {
            return error_card(format!("picture not found: {}", path.display())).into_any_element();
        }
        div()
            .flex()
            .flex_col()
            .gap(px(4.))
            .child(img(path).max_w_full().rounded(px(8.)))
            .when(!alt.is_empty(), |figure| {
                figure.child(div().text_size(px(11.5)).text_color(theme::comment()).child(alt.to_string()))
            })
            .into_any_element()
    }

    fn mermaid(&mut self, source: &str) -> AnyElement {
        let mut hasher = DefaultHasher::new();
        source.hash(&mut hasher);
        let key = hasher.finish();
        if !self.diagrams.contains_key(&key) {
            self.diagrams.insert(key, render_diagram(source, key));
        }
        let outcome = match &self.diagrams[&key] {
            Diagram::Ready { path, width, height } => Ok((path.clone(), *width, *height)),
            Diagram::Failed(error) => Err(error.clone()),
        };
        match outcome {
            Ok((path, width, height)) => {
                let scale = (f32::from(self.width) / width).min(1.);
                div()
                    .flex()
                    .justify_center()
                    .py(px(4.))
                    .child(img(path).w(px(width * scale)).h(px(height * scale)))
                    .into_any_element()
            }
            Err(error) => {
                let source_card = self.code(Some("mermaid"), source);
                div()
                    .flex()
                    .flex_col()
                    .gap(px(6.))
                    .child(error_card(format!("mermaid: {error}")))
                    .child(source_card)
                    .into_any_element()
            }
        }
    }

    fn id(&mut self) -> u64 {
        self.next_id += 1;
        self.next_id
    }
}

pub fn error_card(message: String) -> Div {
    div()
        .rounded(px(8.))
        .bg(theme::red().alpha(0.08))
        .border_1()
        .border_color(theme::red().alpha(0.35))
        .px(px(12.))
        .py(px(8.))
        .text_size(px(12.))
        .text_color(theme::red())
        .child(message)
}

fn render_diagram(source: &str, key: u64) -> Diagram {
    match mermaid::render_svg(source, &theme::mermaid_palette()) {
        Ok(svg) => {
            let (width, height) = mermaid::svg_size(&svg).unwrap_or((480., 240.));
            let dir = std::env::temp_dir().join("attn-zero-diagrams");
            let path = dir.join(format!("{key:016x}.svg"));
            match std::fs::create_dir_all(&dir).and_then(|_| std::fs::write(&path, svg)) {
                Ok(()) => Diagram::Ready { path, width, height },
                Err(error) => Diagram::Failed(format!("writing the diagram: {error}")),
            }
        }
        Err(error) => Diagram::Failed(format!("{error:#}")),
    }
}
