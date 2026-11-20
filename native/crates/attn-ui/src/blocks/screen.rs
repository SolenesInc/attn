use rune::{El, ScrollSpec, col};
use rune_kit::ScrollBar;

use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// What a terminal shows, read as text, such as a peek at a tile off screen: in `Mono`, set in
/// from its edges, scrolling both ways and kept at its end while it grows. A person can select
/// it to copy it.
pub struct Screen;

impl Screen {
    /// `shown`, keyed `key`, filling the room its parent leaves.
    pub fn showing(key: impl Into<String>, shown: impl Into<String>, t: &Tokens) -> El {
        door::inset(
            col()
                .key(key.into())
                .w_full()
                .grow(1.0)
                .min_h(0.0)
                .scroll(ScrollSpec { stick_bottom: true })
                .scroll_xy(),
            Space::S8,
            Space::S8,
        )
        .child(words(shown.into(), Type::Mono, t.text_primary).selectable(true))
        .scroll_bar_styled(crate::scroll_bar(t).both_ways())
    }
}
