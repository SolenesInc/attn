use rune::{Color, El, Tx, row};
use rune_kit::{Button as KitButton, ButtonVariant};

use super::{Control, Radius, Size, Space, Type, door, focus, kit, layers, words};
use crate::Tokens;

/// How a button reads.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Look {
    /// The one main action of a surface, filled in the warm accent.
    Primary,
    #[default]
    Plain,
    /// No fill or edge until the pointer is over it.
    Quiet,
    /// Removes something or turns it off.
    Danger,
}

/// attn's button: one shape for every look and size.
pub struct Button {
    shown: String,
    look: Look,
    size: Size,
    disabled: bool,
    open: bool,
    trailing: Option<El>,
    press: Option<Press>,
    button: KitButton,
}

type Press = Box<dyn FnOnce(KitButton) -> KitButton>;

impl Button {
    pub fn new(shown: impl Into<String>) -> Button {
        let shown = shown.into();
        Button {
            button: KitButton::new(shown.clone()),
            shown,
            look: Look::Plain,
            size: Size::Regular,
            disabled: false,
            open: false,
            trailing: None,
            press: None,
        }
    }

    pub fn look(mut self, look: Look) -> Self {
        self.look = look;
        self
    }

    pub fn size(mut self, size: Size) -> Self {
        self.size = size;
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    /// A mark after the words, such as the chevron of a button that opens something.
    pub fn trailing(mut self, mark: El) -> Self {
        self.trailing = Some(mark);
        self
    }

    /// The button opens something, open now or not. Open, a Plain or Quiet button takes the
    /// selected fill, so it reads apart from hover.
    pub fn expanded(mut self, open: bool) -> Self {
        self.open = open;
        self.button = self.button.expanded(open);
        self
    }

    pub fn help(mut self, help: impl Into<String>) -> Self {
        self.button = self.button.help(help);
        self
    }

    pub fn on_press<S: 'static>(mut self, press: impl Fn(&mut S) + 'static) -> Self {
        self.press = Some(Box::new(move |b: KitButton| b.on_press(press)));
        self
    }

    pub fn on_press_tx<S: 'static>(mut self, press: impl Fn(&mut S, &mut Tx) + 'static) -> Self {
        self.press = Some(Box::new(move |b: KitButton| b.on_press_tx(press)));
        self
    }

    pub fn build(self, t: &Tokens) -> Control {
        let (fill, edge, ink) = match self.look {
            Look::Quiet if self.disabled => (Color::TRANSPARENT, Color::TRANSPARENT, t.text_dimmed),
            _ if self.disabled => (Color::TRANSPARENT, t.border, t.text_dimmed),
            Look::Plain if self.open => (t.bg_selected, t.border_hover, t.text_primary),
            Look::Quiet if self.open => (t.bg_selected, Color::TRANSPARENT, t.text_primary),
            Look::Primary => (t.warning, t.warning, crate::warning_ink(t)),
            Look::Plain => (t.bg_button, t.border_hover, t.text_primary),
            Look::Quiet => (Color::TRANSPARENT, Color::TRANSPARENT, t.text_secondary),
            Look::Danger => (t.bg_button, t.error.alpha(0.45), t.error),
        };
        let shown = self.shown;
        let trailing = self.trailing;
        let label = move |_, _| {
            door::gap(row().items_center(), Space::S4)
                .shrink()
                .min_w(0.0)
                .child(words(shown, Type::Control, ink).truncate())
                .children(trailing)
        };
        let button = match self.press {
            Some(attach) if !self.disabled => attach(self.button),
            _ => self.button,
        };
        let button = button
            .variant(ButtonVariant::Ghost)
            .label_with(label)
            .build(kit(t))
            .h(self.size.height())
            .shrink()
            .min_w(0.0)
            .justify_center()
            .fill(fill)
            .border(1.0, edge);
        let button = door::inset(button, self.size.inset(), Space::S0);
        let button = focus::ring(door::corner(button, Radius::Control), t);
        let button = if self.disabled {
            button.disabled(true)
        } else {
            let surface = if fill.a > 0.0 { fill } else { t.bg_panel };
            layers::pressable(button, surface, t)
        };
        Control::new(button, self.size)
    }
}
