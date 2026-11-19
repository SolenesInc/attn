use rune::El;

use crate::Tokens;

const WIDTH: f32 = 2.0;
const OFFSET: f32 = 2.0;
/// How far a focus ring reaches outside its control.
pub const RING_REACH: f32 = WIDTH + OFFSET;

/// The ring a focusable control draws while the keyboard is on it, outside its edge.
pub(crate) fn ring(e: El, t: &Tokens) -> El {
    e.focus_ring(WIDTH, OFFSET, t.warning)
}

/// The ring a field draws while focus is anywhere inside it, from the pointer or the keyboard:
/// the same drawing as [`ring`], since the field's own editor is what takes focus.
pub(crate) fn ring_within(e: El, t: &Tokens) -> El {
    e.focus_within_ring(WIDTH, OFFSET, t.warning)
        .focus_within_flat()
}

/// The ring of a bordered control in a tight list, drawn over its own edge, so it never
/// reaches a neighbour.
pub(crate) fn ring_on_edge(e: El, t: &Tokens) -> El {
    e.focus_ring(WIDTH, -1.0, t.warning)
}
