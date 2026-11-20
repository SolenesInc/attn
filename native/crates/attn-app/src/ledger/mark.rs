//! The mark a ledger row leads with, which says what state the thing it stands for is in.

use attn_ui::Tokens;
use attn_ui::system::{DotShape, DotSize, dot};
use rune::{Color, Cycle, El};

/// What a row's mark says.
#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) enum Glyph {
    Live(Color),
    Working(Color),
    Closed,
    Refreshing,
    Pinned,
    Scheduled,
    Dirty,
    Clean,
    Removed,
    Error,
}

/// Full, then dim at the half, over 1.4 s.
const PULSE: Cycle = Cycle {
    period: 1.4,
    phase: 0.0,
    stops: &[(0.0, 1.0), (0.5, 0.55), (1.0, 1.0)],
    still: 1.0,
    fps: 30.0,
};

pub(crate) fn mark(g: Glyph, size: DotSize, t: &Tokens) -> El {
    let (shape, ink) = match g {
        Glyph::Live(ink) | Glyph::Working(ink) => (DotShape::Filled, ink),
        Glyph::Dirty => (DotShape::Filled, t.warning),
        Glyph::Closed | Glyph::Clean => (DotShape::Hollow, t.text_muted),
        Glyph::Refreshing => (DotShape::Hollow, t.text_tertiary),
        Glyph::Pinned => (DotShape::Square, t.success),
        Glyph::Scheduled => (DotShape::Hollow, t.warning),
        Glyph::Removed => (DotShape::Filled, t.text_muted),
        Glyph::Error => (DotShape::Filled, t.error),
    };
    let shown = dot(ink, shape, size);
    match g {
        Glyph::Working(_) | Glyph::Refreshing => shown.cycle(PULSE),
        Glyph::Removed => shown.opacity(0.5),
        _ => shown,
    }
}
