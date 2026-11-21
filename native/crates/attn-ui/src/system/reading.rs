use rune::{Color, TextStyle};
use rune_kit::MarkdownStyle;

use super::{Space, door};
use crate::Tokens;
use crate::blocks::DOCUMENT;

/// What a run of a document's text is: the family a document is read in, apart from the
/// interface's [`Type`], since a document is read at length.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Reading {
    /// A paragraph or a quote: 15/400 on a 24 line, the size reading at length asks for.
    Text,
    /// A document's first heading: 20/700, tight.
    Title,
    /// A second-level heading: 17/600.
    Heading,
    /// A heading below that: 15/600, the text's size in its weight.
    Subheading,
    /// A list item or a table cell: 14/400, a step under the text so a list reads as a list.
    Item,
    /// A block of code: 13/400 mono.
    Code,
    /// Code inside a sentence: 12/400 mono, the interface's floor.
    InlineCode,
    /// A diagram's labels: 16/400.
    Diagram,
}

impl Reading {
    /// How tall the role's letters are, the em a document's columns are measured in.
    pub fn size(self) -> f32 {
        self.metrics().0
    }

    pub(crate) const fn metrics(self) -> (f32, f32, f32) {
        match self {
            Reading::Text => (15.0, 400.0, 24.0),
            Reading::Title => (20.0, 700.0, 26.0),
            Reading::Heading => (17.0, 600.0, 24.0),
            Reading::Subheading => (15.0, 600.0, 22.0),
            Reading::Item => (14.0, 400.0, 22.0),
            Reading::Code => (13.0, 400.0, 20.0),
            Reading::InlineCode => (12.0, 400.0, 16.0),
            Reading::Diagram => (16.0, 400.0, 24.0),
        }
    }

    /// The role as a text style, to measure text set in it.
    pub const fn style(self) -> TextStyle {
        door::document::style(self)
    }

    /// The role as a person writes their own notes in it: in a serif, code in mono, each
    /// character drawn as typed, `-->` as its three characters rather than an arrow.
    pub const fn written(self) -> TextStyle {
        door::document::written(self)
    }
}

/// `e`, words set apart from a document's own, such as what stands in for an image that did
/// not load: `role` in italic.
pub fn read_aside(e: rune::El, role: Reading) -> rune::El {
    door::styled(
        e,
        TextStyle {
            italic: true,
            ..role.style()
        },
    )
}

/// A mark painted over a passage of a document in `fill`: a square block exactly as wide and
/// tall as its words, so marks side by side meet with no seam.
pub fn passage_mark(fill: Color) -> rune_kit::Highlight {
    door::flush(rune_kit::Highlight::new().bg(fill))
}

/// `e`, text of a document, set in `role`.
pub fn read_as(e: rune::El, role: Reading) -> rune::El {
    door::styled(e, role.style())
}

/// `e`, text a person writes in a document, set in `role` as they typed it.
pub fn written_as(e: rune::El, role: Reading) -> rune::El {
    door::styled(e, role.written())
}

/// A cell of a table a person writes: its words in `Item`, a head's in its weight, set in
/// from the cell's edges as a document's table sets them.
pub fn written_cell(e: rune::El, head: bool) -> rune::El {
    let style = Reading::Item.written();
    let style = match head {
        true => TextStyle {
            weight: HEAD_WEIGHT,
            ..style
        },
        false => style,
    };
    door::inset(door::styled(e, style), Space::S12, Space::S8)
}

/// How a document reads: its type by [`Reading`] role, its blocks spaced on the scale, list
/// markers in the accent, ring checkboxes and struck finished tasks, italic quotes on an accent
/// rail, code in GitHub's colours on a half-tinted card that scrolls sideways, and tables
/// across the text with hairline rules. Paragraphs, lists, quotes and headings stop at the
/// document measure.
pub fn document_style(t: &Tokens) -> MarkdownStyle {
    door::document::family(t).prose_measure(DOCUMENT)
}

/// A seed's body or a log entry's words in `ink`: a document at the interface's `Body` size,
/// since it is read beside the interface rather than on its own, across the column it is given
/// and with its lists drawn closer.
pub fn note_style(ink: Color, t: &Tokens) -> MarkdownStyle {
    door::document::note(ink, t)
}

/// A table head's words.
pub(crate) const HEAD_WEIGHT: f32 = 600.0;
