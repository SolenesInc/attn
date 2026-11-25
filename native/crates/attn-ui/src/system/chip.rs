use rune::{Color, El, row};

use super::{Radius, Said, Space, Type, door, words};
use crate::Tokens;

/// A number in a small frame: a desktop's, filled with the brand while it is the current one and
/// dashed while it holds nothing.
pub fn number_chip(number: i64, current: bool, empty: bool, t: &Tokens) -> El {
    let ink = if current {
        crate::ink_on(t.brand, t)
    } else {
        t.text_tertiary
    };
    let chip = chip_frame(words(number.to_string(), Type::Tag, ink));
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

/// `face` in a chip's small frame, edged as a number chip at rest, such as the count of the
/// desktops without a number or the overview's mark.
pub fn chip_edge(face: El, t: &Tokens) -> El {
    chip_frame(face).border(1.0, t.border)
}

fn chip_frame(face: El) -> El {
    let chip = row()
        .min_w(door::line_height(Type::Tag) + 2.0 * Space::S2.pt())
        .justify_center()
        .items_center()
        .child(face);
    door::corner(door::inset(chip, Space::S4, Space::S0), Radius::Tag)
}

/// How wide [`chip_button`] at `size` draws a [`number_chip`] or a [`chip_edge`] that says
/// `said`.
pub fn chip_width(said: &str, size: super::Size) -> f32 {
    let frame = (door::line_height(Type::Tag) + 2.0 * Space::S2.pt())
        .max(door::width(said, Type::Tag) + 2.0 * Space::S4.pt());
    size.height().max(frame + 2.0 * Space::S4.pt())
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

/// How many things a card or a group holds, said as `said`: words in the brand on the brand
/// mixed into the panel, round, as the count beside a card's title or a repository's name.
pub fn count_chip(said: impl Into<Said>, t: &Tokens) -> El {
    let ground = crate::colors::mix_srgb(t.bg_panel, t.brand, super::tag::PILL_WASH);
    let ink =
        crate::colors::moved_until_it_reads(t.brand, t.text_primary, ground, crate::colors::READS);
    let chip = row()
        .min_w(door::line_height(Type::Small))
        .justify_center()
        .items_center()
        .fill(ground)
        .child(words(said, Type::Small, ink).single_line());
    door::corner(door::inset(chip, Space::S6, Space::S0), Radius::Round)
}

/// Keys drawn as keys: an outlined cap for each, in the order they are pressed together.
pub fn keycaps(keys: impl IntoIterator<Item = impl Into<String>>, t: &Tokens) -> El {
    door::gap(row().items_center(), Space::S2).children(keys.into_iter().map(|key| {
        let [ink, edge, face] = crate::keycap::hint_colors(t.text_secondary);
        let cap = row()
            .fill(face)
            .border(1.0, edge)
            .child(words(key.into(), Type::Mono, ink));
        door::corner(door::inset(cap, Space::S4, Space::S0), Radius::Tag)
    }))
}

/// How wide [`keycaps`] draws `keys`.
pub fn keycaps_width<'a>(keys: impl IntoIterator<Item = &'a str>) -> f32 {
    let caps: Vec<f32> = keys
        .into_iter()
        .map(|key| door::width(key, Type::Mono) + 2.0 * Space::S4.pt())
        .collect();
    caps.iter().sum::<f32>() + Space::S2.pt() * caps.len().saturating_sub(1) as f32
}

/// How wide [`keys_hint`] draws `keys` and `what`.
pub fn keys_hint_width<'a>(keys: impl IntoIterator<Item = &'a str>, what: &str) -> f32 {
    keycaps_width(keys) + Space::S6.pt() + door::width(what, Type::Small)
}

/// What the keys `keys` do, said after their caps, read as one name.
pub fn keys_hint(
    keys: impl IntoIterator<Item = impl Into<String>>,
    what: impl Into<Said>,
    t: &Tokens,
) -> El {
    let keys: Vec<String> = keys.into_iter().map(Into::into).collect();
    let what = what.into();
    let least = keycaps_width(keys.iter().map(String::as_str));
    door::gap(row().items_center(), Space::S6)
        .min_w(least)
        .label(format!("{} {}", keys.join(" "), what.text()))
        .child(keycaps(keys, t).a11y_hidden())
        .child(
            words(what, Type::Small, t.text_tertiary)
                .single_line()
                .truncate()
                .shrink()
                .a11y_hidden(),
        )
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
        .child(words(slot.into(), Type::Tag, t.text_dimmed).key(key.into()));
    door::corner(door::inset(badge, Space::S2, Space::S0), Radius::Tag)
}

/// `face`, such as a number chip, as a button keyed `key` and read as `label`: a square of
/// `size` at least, filled while it is `selected`. A chip in a list's rows is `Small`; one in
/// a bar of controls is as tall as they are.
pub fn chip_button(
    key: impl Into<String>,
    label: impl Into<String>,
    face: El,
    selected: bool,
    size: super::Size,
    t: &Tokens,
) -> El {
    let side = size.height();
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
