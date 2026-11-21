use rune::{Color, El};

/// How strongly an unavailable control draws: the strength rune-kit draws an unavailable field
/// at, so a field and every other control dim alike.
pub(crate) const STRENGTH: f32 = 0.5;

/// An unavailable control: every part it draws, fill, edge and ink, at one strength, so the
/// levels between its parts stay as they were. Nothing in it takes focus, a press or the
/// pointer, whoever attached them.
pub(crate) fn dim(e: El) -> El {
    unavailable(e).layer_opacity(STRENGTH)
}

/// [`dim`] for a control whose words stay selectable: it takes no focus or press, and the
/// pointer still selects its text.
pub(crate) fn readable(e: El) -> El {
    e.disabled(true).layer_opacity(STRENGTH)
}

/// An unavailable control that draws its own strength, such as the surface's Primary action,
/// whose fill stands at [`STRENGTH`] and whose words keep reading on it.
pub(crate) fn unavailable(e: El) -> El {
    e.disabled(true).inert()
}

/// An unavailable control that draws its own strength and keeps its place on Tab, so the
/// keyboard reaches it and reads why; it takes no press and shows no hover.
pub(crate) fn reachable(e: El) -> El {
    e.disabled(true)
        .focusable(true)
        .hover(Color::TRANSPARENT)
        .press(Color::TRANSPARENT)
}
