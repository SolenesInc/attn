use rune::row;
use rune_kit::TextInput;

use super::{Control, IconSize, Size, Space, Type, door, focus, icon, layers};
use crate::Tokens;

/// A field that narrows a list as it is typed into, led by the search icon: compact, so it
/// sits in a list among its rows.
pub fn search_box(input: TextInput, t: &Tokens) -> Control {
    let size = Size::Compact;
    let field = door::line(input.plain(), Type::Body)
        .width(None)
        .height(door::line_height(Type::Body))
        .build(super::kit(t))
        .grow(1.0);
    let frame = door::gap(row(), Space::S6)
        .w_full()
        .h(size.height())
        .items_center()
        .fill(t.bg_input)
        .child(icon(&crate::icon::SEARCH, IconSize::Small, t.text_dimmed))
        .child(field);
    let frame = door::corner(
        door::sides(frame, size.inset(), size.inset()),
        super::Radius::Control,
    );
    Control::new(
        focus::ring_within(frame.hover(layers::hover_layer(t.bg_input, t)), t),
        size,
    )
}
