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
        Type::Compose => TextStyle::new(18.0, 400.0).lh(27.0),
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

/// `e` inset by a reading measure that has its own receipt, such as a document card's.
pub(crate) fn padded(e: El, points: f32) -> El {
    e.padding(points)
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

/// `e`'s box grown by the focus ring's reach above and below, its layout unchanged, so revealing
/// it shows the ring whole.
pub(crate) fn ring_room(e: El) -> El {
    let reach = super::focus::RING_REACH;
    e.mt(-reach).mb(-reach).pt(reach).pb(reach)
}

/// A scrolling box reaching out by the focus ring's reach above and below, its neighbours where
/// they were, for content that keeps that room at its ends ([`ring_ends`]).
pub(crate) fn over_ring_room(e: El) -> El {
    let reach = super::focus::RING_REACH;
    e.mt(-reach).mb(-reach)
}

/// A scrolling box reaching out by the focus ring's reach on every side, its content inset by
/// that reach and revealed with it.
pub(crate) fn ring_room_around(e: El) -> El {
    let reach = super::focus::RING_REACH;
    e.mx(-reach).my(-reach).padding(reach).scroll_margin(reach)
}

/// Content of a scrolling box with the focus ring's reach as room at its top and its end, so a
/// control's ring stays whole when the box scrolls to either end.
pub(crate) fn ring_ends(e: El) -> El {
    let reach = super::focus::RING_REACH;
    e.pt(reach).pb(reach)
}

pub(crate) fn hang_left(e: El, by: Space) -> El {
    e.ml(-by.pt())
}

pub(crate) fn hang_right(e: El, by: Space) -> El {
    e.mr(-by.pt())
}

/// `e` reaching out by `by` past both sides of its column, so what it sets in by `by` lines up
/// with the column.
pub(crate) fn hang(e: El, by: Space) -> El {
    e.ml(-by.pt()).mr(-by.pt())
}

pub(crate) fn corner(e: El, radius: Radius) -> El {
    e.radius(radius.pt())
}

/// `e` rounded at `radius` but square on the side where it meets the control it is joined to.
pub(crate) fn corner_joined(e: El, radius: Radius, join: super::Join) -> El {
    let r = radius.pt();
    e.corners(match join {
        super::Join::Next => [r, 0.0, 0.0, r],
        super::Join::Previous => [0.0, r, r, 0.0],
    })
}

/// `e` laid over the edge of the control before it, so the two edges read as one.
pub(crate) fn over_previous_edge(e: El) -> El {
    e.ml(-1.0)
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

/// `e` starting `points` before its parent's start, where a clipping parent hides it.
pub(crate) fn behind_start(e: El, points: f32) -> El {
    e.ml(-points)
}

/// `e` drawing nothing past its box.
pub(crate) fn clip(e: El) -> El {
    e.clip_rounded(0.0)
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

/// `e` with as much room above as below, so a band `band` tall at its top stands in the middle of
/// a control of `size`.
pub(crate) fn around_band(e: El, band: f32, size: super::Size) -> El {
    let room = ((size.height() - band) / 2.0).max(0.0);
    e.pt(room).pb(room)
}

/// How tall one line of `kind` stands.
pub(crate) fn line_height(kind: Type) -> f32 {
    style(kind).line_px()
}

/// `e` with room inside for a 1-point edge, so what it holds never covers the edge.
pub(crate) fn inside_edge(e: El) -> El {
    e.padding(1.0)
}

/// `e` clipped to `radius` inside its 1-point edge, so what it holds never covers the edge.
pub(crate) fn clip_inside_corner(e: El, radius: Radius) -> El {
    e.clip_rounded(radius.pt() - 1.0)
}

/// `e` with a 1-point rule of `ink` along its top.
pub(crate) fn rule_above(e: El, ink: rune::Color) -> El {
    e.border_sides([1.0, 0.0, 0.0, 0.0], ink).border_layout()
}

/// The brand's name as its mark draws it, apart from the type roles: 24/700 mono.
pub(crate) const WORDMARK: TextStyle = TextStyle::new(24.0, 700.0).mono();

/// What the brand is, under its wordmark: widely spaced capitals in the interface face.
pub(crate) const TAGLINE: TextStyle = TextStyle::new(12.0, 400.0).tracking(0.9);

/// `e` in `style`, for the text a caller outside the type roles still hands in, such as a
/// glyph button's mark.
pub(crate) fn styled(e: El, style: TextStyle) -> El {
    e.font(style)
}

/// `e` starting `points` before where its line places it.
pub(crate) fn drawn_back(e: El, points: f32) -> El {
    e.ml(-points)
}

/// `e` with its content `points` in from its start.
pub(crate) fn inset_start(e: El, points: f32) -> El {
    e.pl(points)
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

/// `e` inset by `inset` on both sides and reaching out by as much, so what it holds stays on
/// the column's line while its box spans the inset.
pub(crate) fn reach(e: El, inset: Space) -> El {
    e.px(inset.pt()).ml(-inset.pt()).mr(-inset.pt())
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
pub(crate) fn fade_before_hover(e: El, controls: f32) -> El {
    let fade = HOVER_FADE.pt();
    e.clip_fade(0.0, 0.0, fade, 0.0)
        .clip_outset(0.0, 0.0, fade, 0.0)
        .group_clip(
            [0.0; 4],
            [0.0, 0.0, fade + controls + HOVER_FADE_ROOM.pt(), 0.0],
        )
}

pub(crate) fn room_before_hover(e: El, controls: f32) -> El {
    room_after(e, controls + HOVER_FADE_ROOM.pt())
}

const HOVER_FADE: Space = Space::S6;
const HOVER_FADE_ROOM: Space = Space::S2;

pub(crate) fn rows(e: El, top: Space, bottom: Space) -> El {
    e.pt(top.pt()).pb(bottom.pt())
}

/// A style of [`crate::type_`], the styles views used before the type roles, which go once the
/// last view takes its roles.
pub(crate) const fn legacy(size: f32, weight: f32) -> TextStyle {
    TextStyle::new(size, weight)
}

/// A free text input whose words are set as `kind`.
pub(crate) fn text_input(edit: &rune::TextEdit, kind: Type) -> El {
    rune::text_input(edit, style(kind))
}

/// `e` faded as something turned off: at the strength an unavailable control draws, with its
/// presses kept.
pub(crate) fn faded(e: El) -> El {
    e.layer_opacity(super::disabled::STRENGTH)
}

/// `e`, growing on its line, `points` wider than the room the line gives it.
pub(crate) fn grown_past_end(e: El, points: f32) -> El {
    e.mr(-points)
}

/// `e` on a rail of `ink` down its left edge, what it holds starting 12 from the rail.
pub(crate) fn on_rail(e: El, ink: rune::Color) -> El {
    const RAIL: f32 = 2.0;
    e.pl(RAIL + Space::S12.pt())
        .border_sides([0.0, 0.0, 0.0, RAIL], ink)
}
