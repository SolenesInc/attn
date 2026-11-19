//! The controls the settings pages share, and the pages with little of their own.

use attn_ui::Tokens;
use attn_ui::type_;
use rune::{El, text};

/// The small print under a setting, and a block's gap.
pub(super) const CAPTION: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);
pub(super) const S4: f32 = 16.0;

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
