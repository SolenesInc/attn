//! The controls the settings pages share, and the pages with little of their own.

use attn_ui::Tokens;

/// The small print under a setting, and a block's gap.
pub(super) const CAPTION: rune::TextStyle = rune::TextStyle::new(12.0, 400.0);

/// The kit the settings pages build with: the Tauri app's settings paint primary actions in
/// `warning`, where the rest of the app paints them in `brand`.
pub(super) fn kit(t: &Tokens) -> &'static rune_kit::Kit {
    attn_ui::kit(&Tokens {
        brand: t.warning,
        ..*t
    })
}
