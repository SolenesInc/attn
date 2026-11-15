use rune::{El, TextStyle, Tx, text};
use rune_kit::{Button, ButtonVariant, Kit};

/// An icon button whose icon is a glyph of [`crate::icon`], or a short mark such as `×` or
/// `Aa`, named by its label. rune-kit's `IconButton` draws vector icons; attn's icons are
/// font glyphs, so this is a `rune_kit::Button` that draws the glyph in place of its label.
pub struct GlyphButton {
    glyph: String,
    button: Button,
    side: f32,
    font: Option<TextStyle>,
}

impl GlyphButton {
    pub fn new(glyph: impl Into<String>, label: impl Into<String>) -> GlyphButton {
        GlyphButton {
            glyph: glyph.into(),
            button: Button::new(label).variant(ButtonVariant::GhostMuted),
            side: 22.0,
            font: None,
        }
    }

    /// The button's height and least width; 22 points unless set.
    pub fn side(mut self, side: f32) -> Self {
        self.side = side;
        self
    }

    /// The glyph's type; the button's own unless set.
    pub fn font(mut self, font: TextStyle) -> Self {
        self.font = Some(font);
        self
    }

    pub fn variant(mut self, variant: ButtonVariant) -> Self {
        self.button = self.button.variant(variant);
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
            side,
            font,
        } = self;
        button
            .label_with(move |style, ink| text(glyph).font(font.unwrap_or(style)).color(ink))
            .build(k)
            .h(side)
            .min_w(side)
            .px(0.0)
    }
}
