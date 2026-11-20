use rune::{Color, El, Tx, row};

use super::expander::opens_in_place;
use super::{
    Control, Radius, Size, Space, Tone, Type, disabled, door, focus, layers, tag, verbatim,
};
use crate::Tokens;

/// The control that opens and closes a part of a page or a form: a chevron, its name, what
/// follows its name such as a value it sums up to (drawn, and what it says), and tags that say
/// what the closed part holds. Open, it takes the selected fill.
#[allow(clippy::too_many_arguments)]
pub fn disclosure<S: 'static>(
    key: impl Into<String>,
    label: &str,
    open: bool,
    beside: Option<(El, String)>,
    chips: &[String],
    failed: bool,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut S, &mut Tx) + 'static,
) -> Control {
    let size = Size::Compact;
    let key = key.into();
    let ink = if failed { t.error } else { t.text_secondary };
    let fill = if open {
        t.bg_selected
    } else {
        Color::TRANSPARENT
    };
    let (beside_el, beside_said) = match beside {
        Some((el, said)) => (Some(el.shrink().min_w(0.0).a11y_hidden()), Some(said)),
        None => (None, None),
    };
    let description = beside_said
        .into_iter()
        .chain(chips.iter().cloned())
        .collect::<Vec<_>>()
        .join(", ");
    let chip = |(i, said): (usize, &String)| {
        El::from(tag(format!("{key}.tag{i}"), said, Tone::Neutral, t)).a11y_hidden()
    };
    let said = door::gaps(
        row().shrink().flex_wrap().items_center(),
        Space::S4,
        Space::S8,
    )
    .child(
        verbatim(label, Type::Control, ink)
            .wrap_anywhere()
            .shrink()
            .min_w(0.0)
            .a11y_hidden(),
    )
    .children(beside_el)
    .children(chips.iter().enumerate().map(chip));
    let line = row()
        .key(key.clone())
        .role("button")
        .label(label.to_owned())
        .description(description)
        .expanded(open)
        .focusable(true)
        .self_start()
        .items_center()
        .min_h(size.height())
        .fill(fill)
        .child(opens_in_place(open, t))
        .child(said);
    let line = door::gap(door::inset(line, size.inset(), Space::S4), Space::S8);
    let line = door::corner(door::hang_left(line, size.inset()), Radius::Control);
    let line = if disabled {
        disabled::dim(line)
    } else {
        let surface = if open { fill } else { t.bg_panel };
        focus::ring(layers::pressable(line, surface, t).on_press_tx(press), t)
    };
    Control::new(row().w_full().child(line.shrink()), size)
}
