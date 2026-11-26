use rune::{Color, El};

use super::{Control, Radius, Said, Size, Space, Type, door, words};
use crate::Tokens;

/// How a tag reads.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tone {
    Neutral,
    Warn,
    Bad,
    Good,
    /// What the interface suggests rather than a state, such as the recommended answer.
    Accent,
}

const EDGE: f32 = 0.45;
const WASH: f32 = 0.10;

/// A small bordered label in small caps: a state or a kind.
/// How tall a tag stands: its words and the room above and below them.
pub(crate) fn tag_height() -> f32 {
    door::line_height(Type::Tag) + 2.0 * Space::S2.pt()
}

/// What pressing the row it sits in does, such as "Set up ⏎": brand words on the brand mixed
/// into the panel, round, the words taken toward the primary ink only as far as they need to read. Its ground is its own, so the words read the same on a row at rest,
/// hovered or pressed.
pub fn pill(key: impl Into<String>, said: impl Into<Said>, t: &Tokens) -> El {
    tinted_pill(key, said, t.brand, t)
}

/// A state the user should see at a glance, such as "waiting for you": words in `ink` on that ink
/// mixed into the panel, round, the words taken toward the primary ink only as far as they need
/// to read.
pub fn tinted_pill(key: impl Into<String>, said: impl Into<Said>, ink: Color, t: &Tokens) -> El {
    let ground = crate::colors::mix_srgb(t.bg_panel, ink, PILL_WASH);
    let ink =
        crate::colors::moved_until_it_reads(ink, t.text_primary, ground, crate::colors::READS);
    let pill = words(said, Type::Control, ink)
        .key(key.into())
        .single_line()
        .fill(ground);
    door::corner(door::inset(pill, Space::S6, Space::S0), Radius::Round)
}

pub(crate) const PILL_WASH: f32 = 0.14;

/// How wide [`tag`] draws `said`: its capitals, its inset on each side and its edge.
pub fn tag_width(said: &'static str) -> f32 {
    super::width_of(said, Type::Tag) + 2.0 * (Space::S6.pt() + 1.0)
}

pub fn tag(key: impl Into<String>, said: impl Into<Said>, tone: Tone, t: &Tokens) -> Control {
    let (ink, fill) = inks(tone, t);
    let tag = words(said, Type::Tag, ink)
        .key(key.into())
        .fill(fill)
        .border(1.0, edge(ink));
    let tag = door::corner(door::inset(tag, Space::S6, Space::S2), Radius::Tag);
    Control::new(tag, Size::Compact)
}

/// A quiet borderless label in small caps on a soft tonal wash, such as a sidebar tile's kind
/// (`DOC`, `SEED`).
pub fn quiet_tag(key: impl Into<String>, said: impl Into<Said>, ground: Color, t: &Tokens) -> El {
    let fill = crate::colors::mix_srgb(ground, t.text_primary, 0.06);
    let ink = crate::colors::moved_until_it_reads(
        t.text_muted,
        t.text_primary,
        fill,
        crate::colors::READS,
    );
    let tag = words(said, Type::Tag, ink).key(key.into()).fill(fill);
    door::corner(door::inset(tag, Space::S6, Space::S2), Radius::Tag)
}

/// The ink and the wash of a thing that reads in `tone`.
pub(crate) fn inks(tone: Tone, t: &Tokens) -> (Color, Color) {
    match tone {
        Tone::Neutral => (t.text_secondary, Color::TRANSPARENT),
        Tone::Warn => (t.warning, t.warning.alpha(WASH)),
        Tone::Bad => (t.error, t.error.alpha(WASH)),
        Tone::Good => (t.success, t.success.alpha(WASH)),
        Tone::Accent => (t.accent, t.accent.alpha(WASH)),
    }
}

/// The edge of a thing that reads in `ink`.
pub(crate) fn edge(ink: Color) -> Color {
    ink.alpha(EDGE)
}
