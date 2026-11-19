use rune::El;
use rune_kit::{TextArea, TextInput};

use super::{Control, Size, Space, Type, door, focus, layers};
use crate::Tokens;

/// The width every number field takes. It holds eight digits beside the arrows: a context cap of 1,000,000 measures 54.5pt in body
/// type, and the arrows and insets leave 66pt.
const NUMBER_WIDTH: f32 = 112.0;

/// What the kit's single-line box, which sets its words 8 in from its leading edge, needs to
/// bring them to a regular control's inset.
const KIT_LINE_TO_REGULAR: Space = Space::S4;

/// A one-line text field filling its column.
pub fn line_box(input: TextInput, disabled: bool, t: &Tokens) -> Control {
    single_line(input.width(None), disabled, t)
}

/// A field that holds a number, at the one width every number field takes.
pub fn number_box(input: TextInput, disabled: bool, t: &Tokens) -> Control {
    single_line(input.width(Some(NUMBER_WIDTH)), disabled, t)
}

/// A text field of several lines, filling its column.
pub fn area_box(area: TextArea, disabled: bool, t: &Tokens) -> Control {
    let size = Size::Regular;
    let area = door::area(area, Type::Body, size.inset(), Space::S8)
        .placeholder_ink(t.text_dimmed)
        .width(None)
        .disabled(disabled)
        .build(super::kit(t));
    Control::new(boxed(area, disabled, t), size)
}

fn single_line(input: TextInput, disabled: bool, t: &Tokens) -> Control {
    let size = Size::Regular;
    let field = door::line(input, Type::Body)
        .height(size.height())
        .disabled(disabled)
        .build(super::kit(t));
    let field = door::sides(field, KIT_LINE_TO_REGULAR, size.inset());
    Control::new(boxed(field, disabled, t), size)
}

/// The kit draws an unavailable field at half strength itself, so an unavailable field
/// keeps its rest look and only loses its hover and ring.
fn boxed(field: El, disabled: bool, t: &Tokens) -> El {
    let field = field.fill(t.bg_input);
    if disabled {
        return field;
    }
    focus::ring_within(field.hover(layers::hover_layer(t.bg_input, t)), t)
}
