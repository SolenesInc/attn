use crate::system::Size;

/// The longest a line of reading text runs: 75 characters, the far end of a comfortable line.
/// Sentences of 74 and 75 characters from Settings measure 457.5 and 476 in `Body`.
pub(crate) const READING: f32 = 480.0;

/// The longest a field runs: text in it wraps at the reading measure, inside its insets.
pub(crate) const FIELD: f32 = READING + 2.0 * Size::Regular.inset().pt();

/// The narrowest words get beside a control before the control moves under them, in a setting
/// row and in a page's head: the longest setting name, "Settle a turn once you have steered the
/// agent", measures 284.5 in `Name`, so no name breaks beside its control. Under it the words
/// take the whole line, however narrow.
pub(crate) const COPY_FLOOR: f32 = 288.0;

/// The column a fact's name takes: the longest name, "Shared terminal host", measures 118 in
/// `Small`.
pub(crate) const FACT_LABEL: f32 = 128.0;

/// The narrowest a fact's value gets beside its name before it moves under it: a build,
/// "0.13.0 (abcdef123)", measures 130 in `Mono`, so a build keeps its line beside its name.
pub(crate) const FACT_VALUE_FLOOR: f32 = 160.0;

/// The narrowest a fact's tile gets before the tiles wrap: an event bus's sequence range,
/// "1,048,576–1,049,000", measures 137 in `Mono`, 161 inside the tile's insets.
pub(crate) const FACT_TILE_FLOOR: f32 = 164.0;
