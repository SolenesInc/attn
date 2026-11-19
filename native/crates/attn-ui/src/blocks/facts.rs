use rune::{Color, El, col, row};

use super::block::{Block, Kind};
use super::measure::{FACT_LABEL, FACT_TILE_FLOOR, FACT_VALUE_FLOOR};
use crate::Tokens;
use crate::system::{Radius, Space, Tone, Type, door, words};

/// What something is, read and not changed here: names, each with its value. As a list, each
/// value sits beside its name and moves under it where it would get narrower than it reads. As
/// tiles, each name sits over its value on a quiet card, the cards in equal columns that wrap.
pub struct Facts {
    key: Option<String>,
    tiles: bool,
    facts: Vec<Fact>,
}

struct Fact {
    key: String,
    label: String,
    value: String,
    kind: Type,
    tone: Option<Tone>,
    shown: Option<El>,
}

impl Facts {
    pub fn new() -> Facts {
        Facts {
            key: None,
            tiles: false,
            facts: Vec::new(),
        }
    }

    /// Lays the facts out as tiles, each keyed on its value, which it is announced as.
    pub fn tiles(mut self) -> Self {
        self.tiles = true;
        self
    }

    /// Inks the last fact's value in `tone`.
    pub fn tone(mut self, tone: Option<Tone>) -> Self {
        if let Some(fact) = self.facts.last_mut() {
            fact.tone = tone;
        }
        self
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
            tone: None,
            shown: None,
        });
        self
    }

    /// A fact whose value is not plain words, such as a link, a value with a quiet line under it,
    /// a list, or a path with its copy button. The fact is keyed `key` and named by its label;
    /// `value` is read as it is.
    pub fn item(mut self, key: impl Into<String>, label: impl Into<String>, value: El) -> Self {
        self.facts.push(Fact {
            key: key.into(),
            label: label.into(),
            value: String::new(),
            kind: Type::Body,
            tone: None,
            shown: Some(value),
        });
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let mut list = if self.tiles {
            door::gaps(
                row().w_full().flex_wrap().items_start(),
                Space::S8,
                Space::S8,
            )
        } else {
            door::gap(col().w_full(), Space::S4)
        };
        if let Some(key) = self.key {
            list = list.key(key);
        }
        let tiles = self.tiles;
        Block::new(
            Kind::Facts,
            list.children(
                self.facts
                    .into_iter()
                    .map(|fact| if tiles { fact.tile(t) } else { fact.line(t) }),
            ),
        )
    }
}

impl Default for Facts {
    fn default() -> Self {
        Facts::new()
    }
}

impl Fact {
    fn ink(&self, t: &Tokens) -> Color {
        match self.tone {
            Some(Tone::Bad) => t.error,
            Some(Tone::Warn) => t.warning,
            Some(Tone::Good) => t.success,
            Some(Tone::Neutral) => t.text_secondary,
            None => t.text_primary,
        }
    }

    fn tile(self, t: &Tokens) -> El {
        let ink = self.ink(t);
        let value = match self.shown {
            Some(shown) => shown.key(self.key),
            None => words(self.value.clone(), self.kind, ink)
                .key(self.key)
                .label(self.value)
                .truncate(),
        };
        let tile = door::gap(col().grow_from(1.0, FACT_TILE_FLOOR), Space::S2)
            .fill(t.bg_elevated)
            .border(1.0, t.border_subtle)
            .child(words(self.label, Type::Label, t.text_tertiary).truncate())
            .child(value);
        door::corner(door::inset(tile, Space::S12, Space::S8), Radius::Control)
    }

    fn line(self, t: &Tokens) -> El {
        let label = words(self.label.clone(), Type::Small, t.text_tertiary)
            .wrap()
            .a11y_hidden();
        let label = door::first_line_in(label, Type::Small, door::line_height(self.kind))
            .grow_from(0.0, FACT_LABEL);
        let (value, said) = match self.shown {
            Some(shown) => (shown, self.label),
            None => (
                words(self.value.clone(), self.kind, self.ink(t))
                    .wrap_anywhere()
                    .a11y_hidden(),
                format!("{} {}", self.label, self.value),
            ),
        };
        let line = row()
            .key(self.key)
            .label(said)
            .w_full()
            .flex_wrap()
            .items_start()
            .child(label)
            .child(value.grow_from(1.0, FACT_VALUE_FLOOR));
        door::gaps(line, Space::S2, Space::S12)
    }
}
