//! A control calling for attention once: its pill pops in scale while an accent glow blooms
//! behind it and fades, then everything rests where it began.

use rune::{Beat, Easing, El, Sequence, el};

use super::{Radius, door};
use crate::Tokens;

/// How the pill moves and the glow blooms.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PopBloom {
    /// Something arrives where nothing waited: the pill rises from small into place.
    ArrivalFromZero,
    /// Something more arrives: the pill kicks out and settles.
    Arrival,
    /// What waits is still waiting: the pill swells gently while the glow breathes.
    Reminder,
}

/// How far the glow blurs, in points.
const GLOW_BLUR: f32 = 6.0;

const POP_FROM_ZERO: &[Beat] = &[
    Beat::new(0.0).scale(0.6),
    Beat::new(0.2).scale(1.14).easing(Easing::EASE_OUT),
    Beat::new(0.36).scale(0.96),
    Beat::new(0.52),
];
const POP: &[Beat] = &[
    Beat::new(0.0),
    Beat::new(0.16).scale(1.16).easing(Easing::EASE_OUT),
    Beat::new(0.3).scale(0.95),
    Beat::new(0.42).scale(1.02),
    Beat::new(0.52),
];
const SWELL: &[Beat] = &[Beat::new(0.0), Beat::new(0.7).scale(1.06), Beat::new(1.4)];
const BLOOM: &[Beat] = &[
    Beat::new(0.0).opacity(0.0).scale(0.9),
    Beat::new(0.22)
        .opacity(1.0)
        .scale(1.25)
        .easing(Easing::EASE_OUT),
    Beat::new(0.9).opacity(0.0).scale(1.5),
];
const BREATH: &[Beat] = &[
    Beat::new(0.0).opacity(0.0),
    Beat::new(0.7).opacity(1.0).scale(1.25),
    Beat::new(1.4).opacity(0.0).scale(1.35),
];

impl PopBloom {
    /// How long the whole call lasts, until the glow has faded.
    pub fn duration(self) -> f64 {
        self.glow().duration()
    }

    fn pop(self) -> Sequence {
        Sequence::new(match self {
            PopBloom::ArrivalFromZero => POP_FROM_ZERO,
            PopBloom::Arrival => POP,
            PopBloom::Reminder => SWELL,
        })
    }

    fn glow(self) -> Sequence {
        Sequence::new(match self {
            PopBloom::ArrivalFromZero | PopBloom::Arrival => BLOOM,
            PopBloom::Reminder => BREATH,
        })
    }

    /// The glow's strongest alpha: lower on a light ground, where a dark accent glowing at full
    /// strength reads as a smudge.
    fn peak(self, t: &Tokens) -> f32 {
        match (self, t.dark) {
            (PopBloom::Reminder, true) => 0.42,
            (PopBloom::Reminder, false) => 0.25,
            (_, true) => 0.6,
            (_, false) => 0.35,
        }
    }
}

/// `content`, a pill, calling for attention while `pulse` plays: the `kind` it plays, and how
/// many have played, so each new one blooms from its start. `content` keeps its place in the
/// tree whether a pulse plays or not, and with it the keyboard; neither the pop nor the glow
/// moves anything around it.
pub fn pop_bloom(key: &str, pulse: Option<(PopBloom, u64)>, content: El, t: &Tokens) -> El {
    let pill = el().key("pill").child(content);
    let Some((kind, played)) = pulse else {
        return el().key(key).child(pill);
    };
    let glow = door::corner(el(), Radius::Round)
        .key(format!("glow.{played}"))
        .pin(Some(0.0), Some(0.0), Some(0.0), Some(0.0))
        .fill(t.accent.alpha(kind.peak(t)))
        .blur(GLOW_BLUR)
        .inert()
        .sequence(kind.glow());
    el().key(key).child(glow).child(pill.sequence(kind.pop()))
}
