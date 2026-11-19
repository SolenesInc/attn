use rune::{Tx, row};

use super::{Control, Radius, Size, Space, Type, door, focus, layers, words};
use crate::Tokens;

/// The control that opens and closes a part of a page or a form: a caret, its name, and chips
/// that say what the closed part holds.
pub fn disclosure<S: 'static>(
    key: impl Into<String>,
    label: &str,
    open: bool,
    chips: &[String],
    failed: bool,
    t: &Tokens,
    press: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let size = Size::Compact;
    let ink = if failed { t.error } else { t.text_secondary };
    let caret = if open {
        crate::glyph::CARET_DOWN
    } else {
        crate::glyph::CARET_RIGHT
    };
    let chip = |said: &String| {
        let chip = words(said.clone(), Type::Small, t.text_secondary)
            .border(1.0, t.border_subtle)
            .a11y_hidden();
        door::corner(door::inset(chip, Space::S6, Space::S0), Radius::Round)
    };
    let line = row()
        .key(key.into())
        .role("button")
        .label(label.to_owned())
        .description(chips.join(", "))
        .expanded(open)
        .focusable(true)
        .self_start()
        .items_center()
        .h(size.height())
        .child(words(caret, Type::Body, ink).a11y_hidden())
        .child(words(label, Type::Control, ink).a11y_hidden())
        .children(chips.iter().map(chip))
        .on_press_tx(press);
    let line = door::gap(door::inset(line, size.inset(), Space::S0), Space::S8);
    let line = door::hang_left(line, size.inset());
    let line = layers::pressable(door::corner(line, Radius::Control), t.bg_panel, t);
    Control::new(focus::ring(line, t), size)
}
