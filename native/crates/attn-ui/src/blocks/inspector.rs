use rune::{El, col, row};
use rune_kit::ScrollBar;

use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// The narrowest an inspector gets: the Tauri app's `.ledger-inspector { min-width: 280px }`,
/// which holds a session's longest field, a full stamp with its age ("Tue, Nov 17 at 09:58
/// (2 hours ago)"), on one line.
const NARROWEST: f32 = 280.0;

/// The widest an inspector gets, the Tauri app's `max-width: 380px`: past it the list beside it
/// is the one that needs the room.
const WIDEST: f32 = 380.0;

/// Of the room it shares with its list, the share an inspector takes, the Tauri app's
/// `width: 30%`.
const SHARE: f32 = 0.3;

/// The column beside a list that reads its chosen row: the row's name, a line under it that
/// says what it is, then the facts known about it and what acts on it, in a column that scrolls.
pub struct Inspector {
    key: String,
    title: Option<String>,
    label: String,
    kicker: Option<El>,
    parts: Vec<El>,
}

impl Inspector {
    /// `key` names the column, which assistive tools read as its details.
    pub fn new(key: impl Into<String>, title: impl Into<String>) -> Inspector {
        Inspector {
            key: key.into(),
            title: Some(title.into()),
            label: "Details".to_owned(),
            kicker: None,
            parts: Vec::new(),
        }
    }

    /// A column beside the content with no name of its own, read as `label`, such as a note's
    /// outline and the notes that link to it.
    pub fn side(key: impl Into<String>, label: impl Into<String>) -> Inspector {
        Inspector {
            key: key.into(),
            title: None,
            label: label.into(),
            kicker: None,
            parts: Vec::new(),
        }
    }

    /// A block of the column, such as a part that opens and closes, across its width.
    pub fn block(mut self, block: super::Block) -> Self {
        self.parts.push(block.el.w_full());
        self
    }

    /// What the chosen thing is, under its name, such as its state's mark and its kind.
    pub fn kicker(mut self, kicker: El) -> Self {
        self.kicker = Some(kicker);
        self
    }

    /// A part that is not a fact, such as the button that acts on the chosen thing, at its own
    /// width.
    pub fn push(mut self, part: impl Into<El>) -> Self {
        self.parts.push(part.into().self_start());
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let head = self.title.map(|title| {
            door::gap(col().w_full(), Space::S4)
                .child(
                    words(title.clone(), Type::Heading, t.text_primary)
                        .role("heading")
                        .label(title)
                        .wrap(),
                )
                .children(self.kicker)
        });
        let column = door::gap(col().w_full(), Space::S12)
            .children(head)
            .children(self.parts);
        let column = door::frame(column, Space::S12, Space::S16, Space::S16);
        col()
            .key(self.key)
            .role("complementary")
            .label(self.label)
            .w_frac(SHARE)
            .min_w(NARROWEST)
            .max_w(WIDEST)
            .h_full()
            .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
            .scroll(rune::ScrollSpec {
                stick_bottom: false,
            })
            .child(column)
            .scroll_bar_styled(crate::scroll_bar(t))
    }
}

/// A fact's value told in more than one line, such as a value with the quiet line under it.
pub fn lines(parts: impl IntoIterator<Item = El>) -> El {
    door::gap(col().w_full(), Space::S2).children(parts)
}

/// A line of a fact's value: `said` as body text, a long path broken where it must.
pub fn value(said: impl Into<String>, t: &Tokens) -> El {
    words(said, Type::Body, t.text_primary).wrap_anywhere()
}

/// What a fact says about its value, quieter than it.
pub fn aside(said: impl Into<String>, t: &Tokens) -> El {
    words(said, Type::Small, t.text_tertiary).wrap_anywhere()
}

/// The list beside its inspector, filling what they are put in.
pub fn beside(list: El, inspector: El) -> El {
    row()
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(list.grow(1.0).min_w(0.0))
        .child(inspector)
}
