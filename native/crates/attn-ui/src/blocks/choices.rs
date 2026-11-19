use rune::{Color, El, el, row};

use super::block::{Block, Kind};
use crate::Tokens;
use crate::system::{Control, Radius, choices, door};

/// One option chosen among a few, each a row that can say more about itself: Up and Down move
/// the choice.
pub struct Choices {
    key: String,
    label: String,
    options: Vec<El>,
}

impl Choices {
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Choices {
        Choices {
            key: key.into(),
            label: label.into(),
            options: Vec::new(),
        }
    }

    pub fn options(mut self, options: impl IntoIterator<Item = Control>) -> Self {
        self.options.extend(options.into_iter().map(El::from));
        self
    }

    pub fn build(self) -> Block {
        Block::new(
            Kind::Control,
            choices(self.key, &self.label).children(self.options),
        )
    }
}

const SWATCH: (f32, f32) = (14.0, 18.0);

/// A strip of colours side by side, such as a few of a colour scheme's, to mark a choice.
pub fn swatches(colors: impl IntoIterator<Item = Color>, t: &Tokens) -> El {
    let strip = row()
        .border(1.0, t.border)
        .children(
            colors
                .into_iter()
                .map(|color| el().size(SWATCH.0, SWATCH.1).fill(color)),
        )
        .a11y_hidden();
    door::clip_corner(
        door::corner(door::inside_edge(strip), Radius::Tag),
        Radius::Tag,
    )
}
