//! The one module of attn-ui that calls Rune's raw spacing, font and corner methods. Every
//! other module, in this crate and in the app, names a token and comes through here.
#![allow(clippy::disallowed_methods)]

use rune::{El, Icon, Span, TextStyle};

use super::{IconSize, Radius, Space, Type};

pub(crate) const fn style(kind: Type) -> TextStyle {
    match kind {
        Type::PageTitle => TextStyle::new(18.0, 700.0),
        Type::Heading => TextStyle::new(15.0, 600.0),
        Type::Name => TextStyle::new(13.0, 600.0),
        Type::Body => TextStyle::new(13.0, 400.0),
        Type::Small => TextStyle::new(12.0, 400.0),
        Type::Label => TextStyle::new(12.0, 600.0).mono().tracking(1.0),
        Type::Tag => TextStyle::new(12.0, 600.0).mono().tracking(0.8).lh(14.0),
        Type::Mono => TextStyle::new(12.0, 400.0).mono(),
        Type::Control => TextStyle::new(12.0, 600.0),
    }
}

pub(crate) fn icon(glyph: &'static Icon, size: IconSize) -> El {
    rune::icon(glyph, size.pt())
}

/// A mark of [`crate::glyph`] drawn as text `size` high, one line tall.
pub(crate) const fn mark(size: IconSize) -> TextStyle {
    TextStyle::new(size.pt(), 400.0).lh(size.pt())
}

/// `mark` drawn as text inside a control: `size` high, as heavy as a control's words.
pub(crate) fn control_mark(mark: &'static str, size: IconSize) -> El {
    rune::text(mark).label(mark).font(TextStyle {
        weight: style(Type::Control).weight,
        ..self::mark(size)
    })
}

pub(crate) fn font(e: El, kind: Type) -> El {
    e.font(style(kind))
}

pub(crate) fn mono_span(span: Span) -> Span {
    span.mono().size(style(Type::Mono).size)
}

pub(crate) fn gap(e: El, gap: Space) -> El {
    e.gap(gap.pt())
}

/// The gap between the lines a wrapping row breaks onto, and between the items on a line.
pub(crate) fn gaps(e: El, lines: Space, items: Space) -> El {
    e.gaps(lines.pt(), items.pt())
}

pub(crate) fn inset(e: El, x: Space, y: Space) -> El {
    e.px(x.pt()).py(y.pt())
}

pub(crate) fn sides(e: El, left: Space, right: Space) -> El {
    e.pl(left.pt()).pr(right.pt())
}

pub(crate) fn hang_left(e: El, by: Space) -> El {
    e.ml(-by.pt())
}

pub(crate) fn corner(e: El, radius: Radius) -> El {
    e.radius(radius.pt())
}

pub(crate) fn line(input: rune_kit::TextInput, kind: Type) -> rune_kit::TextInput {
    input.text_style(style(kind))
}

pub(crate) fn area(area: rune_kit::TextArea, kind: Type, x: Space, y: Space) -> rune_kit::TextArea {
    area.font(style(kind)).padding(x.pt(), y.pt())
}

/// `e`, which holds text set as `kind`, moved down so its first line centres in a band `band`
/// high, beside controls that centre in the same band.
pub(crate) fn first_line_in(e: El, kind: Type, band: f32) -> El {
    e.pt(((band - style(kind).line_px()) / 2.0).max(0.0))
}

pub(crate) fn above(e: El, space: Space) -> El {
    e.mt(space.pt())
}

/// `space` above `e`, with a rule of `ink` across its middle.
pub(crate) fn ruled_above(e: El, space: Space, ink: rune::Color) -> El {
    let half = space.pt() / 2.0;
    e.mt(half).pt(half).border_sides([1.0, 0.0, 0.0, 0.0], ink)
}

pub(crate) fn below(e: El, space: Space) -> El {
    e.pb(space.pt())
}

pub(crate) fn frame(e: El, top: Space, x: Space, bottom: Space) -> El {
    e.pt(top.pt()).px(x.pt()).pb(bottom.pt())
}

pub(crate) fn clip_corner(e: El, radius: Radius) -> El {
    e.clip_rounded(radius.pt())
}

/// `e`, words of `kind`, with as much room under them as [`first_line_in`] puts above, so
/// their first line centres on a band as tall as a `size` control and they end as far from
/// the edge.
pub(crate) fn on_band(e: El, kind: Type, size: super::Size) -> El {
    let room = ((size.height() - line_height(kind)) / 2.0).max(0.0);
    first_line_in(e, kind, size.height()).pb(room)
}

/// How tall one line of `kind` stands.
pub(crate) fn line_height(kind: Type) -> f32 {
    style(kind).line_px()
}

/// `e` with room inside for a 1-point edge, so what it holds never covers the edge.
pub(crate) fn inside_edge(e: El) -> El {
    e.padding(1.0)
}

/// `e` with `points` of empty room after it, which its line keeps or breaks before.
pub(crate) fn room_after(e: El, points: f32) -> El {
    e.mr(points)
}

/// How wide `said` stands in `kind`, as the runtime lays it out.
pub(crate) fn width(said: &str, kind: Type) -> f32 {
    rune::text::measure(said, &[], style(kind), None).0
}

/// How wide a mark of text stands at `size`.
pub(crate) fn mark_width(said: &str, size: super::IconSize) -> f32 {
    rune::text::measure(said, &[], mark(size), None).0
}

pub(crate) fn mark_text(said: String, size: super::IconSize) -> El {
    rune::text(said).font(mark(size))
}

/// `e`'s words `inset` from its start, `depth` levels of `step` further in.
pub(crate) fn indented(e: El, inset: Space, step: Space, depth: u8) -> El {
    e.pl(inset.pt() + step.pt() * f32::from(depth))
}

/// `e` whole at rest, and while its hover group is hovered ending `reach` points before its end
/// in a fade `fade` wide, for what the hover shows over its end.
pub(crate) fn fade_before_hover(e: El, fade: Space, reach: f32) -> El {
    let fade = fade.pt();
    e.clip_fade(0.0, 0.0, fade, 0.0)
        .clip_outset(0.0, 0.0, fade, 0.0)
        .group_clip([0.0; 4], [0.0, 0.0, fade + reach, 0.0])
}

pub(crate) fn rows(e: El, top: Space, bottom: Space) -> El {
    e.pt(top.pt()).pb(bottom.pt())
}
