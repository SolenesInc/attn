use rune::El;

/// How strongly an unavailable control draws: the strength rune-kit draws an unavailable field
/// at, so a field and every other control dim alike.
pub(crate) const STRENGTH: f32 = 0.5;

/// An unavailable control: every part it draws, fill, edge and ink, at one strength, so the
/// levels between its parts stay as they were. Nothing in it takes focus, a press or the
/// pointer, whoever attached them.
pub(crate) fn dim(e: El) -> El {
    unavailable(e).layer_opacity(STRENGTH)
}

/// An unavailable control that draws its own strength, such as the surface's Primary action,
/// whose fill stands at [`STRENGTH`] and whose words keep reading on it.
pub(crate) fn unavailable(e: El) -> El {
    e.disabled(true).inert()
}

/// Drawn as [`dim`] draws, while it keeps its place on Tab and its accessible state.
pub(crate) fn faded(e: El) -> El {
    e.layer_opacity(STRENGTH)
}
