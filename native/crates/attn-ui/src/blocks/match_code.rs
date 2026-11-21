use rune::{col, text};

use super::block::{Block, Kind};
use crate::Tokens;
use crate::system::{Said, Space, Type, door, words};

/// A short code the person matches by eye against the one another program shows, such as a
/// pairing's, over what to check.
pub struct MatchCode {
    key: String,
    code: String,
    hint: Said,
}

impl MatchCode {
    pub fn new(key: impl Into<String>, code: impl Into<String>, hint: impl Into<Said>) -> Self {
        MatchCode {
            key: key.into(),
            code: code.into(),
            hint: hint.into(),
        }
    }

    pub fn build(self, t: &Tokens) -> Block {
        let code = door::styled(text(self.code.clone()).label(self.code), door::MATCH_CODE)
            .key(format!("{}.code", self.key))
            .color(t.text_primary);
        let hint = words(self.hint, Type::Small, t.text_tertiary)
            .key(format!("{}.hint", self.key))
            .wrap();
        Block::new(
            Kind::Prose,
            door::gap(col().key(self.key), Space::S6)
                .child(code)
                .child(hint),
        )
    }
}
