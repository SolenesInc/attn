use rune::{El, col, row};

use super::block::{Block, Kind};
use super::measure::FIELD;
use crate::Tokens;
use crate::system::{Radius, Said, Size, Space, Type, door, words};

/// A labelled control: its name above it, a note beside the name, and a hint and an error under
/// it. A field runs no longer than the field measure.
pub struct Field {
    key: String,
    label: Said,
    note: Option<Said>,
    controls: Vec<El>,
    action: Option<El>,
    value: Option<Said>,
    hint: Option<Said>,
    error: Option<Said>,
    mark: Option<El>,
    refusal: Option<El>,
}

impl Field {
    /// `key` is the key of the input the field holds, so the two never share one: the field is
    /// `{key}.field`, its label `{key}.label`, the value it shows `{key}.value`, its hint
    /// `{key}.hint` and its error `{key}.error`.
    pub fn new(key: impl Into<String>, label: impl Into<Said>) -> Field {
        Field {
            key: key.into(),
            label: label.into(),
            note: None,
            controls: Vec::new(),
            action: None,
            value: None,
            hint: None,
            error: None,
            mark: None,
            refusal: None,
        }
    }

    /// A control under the label. A field with several stacks them in order.
    pub fn control(mut self, control: impl Into<El>) -> Self {
        self.controls.push(control.into());
        self
    }

    /// A button beside the last control that fills it another way, such as Browse.
    pub fn action(mut self, button: impl Into<El>) -> Self {
        self.action = Some(button.into());
        self
    }

    /// A value read but not changed here, in place of a control.
    pub fn value(mut self, value: impl Into<Said>) -> Self {
        self.value = Some(value.into());
        self
    }

    pub fn note(mut self, note: impl Into<Said>) -> Self {
        self.note = Some(note.into());
        self
    }

    pub fn hint(mut self, hint: impl Into<Said>) -> Self {
        self.hint = Some(hint.into());
        self
    }

    pub fn error<S: Into<Said>>(mut self, error: Option<S>) -> Self {
        self.error = error.map(Into::into);
        self
    }

    /// A mark at the end of the label's line, such as Saved, so the control keeps its width when
    /// it comes and goes.
    pub fn mark(mut self, mark: Option<El>) -> Self {
        self.mark = mark;
        self
    }

    /// Why the value was refused, with its way to try again ([`super::Refusal`]), right under the
    /// control, above the hint, so it shows where the person is looking.
    pub fn refusal(mut self, refusal: Option<El>) -> Self {
        self.refusal = refusal;
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Field, self.laid(t).w_full())
    }

    /// The field on its own, as wide as what it holds, for fields that share a line, such as
    /// the rates of one price.
    pub fn el(self, t: &Tokens) -> El {
        self.laid(t)
    }

    fn laid(self, t: &Tokens) -> El {
        let label = door::gaps(
            row()
                .key(format!("{}.label", self.key))
                .w_full()
                .items_start()
                .flex_wrap(),
            Space::S2,
            Space::S8,
        )
        .child(
            words(self.label, Type::Label, t.text_tertiary)
                .wrap()
                .grow_from(1.0, 0.0),
        )
        .children(self.note.map(|n| {
            words(n, Type::Small, t.text_muted)
                .wrap()
                .shrink()
                .min_w(0.0)
        }))
        .children(self.mark);
        let value = self.value.map(|v| {
            let shown = words(v.clone(), Type::Body, t.text_secondary)
                .key(format!("{}.value", self.key))
                .label(v.text())
                .wrap()
                .w_full()
                .min_h(Size::Regular.height())
                .fill(t.bg_input)
                .border(1.0, t.border_subtle);
            door::corner(
                door::inset(shown, Size::Regular.inset(), Space::S8),
                Radius::Control,
            )
        });
        let mut controls = self.controls;
        if let Some(action) = self.action
            && let Some(last) = controls.pop()
        {
            controls.push(
                door::gap(row().w_full().items_start(), Space::S8)
                    .child(last.grow(1.0).min_w(0.0))
                    .child(action),
            );
        }
        let field = col()
            .key(format!("{}.field", self.key))
            .max_w(FIELD)
            .child(label)
            .children(controls)
            .children(value)
            .children(self.error.map(|e| {
                words(e.clone(), Type::Small, t.error)
                    .key(format!("{}.error", self.key))
                    .role("alert")
                    .label(e.text())
                    .wrap()
            }))
            .children(self.refusal)
            .children(self.hint.map(|h| {
                words(h, Type::Small, t.text_tertiary)
                    .key(format!("{}.hint", self.key))
                    .wrap()
            }));
        door::gap(field, Space::S6)
    }
}
