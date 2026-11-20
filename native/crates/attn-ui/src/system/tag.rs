use rune::{Color, El};

use super::{Control, Radius, Size, Space, Type, door, words};
use crate::Tokens;

/// How a tag reads.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Tone {
    Neutral,
    Warn,
    Bad,
    Good,
}

const EDGE: f32 = 0.45;
const WASH: f32 = 0.10;

/// A small bordered label in small caps: a state or a kind.
/// How tall a tag stands: its words and the room above and below them.
pub(crate) fn tag_height() -> f32 {
    door::line_height(Type::Tag) + 2.0 * Space::S2.pt()
}

/// What pressing the row it sits in does, such as "Set up ⏎": primary words on the brand mixed
/// into the panel, round. Its ground is its own, so the words read the same on a row at rest,
/// hovered or pressed.
pub fn pill(key: impl Into<String>, said: &str, t: &Tokens) -> El {
    let pill = words(said, Type::Control, t.text_primary)
        .key(key.into())
        .single_line()
        .fill(crate::colors::mix_srgb(t.bg_panel, t.brand, PILL_WASH));
    door::corner(door::inset(pill, Space::S6, Space::S0), Radius::Round)
}

const PILL_WASH: f32 = 0.14;

pub fn tag(key: impl Into<String>, said: &str, tone: Tone, t: &Tokens) -> Control {
    let (ink, edge, fill) = match tone {
        Tone::Neutral => (t.text_secondary, t.border_hover, Color::TRANSPARENT),
        Tone::Warn => (t.warning, t.warning.alpha(EDGE), t.warning.alpha(WASH)),
        Tone::Bad => (t.error, t.error.alpha(EDGE), t.error.alpha(WASH)),
        Tone::Good => (t.success, t.success.alpha(EDGE), t.success.alpha(WASH)),
    };
    let tag = words(said, Type::Tag, ink)
        .key(key.into())
        .fill(fill)
        .border(1.0, edge);
    let tag = door::corner(door::inset(tag, Space::S6, Space::S2), Radius::Tag);
    Control::new(tag, Size::Compact)
}
