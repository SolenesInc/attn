//! The queue bar's peek: hovering the waiting pill lists the agents under it, as the Tauri app's
//! `WaitingPeek`, and a row opens its agent or tile.

use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{Align, Color, El, Shadow, Side, Tx, col, text};

use crate::State;

const PILL: &str = "queue-bar.pill";
const PEEK: &str = "queue-bar.peek";
const WIDTH: f32 = 600.0;

/// Whether the pointer is over the pill or over the peek; the peek shows while it is over
/// either.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) struct Hover {
    pill: bool,
    peek: bool,
}

pub(crate) fn pill_hovered(state: &mut State, inside: bool) {
    state.window.bar_peek.pill = inside;
}

/// The pill was pressed: the peek closes until the pointer comes back to the pill.
pub(crate) fn dismiss(state: &mut State) {
    state.window.bar_peek = Hover::default();
}

fn shows(state: &State) -> bool {
    let hover = state.window.bar_peek;
    (hover.pill || hover.peek)
        && state.window.layer.is_none()
        && state.window.sidebar_hidden
        && super::actions::queue_flow(state)
}

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    if !shows(state) {
        return None;
    }
    let (rows, more) = crate::palette::peek_rows(state);
    let rows = rows.iter().map(|r| {
        let key = format!("{PEEK}.{}", r.key);
        let face = crate::palette::row_face(r, key, t);
        match r.selectable() {
            false => face,
            true => {
                let picked = r.key.clone();
                face.role("button")
                    .px(10.0)
                    .radius(8.0)
                    .hover(t.bg_selected)
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                        dismiss(state);
                        crate::palette::open_peeked(state, &picked, tx);
                    })
            }
        }
    });
    let search = crate::palette::label_of(state, &crate::Action::UiActionMenu);
    let more = (more > 0).then(|| {
        let said = match search {
            Some(keys) => format!("{more} more · {keys} to search"),
            None => format!("{more} more"),
        };
        text(said)
            .key(format!("{PEEK}.more"))
            .font(type_::LABEL)
            .color(t.text_tertiary)
            .px(10.0)
            .py(space::S1)
    });
    Some(
        col()
            .key(PEEK)
            .role("group")
            .label("Agents")
            .w(WIDTH)
            .px(6.0)
            .py(8.0)
            .radius(12.0)
            .fill(t.bg_elevated)
            .border(1.0, t.border_hover)
            .box_shadow(Shadow {
                y: 18.0,
                blur: 50.0,
                color: Color::BLACK.alpha(0.55),
                ..Shadow::default()
            })
            .children(rows)
            .children(more)
            .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
                state.window.bar_peek.peek = inside;
            })
            .anchored(PILL, Side::Bottom, Align::Start, 6.0),
    )
}
