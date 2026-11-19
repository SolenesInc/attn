use super::block::{Block, Kind};
use super::measure::READING;
use crate::Tokens;
use crate::system::{Type, words};

/// Reading text, at the reading measure.
pub struct Prose {
    key: String,
    said: String,
    quiet: bool,
}

impl Prose {
    /// What a page or a part of it is for.
    pub fn lead(key: impl Into<String>, said: impl Into<String>) -> Prose {
        Prose {
            key: key.into(),
            said: said.into(),
            quiet: false,
        }
    }

    /// Drawn quieter, as text about something that is off.
    pub fn quiet(mut self, quiet: bool) -> Self {
        self.quiet = quiet;
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let ink = if self.quiet {
            t.text_tertiary
        } else {
            t.text_secondary
        };
        Block::new(
            Kind::Prose,
            words(self.said, Type::Body, ink)
                .key(self.key)
                .wrap()
                .max_w(READING),
        )
    }
}
