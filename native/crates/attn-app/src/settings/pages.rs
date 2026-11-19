//! The controls the settings pages share, and the pages with little of their own.

use attn_ui::Tokens;
use attn_ui::system::Button;
use attn_ui::type_;
use rune::{El, Tx, col, row, text};

pub(super) use super::controls::{
    feature, feature_note, feature_switch, number_field, refusal, refusal_beside, segmented,
    setting_arrows,
};
use crate::State;

/// The small print under a setting, and a block's gap.
pub(super) const CAPTION: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);
pub(super) const S4: f32 = 16.0;

/// A block's heading, as the Tauri app's `.settings-block h3`.
pub(super) const BLOCK_HEADING: rune::TextStyle = rune::TextStyle::new(13.0, 700.0);
/// A row card's title, as the Tauri app's `.settings-row-title`.
const ROW_TITLE: rune::TextStyle = rune::TextStyle::new(13.0, 750.0);

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

/// The small print under a row card's title, as the Tauri app's `.settings-row-copy`.
pub(super) fn row_copy(copy: impl Into<String>, t: &Tokens) -> El {
    text(copy.into())
        .font(CAPTION)
        .color(t.text_secondary)
        .wrap()
}

/// A row card's title and its small print.
pub(super) fn row_words(title: &str, copy: &str, t: &Tokens) -> El {
    col()
        .grow(1.0)
        .min_w(0.0)
        .gap(4.0)
        .child(text(title.to_owned()).font(ROW_TITLE).color(t.text_primary))
        .children((!copy.is_empty()).then(|| row_copy(copy, t)))
}

/// The Tauri app's `.settings-row-card`: one setting on the page's raised surface, its name
/// and explanation on the left, its control on the right.
pub(super) fn row_card(title: &str, copy: &str, t: &Tokens, control: El) -> El {
    card_surface(row(), t)
        .w_full()
        .min_h(56.0)
        .gap(S4)
        .items_center()
        .py(12.0)
        .px(14.0)
        .child(row_words(title, copy, t))
        .child(control)
}

/// The raised surface of a settings card, as the Tauri app's row, plugin and price cards.
pub(super) fn card_surface(el: El, t: &Tokens) -> El {
    el.radius(7.0).fill(t.bg_elevated).border(1.0, t.border)
}

/// One setting as a plain row on the page, as the Tauri app's `.settings-default-row`: its
/// words, from [`row_words`], on the left and its control on the right.
pub(super) fn plain_row(words: El, control: El) -> El {
    row()
        .w_full()
        .gap(24.0)
        .items_center()
        .py(8.0)
        .child(words)
        .child(control)
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

/// A button that reads Enable or Disable and is announced as the switch `name`, as the Tauri
/// app's `.settings-action[role=switch]`.
pub(super) fn toggle(
    key: &str,
    name: &str,
    on: bool,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    El::from(
        Button::new(if on { "Disable" } else { "Enable" })
            .disabled(disabled)
            .on_press_tx(press)
            .build(t)
            .key(key),
    )
    .role("switch")
    .label(name.to_owned())
    .checked(on)
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
