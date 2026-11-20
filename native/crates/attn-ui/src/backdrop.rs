use rune::{Color, El, el};

use crate::Tokens;

/// What a surface over the window lays between itself and the window: a fill over everything
/// behind it, blurred first when `blur` is not 0.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Backdrop {
    fill: Color,
    blur: f32,
}

impl Backdrop {
    /// A surface that holds the person until it closes, such as a dialog or the launcher: the
    /// window dimmed and blurred behind it, so it reads as set aside.
    pub fn modal(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.shadow.alpha(0.5),
            blur: 2.0,
        }
    }

    /// The same dim without the blur, for a surface drawn inside a layer of the window, where
    /// a blur has nothing behind it to blur.
    pub fn dim(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.shadow.alpha(0.5),
            blur: 0.0,
        }
    }

    /// A finder's light wash of the app background, which keeps the window readable behind
    /// the palette and the file opener.
    pub fn finder(t: &Tokens) -> Backdrop {
        Backdrop {
            fill: t.bg_app.alpha(0.6),
            blur: 0.0,
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
