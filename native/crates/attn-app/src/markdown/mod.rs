//! A markdown tile's body: the document the daemon reads from disk, rendered.

mod anchoring;
pub(crate) mod anchors;
pub(crate) mod annotating;
pub(crate) mod drafts;
mod front_matter;
pub(crate) use front_matter::{Value as FrontMatterValue, entries as front_matter_entries};
pub(crate) mod images;
pub(crate) mod links;
mod marks;
mod passages;
pub(crate) mod sending;

use std::collections::HashMap;
use std::rc::Rc;

use attn_ui::type_;
use attn_ui::{GlyphButton, Tokens, glyph};
use rune::tok::{radius, space};
use rune::{Color, El, Image, LinkPaint, ScrollSpec, Span, TextStyle, Tx, col, memo, text};
use rune_kit::ScrollBar;
use rune_kit::{CodeFrame, ImageFrame, Markdown, MarkdownStyle, MissingImage};

use crate::State;

pub use annotating::Readings;
pub use drafts::Drafts;
pub use images::Pictures;
pub(crate) use images::{follow as follow_images, over as with_image_preview};
pub(crate) use links::Document;

/// The reading column's width, as the Tauri reader's card caps it.
const CARD_MAX_WIDTH: f32 = 832.0;
const CARD_BORDER: f32 = 1.0;

/// The Tauri reader's card padding for the room inside the tile's gutter: 1.25rem, then 2, 2.5
/// and 3rem from 768, 1024 and 1280 points (its CSS container queries, at a 14 point rem).
fn card_padding(room: f32) -> f32 {
    match room {
        r if r >= 1280.0 => 42.0,
        r if r >= 1024.0 => 35.0,
        r if r >= 768.0 => 28.0,
        _ => 17.5,
    }
}

/// The card's padding and the text column inside it, in a document view `area` points across.
fn card_fit(area: Option<u32>) -> (f32, Option<f32>) {
    let Some(area) = area else {
        return (card_padding(0.0), None);
    };
    let room = area as f32 - 2.0 * space::S3;
    let padding = card_padding(room);
    let text = room.min(CARD_MAX_WIDTH) - 2.0 * (padding + CARD_BORDER);
    (padding, Some(text.max(0.0)))
}

