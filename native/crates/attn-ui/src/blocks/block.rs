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
    Facts,
    Control,
    Custom,
}

/// A laid-out piece of a page that knows what it is, so its parent spaces it from its
/// neighbours. A block has no outer margin.
pub struct Block {
    pub(crate) kind: Kind,
    pub(crate) el: El,
}

impl Block {
    pub(crate) fn new(kind: Kind, el: El) -> Block {
        Block { kind, el }
    }

    /// A control on a line of its own, such as a segmented choice, at its own width.
    pub fn control(control: impl Into<El>) -> Block {
        Block::new(Kind::Control, control.into().self_start())
    }

    /// A piece no block fits, built in its feature from controls inside a rhythm
    /// ([`super::Inline`], [`super::Stack`]).
    pub fn custom(el: El) -> Block {
        Block::new(Kind::Custom, el)
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
        out = out.child(el);
    }
    out
}

/// What a block runs when it is pressed.
pub(super) type Press<S> = std::rc::Rc<dyn Fn(&mut S, &mut rune::Tx)>;
