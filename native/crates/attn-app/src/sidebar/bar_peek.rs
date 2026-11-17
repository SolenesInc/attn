//! The queue bar's peeks, as the Tauri app's `WaitingPeek` and `RunsPeek`: hovering the waiting
//! pill lists the agents under it, hovering the runs chip lists the automation runs, and a row
//! opens its agent or tile.

use attn_ui::Tokens;
use attn_ui::type_;
use rune::tok::space;
use rune::{Align, El, Shadow, Side, Tx, col, text};

use crate::State;
use crate::palette::Row;

pub(crate) const PILL: &str = "queue-bar.pill";
pub(crate) const RUNS: &str = "queue-bar.runs";
const WIDTH: f32 = 600.0;
/// The room between the anchor and the peek, inside the peek's hover region so the pointer
/// crosses it without closing the peek.
const GAP: f32 = 6.0;

/// What a peek lists, by the queue bar's control it hangs from.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Anchor {
    Waiting,
    Runs,
}

impl Anchor {
    fn key(self) -> &'static str {
        match self {
            Anchor::Waiting => PILL,
            Anchor::Runs => RUNS,
        }
    }

    fn peek(self) -> &'static str {
        match self {
            Anchor::Waiting => "queue-bar.peek",
            Anchor::Runs => "queue-bar.runs-peek",
        }
    }
}

/// The anchor the pointer last came to, and whether it is over that anchor or over the peek;
/// the peek shows while it is over either.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) struct Hover {
    at: Option<Anchor>,
    anchor: bool,
    peek: bool,
}

pub(crate) fn anchor_hovered(state: &mut State, anchor: Anchor, inside: bool) {
    let hover = &mut state.window.bar_peek;
    if inside {
        *hover = Hover {
            at: Some(anchor),
            anchor: true,
            peek: false,
        };
    } else if hover.at == Some(anchor) {
        hover.anchor = false;
    }
}

/// The anchor was pressed, or left the bar: the peek closes until the pointer comes back.
pub(crate) fn dismiss(state: &mut State) {
    state.window.bar_peek = Hover::default();
}

fn shown(state: &State) -> Option<Anchor> {
    let hover = state.window.bar_peek;
    let open = (hover.anchor || hover.peek)
        && state.window.layer.is_none()
        && state.window.sidebar_hidden
        && super::actions::queue_flow(state);
    hover.at.filter(|_| open)
}

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let anchor = shown(state)?;
    let (rows, foot) = match anchor {
        Anchor::Waiting => {
            let (rows, more) = crate::palette::peek_rows(state);
            let search = crate::palette::label_of(state, &crate::Action::UiActionMenu);
            let foot = (more > 0).then(|| match search {
                Some(keys) => format!("{more} more · {keys} to search"),
                None => format!("{more} more"),
            });
            (rows, foot)
        }
        Anchor::Runs => {
            let rows = crate::palette::run_rows(state);
            let walks = rows.iter().any(|r| match &r.look {
                crate::palette::Look::Agent { tag, .. } => tag.is_some(),
                _ => false,
            });
            let next = crate::palette::label_of(state, &crate::Action::SessionNextRun);
            let foot = match (walks, next) {
                (true, Some(keys)) => {
                    format!(
                        "Automation runs never join the queue · {keys} opens the next one needing you"
                    )
                }
                (true, None) => "Automation runs never join the queue".to_owned(),
                (false, _) => {
                    "Automation runs never join the queue · nothing here needs you".to_owned()
                }
            };
            (rows, Some(foot))
        }
    };
    let peek = anchor.peek();
    let rows = rows.iter().map(|r| peek_row(peek, r, t));
    let foot = foot.map(|said| {
        text(said)
            .key(format!("{peek}.more"))
            .font(type_::LABEL)
            .color(t.text_tertiary)
            .px(10.0)
            .py(space::S1)
    });
    let panel = col()
        .key(format!("{peek}.panel"))
        .w(WIDTH)
        .shrink()
        .min_h(0.0)
        .px(6.0)
        .py(8.0)
        .radius(12.0)
        .fill(t.bg_elevated)
        .border(1.0, t.border_hover)
        .box_shadow(Shadow {
            y: 18.0,
            blur: 50.0,
            color: t.shadow.alpha(0.55),
            ..Shadow::default()
        })
        .child(
            col()
                .key(format!("{peek}.rows"))
                .w_full()
                .shrink()
                .min_h(0.0)
                .scroll(rune::ScrollSpec {
                    stick_bottom: false,
                })
                .children(rows),
        )
        .children(foot);
    let (align, label) = match anchor {
        Anchor::Waiting => (Align::Start, "Agents"),
        Anchor::Runs => (Align::End, "Automation runs"),
    };
    Some(
        col()
            .key(peek)
            .role("group")
            .label(label)
            .shrink()
            .min_h(0.0)
            .pt(GAP)
            .child(panel)
            .on_hover_change(|state: &mut State, inside, _: &mut Tx| {
                state.window.bar_peek.peek = inside;
            })
            .anchored_fit(anchor.key(), Side::Bottom, align, 0.0),
    )
}

fn peek_row(peek: &str, r: &Row, t: &Tokens) -> El {
    let key = format!("{peek}.{}", r.key);
    let face = crate::palette::row_face(r, key, t);
    if !r.selectable() {
        return face;
    }
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
