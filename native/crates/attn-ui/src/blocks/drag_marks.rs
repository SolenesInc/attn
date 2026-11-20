use rune::{El, row};

use crate::system::{Radius, Space, Type, door, words};
use crate::{Frame, Tokens};

/// The name of what a person drags, carried by the pointer on a raised label.
pub fn drag_ghost(key: impl Into<String>, said: impl Into<String>, t: &Tokens) -> El {
    let said = said.into();
    let ghost = row()
        .key(key.into())
        .role("status")
        .label(said.clone())
        .child(words(said, Type::Small, t.text_secondary).single_line());
    Frame::panel(t).draw(door::inset(ghost, Space::S8, Space::S4))
}

/// Where a dragged tile would land, laid over that place: the accent's wash inside the focus
/// edge.
pub fn drop_target(key: impl Into<String>, label: impl Into<String>, t: &Tokens) -> El {
    let target = rune::el()
        .key(key.into())
        .role("group")
        .label(label.into())
        .fill(t.accent.alpha(0.14))
        .border(1.5, t.border_focus);
    door::corner(target, Radius::Control)
}
