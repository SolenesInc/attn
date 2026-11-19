use super::block::{Block, Kind};
use super::measure::READING;
use crate::Tokens;
use crate::system::{Type, words};

/// What a place says when it has nothing to show, or how much it holds back: a quiet sentence
/// at the reading measure. A command between backticks is set as one.
pub struct Note {
    key: String,
    said: String,
}

impl Note {
    pub fn new(key: impl Into<String>, said: impl Into<String>) -> Note {
        Note {
            key: key.into(),
            said: said.into(),
        }
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Prose, self.el(t))
    }

    /// The note on its own, for a place that is not a page.
    pub fn el(self, t: &Tokens) -> rune::El {
        words(self.said, Type::Body, t.text_tertiary)
            .key(self.key)
            .role("note")
            .wrap()
            .max_w(READING)
    }
}
