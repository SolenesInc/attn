use rune::{Color, El, Tx, row};

use super::{Control, Radius, Size, disabled, door, focus, layers};
use crate::Tokens;

/// An area that acts as one button and grows with what it holds, such as a row's title that
/// opens the row. Its box hangs out by a compact inset, so what it holds sits on the line of the
/// words around it. Open, it takes the selected fill.
pub fn press_area<S: 'static>(
    key: impl Into<String>,
    label: &str,
    open: bool,
    disabled: bool,
    content: El,
    t: &Tokens,
    press: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let size = Size::Compact;
    let fill = if open {
        t.bg_selected
    } else {
        Color::TRANSPARENT
    };
    let area = row()
        .key(key.into())
        .role("button")
        .label(label.to_owned())
        .expanded(open)
        .focusable(true)
        .w_full()
        .min_w(0.0)
        .items_center()
        .min_h(Size::Regular.height())
        .fill(fill)
        .child(content.grow(1.0).min_w(0.0));
    let area = door::hang_left(
        door::inset(area, size.inset(), super::Space::S0),
        size.inset(),
    );
    let area = door::corner(area, Radius::Control);
    if disabled {
        return Control::new(disabled::dim(area), size);
    }
    let surface = if open { fill } else { t.bg_panel };
    let area = layers::pressable(area, surface, t).on_press_tx(press);
    Control::new(focus::ring(area, t), size)
}
