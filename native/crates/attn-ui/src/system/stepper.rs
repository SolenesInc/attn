use rune::{El, Tx, col, row};

use super::{
    Button, Control, IconSize, Radius, Size, Space, Type, disabled, door, kit, layers, words,
};
use crate::{Tokens, glyph};

type Press<S> = Box<dyn Fn(&mut S, &mut Tx)>;

/// The width a stepper keeps for its value, so steppers in one column line up: "Match app" in
/// `Name` measures 64. On a line narrower than the stepper the value gives way and wraps.
const STEPPER_VALUE: f32 = 80.0;

/// A value between a step down and a step up. `key.down`, `key.value` and `key.up` name its
/// parts; `what` names the value for a screen reader.
pub fn stepper<S: 'static>(
    key: &str,
    what: &str,
    value: String,
    disabled: bool,
    t: &Tokens,
    down: impl Fn(&mut S, &mut Tx) + 'static,
    up: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let step = |dir: &str, said: &str, mark: &'static str, press: Press<S>| {
        El::from(
            Button::mark(mark, IconSize::Small)
                .disabled(disabled)
                .on_press_tx(move |s: &mut S, tx: &mut Tx| press(s, tx))
                .build(t)
                .key(format!("{key}.{dir}"))
                .label(format!("{said} {what}")),
        )
    };
    let shown = words(value.clone(), Type::Name, t.text_primary)
        .key(format!("{key}.value"))
        .role("status")
        .label(value)
        .wrap()
        .grow_from(0.0, STEPPER_VALUE)
        .align(rune::Align::Center);
    let shown = if disabled {
        disabled::dim(shown)
    } else {
        shown
    };
    let line = door::gap(row().key(key.to_owned()).items_center(), Space::S4)
        .max_w_frac(1.0)
        .child(step("down", "Decrease", glyph::MINUS, Box::new(down)))
        .child(shown)
        .child(step("up", "Increase", glyph::PLUS, Box::new(up)));
    Control::new(line, Size::Regular)
}

/// Each arrow's hit area: half a regular field's height less its edges, and wide enough for a
/// fingertip on a trackpad.
const ARROW: (f32, f32) = (16.0, 14.0);

/// The up and down arrows at a number field's right edge, keyed `{key}.up` and `{key}.down`;
/// `step` runs with `true` for up.
pub fn arrows<S: 'static>(
    key: &str,
    t: &Tokens,
    step: impl Fn(&mut S, bool, &mut Tx) + Clone + 'static,
) -> El {
    let glyph = &kit(t).glyph;
    let arrow = |up: bool| {
        let step = step.clone();
        let arrow = row()
            .key(format!("{key}.{}", if up { "up" } else { "down" }))
            .role("button")
            .label(if up { "Increase" } else { "Decrease" })
            .size(ARROW.0, ARROW.1)
            .center()
            .child(
                door::icon(
                    if up {
                        glyph.chevron_up
                    } else {
                        glyph.chevron_down
                    },
                    IconSize::Small,
                )
                .color(t.text_secondary)
                .a11y_hidden(),
            )
            .on_press_tx(move |s: &mut S, tx: &mut Tx| step(s, up, tx));
        layers::pressable(arrow, t.bg_input, t)
    };
    let pair = col()
        .w(ARROW.0)
        .border(1.0, t.border)
        .child(arrow(true))
        .child(arrow(false));
    door::corner(pair, Radius::Tag)
}
