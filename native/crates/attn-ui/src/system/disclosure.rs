use rune::{Color, El, Tx, row};

use super::expander::opens_in_place;
use super::{Control, Radius, Size, Space, Tone, Type, disabled, door, focus, layers, tag, words};
use crate::Tokens;

/// The control that opens and closes a part of a page or a form: a chevron, its name, and tags
/// that say what the closed part holds. Open, it takes the selected fill.
#[allow(clippy::too_many_arguments)]
pub fn disclosure<S: 'static>(
    key: impl Into<String>,
    label: &str,
    open: bool,
    chips: &[String],
    failed: bool,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let size = Size::Compact;
    let key = key.into();
    let ink = if failed { t.error } else { t.text_secondary };
    let fill = if open {
        t.bg_selected
    } else {
        Color::TRANSPARENT
    };
    let chip = |(i, said): (usize, &String)| {
        El::from(tag(format!("{key}.tag{i}"), said, Tone::Neutral, t)).a11y_hidden()
    };
    let line = row()
        .key(key.clone())
        .role("button")
        .label(label.to_owned())
        .description(chips.join(", "))
        .expanded(open)
        .focusable(true)
        .self_start()
        .items_center()
        .h(size.height())
        .fill(fill)
        .child(opens_in_place(open, t))
        .child(words(label, Type::Control, ink).a11y_hidden())
        .children(chips.iter().enumerate().map(chip));
    let line = door::gap(door::inset(line, size.inset(), Space::S0), Space::S8);
    let line = door::corner(door::hang_left(line, size.inset()), Radius::Control);
    if disabled {
        return Control::new(disabled::dim(line), size);
    }
    let surface = if open { fill } else { t.bg_panel };
    let line = layers::pressable(line, surface, t).on_press_tx(press);
    Control::new(focus::ring(line, t), size)
}
