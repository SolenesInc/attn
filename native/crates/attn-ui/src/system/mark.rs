use rune::{Color, El, Span, TextEdit, el, rich, row};

use super::{Radius, Space, Type, door};

/// A state's dot: as tall as a lower-case letter of `Small` text, so it reads as one mark of a
/// line of text.
const DOT: f32 = 6.0;

/// A meter's length and height, kept from the garden's plot bar.
const METER: f32 = 160.0;
const METER_HIGH: f32 = 3.0;

/// The round mark that says what state a thing is in, filled with `ink`.
pub fn dot(ink: Color) -> El {
    door::corner(el().size(DOT, DOT), Radius::Round)
        .fill(ink)
        .a11y_hidden()
}

/// A line that holds a [`dot`] centred on one line of `kind` text, so the dot sits on the first
/// line of the words beside it.
pub fn dot_on_line(ink: Color, kind: Type) -> El {
    row()
        .h(door::line_height(kind))
        .items_center()
        .child(dot(ink))
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
        .map(|&(from, to)| {
            Span::new(from, to)
                .color(mark)
                .weight(door::style(Type::Name).weight)
        })
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
        .map(|(from, to)| door::mono_span(Span::new(from, to)).bg(ground, Space::S2.pt()))
        .into_iter()
        .collect();
    door::font(rich(said.to_owned(), spans).label(said.to_owned()), kind).color(ink)
}

/// A field with no box of its own, its words set as `kind`, for a line that is itself the
/// field's ground, such as a search bar.
pub fn bare_field(edit: &TextEdit, kind: Type) -> El {
    rune::text_input(edit, door::style(kind))
}

/// Space that grows to push what follows to the far end of its line.
pub fn spring() -> El {
    el().grow(1.0)
}
