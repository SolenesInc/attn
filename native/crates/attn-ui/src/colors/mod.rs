//! Every color attn draws, as color schemes: each a set of colors for dark and one for light.
//! This module alone builds colors; clippy refuses Rune's color constructors anywhere else
//! (`native/clippy.toml`).
#![allow(clippy::disallowed_methods)]

use std::sync::LazyLock;

use rune::Color;
use serde_json::Value;

use crate::Tokens;

/// An sRGB color as its three bytes, as a terminal's cells carry it.
pub type Rgb = [u8; 3];

#[derive(Clone, Debug)]
pub struct ColorScheme {
    pub id: &'static str,
    pub name: String,
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

/// The scheme files the daemon checks settings against, attn first and then by id, as
/// `internal/colors` lists them.
const SCHEME_FILES: &[(&str, &str)] = &[
    (
        "attn",
        include_str!("../../../../../internal/colors/schemes/attn.json"),
    ),
    (
        "catppuccin",
        include_str!("../../../../../internal/colors/schemes/catppuccin.json"),
    ),
    (
        "catppuccin-frappe",
        include_str!("../../../../../internal/colors/schemes/catppuccin-frappe.json"),
    ),
];

pub static SCHEMES: LazyLock<Vec<ColorScheme>> = LazyLock::new(|| {
    SCHEME_FILES
        .iter()
        .map(|&(id, json)| ColorScheme::parse(id, json))
        .collect()
});

/// The scheme the `color_scheme` setting names; attn's when it names none or one attn lacks.
pub fn scheme_for(setting: Option<&str>) -> &'static ColorScheme {
    SCHEMES
        .iter()
        .find(|s| Some(s.id) == setting)
        .unwrap_or(&SCHEMES[0])
}

impl ColorScheme {
    fn parse(id: &'static str, json: &str) -> ColorScheme {
        let file: Value = serde_json::from_str(json)
            .unwrap_or_else(|e| panic!("color scheme {id} is not JSON: {e}"));
        ColorScheme {
            id,
            name: file["name"].as_str().unwrap_or(id).to_owned(),
            dark: Colors::layered(&file["dark"], true, None),
            light: Colors::layered(&file["light"], false, None),
        }
    }

    pub fn colors(&self, dark: bool) -> &Colors {
        if dark { &self.dark } else { &self.light }
    }

    /// This scheme under the `color_overrides` setting's colors: as it is where the setting
    /// names no color, or holds none the daemon would accept.
    pub fn with_overrides(&self, overrides: &str) -> ColorScheme {
        let Ok(layer) = serde_json::from_str::<Value>(overrides) else {
            return self.clone();
        };
        ColorScheme {
            dark: Colors::layered(&layer["dark"], true, Some(&self.dark)),
            light: Colors::layered(&layer["light"], false, Some(&self.light)),
            ..self.clone()
        }
    }
}

/// Whether the `color_overrides` setting names at least one color.
pub fn has_overrides(overrides: &str) -> bool {
    let Ok(Value::Object(appearances)) = serde_json::from_str::<Value>(overrides) else {
        return false;
    };
    appearances
        .values()
        .filter_map(Value::as_object)
        .flat_map(|groups| groups.values())
        .filter_map(Value::as_object)
        .any(|names| !names.is_empty())
}

impl Colors {
    /// The colors `layer` names, over `under` where it names none. A scheme file has no
    /// `under` and names every color.
    fn layered(layer: &Value, dark: bool, under: Option<&Colors>) -> Colors {
        Colors {
            ui: tokens(&layer["ui"], dark, under.map(|u| &u.ui)),
            terminal: terminal(&layer["terminal"], under.map(|u| &u.terminal)),
        }
    }
}

fn missing<T>(group: &str, name: &str) -> T {
    panic!("a color scheme has no {group} color {name}")
}

fn ui_color(ui: &Value, name: &str) -> Option<Color> {
    rune::color::hex(ui.get(name)?.as_str()?)
}

macro_rules! tokens {
    ($($name:ident)*) => {
        fn tokens(ui: &Value, dark: bool, under: Option<&Tokens>) -> Tokens {
            Tokens {
                dark,
                $($name: ui_color(ui, stringify!($name))
                    .or(under.map(|u| u.$name))
                    .unwrap_or_else(|| missing("ui", stringify!($name))),)*
            }
        }
    };
}

tokens!(
    bg_app bg_panel bg_elevated bg_selected bg_input bg_tile bg_button bg_overlay
    border border_hover border_focus border_subtle scrollbar_thumb scrollbar_hover
    text_primary text_secondary text_tertiary text_muted text_dimmed
    accent brand error warning success merged on_accent on_brand shadow highlight
    state_idle state_working state_waiting state_approval state_scheduled state_unknown
    inactive find annotation settle settle_ink settle_ink_quiet
);

const ANSI: [&str; 16] = [
    "black",
    "red",
    "green",
    "yellow",
    "blue",
    "magenta",
    "cyan",
    "white",
    "bright_black",
    "bright_red",
    "bright_green",
    "bright_yellow",
    "bright_blue",
    "bright_magenta",
    "bright_cyan",
    "bright_white",
];

fn terminal_color(terminal: &Value, name: &str) -> Option<Rgb> {
    let hex = terminal.get(name)?.as_str()?.strip_prefix('#')?;
    let value = u32::from_str_radix(hex, 16)
        .ok()
        .filter(|_| hex.len() == 6)?;
    Some([(value >> 16) as u8, (value >> 8) as u8, value as u8])
}

fn terminal(terminal: &Value, under: Option<&TerminalColors>) -> TerminalColors {
    let pick = |name: &str, under: Option<Rgb>| {
        terminal_color(terminal, name)
            .or(under)
            .unwrap_or_else(|| missing("terminal", name))
    };
    TerminalColors {
        fg: pick("fg", under.map(|u| u.fg)),
        bg: pick("bg", under.map(|u| u.bg)),
        cursor: pick("cursor", under.map(|u| u.cursor)),
        selection: pick("selection", under.map(|u| u.selection)),
        find: pick("find", under.map(|u| u.find)),
        annotation: pick("annotation", under.map(|u| u.annotation)),
        block: pick("block", under.map(|u| u.block)),
        seed: pick("seed", under.map(|u| u.seed)),
        ansi: std::array::from_fn(|i| pick(ANSI[i], under.map(|u| u.ansi[i]))),
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_scheme_file_is_a_scheme_attn_first_then_by_id() {
        let dir = concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/../../../internal/colors/schemes"
        );
        let mut files: Vec<String> = std::fs::read_dir(dir)
            .unwrap()
            .filter_map(|e| {
                let name = e.unwrap().file_name().into_string().unwrap();
                name.strip_suffix(".json").map(str::to_owned)
            })
            .collect();
        files.sort_by_key(|id| (id != "attn", id.clone()));
        let schemes: Vec<&str> = SCHEMES.iter().map(|s| s.id).collect();
        assert_eq!(schemes, files);
    }
}
