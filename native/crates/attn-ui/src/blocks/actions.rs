use rune::{El, row};

use super::block::{Block, Kind};
use crate::system::{Space, door};

/// A line of buttons. Those pushed after [`Actions::then_end`] sit at its far end; those pushed
/// after [`Actions::then_center`] sit at its centre, whatever the two ends hold.
pub struct Actions {
    key: String,
    start: Vec<El>,
    center: Vec<El>,
    end: Vec<El>,
    at: At,
}

#[derive(Default, PartialEq)]
enum At {
    #[default]
    Start,
    Center,
    End,
}

impl Actions {
    /// A line of buttons keyed `key`.
    pub fn new(key: impl Into<String>) -> Actions {
        Actions {
            key: key.into(),
            start: Vec::new(),
            center: Vec::new(),
            end: Vec::new(),
            at: At::Start,
        }
    }

    pub fn push(mut self, button: impl Into<El>) -> Self {
        match self.at {
            At::Start => self.start.push(button.into()),
            At::Center => self.center.push(button.into()),
            At::End => self.end.push(button.into()),
        }
        self
    }

    pub fn then_center(mut self) -> Self {
        self.at = At::Center;
        self
    }

    pub fn then_end(mut self) -> Self {
        self.at = At::End;
        self
    }

    pub fn build(self) -> Block {
        if !self.center.is_empty() {
            let side = |els: Vec<El>| {
                door::gap(row().w(0.0).grow(1.0).min_w(0.0).items_center(), Space::S8).children(els)
            };
            let line = door::gap(row().w_full().items_center(), Space::S8)
                .child(side(self.start))
                .child(door::gap(row().items_center(), Space::S8).children(self.center))
                .child(side(self.end).justify_end());
            return Block::new(Kind::Actions, line.key(self.key));
        }
        let line = row()
            .w_full()
            .items_center()
            .children(self.start)
            .child(row().grow(1.0))
            .children(self.end);
        Block::new(
            Kind::Actions,
            door::gaps(line.key(self.key).flex_wrap(), Space::S8, Space::S8),
        )
    }
}
