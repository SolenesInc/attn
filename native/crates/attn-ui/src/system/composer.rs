use rune::{El, TextEdit};

use super::{Type, door};
use crate::Tokens;

/// The field a person writes the main thing of a surface in, such as a quick message: bare on
/// its surface, its words set as `Compose`, `placeholder` until it holds any.
pub fn composer(edit: &TextEdit, placeholder: impl Into<String>, t: &Tokens) -> El {
    door::text_input(edit, Type::Compose)
        .w_full()
        .color(t.text_primary)
        .placeholder(placeholder.into(), t.text_muted)
        .caret(t.text_primary, crate::kit(t).selection())
}

/// A free text input, its words set as `kind`, for a field no field box frames.
pub fn text_input(edit: &TextEdit, kind: Type) -> El {
    door::text_input(edit, kind)
}
