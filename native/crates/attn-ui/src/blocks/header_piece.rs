use rune::{Color, El, row};

use crate::system::{
    IconSize, Link, Radius, Said, Space, Type, door, focus, glyph, layers, width_of, words,
};
use crate::{Glyph, Tokens};

/// One piece of a tile header's second line, such as the seed an agent tends, its role in a
/// delegation or where it came from: marks and words in `Small`, one after another on that
/// line and as tall as it. A piece that does something is a quiet button, which takes hover,
/// pressed and the ring around its marks and words. Its width is what it draws, so the header
/// fits its pieces by what they measure.
pub struct HeaderPiece {
    parts: Vec<Part>,
}

enum Part {
    Mark(Glyph, Color),
    Words {
        said: Said,
        ink: Color,
        key: Option<String>,
        within: Option<f32>,
    },
    Count {
        mark: Glyph,
        ink: Color,
        count: String,
        label: String,
    },
    State(Said, Color),
    Link {
        key: String,
        label: String,
        said: String,
        ink: Color,
        then: Then,
    },
    Drawn(El, f32),
}

type Then = Box<dyn FnOnce(El) -> El>;

/// How strongly a state's words tint the ground under them.
const STATE_WASH: f32 = 0.12;

impl Default for HeaderPiece {
    fn default() -> Self {
        HeaderPiece::new()
    }
}

impl HeaderPiece {
    pub fn new() -> HeaderPiece {
        HeaderPiece { parts: Vec::new() }
    }

    /// A mark, at the size that stands beside `Small` words.
    pub fn mark(mut self, mark: impl Into<Glyph>, ink: Color) -> Self {
        self.parts.push(Part::Mark(mark.into(), ink));
        self
    }

    pub fn words(self, said: impl Into<Said>, ink: Color) -> Self {
        self.words_part(said.into(), ink, None, None)
    }

    /// Words keyed `key`, cut short with an ellipsis past `within` points when it is given.
    pub fn words_keyed(
        self,
        key: impl Into<String>,
        said: impl Into<Said>,
        ink: Color,
        within: Option<f32>,
    ) -> Self {
        self.words_part(said.into(), ink, Some(key.into()), within)
    }

    fn words_part(
        mut self,
        said: Said,
        ink: Color,
        key: Option<String>,
        within: Option<f32>,
    ) -> Self {
        self.parts.push(Part::Words {
            said,
            ink,
            key,
            within,
        });
        self
    }

    /// How many of something there are: its mark close beside the number, read as `label`.
    pub fn count(
        mut self,
        mark: impl Into<Glyph>,
        ink: Color,
        count: impl Into<String>,
        label: impl Into<String>,
    ) -> Self {
        self.parts.push(Part::Count {
            mark: mark.into(),
            ink,
            count: count.into(),
            label: label.into(),
        });
        self
    }

    /// A state in words on a wash of its ink, such as a seed's.
    pub fn state(mut self, said: impl Into<Said>, ink: Color) -> Self {
        self.parts.push(Part::State(said.into(), ink));
        self
    }

    /// Words that act on their own, keyed `key` and read as `label`; `then` gives the link its
    /// press, and whatever else its feature hangs on it.
    pub fn link(
        mut self,
        key: impl Into<String>,
        label: impl Into<String>,
        said: impl Into<String>,
        ink: Color,
        then: impl FnOnce(El) -> El + 'static,
    ) -> Self {
        self.parts.push(Part::Link {
            key: key.into(),
            label: label.into(),
            said: said.into(),
            ink,
            then: Box::new(then),
        });
        self
    }

    /// A part its feature draws, such as a role's own mark, `width` points wide.
    pub fn drawn(mut self, part: El, width: f32) -> Self {
        self.parts.push(Part::Drawn(part, width));
        self
    }

    /// How wide the piece stands as a line of words.
    pub fn width(&self) -> f32 {
        let parts: f32 = self.parts.iter().map(Part::width).sum();
        let gaps = self.parts.len().saturating_sub(1) as f32 * GAP.pt();
        parts + gaps
    }

    /// How wide the piece stands as a button: its words and its inset either side.
    pub fn button_width(&self) -> f32 {
        self.width() + 2.0 * INSET.pt()
    }

    /// The piece as words on the line, its parts keeping their own presses.
    pub fn build(self, t: &Tokens) -> El {
        door::gap(row().items_center().min_w(0.0), GAP)
            .h(line())
            .children(self.parts.into_iter().map(|p| p.build(t)))
    }

    /// The piece as one quiet button keyed `key` and read as `label`. Whoever builds it gives
    /// it its press.
    pub fn button(self, key: impl Into<String>, label: impl Into<String>, t: &Tokens) -> El {
        let piece = door::inset(self.build(t), INSET, Space::S0)
            .key(key.into())
            .role("button")
            .label(label.into())
            .focusable(true);
        let piece = door::corner(piece, Radius::Tag);
        focus::ring(layers::pressable(piece, t.bg_panel, t), t)
    }
}

const GAP: Space = Space::S4;
/// The room a link keeps either side of its words for the hover and the ring it reaches out
/// with, so neither covers the words beside it.
const LINK_ROOM: Space = Space::S4;
const INSET: Space = Space::S4;

fn line() -> f32 {
    door::line_height(Type::Small)
}

impl Part {
    fn width(&self) -> f32 {
        match self {
            Part::Mark(..) => IconSize::Small.pt(),
            Part::Words { said, within, .. } => {
                let w = width_of(said.clone(), Type::Small);
                within.map_or(w, |max| w.min(max))
            }
            Part::Count { count, .. } => {
                IconSize::Small.pt() + Space::S2.pt() + width_of(count.clone(), Type::Small)
            }
            Part::State(said, _) => width_of(said.clone(), Type::Small) + 2.0 * Space::S4.pt(),
            Part::Link { said, .. } => width_of(said.clone(), Type::Small) + 2.0 * LINK_ROOM.pt(),
            Part::Drawn(_, width) => *width,
        }
    }

    fn build(self, t: &Tokens) -> El {
        match self {
            Part::Mark(mark, ink) => glyph(mark, IconSize::Small, ink),
            Part::Words {
                said,
                ink,
                key,
                within,
            } => {
                let natural = width_of(said.clone(), Type::Small);
                let words = words(said, Type::Small, ink).single_line().truncate();
                let words = match key {
                    Some(key) => words.key(key),
                    None => words,
                };
                match within {
                    Some(max) if natural > max => words.w(max),
                    _ => words.shrink().min_w(0.0),
                }
            }
            Part::Count {
                mark,
                ink,
                count,
                label,
            } => door::gap(row().items_center(), Space::S2)
                .label(label)
                .child(glyph(mark, IconSize::Small, ink))
                .child(words(count, Type::Small, t.text_tertiary).a11y_hidden()),
            Part::State(said, ink) => {
                let state = words(said, Type::Small, ink)
                    .single_line()
                    .fill(ink.alpha(STATE_WASH));
                door::corner(door::inset(state, Space::S4, Space::S0), Radius::Tag)
            }
            Part::Link {
                key,
                label,
                said,
                ink,
                then,
            } => door::inset(
                row().child(then(
                    Link::new(key, said).ink(ink).build(t).label(label).into(),
                )),
                LINK_ROOM,
                Space::S0,
            ),
            Part::Drawn(part, _) => part,
        }
    }
}
