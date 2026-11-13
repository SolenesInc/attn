//! A markdown tile's body: the document the daemon reads from disk, rendered.

use attn_ui::Tokens;
use rune::tok::{radius, space, type_};
use rune::{El, ScrollSpec, col, memo, text};
use rune_kit::Markdown;

/// The reading column's width, as the Tauri reader's card caps it.
const CARD_MAX_WIDTH: f32 = 832.0;
const CARD_PADDING: f32 = 20.0;

/// The body of the document tile `tile`, from what the daemon last sent for it.
pub(crate) fn body(
    tile: &str,
    title: &str,
    content: Option<&attn_client::TileContent>,
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
    let version = (*revision, title, *t);
    memo(format!("tile.{tile}.document"), version, || {
        col()
            .key(format!("tile.{tile}.document"))
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
                    .child(
                        Markdown::new(content.content.clone())
                            .selectable(true)
                            .build(&attn_ui::kit(t)),
                    ),
            )
    })
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
