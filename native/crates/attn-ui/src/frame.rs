use rune::{Color, El, Shadow};

use crate::Tokens;
use crate::system::{Radius, Space, door};

/// A surface that floats over the window: its fill, its 1-point edge, its corners and the
/// shadow under it. What the surface is decides all four, so a view picks the surface and
/// never a corner or a shadow. A surface anchored to a control takes a card's corners; a surface
/// over the whole window takes a panel's.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Frame {
    fill: Color,
    edge: Color,
    radius: Radius,
    shadow: Shadow,
}

impl Frame {
    /// A list of choices opened from a control: a menu, a select's list, a small popover over a
    /// row.
    pub fn menu(t: &Tokens) -> Frame {
        Frame {
            fill: t.bg_panel,
            edge: t.border,
            radius: Radius::Card,
            shadow: anchored(t),
        }
    }

    /// A panel of details opened from a control, raised above a menu.
    pub fn panel(t: &Tokens) -> Frame {
        Frame {
            fill: t.bg_elevated,
            edge: t.border_hover,
            radius: Radius::Card,
            shadow: anchored(t),
        }
    }

    /// Edged in `ink`, such as the panel that holds the keyboard.
    pub fn edged(self, ink: Color) -> Frame {
        Frame { edge: ink, ..self }
    }

    /// A dialog, a finder, or anything else over the window that holds it until it closes.
    pub fn dialog(t: &Tokens) -> Frame {
        Frame {
            fill: t.bg_panel,
            edge: t.border_hover,
            radius: Radius::Panel,
            shadow: over_window(t),
        }
    }

    /// A bar over a terminal and what it lists.
    pub fn terminal_bar(t: &Tokens) -> Frame {
        Frame {
            fill: t.bg_tile.alpha(0.95),
            edge: t.border,
            radius: Radius::Control,
            shadow: shadow(t, 2.0, 8.0, 0.25),
        }
    }

    pub fn fill(&self) -> Color {
        self.fill
    }

    pub fn edge(&self) -> Color {
        self.edge
    }

    pub fn shadow(&self) -> Shadow {
        self.shadow
    }

    pub fn draw(self, surface: El) -> El {
        door::corner(
            surface
                .fill(self.fill)
                .border(1.0, self.edge)
                .box_shadow(self.shadow),
            self.radius,
        )
    }

    /// [`draw`](Self::draw), what it holds set in from its edge, for a surface a feature lays out
    /// itself from labels, fields and rows, such as the route picker's two lists.
    pub fn draw_holding(self, surface: El) -> El {
        self.draw(door::inset(surface, Space::S8, Space::S8))
    }

    /// [`draw`](Self::draw), with what the surface holds clipped to its corners, inside its
    /// edge.
    pub fn draw_clipped(self, surface: El) -> El {
        door::clip_inside_corner(self.draw(surface), self.radius)
    }
}

fn anchored(t: &Tokens) -> Shadow {
    shadow(t, 16.0, 36.0, 0.42)
}

fn over_window(t: &Tokens) -> Shadow {
    shadow(t, 24.0, 64.0, 0.5)
}

/// `box-shadow: 0 {y}px {blur}px rgba(0, 0, 0, {alpha})`.
pub fn shadow(t: &Tokens, y: f32, blur: f32, alpha: f32) -> Shadow {
    Shadow {
        y,
        blur,
        color: t.shadow.alpha(alpha),
        ..Shadow::default()
    }
}
