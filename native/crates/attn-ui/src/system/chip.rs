use rune::{Color, El, row};

use super::{Radius, Space, Type, door, words};
use crate::Tokens;

/// A number in a small frame: a desktop's, filled with the brand while it is the current one and
/// dashed while it holds nothing.
pub fn number_chip(number: i64, current: bool, empty: bool, t: &Tokens) -> El {
    let ink = if current {
        crate::brand_ink(t)
    } else {
        t.text_tertiary
    };
    let chip = row()
        .min_w(door::line_height(Type::Tag) + 2.0 * Space::S2.pt())
        .justify_center()
        .items_center()
        .child(words(number.to_string(), Type::Tag, ink));
    let chip = door::corner(door::inset(chip, Space::S4, Space::S0), Radius::Tag);
    match (current, empty) {
        (true, _) => chip.fill(t.brand).border(1.0, t.brand),
        (false, true) => chip
            .border(
                1.0,
                crate::colors::mix_srgb(t.border_hover, t.border_focus, 0.4),
            )
            .border_dash(2.0, 2.0),
        (false, false) => chip.border(1.0, t.border),
    }
}

/// How many things wait, as a round count in `fill` with `ink`, such as the turns waiting on a
/// desktop hung off its chip's corner.
pub fn count_badge(count: usize, fill: Color, ink: Color) -> El {
    let badge = row()
        .min_w(door::line_height(Type::Tag))
        .justify_center()
        .items_center()
        .fill(fill)
        .child(words(count.to_string(), Type::Tag, ink));
    door::corner(door::inset(badge, Space::S4, Space::S0), Radius::Round)
}

/// The keys that do something, said quietly at the end of what they do.
pub fn keys_hint(keys: impl Into<String>, t: &Tokens) -> El {
    words(keys, Type::Mono, t.text_dimmed)
}

/// Which desktop a thing is on, as its slot number tucked against its mark on `ground`.
pub fn slot_badge(
    key: impl Into<String>,
    slot: impl Into<String>,
    ground: Color,
    t: &Tokens,
) -> El {
    let badge = row()
        .fill(ground)
        .child(words(slot, Type::Tag, t.text_dimmed).key(key.into()));
    door::corner(door::inset(badge, Space::S2, Space::S0), Radius::Tag)
}

/// `face`, such as a number chip, as a button keyed `key` and read as `label`: a compact square
/// at least, filled while it is `selected`.
pub fn chip_button(
    key: impl Into<String>,
    label: impl Into<String>,
    face: El,
    selected: bool,
    t: &Tokens,
) -> El {
    let side = super::Size::Compact.height();
    let rest = if selected { t.bg_elevated } else { t.bg_panel };
    let button = row()
        .key(key.into())
        .role("button")
        .focusable(true)
        .label(label.into())
        .min_w(side)
        .h(side)
        .justify_center()
        .items_center()
        .fill(rest)
        .child(face);
    let button = door::corner(door::inset(button, Space::S4, Space::S0), Radius::Control);
    super::focus::ring(super::layers::pressable(button, rest, t), t)
}
