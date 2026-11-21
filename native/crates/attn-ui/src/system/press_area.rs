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
    let area = area(key, content, open, t, |area| {
        area.role("button")
            .label(label.to_owned())
            .expanded(open)
            .focusable(true)
    });
    let area = door::corner(area, Radius::Control);
    if disabled {
        return Control::new(disabled::dim(area), Size::Compact);
    }
    let area = layers::pressable(area, surface(open, t), t).on_press_tx(press);
    Control::new(focus::ring(area, t), Size::Compact)
}

/// A list item that the list's arrow keys pick and a press picks, as an area like
/// [`press_area`]'s that takes the selected fill while picked. It is no Tab stop of its own: the
/// list moves the pick, and assistive tools hear the item by `label` and `value`, and as
/// selected while picked.
pub fn pick_area<S: 'static>(
    key: impl Into<String>,
    label: impl Into<String>,
    value: impl Into<String>,
    picked: bool,
    content: El,
    t: &Tokens,
    pick: impl Fn(&mut S, &mut Tx) + 'static,
) -> El {
    let area = area(key, content, picked, t, |area| {
        area.role("listitem")
            .label(label.into())
            .a11y_value(value.into())
            .focus_selected(picked)
    });
    layers::pressable(door::corner(area, Radius::Control), surface(picked, t), t).on_press_tx(pick)
}

fn area(
    key: impl Into<String>,
    content: El,
    chosen: bool,
    t: &Tokens,
    named: impl FnOnce(El) -> El,
) -> El {
    let size = Size::Compact;
    let area = named(row())
        .key(key.into())
        .w_full()
        .min_w(0.0)
        .items_center()
        .min_h(Size::Regular.height())
        .fill(if chosen {
            t.bg_selected
        } else {
            Color::TRANSPARENT
        })
        .child(content.grow(1.0).min_w(0.0));
    door::hang_left(
        door::inset(area, size.inset(), super::Space::S0),
        size.inset(),
    )
}

fn surface(chosen: bool, t: &Tokens) -> Color {
    if chosen { t.bg_selected } else { t.bg_panel }
}
