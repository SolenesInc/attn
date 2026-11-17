//! Marks drawn as text. The first are characters the Tauri app draws in its labels' own font
//! that a bundled font carries (the others are drawings in [`crate::icon`]); the rest are glyphs
//! from Symbols Nerd Font Mono, which [`crate::FONTS`] carries, so each
//! draws on every platform without relying on the system's fallback fonts.

use rune::TextStyle;

/// A mark at the Tauri app's `--font-size-sm`, `--font-size` and `--font-size-lg`, one line
/// high, as its buttons that draw `×` set them.
pub const SMALL: TextStyle = TextStyle::new(11.0, 400.0).lh(11.0);
pub const REGULAR: TextStyle = TextStyle::new(14.0, 400.0).lh(14.0);
pub const LARGE: TextStyle = TextStyle::new(18.0, 400.0).lh(18.0);

pub const TIMES: &str = "×";
pub const PREVIOUS: &str = "▲";
pub const NEXT: &str = "▼";
pub const SUNRISE: &str = "☀";
pub const RELOAD: &str = "↻";

pub const CARET_DOWN: &str = "\u{f0d7}";
pub const CARET_RIGHT: &str = "\u{f0da}";
pub const MOON: &str = "\u{f186}";
pub const CHECKED: &str = "\u{f046}";
pub const CHECK: &str = "\u{f00c}";
pub const UNCHECKED: &str = "\u{f096}";
pub const BANNER: &str = "\u{f024}";
pub const PENCIL: &str = "\u{f040}";
pub const POP_OUT: &str = "\u{f08e}";
pub const BOLT: &str = "\u{f0e7}";
pub const FOLDER: &str = "\u{f07b}";
pub const CLOCK: &str = "\u{f017}";
pub const REFRESH: &str = "\u{f021}";
pub const CIRCLE: &str = "\u{f111}";
pub const CIRCLE_OPEN: &str = "\u{f10c}";
pub const CIRCLE_DOT: &str = "\u{f192}";
pub const COMMENT: &str = "\u{f075}";
pub const GRABBER: &str = "\u{f4a6}";
pub const COPY: &str = "\u{f0c5}";
pub const MERGE: &str = "\u{f419}";
pub const BELL_SLASH: &str = "\u{f1f6}";
pub const SITEMAP: &str = "\u{f0e8}";
pub const BAN: &str = "\u{f05e}";
pub const STAR_OPEN: &str = "\u{f006}";
