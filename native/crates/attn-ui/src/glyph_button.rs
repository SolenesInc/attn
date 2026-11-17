use rune::{Color, El, Icon, TextStyle, Tx, row, text};
use rune_kit::{Button, ButtonVariant, Kit};

/// What a [`GlyphButton`] draws: an icon of [`crate::icon`], or text such as a mark of
/// [`crate::glyph`] or `Aa`.
pub enum Glyph {
    Icon(&'static Icon),
    Text(String),
}

impl Glyph {
    /// An icon `icon_size` points square, or text in `font`, in `ink`.
    pub fn draw(self, icon_size: f32, font: TextStyle, ink: Color) -> El {
        match self {
            Glyph::Icon(icon) => rune::icon(icon, icon_size).color(ink),
            Glyph::Text(glyph) => text(glyph).font(font).color(ink),
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

/// A button that draws a [`Glyph`] in place of its label, which names it. rune-kit's
/// `IconButton` sizes its icon to the kit's controls; attn's buttons size each icon as the
/// Tauri app's CSS does, so this is a `rune_kit::Button` that draws the glyph instead.
pub struct GlyphButton {
    glyph: Glyph,
    button: Button,
    side: f32,
    font: Option<TextStyle>,
    icon_size: f32,
    color: Option<Color>,
    trailing: Option<El>,
}

impl GlyphButton {
    pub fn new(glyph: impl Into<Glyph>, label: impl Into<String>) -> GlyphButton {
        GlyphButton {
            glyph: glyph.into(),
            button: Button::new(label).variant(ButtonVariant::GhostMuted),
            side: 22.0,
            font: None,
            icon_size: 14.0,
            color: None,
            trailing: None,
        }
    }

    /// An icon's width and height; 14 points unless set.
    pub fn icon_size(mut self, size: f32) -> Self {
        self.icon_size = size;
        self
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
            icon_size,
            color,
            trailing,
        } = self;
        let padding = if trailing.is_some() { 4.0 } else { 0.0 };
        button
            .label_with(move |style, ink| {
                let glyph = glyph.draw(icon_size, font.unwrap_or(style), color.unwrap_or(ink));
                match trailing {
                    Some(trailing) => row().gap(4.0).items_center().child(glyph).child(trailing),
                    None => glyph,
                }
            })
            .build(k)
            .h(side)
            .min_w(side)
            .px(padding)
    }
}
