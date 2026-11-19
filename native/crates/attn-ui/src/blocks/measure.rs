use crate::system::Size;

/// The longest a line of reading text runs: 75 characters, the far end of a comfortable line.
/// Sentences of 74 and 75 characters from Settings measure 457.5 and 476 in `Body`.
pub(crate) const READING: f32 = 480.0;

/// The widest a page runs, so every block on it ends on one right edge. Its widest line, a
/// setting row with a full reading line beside the widest control ("Resume for", 250), needs
/// 780; the Delegation table on one line needs 614.
pub(crate) const MEASURE: f32 = 800.0;

/// The longest a field runs: text in it wraps at the reading measure, inside its insets.
pub(crate) const FIELD: f32 = READING + 2.0 * Size::Regular.inset().pt();

/// The narrowest words get beside a control before the control moves under them, in a setting
/// row and in a page's head: the longest setting name, "Settle a turn once you have steered the
/// agent", measures 284.5 in `Name`, so no name breaks beside its control. Under it the words
/// take the whole line, however narrow.
pub(crate) const COPY_FLOOR: f32 = 288.0;

/// A dialog that asks one thing: its lead at the reading measure, inside the dialog's insets.
pub(crate) const PROMPT: f32 = READING + 2.0 * crate::system::Space::S24.pt();
/// The longest a line of a document runs, wider than the reading measure since a document is
/// read on its own. Receipt: attn's own docs (glossary, testing, the guide's front page) set in
/// `Reading::Text` average 6.6 to 7.1 points a character, so a line holds 91 to 97 characters.
pub const DOCUMENT: f32 = 640.0;

/// The column a fact's name takes: the longest name, "Shared terminal host", measures 118 in
/// `Small`.
pub(crate) const FACT_LABEL: f32 = 128.0;

/// The narrowest a fact's value gets beside its name before it moves under it: a build,
/// "0.13.0 (abcdef123)", measures 130 in `Mono`, so a build keeps its line beside its name.
pub(crate) const FACT_VALUE_FLOOR: f32 = 160.0;

/// The narrowest a remark's words get beside who said them before they move under. Measured in
/// the Remark story: in a reader docked at 380 the words stay beside, 182 to 207 wide; in a
/// 300 column they would get 75 and move under, 187 wide.
pub(crate) const REMARK_FLOOR: f32 = 160.0;

/// How far a document's card sets its words in from its edge, by the room the card has.
/// Receipt: the reader the native app replaces padded its card 1.25rem, then 2, 2.5 and 3rem
/// from 768, 1024 and 1280 points of room, at a 14 point rem.
pub fn document_inset(room: f32) -> f32 {
    match room {
        r if r >= 1280.0 => 42.0,
        r if r >= 1024.0 => 35.0,
        r if r >= 768.0 => 28.0,
        _ => 17.5,
    }
}

/// A document card's edge.
pub const DOCUMENT_EDGE: f32 = 1.0;

/// The room between a document tile's edge and its card: the gutter's inset.
pub const DOCUMENT_GUTTER: f32 = 12.0;

/// The inset of a document card `width` points across, and the column it leaves its words.
pub fn document_inside(width: f32) -> (f32, f32) {
    let inset = document_inset(width);
    (inset, (width - 2.0 * (inset + DOCUMENT_EDGE)).max(0.0))
}

/// The narrowest a fact's tile gets before the tiles wrap: an event bus's sequence range,
/// "1,048,576–1,049,000", measures 137 in `Mono`, 161 inside the tile's insets.
pub(crate) const FACT_TILE_FLOOR: f32 = 164.0;
/// The narrowest a table's name column gets before the fixed columns wrap under it: the widest
/// name with its tag, "Orchestrator" with "NEEDS A MODEL", measures 184.
pub(crate) const FLEX_FLOOR: f32 = 200.0;
