use rune::{Color, El, col, row};

use super::block::{Block, Kind};
use super::measure::{FACT_LABEL, FACT_TILE_FLOOR, FACT_VALUE_FLOOR};
use crate::Tokens;
use crate::system::{Radius, Said, Size, Space, Tone, Type, door, words};

/// What something is, read and not changed here: names, each with its value. As a list, each
/// value sits beside its name and moves under it where it would get narrower than it reads. As
/// tiles, each name sits over its value on a quiet card, the cards in equal columns that wrap.
pub struct Facts {
    key: String,
    tiles: bool,
    list: Option<String>,
    facts: Vec<Fact>,
    after: Option<El>,
}

struct Fact {
    key: String,
    label: Said,
    value: Said,
    kind: Type,
    band: f32,
    tone: Option<Tone>,
    shown: Option<El>,
}

impl Facts {
    /// Facts keyed `key`.
    pub fn new(key: impl Into<String>) -> Facts {
        Facts {
            key: key.into(),
            tiles: false,
            list: None,
            facts: Vec::new(),
            after: None,
        }
    }

    /// Lays the facts out as tiles, each keyed on its value, which it is announced as.
    pub fn tiles(mut self) -> Self {
        self.tiles = true;
        self
    }

    /// Assistive tools find the facts as a list named `name`, each fact one of its items.
    pub fn list(mut self, name: impl Into<String>) -> Self {
        self.list = Some(name.into());
        self
    }

    /// Inks the last fact's value in `tone`.
    pub fn tone(mut self, tone: Option<Tone>) -> Self {
        if let Some(fact) = self.facts.last_mut() {
            fact.tone = tone;
        }
        self
    }

    /// A fact in words, keyed `key` and announced as its label and its value.
    pub fn fact(
        self,
        key: impl Into<String>,
        label: impl Into<Said>,
        value: impl Into<Said>,
    ) -> Self {
        self.push(key, label, value, Type::Body)
    }

    /// A fact a person might copy, such as a path, a build or an address, set in Mono.
    pub fn code(
        self,
        key: impl Into<String>,
        label: impl Into<Said>,
        value: impl Into<Said>,
    ) -> Self {
        self.push(key, label, value, Type::Mono)
    }

    fn push(
        mut self,
        key: impl Into<String>,
        label: impl Into<Said>,
        value: impl Into<Said>,
        kind: Type,
    ) -> Self {
        self.facts.push(Fact {
            key: key.into(),
            label: label.into(),
            value: value.into(),
            kind,
            band: door::line_height(kind),
            tone: None,
            shown: None,
        });
        self
    }

    /// A fact whose value is not plain words, such as a link, a value with a quiet line under it,
    /// a list, or a path with its copy button. The fact is keyed `key` and named by its label;
    /// `value` is read as it is.
    pub fn item(self, key: impl Into<String>, label: impl Into<Said>, value: El) -> Self {
        self.shown(key, label, value, door::line_height(Type::Body))
    }

    /// A fact whose value starts with a control of the regular size, such as a path pressed to
    /// copy it. Its label lines up with the control's words.
    pub fn control(self, key: impl Into<String>, label: impl Into<Said>, value: El) -> Self {
        self.shown(key, label, value, Size::Regular.height())
    }

    fn shown(
        mut self,
        key: impl Into<String>,
        label: impl Into<Said>,
        value: El,
        band: f32,
    ) -> Self {
        self.facts.push(Fact {
            key: key.into(),
            label: label.into(),
            value: Said::default(),
            kind: Type::Body,
            band,
            tone: None,
            shown: Some(value),
        });
        self
    }

    /// A control after the tiles, in their row and with a tile's room, such as the button that
    /// opens what they sum up.
    pub fn after(mut self, control: impl Into<El>) -> Self {
        self.after = Some(control.into());
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        Block::new(Kind::Facts, self.el(t))
    }

    /// The facts on their own, for a place that is not a page, such as a document's card.
    pub fn el(self, t: &Tokens) -> El {
        let after = self
            .after
            .map(|control| control.grow_from(1.0, FACT_TILE_FLOOR));
        let mut list = if self.tiles {
            door::gaps(
                row().w_full().flex_wrap().items_start(),
                Space::S8,
                Space::S8,
            )
        } else {
            door::gap(col().w_full(), Space::S8)
        };
        list = list.key(self.key);
        let items = self.list.is_some();
        if let Some(name) = self.list {
            list = list.role("list").label(name);
        }
        let tiles = self.tiles;
        list.children(self.facts.into_iter().map(|fact| {
            let said = fact.said();
            let fact = if tiles { fact.tile(t) } else { fact.line(t) };
            if items {
                fact.role("listitem").label(said)
            } else {
                fact
            }
        }))
        .children(after)
    }
}

impl Fact {
    fn said(&self) -> String {
        match self.shown {
            Some(_) => self.label.text(),
            None => format!("{} {}", self.label.text(), self.value.text()),
        }
    }

    fn ink(&self, t: &Tokens) -> Color {
        match self.tone {
            Some(Tone::Bad) => t.error,
            Some(Tone::Warn) => t.warning,
            Some(Tone::Good) => t.success,
            Some(Tone::Accent) => t.accent,
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
                .label(self.value.text())
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
        let label = door::first_line_in(label, Type::Small, self.band).grow_from(0.0, FACT_LABEL);
        let said = self.said();
        let ink = self.ink(t);
        let value = match self.shown {
            Some(shown) => shown,
            None => words(self.value, self.kind, ink)
                .wrap_anywhere()
                .a11y_hidden(),
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
