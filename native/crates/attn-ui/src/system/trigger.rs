use rune::{Color, El, Tx, el, row};

use super::{Control, Radius, Size, Space, door, focus, kit, layers};
use crate::Tokens;

const CHEVRON: f32 = 12.0;
const EDGE_TO_CONTENT: Space = Space::S6;

/// How a trigger sits on its page.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum TriggerLook {
    /// A box like a field's, for a form.
    #[default]
    Field,
    /// No fill or edge until the pointer is over it, for a value inside a row.
    Quiet,
}

/// The control that opens a menu or a picker and shows what is chosen in it: what it holds,
/// then a chevron, beside its first line, that turns while the menu is open. The caller inks the content, in
/// `text_dimmed` while the trigger is disabled.
pub struct Trigger {
    key: String,
    label: String,
    value: Option<String>,
    content: El,
    look: TriggerLook,
    open: bool,
    unset: bool,
    disabled: bool,
}

impl Trigger {
    /// `label` names the trigger for a screen reader; `content` is what it shows.
    pub fn new(key: impl Into<String>, label: impl Into<String>, content: El) -> Trigger {
        Trigger {
            key: key.into(),
            label: label.into(),
            value: None,
            content,
            look: TriggerLook::Field,
            open: false,
            unset: false,
            disabled: false,
        }
    }

    pub fn look(mut self, look: TriggerLook) -> Self {
        self.look = look;
        self
    }

    /// What a screen reader reads as the chosen value.
    pub fn value(mut self, value: impl Into<String>) -> Self {
        self.value = Some(value.into());
        self
    }

    pub fn open(mut self, open: bool) -> Self {
        self.open = open;
        self
    }

    /// Nothing is chosen yet and something must be: a dashed warm edge asks for a choice.
    pub fn unset(mut self, unset: bool) -> Self {
        self.unset = unset;
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
        let size = Size::Regular;
        let (fill, edge) = match (self.disabled, self.open, self.look) {
            (true, _, _) => (Color::TRANSPARENT, t.border),
            (false, true, _) => (t.bg_selected, t.border_hover),
            (false, false, _) if self.unset => (Color::TRANSPARENT, t.warning.alpha(0.45)),
            (false, false, TriggerLook::Field) => (t.bg_input, t.border),
            (false, false, TriggerLook::Quiet) => (Color::TRANSPARENT, Color::TRANSPARENT),
        };
        let glyph = if self.open {
            kit(t).glyph.chevron_up
        } else {
            kit(t).glyph.chevron_down
        };
        let ink = if self.disabled {
            t.text_dimmed
        } else {
            t.text_tertiary
        };
        let chevron = el()
            .h(size.height() - 2.0 * EDGE_TO_CONTENT.pt())
            .self_start()
            .items_center()
            .child(rune::icon(glyph, CHEVRON).color(ink))
            .a11y_hidden();
        let mut trigger = row()
            .key(self.key)
            .role("button")
            .label(self.label)
            .expanded(self.open)
            .focusable(true)
            .disabled(self.disabled)
            .min_w(0.0)
            .min_h(size.height())
            .items_start()
            .fill(fill)
            .border(1.0, edge)
            .child(self.content.grow(1.0).min_w(0.0).self_center())
            .child(chevron);
        if let Some(value) = self.value {
            trigger = trigger.a11y_value(value);
        }
        if self.unset && !self.open && !self.disabled {
            trigger = trigger.border_dash(4.0, 3.0);
        }
        let trigger = door::gap(
            door::inset(trigger, size.inset(), EDGE_TO_CONTENT),
            Space::S8,
        );
        let trigger = door::corner(trigger, Radius::Control);
        if self.disabled {
            return Control::new(trigger, size);
        }
        let surface = if fill.a > 0.0 { fill } else { t.bg_panel };
        let trigger = layers::pressable(trigger, surface, t).on_press_tx(press);
        Control::new(focus::ring(trigger, t), size)
    }
}
