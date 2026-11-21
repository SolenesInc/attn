//! Marks drawn as text. The first are characters the Tauri app draws in its labels' own font
//! that a bundled font carries (the others are drawings in [`crate::icon`]); the rest are glyphs
//! from Symbols Nerd Font Mono, which [`crate::FONTS`] carries, so each
//! draws on every platform without relying on the system's fallback fonts.

use rune::TextStyle;

use crate::system::{IconSize, door};

/// A mark at each [`IconSize`], one line high, so a mark drawn as text and an icon beside the
/// same words are one size.
pub const SMALL: TextStyle = door::mark(IconSize::Small);
pub const REGULAR: TextStyle = door::mark(IconSize::Regular);
pub const LARGE: TextStyle = door::mark(IconSize::Large);

pub const TIMES: &str = "×";
pub const MINUS: &str = "−";
pub const PLUS: &str = "+";
pub const PREVIOUS: &str = "▲";
pub const NEXT: &str = "▼";
pub const SUNRISE: &str = "☀";
pub const RELOAD: &str = "↻";
pub const ARROW_UP: &str = "↑";
pub const ARROW_DOWN: &str = "↓";

pub const CARET_DOWN: &str = "\u{f0d7}";
pub const CARET_RIGHT: &str = "\u{f0da}";
pub const MOON: &str = "\u{f186}";
pub const CHECKED: &str = "\u{f046}";
pub const CHECK: &str = "\u{f00c}";
pub const UNCHECKED: &str = "\u{f096}";
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
pub const STAR: &str = "\u{f005}";
pub const UNDO: &str = "\u{f0e2}";
pub const PERSON: &str = "\u{f007}";
