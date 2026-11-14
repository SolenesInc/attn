//! A markdown tile's body: the document the daemon reads from disk, rendered.

mod anchoring;
mod anchors;
pub(crate) mod annotating;
pub(crate) mod drafts;
mod front_matter;
mod images;
pub(crate) mod links;
mod marks;
mod passages;
pub(crate) mod sending;

use std::collections::HashMap;
use std::rc::Rc;

use attn_ui::{GlyphButton, Tokens, icon};
use rune::tok::{radius, space, type_};
use rune::{Color, El, Image, LinkPaint, ScrollSpec, Span, TextStyle, Tx, col, memo, text};
use rune_kit::{CodeFrame, ImageFrame, Markdown, MarkdownStyle};

use crate::State;

pub use annotating::Readings;
pub use drafts::Drafts;
pub use images::Pictures;
pub(crate) use images::{follow as follow_images, over as with_image_preview};
pub(crate) use links::Document;

/// The reading column's width, as the Tauri reader's card caps it.
const CARD_MAX_WIDTH: f32 = 832.0;
const CARD_PADDING: f32 = 20.0;

/// The body of the document tile `tile`, opened for `session`, from what the daemon last sent
/// for it. `outposts` names the outpost a document is on.
pub(crate) fn body(
    tile: &str,
    title: &str,
    session: Option<&str>,
    content: Option<&attn_client::TileContent>,
    outposts: &[attn_protocol::OutpostInfo],
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
            .child(card)
    };
    let card = |reading: El| {
        col()
            .w_full()
            .max_w(CARD_MAX_WIDTH)
            .padding(CARD_PADDING)
            .radius(radius::LG)
            .fill(t.bg_panel)
            .border(1.0, t.border.alpha(0.5))
            .child(reading)
    };
    let read = |marking: Option<Marking>| {
        let pictures = pictures.of(outpost.as_ref().map(|o| o.host.as_str()));
        let document = links::Document {
            path: content.path.clone(),
            session: session.map(str::to_owned),
            outpost: outpost.clone(),
            view: view.clone(),
        };
        reader(
            &content.content,
            document,
            pictures,
            kept.clone(),
            marking,
            t,
        )
    };
    if !annotates {
        return memo(view.clone(), version, || scroll(card(read(None))));
    }
    let root = annotating::root_key(tile);
    let reading = memo(root.clone(), version, || {
        read(Some(Marking {
            tile: tile.to_owned(),
            uri,
            highlights: marks,
        }))
    });
    scroll(card(reading).children(annotating::in_document(readings, tile, CARD_PADDING, t)))
}

/// The marks a reader in tile `tile` paints over document `uri`, and where its selections go.
pub(crate) struct Marking {
    tile: String,
    uri: String,
    highlights: Vec<(Vec<std::ops::Range<usize>>, rune_kit::Highlight)>,
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
            }))
        },
    );
    (reading, annotating::in_document(readings, tile, 0.0, t))
}

/// `source` as a document at `document.path` reads: its links go where the reader sends them,
/// its headings are keyed by their slugs for `#fragment` links, and its local images show the
/// ready `pictures` by the paths they resolve to, opening larger on a press. Its `<details>`
/// blocks open as their tags say, or as `folds` says people left them, by their order in the
/// document, and a press on a summary opens or closes its block. With `marking`, it paints
/// marks, a press on one picks it, and a selection let go opens the toolbar.
pub(crate) fn reader(
    source: &str,
    document: Document,
    pictures: Rc<HashMap<String, Image>>,
    kept: annotating::Kept,
    marking: Option<Marking>,
    t: &Tokens,
) -> El {
    let mut document = document;
    let folded = document.view.clone();
    let copied_in = document.view.clone();
    let nth = details_order(source);
    let root = marking.as_ref().map(|m| annotating::root_key(&m.tile));
    if let Some(root) = &root {
        document.view = format!("{}/{root}", document.view);
    }
    let path = document.path.clone();
    let verdict = {
        let path = document.path.clone();
        let outpost = document.outpost.as_ref().map(|o| o.name.clone());
        move |href: &str| links::verdict(links::resolve(&path, href).as_ref(), outpost.as_deref())
    };
    let help = verdict.clone();
    let plain = verdict;
    let text_color = t.text_primary;
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
        .on_link_tx(move |state: &mut State, href: &str, tx: &mut Tx| {
            links::follow(state, &document, href, tx)
        })
        .link_help(move |href| help(href).help)
        .images(move |src| match links::resolve(&path, src) {
            Some(links::Target::Local(file)) => pictures.get(&file).cloned(),
            _ => None,
        })
        .diagram_frame({
            let t = *t;
            move |card, frame| leaves_out(card, frame, &t)
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
        })
        .image_frame(|shown: El, frame: &ImageFrame<'_>| {
            let (image, alt) = (frame.image.clone(), frame.alt.to_owned());
            shown.on_press_tx(move |state: &mut State, tx: &mut Tx| {
                images::show(state, image.clone(), &alt, tx)
            })
        })
        .link_style(move |href| {
            (!plain(href).follows).then(|| {
                (
                    Span::new(0, 0).color(text_color),
                    LinkPaint::new().color(text_color),
                )
            })
        });
    let markdown = markdown.style(look(t));
    let (
        Some(Marking {
            tile,
            uri,
            highlights,
        }),
        Some(root),
    ) = (marking, root)
    else {
        return markdown.build(attn_ui::kit(t));
    };
    let pressed = tile.clone();
    markdown
        .highlights(highlights)
        .on_highlight(move |state: &mut State, key: &str, _: &mut Tx| {
            annotating::mark_pressed(state, &pressed, key)
        })
        .build(attn_ui::kit(t))
        .key(root)
        .on_selection_change(move |state: &mut State, change, tx: &mut Tx| {
            annotating::selection_changed(state, &tile, &uri, change, tx)
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

/// The Tauri reader's type and marks (MarkdownReader.css): 15 point paragraphs, 22, 19 and 16
/// point headings, 13 point code that keeps its lines whole and scrolls sideways, tables at
/// their columns' natural widths, and round task checkboxes.
fn look(t: &Tokens) -> MarkdownStyle {
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
    MarkdownStyle::from_kit(kit)
        .body_font(body)
        .heading_font(1, heading(22.0, 700.0, 28.6))
        .heading_font(2, heading(19.0, 600.0, 26.6))
        .heading_font(3, heading(16.0, 600.0, 24.8))
        .heading_font(4, heading(16.0, 600.0, 24.8))
        .inline_code_font(14.0 / 15.0, 400.0)
        .code_block_font(TextStyle {
            size: 13.0,
            line_height: 17.0,
            ..kit.ty.mono
        })
        .table_font(TextStyle {
            size: 14.0,
            line_height: 17.0,
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
        (icon::CHECK, "Copied!")
    } else {
        (icon::COPY, "Copy code")
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