/// The body of the document tile `tile`, opened for `session`, from what the daemon last sent
/// for it. `outposts` names the outpost a document is on.
pub(crate) fn body(
    tile: &str,
    title: &str,
    session: Option<&str>,
    content: Option<&attn_client::TileContent>,
    (outposts, servers): (&[attn_protocol::OutpostInfo], crate::review::setup::Servers),
    (pictures, drafts, readings, annotates): (&Pictures, &Drafts, &Readings, bool),
    t: &Tokens,
) -> El {
    let Some(attn_client::TileContent {
        revision,
        message: content,
    }) = content
    else {
        return message(t, "Loading…", false);
    };
    if let Some(error) = content.error.as_deref().filter(|e| !e.is_empty()) {
        return message(t, error, true);
    }
    if content.content.trim().is_empty() {
        return message(t, "This file is empty.", false);
    }
    let view = format!("tile.{tile}.document");
    let outpost = (content.host.as_deref().filter(|h| !h.is_empty())).map(|host| links::Outpost {
        host: host.to_owned(),
        name: crate::desktop::outpost_name(host, outposts),
    });
    let uri = drafts::Source::File {
        path: content.path.clone(),
        host: outpost.as_ref().map(|o| o.host.clone()),
    }
    .uri();
    let marks = annotating::highlights(drafts.get(&uri), readings.tiles.get(tile), tile, t);
    let kept = readings.kept(&view);
    let (padding, text_width) = card_fit(kept.width);
    let version = (
        *revision,
        title,
        session,
        outpost.as_ref().map(|o| o.name.clone()),
        pictures.revision,
        (marks.clone(), kept.clone()),
        *t,
    );
    let scroll = |card: El| {
        col()
            .key(view.clone())
            .role("document")
            .label(title)
            .w_full()
            .grow(1.0)
            .min_h(0.0)
            .scroll(ScrollSpec {
                stick_bottom: false,
            })
            .padding(space::S3)
            .items_center()
            .on_layout_tx({
                let view = view.clone();
                move |state: &mut State, [_, _, w, _], tx: &mut Tx| {
                    state.readings.measured(&view, w, tx)
                }
            })
            .child(card)
            .scroll_bar_styled(attn_ui::document_scroll_bar(t))
    };
    let card = |reading: El| {
        col()
            .w_full()
            .max_w(CARD_MAX_WIDTH)
            .padding(padding)
            .radius(radius::LG)
            .fill(t.bg_panel)
            .border(CARD_BORDER, t.border.alpha(0.5))
            .child(reading)
    };
    let document = links::Document {
        path: content.path.clone(),
        session: session.map(str::to_owned),
        outpost: outpost.clone(),
        view: view.clone(),
        opens_seed: None,
    };
    let read_source = |source: &str, marking: Option<Marking>| {
        let pictures = pictures.of(outpost.as_ref().map(|o| o.host.as_str()));
        reader(
            source,
            document.clone(),
            pictures,
            kept.clone(),
            marking,
            text_width.map_or_else(|| look(t), |width| look(t).text_width(width)),
            t,
        )
    };
    if let Some(bundle) = content.review.as_ref() {
        let read_part = |part: std::ops::Range<usize>| {
            let marking = annotates.then(|| Marking {
                tile: tile.to_owned(),
                uri: uri.clone(),
                highlights: within(&marks, &part),
                part: Some(part.start),
            });
            read_source(&content.content[part], marking)
        };
        let card = crate::review::card_key(tile);
        let marking = if annotates {
            annotating::in_document(readings, tile, &card, 0.0, t)
        } else {
            Vec::new()
        };
        return crate::review::body(
            tile,
            &view,
            title,
            (&content.content, bundle, *revision),
            readings,
            (
                &document,
                &pictures.of(outpost.as_ref().map(|o| o.host.as_str())),
                pictures.revision,
            ),
            (&read_part, {
                let mut h = std::hash::DefaultHasher::new();
                std::hash::Hash::hash(
                    &(session, &version.3, version.4, &kept, &marks, annotates),
                    &mut h,
                );
                std::hash::Hasher::finish(&h)
            }),
            marking,
            drafts.get(&uri).map_or(&[][..], |d| &d.marks[..]),
            (content.host.as_deref().filter(|h| !h.is_empty()), servers),
            t,
        );
    }
    let read = |marking: Option<Marking>| read_source(&content.content, marking);
    if !annotates {
        return memo(view.clone(), version, || scroll(card(read(None))));
    }
    let root = annotating::root_key(tile);
    let reading = memo(root.clone(), version, || {
        read(Some(Marking {
            tile: tile.to_owned(),
            uri,
            highlights: marks,
            part: None,
        }))
    });
    let marking = annotating::in_document(readings, tile, &root, padding, t);
    scroll(card(reading).children(marking))
}

/// The marks a reader in tile `tile` paints over document `uri`, and where its selections go.
/// With `part`, the reader draws only the document's source from that byte on, and paints
/// marks by where they sit in it.
pub(crate) struct Marking {
    tile: String,
    uri: String,
    highlights: Vec<(Vec<std::ops::Range<usize>>, rune_kit::Highlight)>,
    part: Option<usize>,
}

