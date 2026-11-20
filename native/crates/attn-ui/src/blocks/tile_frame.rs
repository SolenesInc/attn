use rune::El;

use crate::system::door;

/// The desktop's tile area, which draws nothing past its box while a row of tiles slides
/// through it.
pub fn tile_area(area: El) -> El {
    door::clip(area)
}

/// A tile's header and body, which draw nothing past the tile.
pub fn tile_content(content: El) -> El {
    door::clip(content)
}
