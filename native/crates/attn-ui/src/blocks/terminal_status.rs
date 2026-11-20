use rune::{El, row};

use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// The strip under a terminal that says what its machine is up to, read as `label`: each of
/// `said`, keyed, on one line, the last ones giving way first.
pub fn terminal_status(
    key: impl Into<String>,
    label: impl Into<String>,
    said: Vec<(String, String)>,
    t: &Tokens,
) -> El {
    let strip = row()
        .key(key.into())
        .role("status")
        .label(label.into())
        .w_full()
        .fill(t.bg_panel)
        .children(said.into_iter().map(|(key, said)| {
            words(said.clone(), Type::Small, t.text_secondary)
                .key(key)
                .label(said)
                .single_line()
                .truncate()
        }));
    door::gap(door::inset(strip, Space::S8, Space::S4), Space::S12)
}
