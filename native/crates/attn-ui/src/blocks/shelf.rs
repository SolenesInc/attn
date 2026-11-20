use rune::{El, row};

use crate::Tokens;
use crate::system::{DotSize, Radius, Space, chip_button, door};

/// The title bar's shelf of tiles off screen: chips in a row, each a column's tiles as their
/// marks with its name, either side of a chip per column on screen. The shelf fits as many
/// chips as its room holds, so it reads its widths here.
pub struct Shelf;

/// How far apart the shelf's chips stand.
const GAP: Space = Space::S4;
/// How far apart a chip's marks stand.
const MARK_GAP: Space = Space::S2;
/// A chip's inset either side, as [`chip_button`] draws it.
const CHIP_INSET: Space = Space::S4;

impl Shelf {
    /// The shelf's chips one after another, keyed `key`.
    pub fn row(key: impl Into<String>) -> El {
        door::gap(row().key(key.into()).items_center(), GAP)
    }

    /// The room between two of the shelf's chips.
    pub fn gap() -> f32 {
        GAP.pt()
    }

    /// A column's tiles as their marks, side by side.
    pub fn marks(marks: impl IntoIterator<Item = El>) -> El {
        door::gap(row().items_center(), MARK_GAP).children(marks)
    }

    /// How wide a chip stands that holds only `count` marks of `size`.
    pub fn marks_chip_width(count: usize, size: DotSize) -> f32 {
        let marks = count as f32;
        2.0 * CHIP_INSET.pt() + marks * size.side() + (marks - 1.0).max(0.0) * MARK_GAP.pt()
    }

    /// A chip of the shelf, keyed `key` and read as `label`, showing `face`: filled while it
    /// is `current`. The pointer moves the shelf's chips and the keyboard moves through the
    /// shelf by its shortcuts, so a chip takes no stop on Tab.
    pub fn chip(
        key: impl Into<String>,
        label: impl Into<String>,
        face: El,
        current: bool,
        t: &Tokens,
    ) -> El {
        chip_button(key, label, face, current, t).focusable(false)
    }

    /// The accent ring over a chip while what it brings arrives, keyed `key`, its corners
    /// the chip's.
    pub fn flash(key: impl Into<String>, t: &Tokens) -> El {
        door::corner(
            rune::el()
                .key(key.into())
                .pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
                .border(1.0, t.accent),
            Radius::Control,
        )
    }
}
