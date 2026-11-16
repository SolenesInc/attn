//! Every color attn draws, as color schemes: each a set of colors for dark and one for light.
//! This module alone builds colors; clippy refuses Rune's color constructors anywhere else
//! (`native/clippy.toml`).
#![allow(clippy::disallowed_methods)]

use std::sync::LazyLock;

use rune::Color;

use crate::Tokens;

mod attn;

/// An sRGB color as its three bytes, as a terminal's cells carry it.
pub type Rgb = [u8; 3];

pub struct ColorScheme {
    pub id: &'static str,
    pub name: &'static str,
    pub dark: Colors,
    pub light: Colors,
}

/// Everything drawn in one appearance: the interface's tokens and the terminals' colors.
#[derive(Clone, Copy, Debug)]
pub struct Colors {
    pub ui: Tokens,
    pub terminal: TerminalColors,
}

/// What a terminal draws with until its program sets its own colors, and what lies over its
/// cells.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct TerminalColors {
    pub fg: Rgb,
    pub bg: Rgb,
    pub cursor: Rgb,
    pub selection: Rgb,
    pub find: Rgb,
    pub annotation: Rgb,
    /// A command block's wash and outline.
    pub block: Rgb,
    /// A seed id's mark.
    pub seed: Rgb,
    pub ansi: [Rgb; 16],
}

/// The terminal colors before the window first draws: the default scheme's, in dark.
impl Default for TerminalColors {
    fn default() -> TerminalColors {
        scheme_for(None).dark.terminal
    }
}

pub static SCHEMES: LazyLock<Vec<ColorScheme>> = LazyLock::new(|| vec![attn::scheme()]);

/// The scheme the `color_scheme` setting names; attn's when it names none or one attn lacks.
pub fn scheme_for(setting: Option<&str>) -> &'static ColorScheme {
    SCHEMES
        .iter()
        .find(|s| Some(s.id) == setting)
        .unwrap_or(&SCHEMES[0])
}

impl ColorScheme {
    pub fn colors(&self, dark: bool) -> &Colors {
        if dark { &self.dark } else { &self.light }
    }
}

pub fn rgb([r, g, b]: Rgb) -> Color {
    c(&to_hex([r, g, b]))
}

/// `#rrggbb`, as the daemon takes a terminal color.
pub fn to_hex([r, g, b]: Rgb) -> String {
    format!("#{r:02x}{g:02x}{b:02x}")
}

/// `t` of the way from `a` to `b` in encoded sRGB, as CSS `color-mix(in srgb, ..)`.
pub fn mix_srgb(a: Color, b: Color, t: f32) -> Color {
    let m = |x: f32, y: f32| {
        let (x, y) = (rune::color::srgb_encode(x), rune::color::srgb_encode(y));
        rune::color::srgb_decode(x + (y - x) * t)
    };
    Color {
        r: m(a.r, b.r),
        g: m(a.g, b.g),
        b: m(a.b, b.b),
        a: a.a + (b.a - a.a) * t,
    }
}

fn c(value: &str) -> Color {
    rune::color::hex(value).unwrap_or_else(|| panic!("token {value:?} is not a hex color"))
}
