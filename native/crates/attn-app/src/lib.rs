use rune::tok::{self, type_};
use rune::{AppSpec, Cx, El, col, text};

#[derive(Default)]
pub struct State;

pub fn view(_: &State, _: &mut Cx<State>) -> El {
    let theme = tok::theme(true);
    col().w_full().h_full().center().fill(theme.bg).child(
        text("attn")
            .key("placeholder")
            .font(type_::TITLE)
            .color(theme.text),
    )
}

pub fn app() -> AppSpec<State> {
    AppSpec::new("attn", (960.0, 640.0), State::default, view)
}
