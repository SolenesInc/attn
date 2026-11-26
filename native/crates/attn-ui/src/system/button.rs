use rune::{Color, El, Tx, row};
use rune_kit::{Button as KitButton, ButtonVariant};

use super::{
    Control, IconSize, Join, Radius, Said, Size, Space, Type, disabled, door, focus, kit, layers,
    width_of, words,
};
use crate::Tokens;

/// How a button reads.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Look {
    /// The one main action of a surface, filled in `primary`.
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
    unavailable: bool,
    open: bool,
    chosen: Option<bool>,
    joined: Option<Join>,
    leading: Option<El>,
    quiet: Option<(Said, Said)>,
    at_most: Option<f32>,
    trailing: Option<El>,
    whole: bool,
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
            unavailable: false,
            open: false,
            chosen: None,
            joined: None,
            leading: None,
            quiet: None,
            at_most: None,
            trailing: None,
            whole: false,
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

    /// The button is one half of a [`super::split`], square on the side `join` meets the other.
    pub fn joined(mut self, join: Join) -> Self {
        self.joined = Some(join);
        self
    }

    /// A mark before the words, such as the dot of a count.
    pub fn leading(mut self, mark: El) -> Self {
        self.leading = Some(mark);
        self
    }

    /// The words are never cut, and the button never stands narrower than them and its marks:
    /// what follows them gives way first, such as a count's crumbs that may shrink to nothing.
    pub fn whole(mut self) -> Self {
        self.whole = true;
        self
    }

    /// Quieter words after the button's own, `said` after `lead`, such as "to" and whom it sends
    /// to: `said` gives way first when the button is short of room, and the button's own words
    /// and `lead` stay whole.
    pub fn then_quiet(mut self, lead: impl Into<Said>, said: impl Into<Said>) -> Self {
        self.quiet = Some((lead.into(), said.into()));
        self
    }

    /// Its words, or its quieter words when it has them, stand at most `width` wide and are cut
    /// short past it, such as a session's name, so the button gives way before what stands
    /// beside it.
    pub fn words_at_most(mut self, width: f32) -> Self {
        self.at_most = Some(width);
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

    /// The button turns something on or off, such as a chosen day: on, the selected fill a chosen
    /// segment has, read as checked; off, read as unchecked.
    pub fn chosen(mut self, chosen: bool) -> Self {
        self.chosen = Some(chosen);
        self
    }

    /// The button can't act now, for `reason`: drawn as disabled, it keeps its stop on Tab so
    /// the keyboard can reach it and read why.
    pub fn unavailable(mut self, reason: impl Into<String>) -> Self {
        self.disabled = true;
        self.unavailable = true;
        self.button = self.button.help(reason);
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
            Look::Plain if self.open || self.chosen == Some(true) => {
                (t.bg_selected, t.border_hover, t.text_primary)
            }
            Look::Quiet if self.open || self.chosen == Some(true) => {
                (t.bg_selected, Color::TRANSPARENT, t.text_primary)
            }
            Look::Primary if self.disabled => {
                let fill = t.primary.alpha(disabled::STRENGTH);
                (fill, fill, ink_on_disabled_primary(t))
            }
            Look::Primary => (t.primary, t.primary, t.on_primary),
            Look::Plain => (t.bg_button, t.border_hover, t.text_primary),
            Look::Quiet => (Color::TRANSPARENT, Color::TRANSPARENT, t.text_secondary),
            Look::Danger => (t.bg_button, t.error.alpha(0.45), t.error),
        };
        let square = matches!(self.shown, Shown::Mark(..));
        let face = Face {
            inset: if square { Space::S0 } else { self.size.inset() },
            joined: self.joined,
            whole: self.whole,
            ..if !self.disabled {
                Face::BARE
            } else if self.look == Look::Primary {
                Face {
                    fill,
                    edge,
                    ..Face::BARE
                }
            } else {
                Face {
                    fill,
                    edge,
                    strength: disabled::STRENGTH,
                    ..Face::BARE
                }
            }
        };
        let shown = self.shown;
        let (leading, trailing) = (self.leading, self.trailing);
        let role = Type::Control;
        let whole = self.whole;
        let at_most = self.at_most;
        let most = move |e: El| match at_most {
            Some(width) => e.max_w(width),
            None => e,
        };
        let quiet = self.quiet.map(|(lead, said)| {
            door::gap(row().items_center(), Space::S4)
                .shrink()
                .min_w(0.0)
                .child(words(lead, role, t.text_secondary))
                .child(most(words(said, role, t.text_secondary).truncate()))
        });
        let label = move |_, _| {
            let label = match shown {
                Shown::Words(shown) => {
                    giving_way(door::gap(row().items_center(), Space::S4), whole)
                        .children(leading)
                        .child(match quiet {
                            Some(quiet) => {
                                let at_least = width_of(&shown, role);
                                door::gap(row().items_center(), Space::S4)
                                    .shrink()
                                    .min_w(0.0)
                                    .child(words(shown, role, ink).min_w(at_least))
                                    .child(quiet)
                            }
                            None if whole => words(shown, role, ink),
                            None => most(words(shown, role, ink).truncate()),
                        })
                        .children(trailing)
                }
                Shown::Mark(mark, size) => door::control_mark(mark, size).color(ink),
            };
            face.around(label)
        };
        let button = match self.press {
            Some(attach) if !self.disabled => attach(self.button),
            _ => self.button,
        };
        let button = button
            .variant(ButtonVariant::Ghost)
            .label_with(label)
            .build(kit(t))
            .h(self.size.height());
        let button = door::inset(
            giving_way(button, whole).justify_center(),
            Space::S0,
            Space::S0,
        );
        let button = if self.disabled {
            button
        } else {
            button.fill(fill).border(1.0, edge)
        };
        let button = if square {
            let side = self.size.height();
            button.w(side).min_w(side)
        } else {
            button
        };
        let button = match self.joined {
            Some(join) => focus::ring_inside(door::corner_joined(button, Radius::Control, join), t),
            None => focus::ring(door::corner(button, Radius::Control), t),
        };
        let button = if self.unavailable {
            disabled::reachable(button)
        } else if self.disabled {
            disabled::unavailable(button)
        } else {
            let surface = if fill.a > 0.0 { fill } else { t.bg_panel };
            layers::pressable(button, surface, t)
        };
        let button = match self.chosen {
            Some(on) => button.checked(on),
            None => button,
        };
        Control::new(button, self.size)
    }
}

