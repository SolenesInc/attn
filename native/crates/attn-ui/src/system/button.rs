use rune::{Color, El, Tx, row};
use rune_kit::{Button as KitButton, ButtonVariant};

use super::{
    Control, IconSize, Radius, Said, Size, Space, Type, disabled, door, focus, kit, layers,
    width_of, words,
};
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

/// What a button shows: words, or one mark of [`crate::glyph`].
enum Shown {
    Words(Said),
    Mark(&'static str, IconSize),
}

/// attn's button: one shape for every look and size.
pub struct Button {
    shown: Shown,
    look: Look,
    size: Size,
    disabled: bool,
    open: bool,
    chosen: bool,
    trailing: Option<El>,
    press: Option<Press>,
    button: KitButton,
}

type Press = Box<dyn FnOnce(KitButton) -> KitButton>;

impl Button {
    pub fn new(words: impl Into<Said>) -> Button {
        let words = words.into();
        Button::showing(KitButton::new(words.text()), Shown::Words(words))
    }

    /// A button that shows `mark` `size` high in place of words, square at its size's height.
    /// Name what it does with `.label` on the built control.
    pub fn mark(mark: &'static str, size: IconSize) -> Button {
        Button::showing(KitButton::new(mark), Shown::Mark(mark, size))
    }

    fn showing(button: KitButton, shown: Shown) -> Button {
        Button {
            button,
            shown,
            look: Look::Plain,
            size: Size::Regular,
            disabled: false,
            open: false,
            chosen: false,
            trailing: None,
            press: None,
        }
    }

    pub fn look(mut self, look: Look) -> Self {
        self.look = look;
        self
    }

    /// How wide the button stands at `size` with nothing shrinking it: its words, its inset on
    /// each side and its edge, or its height when it shows a mark.
    pub(crate) fn natural_width(&self, size: Size) -> f32 {
        match &self.shown {
            Shown::Words(words) => width_of(words, Type::Control) + 2.0 * (size.inset().pt() + 1.0),
            Shown::Mark(..) => size.height(),
        }
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

    /// The button is one of a set that is on, such as a chosen day: the selected fill a chosen
    /// segment has, read as checked.
    pub fn chosen(mut self, chosen: bool) -> Self {
        self.chosen = chosen;
        self
    }

    pub fn help(mut self, help: impl Into<String>) -> Self {
        self.button = self.button.help(help);
        self
    }

    /// The keys that press it too, as its help reads them.
    pub fn shortcut(mut self, keys: impl Into<String>) -> Self {
        self.button = self.button.shortcut(keys);
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
            Look::Plain if self.open || self.chosen => {
                (t.bg_selected, t.border_hover, t.text_primary)
            }
            Look::Quiet if self.open || self.chosen => {
                (t.bg_selected, Color::TRANSPARENT, t.text_primary)
            }
            Look::Primary if self.disabled => {
                let fill = t.warning.alpha(disabled::STRENGTH);
                (fill, fill, crate::warning_ink(t))
            }
            Look::Primary => (t.warning, t.warning, crate::warning_ink(t)),
            Look::Plain => (t.bg_button, t.border_hover, t.text_primary),
            Look::Quiet => (Color::TRANSPARENT, Color::TRANSPARENT, t.text_secondary),
            Look::Danger => (t.bg_button, t.error.alpha(0.45), t.error),
        };
        let square = matches!(self.shown, Shown::Mark(..));
        let shown = self.shown;
        let trailing = self.trailing;
        let label = move |_, _| match shown {
            Shown::Words(shown) => door::gap(row().items_center(), Space::S4)
                .shrink()
                .min_w(0.0)
                .child(words(shown, Type::Control, ink).truncate())
                .children(trailing),
            Shown::Mark(mark, size) => door::control_mark(mark, size).color(ink),
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
        let button = if square {
            let side = self.size.height();
            button.w(side).min_w(side)
        } else {
            door::inset(button, self.size.inset(), Space::S0)
        };
        let button = focus::ring(door::corner(button, Radius::Control), t);
        let button = if self.disabled && self.look == Look::Primary {
            disabled::unavailable(button)
        } else if self.disabled {
            disabled::dim(button)
        } else {
            let surface = if fill.a > 0.0 { fill } else { t.bg_panel };
            layers::pressable(button, surface, t)
        };
        let button = if self.chosen {
            button.checked(true)
        } else {
            button
        };
        Control::new(button, self.size)
    }
}