/// The marks `highlights` paints on the document, as a reader of its bytes `part` alone
/// paints them.
fn within(
    highlights: &[(Vec<std::ops::Range<usize>>, rune_kit::Highlight)],
    part: &std::ops::Range<usize>,
) -> Vec<(Vec<std::ops::Range<usize>>, rune_kit::Highlight)> {
    highlights
        .iter()
        .filter_map(|(ranges, highlight)| {
            let inside: Vec<_> = ranges
                .iter()
                .filter(|r| r.start < part.end && part.start < r.end)
                .map(|r| r.start.max(part.start) - part.start..r.end.min(part.end) - part.start)
                .collect();
            (!inside.is_empty()).then(|| (inside, highlight.clone()))
        })
        .collect()
}

/// The reader of seed `seed`'s body in tile `tile`, with the marks `drafts` hold on it, where
/// people mark it as they mark a file: the reader, keyed so its marks' toolbar and comment box
/// find their passage, then those, placed over it.
pub(crate) fn seed_reader(
    (drafts, readings): (&Drafts, &Readings),
    tile: &str,
    seed: &str,
    version: impl std::hash::Hash,
    read: impl FnOnce(Option<Marking>) -> El,
    t: &Tokens,
) -> (El, Vec<El>) {
    let uri = drafts::Source::Seed(seed.to_owned()).uri();
    let highlights = annotating::highlights(drafts.get(&uri), readings.tiles.get(tile), tile, t);
    let reading = memo(
        annotating::root_key(tile),
        (version, highlights.clone()),
        || {
            read(Some(Marking {
                tile: tile.to_owned(),
                uri,
                highlights,
                part: None,
            }))
        },
    );
    let marking = annotating::in_document(readings, tile, &annotating::root_key(tile), 0.0, t);
    (reading, marking)
}

/// `source` drawn in `style` as a document at `document.path` reads: its links go where the reader sends them,
/// its headings are keyed by their slugs for `#fragment` links, and its local images show the
/// ready `pictures` by the paths they resolve to, opening larger on a press. Its `<details>`
/// blocks open as their tags say, or as `folds` says people left them, by their order in the
/// document, and a press on a summary opens or closes its block. With `marking`, it paints
/// marks, a press on one picks it, and a selection let go opens the toolbar; a reader of a part
/// of the document sits in a column of its own, which whoever lays out the parts may key.
pub(crate) fn reader(
    source: &str,
    document: Document,
    pictures: Rc<HashMap<String, Option<Image>>>,
    kept: annotating::Kept,
    marking: Option<Marking>,
    style: MarkdownStyle,
    t: &Tokens,
) -> El {
    let mut document = document;
    let folded = document.view.clone();
    let copied_in = document.view.clone();
    let nth = details_order(source);
    let root = marking.as_ref().map(|m| match m.part {
        None => annotating::root_key(&m.tile),
        Some(start) => annotating::part_key(&m.tile, start),
    });
    if let Some(root) = &root {
        document.view = format!("{}/{root}", document.view);
    }
    let markdown = read_as(source)
        .details(
            {
                let nth = nth.clone();
                let folds = kept.folds;
                move |at, tag| folds.get(&nth(at)).copied().unwrap_or(tag)
            },
            move |state: &mut State, at, open, _: &mut Tx| {
                state.readings.fold(&folded, nth(at), open)
            },
        )
        .diagram_frame({
            let t = *t;
            move |card, frame| leaves_out(framed(card, frame, &t), frame, &t)
        })
        .diagram_source_frame({
            let t = *t;
            move |source, _| not_drawn(source, &t)
        })
        .code_frame({
            let (t, view, copied) = (*t, copied_in, kept.copied);
            move |card, frame| with_copy(card, frame, &view, copied, &t)
        })
        .front_matter_frame({
            let t = *t;
            move |_, yaml| front_matter::card(yaml, &t)
        });
    let markdown = linked(markdown, document, pictures, t);
    let markdown = markdown.style(style);
    let (
        Some(Marking {
            tile,
            uri,
            highlights,
            part,
        }),
        Some(root),
    ) = (marking, root)
    else {
        return markdown.build(attn_ui::kit(t));
    };
    let pressed = tile.clone();
    let in_part = part.is_some();
    let part = part.map(|start| (start, source.to_owned()));
    let reading = markdown
        .highlights(highlights)
        .on_highlight(move |state: &mut State, key: &str, _: &mut Tx| {
            annotating::mark_pressed(state, &pressed, key)
        })
        .build(attn_ui::kit(t))
        .key(root)
        .on_selection_change(move |state: &mut State, change, tx: &mut Tx| {
            let part = part
                .as_ref()
                .map(|(start, source)| (*start, source.as_str()));
            annotating::selection_changed(state, &tile, &uri, part, change, tx)
        });
    if in_part {
        col().w_full().child(reading)
    } else {
        reading
    }
}

