use rune::{Color, El, col};

use crate::system::{Space, door};

/// What a block is, which decides the space its parent leaves round it.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Kind {
    Prose,
    Notice,
    Status,
    Section,
    SettingRow,
    Field,
    Actions,
    Disclosure,
    List,
    Facts,
    Control,
    Custom,
}

/// A laid-out piece of a page that knows what it is, so its parent spaces it from its
/// neighbours. A block has no outer margin.
pub struct Block {
    pub(crate) kind: Kind,
    pub(crate) el: El,
    slot: Option<String>,
}

impl Block {
    /// A block whose element carries its key, which keeps what it holds, such as the keyboard,
    /// when a sibling appears or goes above it.
    pub(crate) fn new(kind: Kind, el: El) -> Block {
        Block {
            kind,
            el,
            slot: None,
        }
    }

    /// A control on a line of its own, such as a segmented choice, at its own width, held in
    /// a slot keyed `key`.
    pub fn control(key: impl Into<String>, control: impl Into<El>) -> Block {
        Block {
            kind: Kind::Control,
            el: control.into().self_start(),
            slot: Some(key.into()),
        }
    }

    /// Names the block as tests and assistive tools find it.
    pub fn key(self, key: impl Into<String>) -> Block {
        Block::new(self.kind, self.el.key(key.into()))
    }

    /// A piece no block fits, built in its feature from controls inside a rhythm
    /// ([`super::Inline`], [`super::Stack`]), held in a slot keyed `key`, which leaves any key
    /// the piece carries to its caller.
    pub fn custom(key: impl Into<String>, el: El) -> Block {
        Block {
            kind: Kind::Custom,
            el,
            slot: Some(key.into()),
        }
    }

    /// A part of the page with no heading of its own, such as a seed's plan: spaced and ruled
    /// from the sections around it as a section is.
    pub fn part(el: El) -> Block {
        Block::new(Kind::Section, el)
    }
}

/// The space between two neighbouring blocks on a page.
pub(crate) fn page_gap(above: Kind, below: Kind) -> Space {
    match (above, below) {
        (Kind::Section, Kind::Section) => Space::S32,
        (_, Kind::Status) | (Kind::Notice, _) | (_, Kind::Notice) => Space::S12,
        _ => Space::S16,
    }
}

/// The space between two neighbouring blocks inside a section, a disclosure or a row's details.
pub(crate) fn inner_gap(above: Kind, below: Kind) -> Space {
    match (above, below) {
        (Kind::SettingRow, Kind::SettingRow) => Space::S8,
        (Kind::Field, Kind::Field) => Space::S16,
        _ => Space::S12,
    }
}

/// `blocks` one under another, each spaced from the one above by `gap`. Where `rule` is given,
/// two sections in a row are set apart by a rule of that ink across their gap.
pub(crate) fn spaced(blocks: Vec<Block>, gap: fn(Kind, Kind) -> Space, rule: Option<Color>) -> El {
    let mut out = col().w_full();
    let mut last: Option<Kind> = None;
    for block in blocks {
        let el = match (last, rule) {
            (None, _) => block.el,
            (Some(Kind::Section), Some(ink)) if block.kind == Kind::Section => {
                door::ruled_above(block.el, gap(Kind::Section, Kind::Section), ink)
            }
            (Some(above), _) => door::above(block.el, gap(above, block.kind)),
        };
        last = Some(block.kind);
        out = out.child(match block.slot {
            Some(key) => col().key(key).w_full().child(el),
            None => el,
        });
    }
    out
}

/// What a block runs when it is pressed.
pub(super) type Press<S> = std::rc::Rc<dyn Fn(&mut S, &mut rune::Tx)>;
