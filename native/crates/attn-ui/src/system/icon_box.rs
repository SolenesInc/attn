use rune::{El, row};

use super::{Control, Radius, Size, door};
use crate::Tokens;

/// A square that holds an icon or an initial, as wide as a regular control is tall. Dashed, it
/// stands for something not chosen yet.
pub fn icon_box(glyph: El, dashed: bool, t: &Tokens) -> Control {
    let side = Size::Regular.height();
    let b = row()
        .size(side, side)
        .center()
        .border(1.0, t.border_subtle)
        .child(glyph);
    let b = door::corner(b, Radius::Control);
    let b = if dashed {
        b.border_dash(3.0, 3.0)
    } else {
        b.fill(t.bg_elevated)
    };
    Control::new(b, Size::Regular)
}
