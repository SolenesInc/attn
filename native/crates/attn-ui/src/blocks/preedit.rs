use rune::{Color, El, text};

use crate::system::door;

/// What an input method composes, drawn in a terminal's cells: the terminal's own monospace at
/// its cell's `size` and `line` height, underlined, on `ground` in `ink`.
pub fn preedit(said: String, size: f32, line: f32, ink: Color, ground: Color) -> El {
    let end = said.len() as u32;
    let style = rune::TextStyle {
        size,
        line_height: line,
        family: rune::Family::Mono,
        ..door::style(crate::system::Type::Body)
    };
    door::styled(text(said), style)
        .fill(ground)
        .color(ink)
        .single_line()
        .decorations(vec![rune::Span {
            start: 0,
            end,
            underline: Some(ink),
            ..rune::Span::default()
        }])
}
