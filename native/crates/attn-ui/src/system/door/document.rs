use rune::{Color, TextStyle};
use rune_kit::MarkdownStyle;
use rune_kit::syntax::Syntax;

use crate::Tokens;
use crate::system::reading::HEAD_WEIGHT;
use crate::system::{Reading, Space, Type};

pub(crate) const fn style(role: Reading) -> TextStyle {
    let (size, weight, line) = role.metrics();
    let face = TextStyle::new(size, weight).lh(line);
    match role {
        Reading::Code | Reading::InlineCode => face.mono(),
        Reading::Title => face.tracking(-0.5),
        _ => face,
    }
}

pub(crate) const fn written(role: Reading) -> TextStyle {
    let typed = style(role).features("'liga' 0, 'calt' 0");
    match role {
        Reading::Code | Reading::InlineCode => typed,
        _ => super::serif(typed),
    }
}

pub(crate) fn family(t: &Tokens) -> MarkdownStyle {
    let kit = crate::kit(t);
    let text = t.text_primary.alpha(0.9);
    let code = crate::colors::syntax(t.dark);
    let pt = |s: Space| s.pt();
    MarkdownStyle::from_kit(kit)
        .text(text)
        .body_font(Reading::Text.style())
        .heading_font(1, Reading::Title.style())
        .heading_font(2, Reading::Heading.style())
        .heading_font(3, Reading::Subheading.style())
        .heading_font(4, Reading::Subheading.style())
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
        .list_font(Reading::Item.style())
        .list_marker(t.accent.alpha(0.6))
        .list(pt(Space::S24), pt(Space::S6))
        .list_margin(pt(Space::S6), pt(Space::S6))
        .list_markers_outside(true)
        .list_marker_gap(pt(Space::S8))
        .list_tabular_figures(true)
        .link_underline(Color::TRANSPARENT)
        .inline_code_size(Reading::InlineCode.style().size)
        .inline_code_font(1.0, 400.0)
        .inline_code_pad(pt(Space::S6))
        .inline_code_pad_y(pt(Space::S2))
        .inline_code_bg(t.bg_elevated)
        .code_block_font(Reading::Code.style())
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
        .table_font(Reading::Item.style())
        .table_outline(None)
        .table_header_rule(t.border)
        .table_rules(t.border.alpha(0.5))
        .table_header_fill(t.bg_elevated.alpha(0.3))
        .table_header_text(text)
        .table_text(t.text_primary.alpha(0.8))
        .table_header_weight(HEAD_WEIGHT)
        .table_cell_padding(pt(Space::S12), pt(Space::S8))
        .table_margin(pt(Space::S16), pt(Space::S16))
        .table_full_width(true)
        .table_scroll_bar(t.border, t.text_tertiary)
        .code_scroll_bar(crate::code_scroll_bar(t))
        .diagram_font(Reading::Diagram.style())
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

pub(crate) fn note(ink: Color, t: &Tokens) -> MarkdownStyle {
    let body = TextStyle {
        line_height: NOTE_LINE,
        ..super::style(Type::Body)
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
