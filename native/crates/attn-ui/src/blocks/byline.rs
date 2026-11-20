use rune::{Color, El, col, row};

use crate::Tokens;
use crate::system::{Said, Space, Type, door, words};

/// What a row or a page says about a thing in passing, one fact after another with a dot
/// between: its id, who tends it, how far along it is.
#[derive(Default)]
pub struct Byline {
    key: Option<String>,
    items: Vec<El>,
    wrap: bool,
}

impl Byline {
    pub fn new() -> Byline {
        Byline::default()
    }

    pub fn key(mut self, key: impl Into<String>) -> Self {
        self.key = Some(key.into());
        self
    }

    /// An id or a path, read as data.
    pub fn id(self, said: impl Into<String>, ink: Color) -> Self {
        self.push(words(said.into(), Type::Mono, ink))
    }

    /// A fact in words.
    pub fn said(self, said: impl Into<Said>, t: &Tokens) -> Self {
        self.push(
            words(said, Type::Small, t.text_tertiary)
                .truncate()
                .shrink()
                .min_w(0.0),
        )
    }

    /// A fact that is more than words, such as a state with its dot or a link.
    pub fn push(mut self, fact: impl Into<El>) -> Self {
        self.items.push(fact.into());
        self
    }

    pub fn push_all(mut self, facts: impl IntoIterator<Item = El>) -> Self {
        self.items.extend(facts);
        self
    }

    /// The facts go onto further lines when one runs out, instead of the last ones shortening.
    pub fn wrap(mut self) -> Self {
        self.wrap = true;
        self
    }

    pub fn is_empty(&self) -> bool {
        self.items.is_empty()
    }

    pub fn build(self, t: &Tokens) -> El {
        if self.wrap {
            return self.wrapped(t);
        }
        let mut line = door::gap(row().min_w(0.0).items_center(), Space::S6);
        if let Some(key) = self.key {
            line = line.key(key);
        }
        for (i, item) in self.items.into_iter().enumerate() {
            if i > 0 {
                line = line.child(separator(t));
            }
            line = line.child(item);
        }
        line
    }

    /// Each fact after its separator, the line pulled one separator before its start and
    /// clipped there, so the separator before a line's first fact is never drawn and none ends
    /// a line.
    fn wrapped(self, t: &Tokens) -> El {
        let facts = self.items.into_iter().map(|item| {
            row()
                .items_center()
                .min_w(0.0)
                .max_w_frac(1.0)
                .child(row().w(SEPARATOR).justify_center().child(separator(t)))
                .child(item)
        });
        let line =
            door::gaps(row().flex_wrap().items_center(), Space::S4, Space::S0).children(facts);
        let mut held =
            door::clip(col().min_w(0.0).max_w_frac(1.0)).child(door::behind_start(line, SEPARATOR));
        if let Some(key) = self.key {
            held = held.key(key);
        }
        held
    }
}

fn separator(t: &Tokens) -> El {
    words("·", Type::Small, t.text_dimmed).a11y_hidden()
}

/// A separator and the room either side of it: the dot in `Small` measures 3.5, with 6 on
/// each side as between facts on one line.
const SEPARATOR: f32 = 15.5;
