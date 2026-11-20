use rune::El;

use crate::system::{Space, Type, door, words};
use crate::{Frame, Tokens};

/// A short status raised where something just happened, such as a drag over a terminal that
/// marked nothing, `width` wide at most.
pub fn tip(key: impl Into<String>, said: impl Into<String>, width: f32, t: &Tokens) -> El {
    let tip = words(said.into(), Type::Small, t.text_primary)
        .key(key.into())
        .role("status")
        .wrap()
        .w(width);
    Frame::panel(t).draw(door::inset(tip, Space::S8, Space::S6))
}
