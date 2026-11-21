use rune::{Color, El, Span, el, rich, row};

use super::{Radius, Space, Type, door};

/// A meter's length and height, kept from the garden's plot bar.
const METER: f32 = 160.0;
const METER_HIGH: f32 = 3.0;

/// A line that holds a state's small filled [`super::dot`] in `ink`, centred on one line of `kind` text, so the dot sits on the first
/// line of the words beside it.
pub fn dot_on_line(ink: Color, kind: Type) -> El {
    row()
        .h(door::line_height(kind))
        .items_center()
        .child(super::dot(
            ink,
            super::DotShape::Filled,
            super::DotSize::Small,
        ))
}

/// How a whole splits into parts, each part `count` long in its `ink`, side by side on one bar.
pub fn meter(parts: impl IntoIterator<Item = (f32, Color)>) -> El {
    let bar = row().w(METER).h(METER_HIGH).a11y_hidden().children(
        parts
            .into_iter()
            .filter(|(count, _)| *count > 0.0)
            .map(|(count, ink)| el().h_full().grow(count).fill(ink)),
    );
    door::gap(door::clip_corner(bar, Radius::Tag), Space::S2)
}

/// `said` as `kind` in `ink`, with each byte range of `marks` drawn in `mark` at `Name` weight,
/// as a search shows what it matched.
pub fn marked(said: &str, marks: &[(usize, usize)], kind: Type, ink: Color, mark: Color) -> El {
    let spans = marks
        .iter()
        .map(|&(from, to)| Span::new(from, to).color(mark).bold())
        .collect();
    door::font(rich(said.to_owned(), spans).label(said.to_owned()), kind).color(ink)
}

/// `said` as `kind` in `ink`, with the byte range `command` set as a command: `Mono` on a quiet
/// ground.
pub fn with_command(
    said: &str,
    command: Option<(usize, usize)>,
    kind: Type,
    ink: Color,
    ground: Color,
) -> El {
    let spans = command
        .map(|(from, to)| grounded(door::mono_span(Span::new(from, to)), ground))
        .into_iter()
        .collect();
    door::font(rich(said.to_owned(), spans).label(said.to_owned()), kind).color(ink)
}

/// `span` on a quiet `ground` that reaches a little past its letters, as a command or a changed
/// word is set apart from the words around it.
pub fn grounded(span: Span, ground: Color) -> Span {
    door::grounded(span, ground)
}

/// `span` as code inside a sentence, in `InlineCode`, on `ground` when there is one.
pub fn code_span(span: Span, ground: Option<Color>) -> Span {
    let code = door::inline_code(span);
    match ground {
        Some(ground) => grounded(code, ground),
        None => code,
    }
}
