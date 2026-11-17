use rune::{Color, El, el};

use crate::Tokens;

/// What a surface over the window lays between itself and the window: a fill over everything
/// behind it, blurred first when `blur` is not 0, as the Tauri app's CSS dims or washes the
/// window under that surface.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Backdrop {
    pub fill: Color,
    pub blur: f32,
}

impl Backdrop {
    /// A dialog that holds the person until it closes: `.whats-new-overlay`,
    /// `.shortcuts-overlay`, `.shortcut-editor-overlay`.
    pub fn modal(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.shadow.alpha(0.7),
            blur: 4.0,
        }
    }

    /// The scheme's own overlay: `.settings-overlay`, `.diagnostic-report-overlay`,
    /// `.desktop-overview-scrim`.
    pub fn overlay(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.bg_overlay,
            blur: 0.0,
        }
    }

    /// The command palette's wash of the app background: `.unified-palette`.
    pub fn palette(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.bg_app.alpha(0.6),
            blur: 0.0,
        }
    }

    /// A finder's lighter wash of the app background: `.palette`, which the file opener and
    /// the notebook's finder share.
    pub fn finder(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.bg_app.alpha(0.55),
            blur: 0.0,
        }
    }

    /// The new-session dialog's: `.location-picker-overlay`.
    pub fn launcher(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.shadow.alpha(0.5),
            blur: 2.0,
        }
    }

    /// The backdrop over the whole of its parent, keyed `key`.
    pub fn draw(self, key: impl Into<String>) -> El {
        el().key(key.into())
            .absolute(0.0, 0.0)
            .w_full()
            .h_full()
            .backdrop_blur(self.blur)
            .fill(self.fill)
            .a11y_hidden()
    }
}
