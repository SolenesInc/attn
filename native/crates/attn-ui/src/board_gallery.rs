//! The gallery's story for a board of cards, such as Home, a variant per state.

use rune::{Cx, El, Playground, Story, Tx, col, el};

use crate::blocks::{Block, Board, BoardCard, Notice, Prose};
use crate::colors::scheme_for;
use crate::system::{
    Button, DotShape, DotSize, GroupHead, IconSize, ListRow, Look, Size, Type, dot, icon_button,
    words,
};

/// What a board story shows: the state its variant sets.
#[derive(Default)]
pub struct Boards {
    pub rings: bool,
    pub long: bool,
    pub width: Width,
}

/// How wide the board stands.
#[derive(Default, Clone, Copy)]
pub enum Width {
    /// Home in a 1280-point window beside the sidebar at its default width.
    #[default]
    Wide,
    /// Too narrow for two cards side by side.
    Narrow,
    /// Home in the smallest window, 800 x 600, at 150% text, beside the sidebar: 315 points, as
    /// the tour's picture of that window measures it.
    Smallest,
}

impl Width {
    fn points(self) -> f32 {
        match self {
            Width::Wide => 1040.0,
            Width::Narrow => 600.0,
            Width::Smallest => 315.0,
        }
    }
}

/// The board stories, by name, with the keys of what they draw and the variants they add to the
/// common ones: what the state sheets and the rule tests read.
pub const BOARD_SUBJECTS: &[(&str, &[&str], &[&str])] = &[("Blocks/Board", &["board"], &[])];

/// The least size a card of the story keeps, as Home's cards do.
pub const CARD_LEAST: (f32, f32) = (300.0, 200.0);

/// The longest name a row meets: a session named after its worktree's folder.
const LONG_NAME: &str =
    "build-and-release-platform-migrate-production-permissions-worktree-tokyo-region-primary";

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground.story(
        Story::new("Blocks/Board", Boards::default, board)
            .variant("Rest", |_| {})
            .variant("Focus", |s| s.rings = true)
            .variant("Long", |s| s.long = true)
            .variant("Narrow", |s| s.width = Width::Narrow)
            .variant("Narrow long", |s| {
                s.width = Width::Narrow;
                s.long = true;
            })
            .variant("Smallest long", |s| {
                s.width = Width::Smallest;
                s.long = true;
            }),
    )
}

fn noop(_: &mut Boards, _: &mut Tx) {}

fn board(s: &Boards, cx: &mut Cx<Boards>) -> El {
    let t = scheme_for(None).colors(cx.system_dark()).ui;
    if s.rings {
        cx.force_focus_rings();
    }
    let name = |short: &str| {
        if s.long {
            format!("{short}-{LONG_NAME}")
        } else {
            short.to_owned()
        }
    };
    let row = |key: &str, said: String, ink| {
        ListRow::new(format!("board.row.{key}"), said.clone())
            .lead(dot(ink, DotShape::Filled, DotSize::Small))
            .title(said)
            .aside(words("4m", Type::Mono, t.text_dimmed))
            .build(&t)
            .on_press_tx(noop)
    };
    let group = col()
        .key("board.group")
        .role("list")
        .label("Working")
        .w_full()
        .child(
            GroupHead::new("board.group.head", "Working")
                .section("Working")
                .aside(words("2", Type::Mono, t.text_tertiary))
                .heading()
                .build(&t),
        )
        .child(row("one", name("frontend"), t.state_working))
        .child(row("two", name("landing"), t.state_working));
    let sessions = BoardCard::new("board.sessions", "Sessions")
        .least(CARD_LEAST.0, CARD_LEAST.1)
        .action(
            Button::new("New")
                .look(Look::Plain)
                .size(Size::Compact)
                .on_press_tx(noop)
                .build(&t),
        )
        .push(Block::custom("board.group.slot", group))
        .build(&t);
    let empty = BoardCard::new("board.prs", "Pull Requests")
        .least(CARD_LEAST.0, CARD_LEAST.1)
        .action(
            icon_button(
                "board.refresh",
                "Refresh",
                &crate::icon::REFRESH,
                IconSize::Regular,
            )
            .on_press_tx(noop)
            .build(&t),
        )
        .push(Block::custom(
            "board.empty.slot",
            Prose::empty(
                "board.empty",
                if s.long {
                    "GitHub polling is off for instance tour. Start its daemon with \
                     ATTN_GITHUB_POLLING=on to poll with your gh credentials."
                } else {
                    "No PRs need attention"
                },
            )
            .el(&t),
        ))
        .build(&t);
    let page = Board::new("board", "Home")
        .brand(
            Some(el().w(64.0).h(64.0).fill(t.brand)),
            "attn",
            "attention hub",
            icon_button(
                "board.settings",
                "Settings",
                &crate::icon::SETTINGS,
                IconSize::Large,
            )
            .on_press_tx(noop)
            .build(&t),
        )
        .top(Notice::info("board.notice", "GitHub rate limited. Resuming in 4m 12s").build(&t))
        .card(sessions)
        .card(empty)
        .build(&t);
    el().w_full().h_full().fill(t.bg_app).child(
        col()
            .w_full()
            .h_full()
            .items_center()
            .child(col().w(s.width.points()).h_full().child(page)),
    )
}