/// `markdown` read as a document at `document.path`: its links go where the reader sends them
/// and read as text where it does not follow them, and its local images show the ready
/// `pictures` by the paths they resolve to, opening larger on a press.
pub(crate) fn linked(
    markdown: Markdown,
    document: Document,
    pictures: Rc<HashMap<String, Option<Image>>>,
    t: &Tokens,
) -> Markdown {
    let path = document.path.clone();
    let named_path = path.clone();
    let help = document.clone();
    let plain = document.clone();
    let text_color = t.text_primary;
    markdown
        .on_link_tx(move |state: &mut State, href: &str, tx: &mut Tx| {
            links::follow(state, &document, href, tx)
        })
        .link_help(move |href| help.verdict(href).help)
        .missing_image_frame({
            let (path, pictures, t) = (named_path, pictures.clone(), *t);
            move |_, image| {
                let failed = match links::resolve(&path, image.src) {
                    Some(links::Target::Local(file))
                        if !path.is_empty() && links::is_image(&file) =>
                    {
                        Some(matches!(pictures.get(&file), Some(None)))
                    }
                    _ => None,
                };
                missing(image, failed, &t)
            }
        })
        .images(move |src| match links::resolve(&path, src) {
            Some(links::Target::Local(file)) => pictures.get(&file).cloned().flatten(),
            _ => None,
        })
        .image_frame(|shown: El, frame: &ImageFrame<'_>| {
            let (image, alt) = (frame.image.clone(), frame.alt.to_owned());
            shown.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                images::show(state, image.clone(), &alt, tx)
            })
        })
        .link_style(move |href| {
            (!plain.verdict(href).follows).then(|| {
                (
                    Span::new(0, 0).color(text_color),
                    LinkPaint::new().color(text_color),
                )
            })
        })
}

/// Which of `source`'s `<details>` blocks the one whose tag starts at a source byte is, counted
/// from 0, so a block keeps its open state when the text above it changes.
fn details_order(source: &str) -> Rc<dyn Fn(usize) -> usize> {
    let starts: Vec<usize> = source
        .to_ascii_lowercase()
        .match_indices("<details")
        .map(|(at, _)| at)
        .collect();
    Rc::new(move |at| starts.partition_point(|&start| start < at))
}

