use rune::{Color, El, el};

use super::{Radius, door};

/// How large a state's dot is, by what it stands beside.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DotSize {
    /// 6: beside `Small`, `Label` and `Tag` text.
    Small,
    /// 8: leading a row, such as an agent's state in the sidebar.
    Regular,
}

/// What a state's dot says by its shape.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DotShape {
    /// Filled: live or working, changed, failed.
    Filled,
    /// A ring: closed, clean, waiting for its time.
    Hollow,
    /// A square, its corners sharp so it reads apart from a round dot at this size: kept.
    Square,
}

/// How thick a hollow dot's ring is, and the ring of ground round a dot laid over a mark.
pub(crate) const RING: f32 = 1.5;

impl DotSize {
    /// How wide the dot stands, for a layout that reserves its room.
    pub const fn side(self) -> f32 {
        match self {
            DotSize::Small => 6.0,
            DotSize::Regular => 8.0,
        }
    }
}

/// The mark that says what state a thing is in, in `ink`.
pub fn dot(ink: Color, shape: DotShape, size: DotSize) -> El {
    let mark = el().size(size.side(), size.side()).a11y_hidden();
    match shape {
        DotShape::Filled => door::corner(mark, Radius::Round).fill(ink),
        DotShape::Hollow => door::corner(mark, Radius::Round).border(RING, ink),
        DotShape::Square => mark.fill(ink),
    }
}

/// How wide a [`ringed_dot`] stands, its ring included.
pub const RINGED_DOT: f32 = DotSize::Small.side() + 2.0 * RING;

/// A small dot laid over a mark's corner, such as turns waiting on a desktop, ringed in `ground`
/// so it reads apart from the mark it lies on.
pub fn ringed_dot(ink: Color, ground: Color) -> El {
    door::corner(el().size(RINGED_DOT, RINGED_DOT), Radius::Round)
        .fill(ink)
        .border(RING, ground)
        .inert()
        .a11y_hidden()
}
