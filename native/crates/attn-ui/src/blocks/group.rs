use std::rc::Rc;

use rune::{KeyEvent, Tx};

use super::block::{Block, Kind, page_gap, spaced};
use crate::Tokens;

type Keys<S> = Rc<dyn Fn(&mut S, &KeyEvent, &mut Tx) -> bool>;

/// Blocks a page shows as one part read as `label`, such as a list and what its chosen row
/// holds. Given keys, the focus can rest on it and its keys answer while the focus is on it or
/// on any control in it. Its blocks are spaced as a page spaces its own.
pub struct Group<S> {
    key: String,
    label: String,
    keys: Option<Keys<S>>,
    roving: bool,
    blocks: Vec<Block>,
}

impl<S: 'static> Group<S> {
    /// A group keyed `key`, read as `label`.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Group<S> {
        Group {
            key: key.into(),
            label: label.into(),
            keys: None,
            roving: false,
            blocks: Vec::new(),
        }
    }

    /// The keys the group answers; `keys` says whether it took the key.
    pub fn keys(mut self, keys: impl Fn(&mut S, &KeyEvent, &mut Tx) -> bool + 'static) -> Self {
        self.keys = Some(Rc::new(keys));
        self
    }

    /// The options in the group are one stop of Tab, the chosen one taking it, and the arrows
    /// move among them, choosing the one they reach, as a list box does. The page reveals the
    /// option that takes the focus, however far down the list it is.
    pub fn roving(mut self) -> Self {
        self.roving = true;
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.blocks.push(block);
        self
    }

    pub fn push_all(mut self, blocks: impl IntoIterator<Item = Block>) -> Self {
        self.blocks.extend(blocks);
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let mut el = spaced(self.blocks, page_gap, Some(t.border_subtle))
            .key(self.key)
            .role("group")
            .label(self.label);
        if let Some(keys) = self.keys {
            el = el
                .focusable(true)
                .on_key(move |s: &mut S, ev: &KeyEvent, tx: &mut Tx| keys(s, ev, tx));
        }
        if self.roving {
            el = el.focus_group().focus_follows();
        }
        Block::new(Kind::Custom, el)
    }
}
