use rune::Tx;

use super::{Button, Control, Look, Size, kit};
use crate::Tokens;

const CHEVRON: f32 = 12.0;

/// A quiet control that opens and closes something nearby: its words and a chevron that turns
/// with it.
pub fn expander<S: 'static>(
    key: impl Into<String>,
    said: impl Into<String>,
    open: bool,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let glyph = if open {
        kit(t).glyph.chevron_up
    } else {
        kit(t).glyph.chevron_down
    };
    Button::new(said)
        .look(Look::Quiet)
        .size(Size::Compact)
        .trailing(
            rune::icon(glyph, CHEVRON)
                .color(if disabled {
                    t.text_dimmed
                } else {
                    t.text_tertiary
                })
                .a11y_hidden(),
        )
        .expanded(open)
        .disabled(disabled)
        .on_press_tx(press)
        .build(t)
        .key(key)
}
