//! The text styles attn draws with. Views take them from here rather than from `rune::tok`, so
//! attn's type follows the Tauri app's CSS in one place.

use rune::{El, TextStyle, text};

pub use rune::tok::type_::{BODY, LABEL, SECTION, TITLE};

/// The Tauri app's small text, `font-size: var(--font-size-sm)`: what a surface says about
/// itself, such as states, counts, ages and who did what. The native app draws it a point larger.
pub const CAPTION: TextStyle = TextStyle::new(12.0, 400.0);

/// The Tauri app's small monospace text, `font-family: 'JetBrains Mono', …`: key hints, ids,
/// paths, counts, ages and other values read as data rather than prose.
pub const MONO_CAPTION: TextStyle = TextStyle::new(12.0, 400.0).mono();

/// The Tauri app's uppercase monospace headings and labels, `text-transform: uppercase;
/// letter-spacing: .04em–.16em`: section headings, kickers, field labels and badges.
pub const CAPS: TextStyle = SECTION.mono().tracking(1.0);

/// [`CAPS`] at bold weight, as the Tauri app's settings labels and panel titles.
pub const BOLD_CAPS: TextStyle = TextStyle {
    weight: 700.0,
    ..CAPS
};

/// The Tauri app's small bordered tags and pills, such as a tile's kind and a settings page's
/// state: bold uppercase monospace.
pub const BADGE: TextStyle = TextStyle::new(12.0, 700.0).mono().tracking(0.6);

/// The Tauri app's uppercase labels in the interface face, `text-transform: uppercase;
/// letter-spacing: .04em–.1em` with no monospace: group labels and small titles.
pub const SANS_CAPS: TextStyle = TextStyle::new(11.5, 400.0).tracking(0.9);

/// The title of a drawer or panel, as the Tauri app's drawer headers: uppercase monospace at
/// medium weight, widely spaced.
pub const DRAWER_TITLE: TextStyle = TextStyle {
    weight: 500.0,
    tracking: 1.7,
    ..CAPS
};

/// `label` drawn uppercase in `style`, as CSS `text-transform: uppercase` draws it: the label
/// it reads as stays `label`.
pub fn upper(label: &str, style: TextStyle) -> El {
    text(label.to_uppercase()).label(label).font(style)
}

/// `label` as one of the Tauri app's uppercase monospace headings, in [`CAPS`].
pub fn caps(label: &str) -> El {
    upper(label, CAPS)
}

/// `label` as one of the Tauri app's uppercase labels in the interface face, in [`SANS_CAPS`].
pub fn sans_caps(label: &str) -> El {
    upper(label, SANS_CAPS)
}
