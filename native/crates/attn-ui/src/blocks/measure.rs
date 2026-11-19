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