/// The Tauri reader's type and marks (MarkdownReader.css, at its 14 point rem): 15 point
/// paragraphs, 19.25, 16.625 and 14 point headings, 14 point list items with their markers in
/// the accent, 13 point code that keeps its lines whole and scrolls sideways, tables across the
/// text with hairlines under their rows, round task checkboxes, and diagrams in mermaid's 16
/// point labels.
pub(crate) fn look(t: &Tokens) -> MarkdownStyle {
    let kit = attn_ui::kit(t);
    let body = TextStyle {
        size: 15.0,
        line_height: 24.4,
        ..kit.ty.body
    };
    let heading = |size: f32, weight: f32, line_height: f32| TextStyle {
        size,
        weight,
        line_height,
        ..body
    };
    let small = TextStyle {
        size: 14.0,
        line_height: 22.75,
        ..body
    };
    MarkdownStyle::from_kit(kit)
        .body_font(body)
        .heading_font(1, heading(19.25, 700.0, 25.0).tracking(-0.025 * 19.25))
        .heading_font(2, heading(16.625, 600.0, 23.3))
        .heading_font(3, heading(14.0, 600.0, 21.7))
        .heading_font(4, heading(14.0, 600.0, 21.7))
        .deep_heading(t.text_primary)
        .list_font(small)
        .list_marker(t.accent.alpha(0.6))
        .inline_code_font(12.25 / 15.0, 400.0)
        .inline_code_pad(5.25)
        .code_block_font(TextStyle {
            size: 13.0,
            line_height: 21.1,
            ..kit.ty.mono
        })
        .table_font(small)
        .table_outline(None)
        .table_header_rule(t.border)
        .table_rules(t.border.alpha(0.5))
        .table_header_fill(t.bg_elevated.alpha(0.3))
        .table_header_weight(600.0)
        .table_cell_padding(10.5, 7.0)
        .table_margin(14.0, 14.0)
        .table_full_width(true)
        .table_scroll_bar(t.border, t.text_tertiary)
        .diagram_font(TextStyle {
            size: 16.0,
            line_height: 24.0,
            ..body
        })
        .task_checkbox(
            16.0,
            8.0,
            8.0,
            None,
            t.success,
            t.bg_app,
            Color::TRANSPARENT,
            t.text_secondary.alpha(0.5),
        )
}

/// An image with no picture to show, in the Tauri reader's words: for a document's local file,
/// missing when its read `failed` and loading until then; blocked for anything else.
fn missing(image: &MissingImage<'_>, failed: Option<bool>, t: &Tokens) -> El {
    let named = if image.alt.is_empty() {
        image.src.rsplit('/').next().unwrap_or(image.src)
    } else {
        image.alt
    };
    let words = match failed {
        Some(true) => format!("[missing image: {named}]"),
        Some(false) => format!("[loading image: {named}]"),
        None => {
            let named = [image.alt, image.src]
                .into_iter()
                .find(|s| !s.is_empty())
                .unwrap_or("unknown source");
            format!("[blocked image: {named}]")
        }
    };
    text(words)
        .font(TextStyle {
            size: 15.0,
            line_height: 24.4,
            italic: true,
            ..attn_ui::kit(t).ty.body
        })
        .color(t.text_tertiary)
        .wrap()
}

/// `source` as the reader reads it, which a mark's passage is found in.
fn read_as(source: &str) -> Markdown {
    Markdown::new(source.to_owned())
        .selectable(true)
        .smart_punctuation(true)
        .block_keys(anchors::heading_keys(source))
        .scroll_wide(true)
}

/// A code block's card with a button that copies its code, shown while the pointer is over the
/// card, and saying "Copied!" a while after a copy.
fn with_copy(card: El, frame: &CodeFrame<'_>, view: &str, copied: Option<usize>, t: &Tokens) -> El {
    if frame.open {
        return card;
    }
    let done = copied == Some(frame.index);
    let (glyph, label) = if done {
        (glyph::CHECK, "Copied!")
    } else {
        (glyph::COPY, "Copy code")
    };
    let (code, view, index) = (frame.source.to_owned(), view.to_owned(), frame.index);
    let button = GlyphButton::new(glyph, label)
        .help(label)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            tx.copy(code.clone());
            state.readings.copied(&view, index, tx);
        })
        .build(attn_ui::kit(t))
        .key("copy")
        .pin(None, Some(space::S2), Some(space::S2), None);
    let button = if done {
        button.color(t.success)
    } else {
        button.group_opacity(0.0, 1.0)
    };
    col().w_full().hover_group().child(card).child(button)
}

/// A diagram that cannot draw, shown as its source under a note that says so.
fn not_drawn(source: El, t: &Tokens) -> El {
    col()
        .w_full()
        .gap(space::S1)
        .child(
            text("Diagram failed to render")
                .font(type_::LABEL)
                .color(t.error),
        )
        .child(source)
}

