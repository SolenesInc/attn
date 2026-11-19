use rune::{El, Tx, col, row};

use super::{Button, Control, Radius, Size, Space, Type, door, kit, layers, words};
use crate::Tokens;

type Press<S> = Box<dyn Fn(&mut S, &mut Tx)>;

/// The width a stepper keeps for its value, so steppers in one column line up: "Match app" in
/// `Name` measures 64.
const STEPPER_VALUE: f32 = 80.0;

/// A value between a step down and a step up. `key.down`, `key.value` and `key.up` name its
/// parts; `what` names the value for a screen reader.
pub fn stepper<S: 'static>(
    key: &str,
    what: &str,
    value: String,
    t: &Tokens,
    down: impl Fn(&mut S, &mut Tx) + 'static,
    up: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let step = |dir: &str, said: &str, mark: &str, press: Press<S>| {
        El::from(
            Button::new(mark)
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
        .w(STEPPER_VALUE)
        .align(rune::Align::Center);
    let line = door::gap(row().key(key.to_owned()).items_center(), Space::S4)
        .child(step("down", "Decrease", "−", Box::new(down)))
        .child(shown)
        .child(step("up", "Increase", "+", Box::new(up)));
    Control::new(line, Size::Regular)
}

const ARROW: (f32, f32) = (14.0, 9.0);
const ARROW_GLYPH: f32 = 8.0;

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
                rune::icon(
                    if up {
                        glyph.chevron_up
                    } else {
                        glyph.chevron_down
                    },
                    ARROW_GLYPH,
                )
                .color(t.text_secondary)
                .a11y_hidden(),
            )
            .on_press_tx(move |s: &mut S, tx: &mut Tx| step(s, up, tx));
        layers::pressable(arrow, t.text_secondary)
    };
    let pair = col()
        .w(ARROW.0)
        .border(1.0, t.border)
        .child(arrow(true))
        .child(arrow(false));
    door::corner(pair, Radius::Tag)
}
