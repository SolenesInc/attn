use rune::{El, row};

use super::{Radius, Space, Type, door, focus, words};
use crate::Tokens;

/// Where something dragged can be dropped to make a new place for it, such as a new desktop:
/// an outlined area in the accent, filled while the drag is over it.
pub fn drop_zone(key: impl Into<String>, said: &str, over: bool, t: &Tokens) -> El {
    let zone = row()
        .key(key.into())
        .role("button")
        .focusable(true)
        .label(said.to_owned())
        .a11y_value(if over { "drop here" } else { "" })
        .w_full()
        .center()
        .fill(t.accent.alpha(if over { 0.14 } else { 0.04 }))
        .border(1.5, t.accent.alpha(if over { 1.0 } else { 0.5 }))
        .child(words(format!("+ {said}"), Type::Mono, t.accent));
    let zone = door::corner(door::inset(zone, Space::S12, Space::S12), Radius::Card);
    focus::ring_on_edge(zone, t)
}
