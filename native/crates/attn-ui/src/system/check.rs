use rune::{Color, Tx, col, el, row, text};

use super::{Control, Radius, Said, Size, Space, Type, disabled, door, focus, layers, words};
use crate::{Tokens, glyph};

const BOX: f32 = 14.0;

/// One option a person ticks or unticks on its own: a box, its name and a note under it. Its
/// box hangs out by a compact inset, so its words sit on the line of the words around it.
pub struct Check {
    key: String,
    name: Said,
    note: Option<Said>,
    checked: bool,
    disabled: bool,
}

impl Check {
    pub fn new(key: impl Into<String>, name: impl Into<Said>) -> Check {
        Check {
            key: key.into(),
            name: name.into(),
            note: None,
            checked: false,
            disabled: false,
        }
    }

    pub fn note(mut self, note: impl Into<Said>) -> Self {
        self.note = Some(note.into());
        self
    }

    pub fn checked(mut self, checked: bool) -> Self {
        self.checked = checked;
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    pub fn build<S: 'static>(
        self,
        t: &Tokens,
        press: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Control {
        let size = Size::Compact;
        let (fill, edge, ink) = match (self.checked, self.disabled) {
            (true, false) => (t.primary, t.primary, t.on_primary),
            (true, true) => (t.text_tertiary, t.text_tertiary, t.bg_panel),
            (false, _) => (t.bg_input, t.border_hover, t.bg_input),
        };
        let mark = door::corner(el().size(BOX, BOX), Radius::Tag)
            .fill(fill)
            .border(1.0, edge)
            .center()
            .children(
                self.checked
                    .then(|| door::styled(text(glyph::CHECK), door::style(Type::Mono)).color(ink)),
            )
            .a11y_hidden();
        let mark = row()
            .h(door::line_height(Type::Name))
            .items_center()
            .child(mark);
        let said = door::gap(col().grow(1.0).min_w(0.0), Space::S2)
            .child(
                words(self.name.clone(), Type::Name, t.text_primary)
                    .wrap()
                    .a11y_hidden(),
            )
            .children(self.note.clone().map(|note| {
                words(note, Type::Small, t.text_tertiary)
                    .wrap()
                    .a11y_hidden()
            }));
        let line = row()
            .key(self.key)
            .role("checkbox")
            .label(self.name.text())
            .description(self.note.map(|note| note.text()).unwrap_or_default())
            .checked(self.checked)
            .focusable(!self.disabled)
            .w_full()
            .items_start()
            .fill(Color::TRANSPARENT)
            .child(mark)
            .child(said);
        let line = door::gap(line, Space::S8);
        let line = door::hang_left(door::inset(line, size.inset(), Space::S4), size.inset());
        let line = door::corner(line, Radius::Control);
        if self.disabled {
            return Control::new(disabled::dim(line), size);
        }
        let line = layers::pressable(line, t.bg_panel, t).on_press_tx(press);
        Control::new(focus::ring(line, t), size)
    }
}
