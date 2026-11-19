use rune::El;

use crate::Tokens;
use crate::system::{Radius, door};

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
}
