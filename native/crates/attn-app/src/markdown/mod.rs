//! A markdown tile's body: the document the daemon reads from disk, rendered.

mod anchors;
mod images;
mod links;

use std::collections::HashMap;
use std::rc::Rc;

use attn_ui::Tokens;
use rune::tok::{radius, space, type_};
use rune::{El, Image, LinkPaint, ScrollSpec, Span, Tx, col, memo, text};
use rune_kit::{ImageFrame, Markdown};

use crate::State;

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
    pictures: &Pictures,
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
    let version = (
        *revision,
        title,
        session,
        outpost.as_ref().map(|o| o.name.clone()),
        pictures.revision,
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
                    .child(reader(&content.content, document, pictures, t)),
            )
    })
}

/// `source` as a document at `document.path` reads: its links go where the reader sends them,
/// its headings are keyed by their slugs for `#fragment` links, and its local images show the
/// ready `pictures` by the paths they resolve to, opening larger on a press.
pub(crate) fn reader(
    source: &str,
    document: Document,
    pictures: Rc<HashMap<String, Image>>,
    t: &Tokens,
) -> El {
    let path = document.path.clone();
    let verdict = {
        let path = document.path.clone();
        let outpost = document.outpost.as_ref().map(|o| o.name.clone());
        move |href: &str| links::verdict(links::resolve(&path, href).as_ref(), outpost.as_deref())
    };
    let help = verdict.clone();
    let plain = verdict;
    let text_color = t.text_primary;
    Markdown::new(source.to_owned())
        .selectable(true)
        .block_keys(anchors::heading_keys(source))
        .on_link_tx(move |state: &mut State, href: &str, tx: &mut Tx| {
            links::follow(state, &document, href, tx)
        })
        .link_help(move |href| help(href).help)
        .images(move |src| match links::resolve(&path, src) {
            Some(links::Target::Local(file)) => pictures.get(&file).cloned(),
            _ => None,
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
        })
        .build(&attn_ui::kit(t))
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
