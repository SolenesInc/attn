use rune::{El, col, row};
use rune_kit::ScrollBar;

use crate::Tokens;
use crate::system::{Said, Space, Type, door, words};

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

/// In a page, of the room a list and its inspector share past their floors, the parts each
/// takes: the inspector three to the list's one, the split main's Analytics page drew at 1380
/// points (a 395 point list beside a 623 point inspector).
const LIST_PARTS: f32 = 1.0;
const INSPECTOR_PARTS: f32 = 3.0;

/// The column beside a list that reads its chosen row: the row's name, a line under it that
/// says what it is, then the facts known about it and what acts on it, in a column that scrolls.
pub struct Inspector {
    key: String,
    title: Option<Said>,
    label: String,
    kicker: Option<El>,
    parts: Vec<El>,
}

impl Inspector {
    /// `key` names the column, which assistive tools read as its details.
    pub fn new(key: impl Into<String>, title: impl Into<Said>) -> Inspector {
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
        let (key, label) = (self.key.clone(), self.label.clone());
        let column = self.column(t);
        col()
            .key(key)
            .role("complementary")
            .label(label)
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

    /// The column as part of a page that scrolls, with no scroll of its own: the page reveals
    /// what the column holds, so a control in it is never focused out of view.
    pub fn in_page(self, t: &Tokens) -> El {
        let (key, label) = (self.key.clone(), self.label.clone());
        let column = self.column(t);
        col()
            .key(key)
            .role("complementary")
            .label(label)
            .grow_from(INSPECTOR_PARTS, NARROWEST)
            .border_sides([0.0, 0.0, 0.0, 1.0], t.border_subtle)
            .child(column)
    }

    fn column(self, t: &Tokens) -> El {
        let head = self.title.map(|title| {
            door::gap(col().w_full(), Space::S4)
                .child(
                    words(&title, Type::Heading, t.text_primary)
                        .role("heading")
                        .label(title.text())
                        .wrap(),
                )
                .children(self.kicker)
        });
        let column = door::gap(col().w_full(), Space::S12)
            .children(head)
            .children(self.parts);
        door::frame(column, Space::S12, Space::S16, Space::S16)
    }
}

/// A fact's value told in more than one line, such as a value with the quiet line under it.
pub fn lines(parts: impl IntoIterator<Item = El>) -> El {
    door::gap(col().w_full(), Space::S2).children(parts)
}

/// A line of a fact's value: `said` as body text, a long path broken where it must.
pub fn value(said: impl Into<Said>, t: &Tokens) -> El {
    words(said, Type::Body, t.text_primary).wrap_anywhere()
}

/// What a fact says about its value, quieter than it.
pub fn aside(said: impl Into<Said>, t: &Tokens) -> El {
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

/// A list built as a block, such as one that holds more than one list, with its inspector
/// beside it in a page that scrolls: the two share the row while both fit, neither narrower
/// than an inspector gets and the inspector taking the larger part, and the inspector goes
/// under the list where they don't. Their widths come from the page, never from what they
/// hold, so a part opening in either moves nothing sideways. Give it an
/// inspector built [`Inspector::in_page`], so the page is the one thing that scrolls. The row
/// is keyed `key`, so the focus in either keeps its place while blocks above come and go.
pub fn beside_list(key: impl Into<String>, list: super::Block, inspector: El) -> super::Block {
    super::Block::new(
        super::block::Kind::Custom,
        row()
            .key(key.into())
            .w_full()
            .flex_wrap()
            .items_start()
            .child(list.el.grow_from(LIST_PARTS, NARROWEST))
            .child(inspector),
    )
}
