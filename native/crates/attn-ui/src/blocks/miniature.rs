use rune::{El, el};

use crate::Tokens;
use crate::system::{Radius, Said, Space, Type, door, words};

/// A layout drawn small, such as a desktop's tiles in the overview: on the app's ground, each
/// tile a box under its name, the active one edged in the accent. Whoever draws it splits the
/// tiles as its layout does.
pub struct Miniature;

impl Miniature {
    /// The ground the tiles of `layout` stand on, `aspect` as tall as it is wide, or empty.
    pub fn ground(layout: Option<El>, aspect: f32, t: &Tokens) -> El {
        door::corner(
            el().w_full()
                .aspect_ratio(aspect)
                .fill(t.bg_app)
                .children(layout),
            Radius::Tag,
        )
    }

    /// One tile of the layout, named `name`, `active` when it is the one acted on.
    pub fn tile(name: impl Into<Said>, active: bool, t: &Tokens) -> El {
        let (edge, ink) = if active {
            (t.accent, t.text_primary)
        } else {
            (t.border, t.text_secondary)
        };
        let tile = el()
            .w_full()
            .h_full()
            .min_w(0.0)
            .min_h(0.0)
            .fill(t.bg_button)
            .border(1.0, edge)
            .child(words(name, Type::Small, ink).single_line().truncate());
        door::clip(door::inset(tile, Space::S4, Space::S2))
    }
}
