use rune::{El, Spring, Tx, el, row};

use super::{Radius, Space, door};
use crate::Tokens;

const DOT: f32 = 7.0;
const CURRENT: f32 = 18.0;

/// A dot grows into the current step's bar over 160 ms.
const GROW: Spring = Spring::new(0.16, 0.0);

/// Where a person is in a few steps: a dot per step, the current one drawn as a bar, the row
/// keyed `key`. Each dot is a button named by `name(i)` that goes to its step, keyed
/// `{dot}.{i}`.
pub fn pager<S: 'static>(
    (key, dot): (&str, &str),
    steps: usize,
    current: usize,
    name: impl Fn(usize) -> String,
    t: &Tokens,
    go: impl Fn(&mut S, usize, &mut Tx) + Clone + 'static,
) -> El {
    let dots = (0..steps).map(|i| {
        let here = i == current;
        let go = go.clone();
        door::corner(
            el().key(format!("{dot}.{i}"))
                .role("button")
                .label(name(i))
                .a11y_value(if here { "current" } else { "" })
                .size(if here { CURRENT } else { DOT }, DOT)
                .fill(if here {
                    t.brand
                } else {
                    t.text_tertiary.alpha(0.6)
                })
                .motion(GROW),
            Radius::Round,
        )
        .on_press_tx(move |state: &mut S, tx: &mut Tx| go(state, i, tx))
    });
    door::gap(row().key(key.to_owned()).items_center(), Space::S6)
        .children(dots.collect::<Vec<_>>())
}
