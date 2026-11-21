use rune::{El, col, el};

use super::Inline;
use crate::Tokens;
use crate::system::{Radius, Space, door};

/// The band across a window's top that the window drags by: its parts on one joined line over
/// the panel's ground, with a rule under it.
pub struct WindowBar;

impl WindowBar {
    /// The bar `height` tall. Its first part starts `lights` in from its left edge, past the
    /// window's own controls, when the window shows them, and [`WindowBar::inset`] in otherwise.
    pub fn across(lights: Option<f32>, height: f32, t: &Tokens) -> El {
        let bar = Inline::joined()
            .w_full()
            .h(height)
            .items_center()
            .fill(t.bg_panel)
            .border_sides([0.0, 0.0, 1.0, 0.0], t.border);
        match lights {
            Some(start) => door::inset_start(door::sides(bar, Space::S0, INSET), start),
            None => door::sides(bar, INSET, INSET),
        }
    }

    /// Where a bar's first part starts when the window shows no controls of its own, and where
    /// its last part ends.
    pub fn inset() -> f32 {
        INSET.pt()
    }
}

const INSET: Space = Space::S8;

/// Where a short word about the whole window stands: at the window's foot, set in from its
/// edges.
#[derive(Clone, Copy)]
pub enum Foot {
    /// At its centre, such as the keys a sequence waits for.
    Centre,
    /// At its end, such as a notice that says what an action did.
    End,
}

/// `surface` keyed `key` at the window's foot, over everything else, where `foot` says.
pub fn at_foot(key: impl Into<String>, surface: El, foot: Foot) -> El {
    let column = col().w_full().h_full().justify_end();
    let column = match foot {
        Foot::Centre => column.items_center(),
        Foot::End => column.items_end(),
    };
    el().key(key.into())
        .absolute(0.0, 0.0)
        .w_full()
        .h_full()
        .child(door::inset(column, Space::S16, Space::S16).child(surface))
}

/// A ring around the part of the window that something points at, such as a callout's
/// target, standing just outside `rect`, the part's rectangle in the window.
pub fn pointing_ring(key: impl Into<String>, rect: [f32; 4], t: &Tokens) -> El {
    let ring = el()
        .key(key.into())
        .absolute(rect[0] - RING_OUTSET, rect[1] - RING_OUTSET)
        .size(rect[2] + RING_OUTSET * 2.0, rect[3] + RING_OUTSET * 2.0)
        .border(2.0, t.accent)
        .a11y_hidden()
        .inert();
    door::corner(ring, Radius::Card)
}

/// How far a pointing ring stands outside what it points at.
const RING_OUTSET: f32 = 3.0;
