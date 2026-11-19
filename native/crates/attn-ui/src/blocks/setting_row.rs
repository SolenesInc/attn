use rune::{El, col, row};

use super::block::{Block, Kind};
use super::measure::COPY_FLOOR;
use crate::Tokens;
use crate::system::{Radius, Size, Space, Type, door, words};

/// One setting on a raised card: its name and what it does on the left, its control on the
/// right. Where the words would get narrower than they read, the control moves under them, and
/// the words then wrap to the card however narrow it is.
pub struct SettingRow {
    key: Option<String>,
    title: String,
    copy: String,
    note: Option<El>,
    control: El,
}

impl SettingRow {
    pub fn new(title: impl Into<String>, copy: impl Into<String>, control: impl Into<El>) -> Self {
        SettingRow {
            key: None,
            title: title.into(),
            copy: copy.into(),
            note: None,
            control: control.into(),
        }
    }

    pub fn key(mut self, key: impl Into<String>) -> Self {
        self.key = Some(key.into());
        self
    }

    /// Small print under the copy, such as what else the setting does or a shortcut.
    pub fn note(mut self, note: Option<El>) -> Self {
        self.note = note;
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let said = door::gap(col().grow_from(1.0, COPY_FLOOR), Space::S4)
            .child(words(self.title, Type::Name, t.text_primary).wrap())
            .children(
                (!self.copy.is_empty())
                    .then(|| words(self.copy, Type::Small, t.text_secondary).wrap()),
            )
            .children(self.note);
        let mut card = row()
            .flex_wrap()
            .w_full()
            .min_h(Size::Regular.height() + 2.0 * Space::S12.pt())
            .items_center()
            .fill(t.bg_elevated)
            .border(1.0, t.border_subtle)
            .child(said)
            .child(self.control);
        if let Some(key) = self.key {
            card = card.key(key);
        }
        let card = door::gaps(
            door::inset(card, Space::S16, Space::S12),
            Space::S12,
            Space::S16,
        );
        Block::new(Kind::SettingRow, door::corner(card, Radius::Card))
    }
}
