use rune::{El, row};

use super::block::{Block, Kind};
use crate::system::{Space, door};

/// A line of buttons. Those pushed after [`Actions::then_end`] sit at its far end.
#[derive(Default)]
pub struct Actions {
    start: Vec<El>,
    end: Vec<El>,
    at_end: bool,
}

impl Actions {
    pub fn new() -> Actions {
        Actions::default()
    }

    pub fn push(mut self, button: impl Into<El>) -> Self {
        if self.at_end {
            self.end.push(button.into());
        } else {
            self.start.push(button.into());
        }
        self
    }

    pub fn then_end(mut self) -> Self {
        self.at_end = true;
        self
    }

    pub fn build(self) -> Block {
        let line = row()
            .w_full()
            .items_center()
            .children(self.start)
            .child(row().grow(1.0))
            .children(self.end);
        Block::new(
            Kind::Actions,
            door::gaps(line.flex_wrap(), Space::S8, Space::S8),
        )
    }
}
