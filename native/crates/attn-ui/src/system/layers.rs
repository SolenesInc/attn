use rune::{Color, El};

use crate::Tokens;

const HOVER: f32 = 0.08;
const PRESSED: f32 = 0.12;

/// What a pointer lays over a plain surface: the text colour at 8%, so hover reads the same on
/// any surface the text is set on, in dark and in light.
pub fn hover_layer(t: &Tokens) -> Color {
    t.text_primary.alpha(HOVER)
}

/// What lies over a plain surface while it is held down: the text colour at 12%.
pub fn pressed_layer(t: &Tokens) -> Color {
    t.text_primary.alpha(PRESSED)
}

/// Hover and pressed over a surface whose words are `ink`: its ink at 8% and 12%, which shows on
/// whatever fill the words were chosen to read on.
pub(crate) fn pressable(e: El, ink: Color) -> El {
    e.hover(ink.alpha(HOVER)).press(ink.alpha(PRESSED))
}
