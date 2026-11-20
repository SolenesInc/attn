use std::rc::Rc;

use rune::{El, Tx, col};

use super::block::{Block, Kind, Press, inner_gap, spaced};
use crate::Tokens;
use crate::system::{Said, Space, disclosure, door};

/// A part of a page that opens and closes, with tags that say what it holds while closed. `key`
/// names its control; `{key}.part` names the whole part, a group read as its label.
pub struct Disclosure<S> {
    key: String,
    label: Said,
    open: bool,
    beside: Option<(El, String)>,
    tags: Vec<String>,
    failed: bool,
    disabled: bool,
    toggle: Press<S>,
    blocks: Vec<Block>,
}

impl<S: 'static> Disclosure<S> {
    pub fn new(
        key: impl Into<String>,
        label: impl Into<Said>,
        open: bool,
        toggle: impl Fn(&mut S, &mut Tx) + 'static,
    ) -> Disclosure<S> {
        Disclosure {
            key: key.into(),
            label: label.into(),
            open,
            beside: None,
            tags: Vec::new(),
            failed: false,
            disabled: false,
            toggle: Rc::new(toggle),
            blocks: Vec::new(),
        }
    }

    /// What follows the part's name on its control, such as the value the part sums up to,
    /// drawn as `shown` and said as `said`.
    pub fn beside(mut self, shown: El, said: impl Into<String>) -> Self {
        self.beside = Some((shown, said.into()));
        self
    }

    pub fn tags(mut self, tags: Vec<String>) -> Self {
        self.tags = tags;
        self
    }

    /// Something inside failed, so the closed part still shows it.
    pub fn failed(mut self, failed: bool) -> Self {
        self.failed = failed;
        self
    }

    pub fn disabled(mut self, disabled: bool) -> Self {
        self.disabled = disabled;
        self
    }

    pub fn push(mut self, block: Block) -> Self {
        self.blocks.push(block);
        self
    }

    pub fn build(self, t: &Tokens) -> Block {
        let toggle = self.toggle;
        let part = format!("{}.part", self.key);
        let label = self.label.text();
        let control = disclosure(
            self.key,
            self.label,
            self.open,
            self.beside,
            &self.tags,
            self.failed,
            self.disabled,
            t,
            move |s: &mut S, tx: &mut Tx| toggle(s, tx),
        );
        let body = (self.open && !self.blocks.is_empty()).then(|| {
            door::sides(spaced(self.blocks, inner_gap, None), Space::S16, Space::S0)
                .border_sides([0.0, 0.0, 0.0, 1.0], t.border)
        });
        let el = col()
            .key(part)
            .role("group")
            .label(label)
            .w_full()
            .child(El::from(control))
            .children(body);
        Block::new(Kind::Disclosure, door::gap(el, Space::S12))
    }
}
