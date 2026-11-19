use rune::{El, col, row};

use super::block::{Block, Kind};
use super::measure::{FACT_LABEL, FACT_VALUE_FLOOR};
use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// What something is, read and not changed here: a column of names, each with its value beside
/// it. Where a value would get narrower than it reads, it moves under its name.
pub struct Facts {
    key: Option<String>,
    facts: Vec<Fact>,
}

struct Fact {
    key: String,
    label: String,
    value: String,
    kind: Type,
}

impl Facts {
    pub fn new() -> Facts {
        Facts {
            key: None,
            facts: Vec::new(),
        }
    }

    pub fn key(mut self, key: impl Into<String>) -> Self {
        self.key = Some(key.into());
        self
    }

    /// A fact in words, keyed `key` and announced as its label and its value.
    pub fn fact(
        self,
        key: impl Into<String>,
        label: impl Into<String>,
        value: impl Into<String>,
    ) -> Self {
        self.push(key, label, value, Type::Body)
    }

    /// A fact a person might copy, such as a path, a build or an address, set in Mono.
    pub fn code(
        self,
        key: impl Into<String>,
        label: impl Into<String>,
        value: impl Into<String>,
    ) -> Self {
        self.push(key, label, value, Type::Mono)
    }

    fn push(
        mut self,
        key: impl Into<String>,
        label: impl Into<String>,
        value: impl Into<String>,
        kind: Type,
    ) -> Self {
        self.facts.push(Fact {
            key: key.into(),
            label: label.into(),
            value: value.into(),
            kind,
        });
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let mut list = door::gap(col().w_full(), Space::S4);
        if let Some(key) = self.key {
            list = list.key(key);
        }
        Block::new(
            Kind::Facts,
            list.children(self.facts.into_iter().map(|fact| fact.build(t))),
        )
    }
}

impl Default for Facts {
    fn default() -> Self {
        Facts::new()
    }
}

impl Fact {
    fn build(self, t: &Tokens) -> El {
        let label = words(self.label.clone(), Type::Small, t.text_tertiary)
            .wrap()
            .a11y_hidden();
        let label = door::first_line_in(label, Type::Small, door::line_height(self.kind))
            .grow_from(0.0, FACT_LABEL);
        let value = words(self.value.clone(), self.kind, t.text_primary)
            .wrap_anywhere()
            .grow_from(1.0, FACT_VALUE_FLOOR)
            .a11y_hidden();
        let line = row()
            .key(self.key)
            .label(format!("{} {}", self.label, self.value))
            .w_full()
            .flex_wrap()
            .items_start()
            .child(label)
            .child(value);
        door::gaps(line, Space::S2, Space::S12)
    }
}
