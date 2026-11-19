use rune::{El, col, row};

use crate::Tokens;
use crate::system::{Space, Type, door, spring, words};

/// One entry of a log: who wrote it, what kind it is and when, over its words, on a rail down
/// its left side. A rail in the warm accent marks an entry addressed to whoever comes next.
pub struct Entry {
    key: String,
    who: String,
    kind: Option<String>,
    when: String,
    body: Option<El>,
    accent: bool,
}

impl Entry {
    pub fn new(key: impl Into<String>, who: impl Into<String>, when: impl Into<String>) -> Entry {
        Entry {
            key: key.into(),
            who: who.into(),
            kind: None,
            when: when.into(),
            body: None,
            accent: false,
        }
    }

    pub fn kind(mut self, kind: impl Into<String>) -> Self {
        self.kind = Some(kind.into());
        self
    }

    pub fn body(mut self, body: El) -> Self {
        self.body = Some(body);
        self
    }

    pub fn accent(mut self, accent: bool) -> Self {
        self.accent = accent;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let head = door::gap(row().w_full().items_center(), Space::S8)
            .child(
                words(self.who, Type::Small, t.text_secondary)
                    .truncate()
                    .shrink(),
            )
            .children(
                self.kind
                    .map(|kind| words(kind, Type::Small, t.text_tertiary)),
            )
            .child(spring())
            .child(words(self.when, Type::Small, t.text_tertiary));
        let entry = col()
            .key(self.key)
            .role("listitem")
            .w_full()
            .child(head)
            .children(self.body);
        rail(door::gap(entry, Space::S4), self.accent, t)
    }
}

/// `content` on a rail down its left side, as words quoted from someone: in the warm accent
/// when they are addressed to whoever comes next.
pub fn rail(content: El, accent: bool, t: &Tokens) -> El {
    let ink = if accent { t.brand } else { t.border_subtle };
    door::sides(
        content.border_sides([0.0, 0.0, 0.0, RAIL], ink),
        Space::S12,
        Space::S0,
    )
}

const RAIL: f32 = 2.0;

/// Log entries one under another.
pub fn entries(entries: impl IntoIterator<Item = El>) -> El {
    door::gap(col().w_full(), Space::S16).children(entries)
}
