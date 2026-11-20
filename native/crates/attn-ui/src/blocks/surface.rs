use rune::{El, col};

use crate::Tokens;
use crate::system::{Radius, Space, door};

/// What a surface that holds other parts is, which decides its fill, its edge and its corners.
pub struct Surface;

impl Surface {
    /// A reader that holds a window, framed apart from the work around it.
    pub fn panel(content: El, t: &Tokens) -> El {
        door::corner(content.fill(t.bg_panel).border(1.0, t.border), Radius::Card)
    }

    /// One thing among others on a page or in a column, such as a seed on a board or a
    /// document in a tile.
    pub fn card(content: El, t: &Tokens) -> El {
        door::corner(
            content.fill(t.bg_panel).border(1.0, t.border_subtle),
            Radius::Card,
        )
    }

    /// A card whose rows run to its edges, such as a listing of code: clipped at its corners
    /// so the rows' tints follow them.
    pub fn listing(content: El, t: &Tokens) -> El {
        door::clip_corner(Surface::card(content, t), Radius::Card)
    }

    /// A document on its card, its words `inset` in from the card's edge, as
    /// [`super::document_inset`] gives it for the room.
    pub fn document(reading: El, inset: f32, t: &Tokens) -> El {
        door::corner(
            door::padded(col().w_full(), inset)
                .fill(t.bg_panel)
                .border(super::measure::DOCUMENT_EDGE, t.border.alpha(0.5)),
            Radius::Panel,
        )
        .child(reading)
    }

    /// The room between a document tile's edge and what it shows.
    pub fn gutter(content: El) -> El {
        door::padded(content, super::measure::DOCUMENT_GUTTER)
    }

    /// A figure drawn on its own ground, such as a diagram too large to fit, set in from the
    /// ground's edge: on `fill` with an edge of `edge`.
    pub fn figure(content: El, fill: rune::Color, edge: rune::Color) -> El {
        Surface::ground(door::inset(content, Space::S16, Space::S16), fill, edge)
    }

    /// A drawing on its own ground, filling it to its edge: on `fill` with an edge of `edge`.
    pub fn ground(content: El, fill: rune::Color, edge: rune::Color) -> El {
        door::corner(content.fill(fill).border(1.0, edge), Radius::Card)
    }

    /// A short list raised over the page it opens on, such as a card's menu: the elevated
    /// surface with an edge, clipped at its corners so its rows' tints follow them.
    pub fn raised(content: El, t: &Tokens) -> El {
        door::clip_corner(
            door::corner(
                content.fill(t.bg_elevated).border(1.0, t.border),
                Radius::Control,
            ),
            Radius::Control,
        )
    }
}
