use rune::{Color, El};

use super::door;

/// How large an icon or a mark is drawn, named by what it sits beside. Only `attn-ui` turns a
/// size into points: a view names a size, never a number.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum IconSize {
    /// 14: beside `Small`, `Label` and `Tag` text.
    Small,
    /// 16: beside `Body` and `Name` text, and inside a control.
    Regular,
    /// 18: alone in an icon-only button.
    Large,
}

impl IconSize {
    pub(crate) const fn pt(self) -> f32 {
        match self {
            IconSize::Small => 14.0,
            IconSize::Regular => 16.0,
            IconSize::Large => 18.0,
        }
    }
}

/// `glyph` at `size` in `ink`.
pub fn icon(glyph: impl Into<rune::IconSource>, size: IconSize, ink: Color) -> El {
    door::icon(glyph, size).color(ink)
}

/// `glyph`, an icon or a mark drawn as text, at `size` in `ink`.
pub fn glyph(glyph: impl Into<crate::Glyph>, size: IconSize, ink: Color) -> El {
    match glyph.into() {
        crate::Glyph::Icon(drawn) => door::icon(drawn, size).color(ink),
        crate::Glyph::Text(said) => rune::row()
            .label(said.to_string())
            .size(size.pt(), size.pt())
            .center()
            .child(door::mark_text(said, size).color(ink)),
    }
}
