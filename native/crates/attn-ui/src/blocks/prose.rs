use super::block::{Block, Kind};
use super::measure::READING;
use crate::Tokens;
use crate::system::{Said, Type, words};

/// Reading text, at the reading measure.
pub struct Prose {
    key: String,
    said: Said,
    quiet: bool,
    kind: Type,
    note: bool,
    code: Option<String>,
}

impl Prose {
    /// What a page or a part of it is for.
    pub fn lead(key: impl Into<String>, said: impl Into<Said>) -> Prose {
        Prose {
            key: key.into(),
            said: said.into(),
            quiet: false,
            kind: Type::Body,
            note: false,
            code: None,
        }
    }

    /// That a list or a part of a page has nothing in it yet, read as a note.
    pub fn empty(key: impl Into<String>, said: impl Into<Said>) -> Prose {
        Prose {
            note: true,
            ..Prose::lead(key, said).quiet(true)
        }
    }

    /// Small print about the blocks above it, such as how to add to a list.
    pub fn hint(key: impl Into<String>, said: impl Into<Said>) -> Prose {
        Prose {
            kind: Type::Small,
            ..Prose::lead(key, said).quiet(true)
        }
    }

    /// Drawn quieter, as text about something that is off.
    pub fn quiet(mut self, quiet: bool) -> Self {
        self.quiet = quiet;
        self
    }

    /// A value after the words, such as a path, set in Mono and read exactly as given.
    pub fn code(mut self, value: impl Into<String>) -> Self {
        self.code = Some(value.into());
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Prose, self.el(t))
    }

    /// The words on their own, for a place that is not a page.
    pub fn el(self, t: &Tokens) -> rune::El {
        let ink = if self.quiet {
            t.text_tertiary
        } else {
            t.text_secondary
        };
        let said = match self.code {
            Some(code) => words(self.said.then_code(code), self.kind, ink),
            None => words(self.said, self.kind, ink),
        }
        .key(self.key)
        .wrap()
        .max_w(READING);
        if self.note { said.role("note") } else { said }
    }
}
