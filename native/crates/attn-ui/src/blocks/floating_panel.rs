use rune::{Drag, El, Tx, col, row};
use rune_kit::ScrollBar;

use super::block::{Block, Kind, spaced};
use crate::system::{Space, Type, door, words};
use crate::{Frame, Tokens};

/// A panel laid over a surface that a person moves by its head, such as the annotations over a
/// terminal: its grip and title, a count or a control beside the title, a body of blocks that
/// scrolls when the panel runs out of room, and a foot.
pub struct FloatingPanel {
    key: String,
    title: String,
    width: f32,
    max_height: Option<f32>,
    aside: Vec<El>,
    body: Vec<Block>,
    foot: Option<El>,
}

impl FloatingPanel {
    /// `key` names the panel, a group read as `title`, `width` wide; its head is `{key}.head`.
    pub fn new(key: impl Into<String>, title: impl Into<String>, width: f32) -> FloatingPanel {
        FloatingPanel {
            key: key.into(),
            title: title.into(),
            width,
            max_height: None,
            aside: Vec::new(),
            body: Vec::new(),
            foot: None,
        }
    }

    /// The tallest the panel stands, such as the room its surface leaves it: its body scrolls
    /// past that.
    pub fn max_height(mut self, height: f32) -> Self {
        self.max_height = Some(height);
        self
    }

    /// What stands at the head's end, such as how many things the panel holds.
    pub fn aside(mut self, aside: impl Into<El>) -> Self {
        self.aside.push(aside.into());
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.body.push(block);
        self
    }

    pub fn push_all(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.body.extend(blocks);
        self
    }

    /// The line at the panel's foot, such as what it holds and the action that sends it.
    pub fn foot(mut self, foot: impl Into<El>) -> Self {
        self.foot = Some(foot.into());
        self
    }

    /// The panel, its head moving it with `drag`.
    pub fn build<S: 'static>(
        self,
        t: &Tokens,
        drag: impl Fn(&mut S, &mut Drag, &mut Tx) + 'static,
    ) -> El {
        let head = door::gap(
            row()
                .key(format!("{}.head", self.key))
                .w_full()
                .items_center()
                .cursor(rune::Cursor::Grab)
                .on_drag(drag),
            Space::S8,
        )
        .child(words(crate::glyph::GRABBER, Type::Small, t.text_tertiary))
        .child(
            words(self.title.clone(), Type::Name, t.text_primary)
                .single_line()
                .truncate()
                .grow(1.0)
                .min_w(0.0),
        )
        .children(self.aside);
        let body = (!self.body.is_empty()).then(|| {
            spaced(self.body, gap, None)
                .shrink()
                .min_h(0.0)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .scroll_bar_styled(crate::scroll_bar(t))
        });
        let surface = door::gap(
            col()
                .key(self.key)
                .role("group")
                .label(self.title)
                .w(self.width)
                .min_h(0.0)
                .child(head)
                .children(body)
                .children(self.foot),
            Space::S8,
        );
        let surface = match self.max_height {
            Some(height) => surface.max_h(height),
            None => surface,
        };
        Frame::menu(t).draw_clipped(door::inset(surface, Space::S12, Space::S8))
    }
}

/// A floating panel's blocks stand close, as in a popover.
fn gap(_: Kind, _: Kind) -> Space {
    Space::S8
}
