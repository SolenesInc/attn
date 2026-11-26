use rune::{Beat, Color, El, Sequence, Tx, el, row};
use rune_kit::{Button as KitButton, ButtonVariant};

use super::{
    Control, IconSize, Radius, Size, Space, Type, disabled, door, focus, kit, layers, words,
};
use crate::{Glyph, Tokens};

/// The widest a button's words stand before they elide: "renovate-bot" and "dependabot[bot]"
/// whole in `Small`, the longest of the logins that ask for reviews most.
const SAID_WIDEST: f32 = 96.0;
/// A dot's side, and the ring of the ground around it that keeps it apart from the icon.
const DOT: f32 = super::DotSize::Small.side();
const DOT_RING: f32 = super::dot::RING;

/// A button that is only an icon, named by its label: square, as tall as its size, its icon
/// drawn at its icon size whatever the size of the square.
pub struct IconButton {
    key: String,
    glyph: Glyph,
    icon: IconSize,
    size: Size,
    disabled: bool,
    open: bool,
    ink: Option<Color>,
    count: Option<String>,
    keys: Option<String>,
    said: Option<String>,
    dot: Option<Color>,
    busy: bool,
    edged: bool,
    button: KitButton,
}

/// An icon-only button keyed `key` and read as `label`, its icon `glyph` at `icon`.
pub fn icon_button(
    key: impl Into<String>,
    label: impl Into<String>,
    glyph: impl Into<Glyph>,
    icon: IconSize,
) -> IconButton {
    IconButton {
        key: key.into(),
        glyph: glyph.into(),
        icon,
        size: Size::Compact,
        disabled: false,
        open: false,
        ink: None,
        count: None,
        keys: None,
        said: None,
        dot: None,
        busy: false,
        edged: false,
        button: KitButton::new(label),
    }
}

impl IconButton {
    pub fn size(mut self, size: Size) -> Self {
        self.size = size;
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    /// The button opens something, open now or not. Open, it takes the selected fill, apart
    /// from hover.
    pub fn expanded(mut self, open: bool) -> Self {
        self.open = open;
        self.button = self.button.expanded(open);
        self
    }

    /// The button keeps an edge at rest, for a control that stands alone in a bar.
    pub fn edged(mut self) -> Self {
        self.edged = true;
        self
    }

    /// The button stands for what is current, or for an action waiting on a second press: it
    /// takes the selected fill and says nothing about opening anything.
    pub fn selected(mut self, selected: bool) -> Self {
        self.open = selected;
        self
    }

    /// The icon in `ink` rather than the quiet ink every icon button draws in.
    pub fn ink(mut self, ink: Color) -> Self {
        self.ink = Some(ink);
        self
    }

    /// How many things wait behind the button, beside the icon and never over it: the button
    /// becomes a pill around both, filled with the accent, and takes the selected fill while what
    /// it opens is open.
    pub fn count(mut self, count: Option<String>) -> Self {
        self.count = count;
        self
    }

    /// The keys that press the button, said after its icon inside it: the button widens to hold
    /// them.
    pub fn keys(mut self, keys: Option<String>) -> Self {
        self.keys = keys;
        self
    }

    /// Words after the icon, such as whose the thing is: the button widens to hold them, up to
    /// a point, and narrows down to its icon when its row is short of room, its words eliding;
    /// its label still names what it does.
    pub fn said(mut self, said: impl Into<String>) -> Self {
        self.said = Some(said.into());
        self
    }

    /// A dot of `ink` on the icon's top end, saying something waits there, such as unread
    /// notifications.
    pub fn dot(mut self, dot: Option<Color>) -> Self {
        self.dot = dot;
        self
    }

    /// What the button does is under way: three dots bounce where its icon was, in the same box
    /// with the same help, and it takes no press until the work ends.
    pub fn busy(mut self, busy: bool) -> Self {
        self.busy = busy;
        self.disabled |= busy;
        self
    }

    pub fn help(mut self, help: impl Into<String>) -> Self {
        self.button = self.button.help(help);
        self
    }

    pub fn on_press<S: 'static>(mut self, press: impl Fn(&mut S) + 'static) -> Self {
        self.button = self.button.on_press(press);
        self
    }

