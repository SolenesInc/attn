use rune::{Color, El, Tx, col, el, row};

use super::{
    Control, IconSize, Radius, Size, Space, Type, disabled, door, focus, glyph, layers, words,
};
use crate::Tokens;

const DOT: f32 = 14.0;

/// The narrowest a choice's name gets beside its mark before it takes a line of its own under
/// it: the longest word of a colour scheme's name, "Catppuccin", measures 72.5 in `Name`.
const NAME_FLOOR: f32 = 80.0;

/// One option of a list of choices: a row with its mark, its name and a note under it.
pub struct Choice {
    key: String,
    name: String,
    note: Option<String>,
    leading: Option<El>,
    tag: Option<El>,
    many: bool,
    chosen: bool,
    disabled: bool,
}

impl Choice {
    pub fn new(key: impl Into<String>, name: impl Into<String>) -> Choice {
        Choice {
            key: key.into(),
            name: name.into(),
            note: None,
            leading: None,
            tag: None,
            many: false,
            chosen: false,
            disabled: false,
        }
    }

    pub fn note(mut self, note: impl Into<String>) -> Self {
        self.note = Some(note.into());
        self
    }

    /// What marks it before its name, such as a swatch.
    pub fn leading(mut self, mark: El) -> Self {
        self.leading = Some(mark);
        self
    }

    /// A tag after its name, such as the one it is recommended to choose.
    pub fn tag(mut self, tag: impl Into<El>) -> Self {
        self.tag = Some(tag.into());
        self
    }

    /// Any number of its list may be chosen, not only one: a box that takes a check, read as a
    /// checkbox, in place of a dot.
    pub fn many(mut self, many: bool) -> Self {
        self.many = many;
        self
    }

    pub fn chosen(mut self, chosen: bool) -> Self {
        self.chosen = chosen;
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    /// Its row, the `i`th of `n`, chosen with `press`.
    pub fn build<S: 'static>(
        self,
        (i, n): (usize, usize),
        t: &Tokens,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Control {
        let warm = self.chosen && !self.disabled;
        let (edge, ink) = match (warm, self.chosen) {
            (true, _) => (t.warning, t.text_primary),
            (false, true) => (t.border_hover, t.text_primary),
            (false, false) => (t.border, t.text_secondary),
        };
        let fill = if self.chosen {
            t.bg_elevated
        } else {
            t.bg_panel
        };
        let mark_ink = match (warm, self.chosen) {
            (true, _) => t.warning,
            (false, true) => t.text_tertiary,
            (false, false) => t.border_hover,
        };
        let dot = if self.many {
            let square = door::corner(el().size(DOT, DOT).center(), Radius::Tag);
            if self.chosen {
                square
                    .fill(mark_ink)
                    .child(glyph(crate::glyph::CHECK, IconSize::Small, t.bg_app))
            } else {
                square.border(1.0, mark_ink)
            }
        } else {
            door::corner(el().size(DOT, DOT), Radius::Round)
                .border(if self.chosen { 4.0 } else { 1.0 }, mark_ink)
        }
        .a11y_hidden();
        let name = words(self.name.clone(), Type::Name, ink)
            .wrap()
            .a11y_hidden();
        let name = match self.tag {
            Some(tag) => door::gaps(row().flex_wrap().items_center(), Space::S2, Space::S8)
                .child(name.shrink())
                .child(tag),
            None => name,
        };
        let named = door::gap(col().grow_from(1.0, NAME_FLOOR), Space::S2)
            .child(name)
            .children(self.note.map(|note| {
                words(note, Type::Small, t.text_tertiary)
                    .wrap()
                    .a11y_hidden()
            }));
        let said = door::gaps(
            row().grow(1.0).flex_wrap().items_center(),
            Space::S8,
            Space::S12,
        )
        .children(self.leading)
        .child(named);
        let row = row()
            .key(self.key)
            .role(if self.many { "checkbox" } else { "radio" })
            .label(self.name)
            .checked(self.chosen)
            .focusable(true)
            .focus_selected(self.chosen)
            .position_in_set(i + 1, n)
            .w_full()
            .min_h(Size::Regular.height())
            .items_center()
            .fill(if self.chosen {
                fill
            } else {
                Color::TRANSPARENT
            })
            .border(1.0, edge)
            .child(dot)
            .child(said);
        let row = door::inset(door::gap(row, Space::S12), Space::S12, Space::S8);
        let row = door::corner(row, Radius::Card);
        if self.disabled {
            return Control::new(disabled::dim(row), Size::Regular);
        }
        let row = layers::pressable(row, fill, t).on_press_tx(press);
        Control::new(focus::ring_on_edge(row, t), Size::Regular)
    }
}

/// The group that holds a list of choices any number of which may be on: each is a stop of Tab
/// of its own.
pub fn checks(key: impl Into<String>, label: &str) -> El {
    let group = col()
        .key(key.into())
        .role("group")
        .label(label.to_owned())
        .w_full();
    door::gap(group, Space::S4)
}

/// The group that holds a list of choices: the arrows move the choice.
pub fn choices(key: impl Into<String>, label: &str) -> El {
    let group = col()
        .key(key.into())
        .role("radiogroup")
        .label(label.to_owned())
        .focus_group()
        .w_full();
    door::gap(group, Space::S4)
}
