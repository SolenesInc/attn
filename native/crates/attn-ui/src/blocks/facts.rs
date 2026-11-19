use rune::{Color, El, row};

use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// What a row or a page says about a thing in passing, one fact after another with a dot
/// between: its id, who tends it, how far along it is.
#[derive(Default)]
pub struct Facts {
    key: Option<String>,
    items: Vec<El>,
    wrap: bool,
}

impl Facts {
    pub fn new() -> Facts {
        Facts::default()
    }

    pub fn key(mut self, key: impl Into<String>) -> Self {
        self.key = Some(key.into());
        self
    }

    /// An id or a path, read as data.
    pub fn id(self, said: impl Into<String>, ink: Color) -> Self {
        self.push(words(said, Type::Mono, ink))
    }

    /// A fact in words.
    pub fn said(self, said: impl Into<String>, t: &Tokens) -> Self {
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
        let mut line = row().min_w(0.0).items_center();
        if let Some(key) = self.key {
            line = line.key(key);
        }
        for (i, item) in self.items.into_iter().enumerate() {
            if i > 0 {
                line = line.child(words("·", Type::Small, t.text_dimmed).a11y_hidden());
            }
            line = line.child(item);
        }
        if self.wrap {
            door::gaps(line.flex_wrap().max_w_frac(1.0), Space::S4, Space::S6)
        } else {
            door::gap(line, Space::S6)
        }
    }
}
