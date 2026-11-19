use rune::{El, Tx};

use super::{Button, Control, Look, Size, kit};
use crate::Tokens;

const CHEVRON: f32 = 12.0;

/// A quiet control that opens and closes something nearby: its words and the mark of a control
/// that opens in place.
pub fn expander<S: 'static>(
    key: impl Into<String>,
    said: impl Into<String>,
    open: bool,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    Button::new(said)
        .look(Look::Quiet)
        .size(Size::Compact)
        .trailing(opens_in_place(open, t))
        .expanded(open)
        .disabled(disabled)
        .on_press_tx(press)
        .build(t)
        .key(key)
}

/// The mark of a control that opens what it holds below itself: a chevron pointing at its
/// words while closed, turned down while open.
pub fn opens_in_place(open: bool, t: &Tokens) -> El {
    let chevron = rune::icon(kit(t).glyph.chevron_down, CHEVRON).color(t.text_tertiary);
    let chevron = if open {
        chevron
    } else {
        chevron.rotate(-std::f32::consts::FRAC_PI_2)
    };
    chevron.a11y_hidden()
}
