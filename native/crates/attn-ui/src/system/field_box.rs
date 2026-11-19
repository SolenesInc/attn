use rune_kit::{TextArea, TextInput};

use super::{Control, Size, Space, Type, door, layers};
use crate::Tokens;

/// The width every number field takes: "600", the widest value a number setting holds, with
/// the field's insets and its arrows measures 62.
const NUMBER_WIDTH: f32 = 88.0;

/// What the kit's single-line box, which sets its words 8 in from its edge, needs to bring them
/// to a regular control's inset.
const KIT_LINE_TO_REGULAR: Space = Space::S4;

/// A one-line text field filling its column.
pub fn line_box(input: TextInput, t: &Tokens) -> Control {
    single_line(input.width(None), t)
}

/// A field that holds a number, at the one width every number field takes.
pub fn number_box(input: TextInput, t: &Tokens) -> Control {
    single_line(input.width(Some(NUMBER_WIDTH)), t)
}

/// A text field of several lines, filling its column.
pub fn area_box(area: TextArea, t: &Tokens) -> Control {
    let size = Size::Regular;
    let area = door::area(area, Type::Body, size.inset(), Space::S8)
        .placeholder_ink(t.text_dimmed)
        .width(None);
    let area = area.build(super::kit(t)).fill(t.bg_input);
    Control::new(area.hover(layers::hover_layer(t)), size)
}

fn single_line(input: TextInput, t: &Tokens) -> Control {
    let size = Size::Regular;
    let field = input
        .text_style(door::style(Type::Body))
        .height(size.height())
        .build(super::kit(t))
        .fill(t.bg_input)
        .hover(layers::hover_layer(t));
    Control::new(door::inset_left(field, KIT_LINE_TO_REGULAR), size)
}