    pub fn on_press_tx<S: 'static>(mut self, press: impl Fn(&mut S, &mut Tx) + 'static) -> Self {
        self.button = self.button.on_press_tx(press);
        self
    }

    /// How wide the button stands as drawn: its mark, its count and keys after it, and the room
    /// at its sides, or a square when that is wider.
    pub(crate) fn width(&self) -> f32 {
        let mark = match &self.glyph {
            Glyph::Icon(_) => self.icon.pt(),
            Glyph::Text(said) => door::mark_width(said, self.icon),
        };
        let words = self
            .count
            .iter()
            .map(|count| (count, Type::Tag))
            .chain(self.keys.iter().map(|keys| (keys, Type::Mono)))
            .map(|(said, kind)| Space::S4.pt() + door::width(said, kind))
            .sum::<f32>()
            + self
                .said
                .iter()
                .map(|said| Space::S4.pt() + door::width(said, Type::Small).min(SAID_WIDEST))
                .sum::<f32>();
        self.around(mark + words)
    }

    /// How narrow the button goes: its words, if it has any of its own, elided away.
    pub(crate) fn least_width(&self) -> f32 {
        match &self.said {
            Some(said) => self.width() - door::width(said, Type::Small).min(SAID_WIDEST),
            None => self.width(),
        }
    }

    fn around(&self, inside: f32) -> f32 {
        let (inset, _) = self.frame();
        self.size.height().max(inside + 2.0 * inset.pt())
    }

    /// The room at the button's sides and its corner: a counted button is a pill, a button with
    /// keys a wider control, a bare icon a square.
    fn frame(&self) -> (Space, Radius) {
        match (
            self.count.is_some(),
            self.keys.is_some() || self.said.is_some(),
        ) {
            (true, _) => (Space::S8, Radius::Round),
            (false, true) => (Space::S6, Radius::Control),
            (false, false) if self.size == Size::Small => (Space::S0, Radius::Control),
            (false, false) => (Space::S4, Radius::Control),
        }
    }

    pub fn build(self, t: &Tokens) -> Control {
        let counted = self.count.is_some();
        let (fill, ink) = match (counted, self.open) {
            (true, false) => (t.accent, crate::ink_on(t.accent, t)),
            (_, true) => (t.bg_selected, t.text_primary),
            (false, false) => (Color::TRANSPARENT, t.text_secondary),
        };
        let ink = self.ink.unwrap_or(ink);
        let (inset, corner) = self.frame();
        let eliding = self.said.is_some();
        let least = self.least_width();
        let (glyph, icon, count, keys, said) =
            (self.glyph, self.icon, self.count, self.keys, self.said);
        let keys_ink = t.text_dimmed;
        let ground = if fill.a > 0.0 { fill } else { t.bg_panel };
        let dot = self.dot;
        let busy = self.busy;
        let label = move |_, _| {
            if busy {
                return busy_dots(ink);
            }
            let mark = match glyph {
                Glyph::Icon(drawn) => door::icon(drawn, icon).color(ink),
                Glyph::Text(said) => door::mark_text(said, icon).color(ink),
            };
            let mark = match dot {
                Some(dot_ink) => row()
                    .child(mark)
                    .child(super::ringed_dot(dot_ink, ground).absolute(icon.pt() - DOT, -DOT_RING)),
                None => mark,
            };
            door::gap(row().items_center(), Space::S4)
                .child(mark)
                .children(count.map(|count| words(count, Type::Tag, ink)))
                .children(keys.map(|keys| words(keys, Type::Mono, keys_ink)))
                .children(said.map(|said| {
                    words(said, Type::Small, ink)
                        .single_line()
                        .truncate()
                        .shrink()
                        .max_w(SAID_WIDEST)
                }))
        };
        let label = move |a, b| {
            let label = label(a, b);
            if eliding {
                label.shrink().min_w(0.0)
            } else {
                label
            }
        };
        let side = self.size.height();
        let button = if self.disabled {
            self.button.disabled(true)
        } else {
            self.button
        };
        let button = button
            .variant(ButtonVariant::Ghost)
            .label_with(label)
            .build(kit(t))
            .key(self.key)
            .h(side)
            .min_w(side)
            .justify_center()
            .fill(fill);
        let button = if eliding {
            button.shrink().min_w(least)
        } else {
            button
        };
        let button = if self.edged {
            button.border(1.0, t.border)
        } else {
            button
        };
        let button = door::corner(door::inset(button, inset, Space::S0), corner);
        let button = focus::ring(button, t);
        let button = if self.disabled {
            disabled::dim(button)
        } else {
            let surface = if fill.a > 0.0 { fill } else { t.bg_panel };
            layers::pressable(button, surface, t)
        };
        Control::new(button, self.size)
    }
}

/// A busy button's dots: three, 4 points across and 2 apart, each bouncing from 0.6 scale and
/// half opacity to whole and back over 1.4 s, 0.16 s after the one before.
fn busy_dots(ink: Color) -> El {
    const SIDE: f32 = 4.0;
    const BOUNCE: &[Beat] = &[
        Beat::new(0.0).opacity(0.5).scale(0.6),
        Beat::new(0.56),
        Beat::new(1.12).opacity(0.5).scale(0.6),
        Beat::new(1.4).opacity(0.5).scale(0.6),
    ];
    door::gap(row().items_center(), Space::S2).children([0.0, 0.16, 0.32].map(|delay| {
        door::corner(el().size(SIDE, SIDE), Radius::Round)
            .fill(ink)
            .sequence(Sequence::new(BOUNCE).delay(delay).repeat())
    }))
}