/// The widest a diagram's card draws in a document, the kit's default.
const DIAGRAM_ROOM: f32 = 680.0;
/// A diagram whose card would draw it smaller than this share of its natural size shows at its
/// natural size instead, scrolling, as the Tauri reader's `OVERSIZED_FIT_RATIO` does.
const LEAST_FIT: f32 = 0.8;
/// `source` at its natural size, laid out for the room a document gives it, so a diagram that
/// turns top-down to fit that room turns here too.
pub(crate) fn natural(source: &str, t: &Tokens) -> Option<rune_kit::mermaid::Drawing> {
    rune_kit::mermaid::drawing(source, &look(t).diagram_min_scale(1.0), DIAGRAM_ROOM)
}

/// The tallest a large diagram's scroll box grows, as the Tauri reader's `min(52vh, 410px)`.
const LARGE_HEIGHT: f32 = 410.0;

/// A diagram as the Tauri reader frames it: a `diagram` bar over its card, and a diagram too
/// wide to read fitted shows at its natural size in a box that scrolls, its bar saying so and
/// offering to focus it, which opens it over the window to zoom and pan.
fn framed(card: El, frame: &rune_kit::DiagramFrame<'_>, t: &Tokens) -> El {
    let style = look(t);
    let large = (!frame.open)
        .then(|| rune_kit::mermaid::drawing(frame.source, &style, DIAGRAM_ROOM))
        .flatten()
        .filter(|fitted| fitted.scale < LEAST_FIT)
        .and_then(|_| natural(frame.source, t));
    let label = text("diagram")
        .font(TextStyle {
            size: 11.0,
            line_height: 16.0,
            tracking: 0.2,
            ..attn_ui::kit(t).ty.mono
        })
        .color(t.text_tertiary);
    let Some(drawing) = large else {
        return col().w_full().gap(space::S1).child(label).child(card);
    };
    let (source, size) = (frame.source.to_owned(), drawing.size);
    let bar = rune::row()
        .w_full()
        .items_center()
        .gap(space::S2)
        .child(label.grow(1.0))
        .child(
            rune::row()
                .items_center()
                .gap(space::S1)
                .child(rune::el().size(6.0, 6.0).radius(3.0).fill(t.warning))
                .child(
                    text("Large diagram · 100%")
                        .font(TextStyle::new(11.0, 400.0))
                        .color(t.text_tertiary),
                ),
        )
        .child(
            rune_kit::Button::new("Focus diagram")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    images::show_diagram(state, &source, size, tx)
                })
                .build(attn_ui::kit(t))
                .key("focus"),
        );
    let scrolled = col()
        .key("large")
        .w_full()
        .max_h(LARGE_HEIGHT)
        .padding(16.0)
        .radius(radius::MD)
        .border(1.0, drawing.surface_border)
        .fill(drawing.surface)
        .scroll_xy()
        .child(drawing.el);
    col().w_full().gap(space::S1).child(bar).child(scrolled)
}

/// A drawn diagram, with what it leaves out because it cannot read it said under it.
fn leaves_out(card: El, frame: &rune_kit::DiagramFrame<'_>, t: &Tokens) -> El {
    let skipped = if frame.open {
        Vec::new()
    } else {
        rune_kit::mermaid::skipped(frame.source)
    };
    if skipped.is_empty() {
        return card;
    }
    col()
        .w_full()
        .gap(space::S1)
        .child(card)
        .child(
            text("This diagram leaves out what it cannot read:")
                .font(type_::LABEL)
                .color(t.error),
        )
        .children(
            skipped
                .into_iter()
                .map(|statement| text(statement).font(type_::MONO_CAPTION).color(t.error)),
        )
}

fn message(t: &Tokens, message: &str, error: bool) -> El {
    col()
        .w_full()
        .h_full()
        .center()
        .child(
            text(message)
                .font(type_::BODY)
                .color(if error { t.error } else { t.text_tertiary }),
        )
}
