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

use std::collections::{BTreeMap, HashMap};
use std::rc::Rc;

use attn_ui::Tokens;
use rune::tok::{radius, space, type_};
use rune::{El, Image, LinkPaint, ScrollSpec, Span, Tx, col, memo, text};
use rune_kit::{ImageFrame, Markdown};

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
    let uri = drafts::Source {
        path: content.path.clone(),
        host: outpost.as_ref().map(|o| o.host.clone()),
    }
    .uri();
    let marks = annotating::highlights(drafts.get(&uri), readings.tiles.get(tile), tile, t);
    let folds = readings.folds(&view);
    let version = (
        *revision,
        title,
        session,
        outpost.as_ref().map(|o| o.name.clone()),
        pictures.revision,
        (marks.clone(), folds.clone()),
        *t,
    );
    memo(view.clone(), version, || {
        let pictures = pictures.of(outpost.as_ref().map(|o| o.host.as_str()));
        let document = links::Document {
            path: content.path.clone(),
            session: session.map(str::to_owned),
            outpost,
            view: view.clone(),
        };
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
            .child(
                col()
                    .w_full()
                    .max_w(CARD_MAX_WIDTH)
                    .padding(CARD_PADDING)
                    .radius(radius::LG)
                    .fill(t.bg_panel)
                    .border(1.0, t.border.alpha(0.5))
                    .child(reader(
                        &content.content,
                        document,
                        pictures,
                        folds,
                        annotates.then(|| Marking {
                            tile: tile.to_owned(),
                            uri,
                            highlights: marks,
                        }),
                        t,
                    )),
            )
    })
}

/// The marks a reader in tile `tile` paints over document `uri`, and where its selections go.
pub(crate) struct Marking {
    tile: String,
    uri: String,
    highlights: Vec<(Vec<std::ops::Range<usize>>, rune_kit::Highlight)>,
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
    folds: BTreeMap<usize, bool>,
    marking: Option<Marking>,
    t: &Tokens,
) -> El {
    let mut document = document;
    let folded = document.view.clone();
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

/// `source` as the reader reads it, which a mark's passage is found in.
fn read_as(source: &str) -> Markdown {
    Markdown::new(source.to_owned())
        .selectable(true)
        .smart_punctuation(true)
        .block_keys(anchors::heading_keys(source))
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
