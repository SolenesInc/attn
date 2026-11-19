//! The controls the settings pages share, and the pages with little of their own.

use attn_ui::Tokens;
use attn_ui::type_;
use rune::{El, col, text};

/// The small print under a setting, and a block's gap.
pub(super) const CAPTION: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);
pub(super) const S4: f32 = 16.0;

/// A block's heading, as the Tauri app's `.settings-block h3`.
pub(super) const BLOCK_HEADING: rune::TextStyle = rune::TextStyle::new(13.0, 700.0);

/// A titled block of a page, as the Tauri app's `.settings-block`: its heading and what it is
/// for, then its controls.
pub(super) fn block(key: &str, title: &str, description: &str, t: &Tokens, body: Vec<El>) -> El {
    col()
        .key(format!("settings.block.{key}"))
        .role("group")
        .label(title.to_owned())
        .w_full()
        .gap(12.0)
        .pb(22.0)
        .child(
            col()
                .w_full()
                .gap(8.0)
                .child(
                    text(title.to_owned())
                        .font(BLOCK_HEADING)
                        .color(t.text_primary),
                )
                .children((!description.is_empty()).then(|| {
                    text(description.to_owned())
                        .font(type_::BODY)
                        .color(t.text_tertiary)
                        .wrap()
                })),
        )
        .child(col().w_full().gap(10.0).children(body))
}

/// A block that follows another on its page, set off by a rule as the Tauri app's
/// `.settings-block ~ .settings-block`.
pub(super) fn after_block(block: El, t: &Tokens) -> El {
    block.pt(20.0).border_sides([1.0, 0.0, 0.0, 0.0], t.border)
}

/// Blocks one after another, each after the first set off by a rule.
pub(super) fn blocks(blocks: Vec<El>, t: &Tokens) -> Vec<El> {
    blocks
        .into_iter()
        .enumerate()
        .map(|(i, block)| if i == 0 { block } else { after_block(block, t) })
        .collect()
}

/// The kit the settings pages build with: the Tauri app's settings paint primary actions in
/// `warning`, where the rest of the app paints them in `brand`.
pub(super) fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    attn_ui::kit(&Tokens {
        brand: t.warning,
        ..*t
    })
}

/// A line that says a list or a page has nothing in it, as the Tauri app's `.settings-empty`.
pub(super) fn empty(said: impl Into<String>, t: &Tokens) -> El {
    text(said.into()).font(type_::BODY).color(t.text_tertiary)
}

/// A hint beside a control, as the Tauri app's `.settings-hint`.
pub(super) fn hint(said: impl Into<String>, t: &Tokens) -> El {
    text(said.into()).font(CAPTION).color(t.text_muted)
}

/// A warning line, as the Tauri pages show a plugin's trouble.
pub(super) fn warning(key: String, message: String, t: &Tokens) -> El {
    text(message.clone())
        .key(key)
        .role("alert")
        .label(message)
        .font(CAPTION)
        .color(t.warning)
        .wrap()
}
