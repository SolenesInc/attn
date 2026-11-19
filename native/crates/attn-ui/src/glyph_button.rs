use rune::{Color, El, Icon, TextStyle, Tx, row, text};
use rune_kit::{Button, ButtonVariant, Kit};

use crate::system::{IconSize, Space, door};

/// What a [`GlyphButton`] draws: an icon of [`crate::icon`], or text such as a mark of
/// [`crate::glyph`] or `Aa`.
pub enum Glyph {
    Icon(&'static Icon),
    Text(String),
}

impl Glyph {
    fn draw(self, font: TextStyle, ink: Color) -> El {
        match self {
            Glyph::Icon(icon) => door::icon(icon, IconSize::Small).color(ink),
            Glyph::Text(glyph) => door::styled(text(glyph), font).color(ink),
        }
    }
}

impl From<&'static Icon> for Glyph {
    fn from(icon: &'static Icon) -> Glyph {
        Glyph::Icon(icon)
    }
}

impl From<&str> for Glyph {
    fn from(text: &str) -> Glyph {
        Glyph::Text(text.to_owned())
    }
}

impl From<String> for Glyph {
    fn from(text: String) -> Glyph {
        Glyph::Text(text)
    }
}

/// A glyph button's height and least width.
const SIDE: f32 = 22.0;

/// A button that draws a [`Glyph`] in place of its label, which names it. rune-kit's
/// `IconButton` sizes its icon to the kit's controls; attn's buttons size each icon
/// themselves, so this is a `rune_kit::Button` that draws the glyph instead.
pub struct GlyphButton {
    glyph: Glyph,
    button: Button,
    color: Option<Color>,
    trailing: Option<El>,
}

impl GlyphButton {
    pub fn new(glyph: impl Into<Glyph>, label: impl Into<String>) -> GlyphButton {
        GlyphButton {
            glyph: glyph.into(),
            button: Button::new(label).variant(ButtonVariant::GhostMuted),
            color: None,
            trailing: None,
        }
    }

    /// The glyph's color; the variant's ink unless set.
    pub fn color(mut self, color: Color) -> Self {
        self.color = Some(color);
        self
    }

    pub fn variant(mut self, variant: ButtonVariant) -> Self {
        self.button = self.button.variant(variant);
        self
    }

    /// Something drawn after the glyph, such as the keycap of the button's key.
    pub fn trailing(mut self, trailing: El) -> Self {
        self.trailing = Some(trailing);
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.button = self.button.disabled(disabled);
        self
    }

    pub fn help(mut self, help: impl Into<String>) -> Self {
        self.button = self.button.help(help);
        self
    }

    pub fn on_press<S: 'static>(mut self, f: impl Fn(&mut S) + 'static) -> Self {
        self.button = self.button.on_press(f);
        self
    }

    pub fn on_press_tx<S: 'static>(mut self, f: impl Fn(&mut S, &mut Tx) + 'static) -> Self {
        self.button = self.button.on_press_tx(f);
        self
    }

    pub fn build(self, k: &Kit) -> El {
        let GlyphButton {
            glyph,
            button,
            color,
            trailing,
        } = self;
        let padding = if trailing.is_some() {
            Space::S4
        } else {
            Space::S0
        };
        let button = button
            .label_with(move |style, ink| {
                let glyph = glyph.draw(style, color.unwrap_or(ink));
                match trailing {
                    Some(trailing) => door::gap(row(), Space::S4)
                        .items_center()
                        .child(glyph)
                        .child(trailing),
                    None => glyph,
                }
            })
            .build(k)
            .h(SIDE)
            .min_w(SIDE);
        door::inset(button, padding, Space::S0)
    }
}
