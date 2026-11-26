use rune::{Color, El, Tx, el, row};

use super::{Control, IconSize, Radius, Size, Space, disabled, door, focus, kit, layers};
use crate::Tokens;

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
/// then a chevron, beside its first line, that turns while the menu is open. Disabled, it draws its
/// rest look with what it holds at half strength.
pub struct Trigger {
    key: String,
    label: String,
    value: Option<String>,
    content: El,
    look: TriggerLook,
    size: Size,
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
            size: Size::Regular,
            open: false,
            unset: false,
            disabled: false,
        }
    }

    pub fn look(mut self, look: TriggerLook) -> Self {
        self.look = look;
        self
    }

    pub fn size(mut self, size: Size) -> Self {
        self.size = size;
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
        let size = self.size;
        let open = self.open && !self.disabled;
        let unset = self.unset && !open && !self.disabled;
        let (fill, edge) = match (open, self.look) {
            (true, _) => (t.bg_selected, t.border_hover),
            (false, _) if unset => (Color::TRANSPARENT, t.warning.alpha(0.45)),
            (false, TriggerLook::Field) => (t.bg_input, t.border),
            (false, TriggerLook::Quiet) => (Color::TRANSPARENT, Color::TRANSPARENT),
        };
        let glyph = if open {
            kit(t).glyph.chevron_up
        } else {
            kit(t).glyph.chevron_down
        };
        let chevron = el()
            .h(size.height() - 2.0 * edge_to_content(size).pt())
            .self_start()
            .items_center()
            .child(door::icon(glyph, IconSize::Regular).color(t.text_tertiary))
            .a11y_hidden();
        let mut trigger = row()
            .key(self.key)
            .role("button")
            .label(self.label)
            .expanded(open)
            .focusable(true)
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
        if unset {
            trigger = trigger.border_dash(4.0, 3.0);
        }
        let trigger = door::gap(
            door::inset(trigger, size.inset(), edge_to_content(size)),
            Space::S8,
        );
        let trigger = door::corner(trigger, Radius::Control);
        if self.disabled {
            return Control::new(disabled::dim(trigger), size);
        }
        let surface = if fill.a > 0.0 { fill } else { t.bg_panel };
        let trigger = layers::pressable(trigger, surface, t).on_press_tx(press);
        Control::new(focus::ring(trigger, t), size)
    }
}

/// The room above and below what a trigger holds: as much as a line of words leaves it at its
/// height.
fn edge_to_content(size: Size) -> Space {
    match size {
        Size::Regular => Space::S6,
        Size::Compact | Size::Small => Space::S4,
    }
}