/// `el` shrinking when its row runs out of room: down to nothing, or for a button whose words
/// are whole, down to what it holds at its narrowest, its words whole beside its marks.
fn giving_way(el: El, whole: bool) -> El {
    if whole {
        el.shrink_first()
    } else {
        el.shrink().min_w(0.0)
    }
}

/// What a button draws inside its focus ring. An unavailable one draws its fill, its edge and
/// its words here, together at `strength`, so the ring round it stays whole; an available one
/// keeps its fill and edge on the button, under its hover and press.
#[derive(Clone, Copy)]
struct Face {
    fill: Color,
    edge: Color,
    inset: Space,
    joined: Option<Join>,
    whole: bool,
    strength: f32,
}

impl Face {
    const BARE: Face = Face {
        fill: Color::TRANSPARENT,
        edge: Color::TRANSPARENT,
        inset: Space::S0,
        joined: None,
        whole: false,
        strength: 1.0,
    };

    fn around(self, label: El) -> El {
        let face = giving_way(row().w_full().h_full(), self.whole)
            .items_center()
            .justify_center()
            .fill(self.fill)
            .border(1.0, self.edge)
            .child(label);
        let face = door::inset(face, self.inset, Space::S0);
        match self.joined {
            Some(join) => door::corner_joined(face, Radius::Control, join),
            None => door::corner(face, Radius::Control),
        }
        .layer_opacity(self.strength)
    }
}

/// Whichever of `on_primary` and the text colour reads further on a disabled Primary's
/// half-strength fill, so `on_primary` answers only to the full-strength one.
fn ink_on_disabled_primary(t: &Tokens) -> Color {
    let fill = crate::colors::mix_srgb(t.bg_panel, t.primary, disabled::STRENGTH);
    crate::colors::reads_furthest(fill, [t.on_primary, t.text_primary])
}
