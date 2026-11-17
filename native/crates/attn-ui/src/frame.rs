use rune::{Color, El, Shadow};

use crate::Tokens;

/// How a floating surface is framed: its fill, its 1 point edge, its corners and the shadow
/// under it, as the Tauri app's CSS draws that surface.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Frame {
    pub fill: Color,
    pub edge: Color,
    pub radius: f32,
    pub shadow: Shadow,
}

impl Frame {
    /// A menu or a small popover over a row: `.session-actions-popover`, `.snooze-menu`,
    /// `.rename-popover`.
    pub fn menu(t: &Tokens) -> Frame {
        Frame {
            fill: t.bg_panel,
            edge: t.border,
            radius: 8.0,
            shadow: shadow(t, 16.0, 36.0, 0.42),
        }
    }

    /// A popover that opens from a tile header: `.session-usage-popover`,
    /// `.tended-seeds-popover`.
    pub fn panel(t: &Tokens) -> Frame {
        Frame {
            fill: t.bg_elevated,
            edge: t.border_hover,
            radius: 10.0,
            shadow: shadow(t, 12.0, 36.0, 0.25),
        }
    }

    /// A bar over a terminal and what it lists: `.ghostty-find-bar`, `.ghostty-filter-bar`,
    /// `.ghostty-filter-results`.
    pub fn terminal_bar(t: &Tokens) -> Frame {
        Frame {
            fill: t.bg_tile.alpha(0.95),
            edge: t.border,
            radius: 6.0,
            shadow: shadow(t, 2.0, 8.0, 0.25),
        }
    }

    pub fn draw(self, surface: El) -> El {
        surface
            .fill(self.fill)
            .border(1.0, self.edge)
            .radius(self.radius)
            .box_shadow(self.shadow)
    }
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
