use rune::El;
use rune_kit::{TextArea, TextInput};

use super::{Control, Size, Space, Type, disabled, door, focus, layers};
use crate::Tokens;

/// The width every number field takes. It holds eight digits beside the arrows: a context cap of 1,000,000 measures 54.5pt in body
/// type, and the arrows and insets leave 66pt.
const NUMBER_WIDTH: f32 = 112.0;

/// What the kit's single-line box, which sets its words 8 in from its leading edge, needs to
/// bring them to a regular control's inset.
const KIT_LINE_TO_REGULAR: Space = Space::S4;

/// The kit's red edge is one point wide.
const REFUSED_EDGE: f32 = 1.0;

/// What a field box holds besides its value.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum FieldState {
    Ready,
    /// The value was refused: a red edge, kept while the field has focus.
    Failed,
    Disabled,
}

impl FieldState {
    pub fn failed_if(refused: bool) -> Self {
        if refused { Self::Failed } else { Self::Ready }
    }

    pub fn disabled_if(disabled: bool) -> Self {
        if disabled {
            Self::Disabled
        } else {
            Self::Ready
        }
    }
}

/// A one-line text field filling its column.
pub fn line_box(input: TextInput, state: FieldState, t: &Tokens) -> Control {
    single_line(input.width(None), Type::Body, state, t)
}

/// A one-line text field filling its column whose value is code, such as a command or a cron
/// expression: the same box as [`line_box`], its value set in `Mono`.
pub fn code_box(input: TextInput, state: FieldState, t: &Tokens) -> Control {
    single_line(input.width(None), Type::Mono, state, t)
}

/// How wide a [`line_box`] is that shows `words` whole, as its value or its placeholder.
pub fn line_box_width(words: &'static str) -> f32 {
    super::width_of(words, Type::Body) + 2.0 * Size::Regular.inset().pt()
}

/// A field that holds a number, at the one width every number field takes.
pub fn number_box(input: TextInput, state: FieldState, t: &Tokens) -> Control {
    single_line(input.width(Some(NUMBER_WIDTH)), Type::Body, state, t)
}

/// A text field of several lines, filling its column.
pub fn area_box(area: TextArea, state: FieldState, t: &Tokens) -> Control {
    let size = Size::Regular;
    let area = door::area(area, Type::Body, size.inset(), Space::S8)
        .placeholder_ink(t.text_dimmed)
        .disabled_ink(t.text_primary)
        .width(None)
        .disabled(state == FieldState::Disabled)
        .invalid(state == FieldState::Failed)
        .build(super::kit(t));
    Control::new(boxed(area, state, t), size)
}

fn single_line(input: TextInput, value: Type, state: FieldState, t: &Tokens) -> Control {
    let size = Size::Regular;
    let field = door::line(input, value)
        .height(size.height())
        .fade_overflow()
        .disabled_ink(t.text_primary)
        .disabled(state == FieldState::Disabled)
        .invalid(state == FieldState::Failed)
        .build(super::kit(t));
    let field = door::sides(field, KIT_LINE_TO_REGULAR, size.inset());
    Control::new(boxed(field, state, t), size)
}

/// An unavailable field is its rest look dimmed as every unavailable control is, with no hover
/// and no ring; the kit, given the value's own ink for it, leaves the dimming to that one rule.
/// A refused field's red edge is its border, inside its box: the kit's own red edge is an
/// outline, which the focus ring takes the place of.
fn boxed(field: El, state: FieldState, t: &Tokens) -> El {
    let field = match state {
        FieldState::Failed => field.border(REFUSED_EDGE, super::kit(t).c.danger.fg),
        FieldState::Ready | FieldState::Disabled => field,
    };
    let field = field.fill(t.bg_input);
    if state == FieldState::Disabled {
        return disabled::dim(field);
    }
    focus::ring_within(field.hover(layers::hover_layer(t.bg_input, t)), t)
}
