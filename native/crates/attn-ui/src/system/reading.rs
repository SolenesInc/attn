use rune::{Color, TextStyle};
use rune_kit::MarkdownStyle;
use rune_kit::syntax::Syntax;

use super::{Space, Type, door};
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
    fn style(self, t: &Tokens) -> TextStyle {
        let kit = crate::kit(t);
        let (size, weight, line) = match self {
            Reading::Text => (15.0, 400.0, 24.0),
            Reading::Title => (20.0, 700.0, 26.0),
            Reading::Heading => (17.0, 600.0, 24.0),
            Reading::Subheading => (15.0, 600.0, 22.0),
            Reading::Item => (14.0, 400.0, 22.0),
            Reading::Code => (13.0, 400.0, 20.0),
            Reading::InlineCode => (12.0, 400.0, 16.0),
            Reading::Diagram => (16.0, 400.0, 24.0),
        };
        let face = match self {
            Reading::Code | Reading::InlineCode => kit.ty.mono,
            _ => kit.ty.body,
        };
        let tracking = match self {
            Reading::Title => -0.5,
            _ => face.tracking,
        };
        TextStyle {
            size,
            weight,
            line_height: line,
            tracking,
            ..face
        }
    }
}

/// How a document reads: its type by [`Reading`] role, its blocks spaced on the scale, list
/// markers in the accent, ring checkboxes and struck finished tasks, italic quotes on an accent
/// rail, code in GitHub's colours on a half-tinted card that scrolls sideways, and tables
/// across the text with hairline rules. Paragraphs, lists, quotes and headings stop at the
/// document measure.
pub fn document_style(t: &Tokens) -> MarkdownStyle {
    family(t).prose_measure(DOCUMENT)
}

fn family(t: &Tokens) -> MarkdownStyle {
    let kit = crate::kit(t);
    let text = t.text_primary.alpha(0.9);
    let code = crate::colors::syntax(t.dark);
    let pt = |s: Space| s.pt();
    MarkdownStyle::from_kit(kit)
        .text(text)
        .body_font(Reading::Text.style(t))
        .heading_font(1, Reading::Title.style(t))
        .heading_font(2, Reading::Heading.style(t))
        .heading_font(3, Reading::Subheading.style(t))
        .heading_font(4, Reading::Subheading.style(t))
        .heading_text(1, t.text_primary)
        .heading_text(2, text)
        .heading_text(3, t.text_primary.alpha(0.8))
        .deep_heading(t.text_primary)
        .paragraph_margin(pt(Space::S16))
        .heading_margin(1, pt(Space::S24), pt(Space::S16))
        .heading_margin(2, pt(Space::S32), pt(Space::S12))
        .heading_margin(3, pt(Space::S24), pt(Space::S8))
        .heading_margin(4, pt(Space::S16), pt(Space::S8))
        .heading_margins_collapse(true)
        .list_font(Reading::Item.style(t))
        .list_marker(t.accent.alpha(0.6))
        .list(pt(Space::S24), pt(Space::S6))
        .list_margin(pt(Space::S6), pt(Space::S6))
        .list_markers_outside(true)
        .list_marker_gap(pt(Space::S8))
        .list_tabular_figures(true)
        .link_underline(Color::TRANSPARENT)
        .inline_code_size(Reading::InlineCode.style(t).size)
        .inline_code_font(1.0, 400.0)
        .inline_code_pad(pt(Space::S6))
        .inline_code_pad_y(pt(Space::S2))
        .inline_code_bg(t.bg_elevated)
        .code_block_font(Reading::Code.style(t))
        .code_block(t.bg_elevated.alpha(0.5), t.border.alpha(0.3))
        .code_block_padding(pt(Space::S16), pt(Space::S12))
        .code_margin(pt(Space::S16), pt(Space::S16))
        .code_block_text(code.color(Syntax::Variable))
        .syntax_theme(code)
        .quote(t.accent.alpha(0.5), RAIL, pt(Space::S16), t.text_secondary)
        .quote_italic(true)
        .quote_margin(pt(Space::S16), pt(Space::S16))
        .rule(t.border.alpha(0.3))
        .rule_margin(pt(Space::S32), pt(Space::S32))
        .lines(1.0)
        .table_font(Reading::Item.style(t))
        .table_outline(None)
        .table_header_rule(t.border)
        .table_rules(t.border.alpha(0.5))
        .table_header_fill(t.bg_elevated.alpha(0.3))
        .table_header_text(text)
        .table_text(t.text_primary.alpha(0.8))
        .table_header_weight(600.0)
        .table_cell_padding(pt(Space::S12), pt(Space::S8))
        .table_margin(pt(Space::S16), pt(Space::S16))
        .table_full_width(true)
        .table_scroll_bar(t.border, t.text_tertiary)
        .code_scroll_bar(crate::code_scroll_bar(t))
        .diagram_font(Reading::Diagram.style(t))
        .task_checkbox(
            CHECKBOX,
            pt(Space::S8),
            pt(Space::S8),
            None,
            Color::TRANSPARENT,
            t.success,
            Color::TRANSPARENT,
            t.text_secondary.alpha(0.5),
        )
        .task_checked(Color::TRANSPARENT, t.success, t.success)
        .task_ring(RAIL, CHECK_RING)
        .task_done(t.text_secondary)
}

/// A seed's body or a log entry's words in `ink`: a document at the interface's `Body` size,
/// since it is read beside the interface rather than on its own, across the column it is given
/// and with its lists drawn closer.
pub fn note_style(ink: Color, t: &Tokens) -> MarkdownStyle {
    let body = TextStyle {
        line_height: NOTE_LINE,
        ..door::style(Type::Body)
    };
    let body = TextStyle {
        family: crate::kit(t).ty.body.family,
        ..body
    };
    family(t)
        .body_font(body)
        .list_font(body)
        .list(Space::S24.pt(), Space::S4.pt())
        .text(ink)
        .deep_heading(ink)
}

/// A quote's rail and a checkbox's ring.
const RAIL: f32 = 2.0;
/// A task's checkbox, as tall as a line of list text is high less its leading.
const CHECKBOX: f32 = 16.0;
const CHECK_RING: f32 = 11.0;
/// `Body` read as a document: 13 on a 20 line.
const NOTE_LINE: f32 = 20.0;
