use rune::{El, row};

use crate::Tokens;
use crate::system::{Said, Space, Type, door, words};

/// The way back from where a reader stands: each step a [`crate::system::Link`] that climbs to
/// it, then the place itself, one after another with a mark between.
pub struct Trail {
    key: String,
    label: String,
    parts: Vec<El>,
}

impl Trail {
    /// `label` names the trail for assistive tools.
    pub fn new(key: impl Into<String>, label: impl Into<String>) -> Trail {
        Trail {
            key: key.into(),
            label: label.into(),
            parts: Vec::new(),
        }
    }

    /// A step back, a [`crate::system::Link`] with its press.
    pub fn step(mut self, link: impl Into<El>) -> Self {
        self.parts.push(link.into());
        self
    }

    /// Where the reader stands, named and not a way anywhere.
    pub fn here(mut self, key: impl Into<String>, said: impl Into<Said>, t: &Tokens) -> Self {
        self.parts.push(
            words(said, Type::Small, t.text_secondary)
                .key(key.into())
                .truncate()
                .shrink()
                .min_w(0.0),
        );
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let mut trail = row()
            .key(self.key)
            .role("navigation")
            .label(self.label)
            .min_w(0.0)
            .items_center();
        for (i, part) in self.parts.into_iter().enumerate() {
            if i > 0 {
                trail = trail.child(words("›", Type::Small, t.text_dimmed).a11y_hidden());
            }
            trail = trail.child(part);
        }
        door::gap(trail, Space::S8)
    }
}
