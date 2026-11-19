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

/// How thick a hollow dot's ring is.
const RING: f32 = 1.5;

impl DotSize {
    const fn side(self) -> f32 {
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
