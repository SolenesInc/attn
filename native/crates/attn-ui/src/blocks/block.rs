use rune::{Color, El, col};
use rune_kit::ScrollBar;

use crate::Tokens;
use crate::system::{RING_REACH, Space, door};

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
    Table,
    Control,
    Custom,
}

/// A laid-out piece of a page that knows what it is, so its parent spaces it from its
/// neighbours. A block has no outer margin.
pub struct Block {
    pub(crate) kind: Kind,
    pub(crate) el: El,
    slot: Option<String>,
    wide: bool,
}

impl Block {
    /// A block whose element carries its key, which keeps what it holds, such as the keyboard,
    /// when a sibling appears or goes above it.
    pub(crate) fn new(kind: Kind, el: El) -> Block {
        Block {
            kind,
            el,
            slot: None,
            wide: false,
        }
    }

    /// A control on a line of its own, such as a segmented choice, at its own width, held in
    /// a slot keyed `key`.
    pub fn control(key: impl Into<String>, control: impl Into<El>) -> Block {
        Block {
            kind: Kind::Control,
            el: control.into().self_start(),
            slot: Some(key.into()),
            wide: false,
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
            wide: false,
        }
    }

    /// The block runs past a page's reading measure to the room its pictures want, as an ask's
    /// words do: see [`super::Sheet::measure`].
    pub fn wide(self) -> Block {
        Block { wide: true, ..self }
    }

    /// The block at most `text` wide, or `wide` when it is wide, centred in its column.
    pub(crate) fn measured(self, text: f32, wide: f32) -> Block {
        let most = if self.wide { wide } else { text };
        let el = match self.slot {
            Some(key) => col().key(key).w_full().child(self.el),
            None => self.el,
        };
        Block {
            el: el.w_full().max_w(most).self_center(),
            slot: None,
            ..self
        }
    }

    /// The block with its element changed by `f`, such as a handler or a mark added to a
    /// section.
    pub fn map(self, f: impl FnOnce(El) -> El) -> Block {
        Block {
            el: f(self.el),
            ..self
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

/// Whether a bounded surface stands as tall as what it holds, up to its container, or fills
/// its container.
#[derive(Clone, Copy, PartialEq)]
pub(crate) enum Room {
    Fit,
    Fill,
}

/// A surface bounded by its container, as a dialog, a popover or a sheet is: `head` and `foot`
/// keep their size, and `body`, built with [`scrolling`], gives way to the room left between
/// them, shrinking where the surface fits what it holds and growing where it fills.
pub(crate) fn bounded(head: El, body: Option<El>, foot: Option<El>, room: Room) -> El {
    let column = match room {
        Room::Fit => col().shrink(),
        Room::Fill => col().grow(1.0),
    };
    let body = body.map(|body| match room {
        Room::Fit => body.shrink(),
        Room::Fill => body.grow(1.0),
    });
    column
        .w_full()
        .min_h(0.0)
        .child(head)
        .children(body)
        .children(foot)
}

/// How wide a scrolled body's edge fades while what it holds runs past that edge, and so how far
/// from that edge a row brought into view stops: clear of the fade, and of its focus ring's reach.
const EDGE_FADE: Space = Space::S16;

/// Where a scrolled body opens: at its start, or at its end and staying there as more arrives,
/// as messages whose newest is last.
#[derive(Clone, Copy, PartialEq)]
pub(crate) enum Opens {
    AtStart,
    AtEnd,
}

/// A bounded surface's body as it scrolls: an edge fades while rows run under it, so the body
/// reads as passing under its head and foot; a row brought into view stops clear of that fade;
/// and a focus ring at rest against the top or bottom edge draws whole past it.
pub(crate) fn scrolling(body: El, opens: Opens, t: &Tokens) -> El {
    body.scroll(rune::ScrollSpec {
        stick_bottom: opens == Opens::AtEnd,
    })
    .scroll_margin(EDGE_FADE.pt())
    .scroll_edge_fade(EDGE_FADE.pt())
    .clip_outset(0.0, RING_REACH, 0.0, RING_REACH)
    .scroll_bar_styled(crate::scroll_bar(t))
    .min_h(0.0)
}

/// What a block runs when it is pressed.
pub(super) type Press<S> = std::rc::Rc<dyn Fn(&mut S, &mut rune::Tx)>;
