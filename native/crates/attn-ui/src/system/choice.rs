use rune::{Color, El, Tx, col, el, row};

use super::{Control, Radius, Size, Space, Type, disabled, door, focus, layers, words};
use crate::Tokens;

const DOT: f32 = 14.0;

/// One option of a list of choices: a row with its mark, its name and a note under it.
pub struct Choice {
    key: String,
    name: String,
    note: Option<String>,
    leading: Option<El>,
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
        let dot = door::corner(el().size(DOT, DOT), Radius::Round)
            .border(
                if self.chosen { 4.0 } else { 1.0 },
                match (warm, self.chosen) {
                    (true, _) => t.warning,
                    (false, true) => t.text_tertiary,
                    (false, false) => t.border_hover,
                },
            )
            .a11y_hidden();
        let named = door::gap(col().grow(1.0).min_w(0.0), Space::S2)
            .child(
                words(self.name.clone(), Type::Name, ink)
                    .wrap()
                    .a11y_hidden(),
            )
            .children(self.note.map(|note| {
                words(note, Type::Small, t.text_tertiary)
                    .wrap()
                    .a11y_hidden()
            }));
        let row = row()
            .key(self.key)
            .role("radio")
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
            .children(self.leading)
            .child(named);
        let row = door::inset(door::gap(row, Space::S12), Space::S12, Space::S8);
        let row = door::corner(row, Radius::Card);
        if self.disabled {
            return Control::new(disabled::dim(row), Size::Regular);
        }
        let row = layers::pressable(row, fill, t).on_press_tx(press);
        Control::new(focus::ring_on_edge(row, t), Size::Regular)
    }
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
