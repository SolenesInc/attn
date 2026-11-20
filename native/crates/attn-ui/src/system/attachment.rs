use rune::{El, el, row};

use super::{Radius, Said, Space, Type, door, words};
use crate::Tokens;

/// A file attached to a message, as a chip: its kind, its name, how big it is or that it is
/// still attaching, and the button that takes it off. Keyed `key`, read as `name`.
pub fn file_chip(
    key: impl Into<String>,
    name: impl Into<Said>,
    kind: impl Into<Said>,
    detail: impl Into<Said>,
    remove: Option<El>,
    t: &Tokens,
) -> El {
    let name = name.into();
    let chip = door::gap(row().items_center().min_w(0.0), Space::S4)
        .key(key.into())
        .role("listitem")
        .label(name.text())
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .child(words(kind, Type::Mono, t.text_tertiary))
        .child(
            words(name, Type::Small, t.text_primary)
                .single_line()
                .truncate()
                .shrink()
                .min_w(0.0)
                .max_w(NAME_MAX),
        )
        .child(words(detail, Type::Small, t.text_tertiary).single_line())
        .children(remove);
    door::corner(door::inset(chip, Space::S8, Space::S2), Radius::Control)
}

/// A chip's longest name before it ends in an ellipsis: a screenshot's dated name.
const NAME_MAX: f32 = 180.0;

/// An image attached to a message, as its picture cropped to `size`, with the button that takes
/// it off in its corner. Keyed `key`, read as `name`.
pub fn image_tile(
    key: impl Into<String>,
    name: &str,
    picture: Option<El>,
    (w, h): (f32, f32),
    remove: Option<El>,
    t: &Tokens,
) -> El {
    let corner = remove.map(|remove| {
        door::inset(
            row().absolute(0.0, 0.0).w_full().justify_end(),
            Space::S4,
            Space::S4,
        )
        .child(remove)
    });
    let tile = el()
        .key(key.into())
        .role("listitem")
        .label(name.to_owned())
        .size(w, h)
        .fill(t.bg_elevated)
        .border(1.0, t.border)
        .children(picture)
        .children(corner);
    door::clip_inside_corner(door::corner(tile, Radius::Card), Radius::Card)
}

/// What lies over a surface while files are dragged over it: the surface washed out, an accent
/// edge, and `said` in its middle.
pub fn drop_overlay(key: impl Into<String>, said: impl Into<Said>, t: &Tokens) -> El {
    let overlay = el()
        .key(key.into())
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .fill(t.bg_overlay)
        .border(2.0, t.accent)
        .items_center()
        .justify_center()
        .child(words(said, Type::Body, t.text_primary));
    door::corner(overlay, Radius::Panel)
}
