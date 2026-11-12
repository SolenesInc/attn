use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, col, text};

use crate::State;

pub(crate) fn view(_state: &State, t: &Tokens) -> El {
    col()
        .key("home")
        .role("main")
        .label("Home")
        .grow(1.0)
        .h_full()
        .padding(space::S6)
        .gap(space::S3)
        .child(text("Home").font(type_::TITLE).color(t.text_primary))
}
