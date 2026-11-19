use rune::El;

/// How strongly an unavailable control draws: the strength rune-kit draws an unavailable field
/// at, so a field and every other control dim alike.
pub(crate) const STRENGTH: f32 = 0.5;

/// An unavailable control: every part it draws, fill, edge and ink, at one strength, so the
/// levels between its parts stay as they were. It takes no focus; the caller leaves it no press,
/// no layers and no ring.
pub(crate) fn dim(e: El) -> El {
    e.disabled(true).layer_opacity(STRENGTH)
}
