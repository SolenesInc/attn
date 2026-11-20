use rune::{El, col, el};

use crate::Tokens;
use crate::system::{Radius, door};

/// The parts of a figure laid out to its own coordinates, such as a sequence diagram: the
/// figure's layout sizes each part and pins it where it goes.
pub struct Figure;

impl Figure {
    /// The box around a group of the figure's parts, such as a loop or an alternative: an edge
    /// and a faint tint.
    pub fn group(width: f32, height: f32, t: &Tokens) -> El {
        let group = el()
            .w(width)
            .h(height)
            .border(1.0, t.border)
            .fill(t.text_primary.alpha(0.03))
            .a11y_hidden();
        door::corner(group, Radius::Control)
    }

    /// One of the figure's parties, such as a participant: what it holds centred on an elevated
    /// box with an edge, round at its ends when it stands for a person.
    pub fn node(width: f32, height: f32, person: bool, t: &Tokens) -> El {
        let node = col()
            .w(width)
            .h(height)
            .center()
            .border(1.0, t.border_hover)
            .fill(t.bg_elevated);
        door::corner(
            node,
            if person {
                Radius::Round
            } else {
                Radius::Control
            },
        )
    }

    /// The band across one of the figure's lines, such as a message, that a reader stands on:
    /// tinted in the accent while it is current.
    pub fn band(width: f32, height: f32, current: bool, t: &Tokens) -> El {
        let band = door::corner(el().w(width).h(height), Radius::Control);
        if current {
            band.fill(t.accent.alpha(0.14)).focus_selected(true)
        } else {
            band
        }
    }
}
