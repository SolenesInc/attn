//! The gallery's stories for what a desktop's tiles draw over and around their terminals: the
//! find and filter bars, the notices over a terminal, the strip under it, the review tabs and
//! the marks a drag leaves. One story each, a variant per state.

use rune::{Cx, El, Playground, Story, TextEdit, Tx, col, el};
use rune_kit::TextInput;

use crate::Tokens;
use crate::blocks::{
    Inline, Notice, Tabs, TerminalBar, drag_ghost, drop_target, terminal_notices, terminal_status,
};
use crate::colors::scheme_for;
use crate::system::{Button, IconSize, Look, Size, Space, door, icon_button};

/// What a tile story shows: the state its variant sets.
#[derive(Default)]
pub struct Tiles {
    pub on: bool,
    pub long: bool,
    pub empty: bool,
    pub smallest: bool,
    pub presses: u32,
    pub field: TextEdit,
}

/// The narrowest tile a terminal stands in: the smallest window, 800 by 600 at 150% text, split
/// in two side by side.
const SMALLEST: f32 = 533.0 / 2.0;
/// A story's terminal, as tall as a tile in the smallest window.
const TERMINAL_H: f32 = 260.0;
/// A tile at the default window's share of a row of two.
const WIDE: f32 = 620.0;

/// Every story of this module, by sheet: its name, its story, the keys of what it draws, its
/// variants in order (the first is its rest), and the key suffix hover and pressed aim at, or
/// `None` for a story that takes no pointer.
pub type TileSubject = (
    &'static str,
    &'static str,
    &'static [&'static str],
    &'static [&'static str],
    Option<&'static str>,
);

pub const TILE_SUBJECTS: &[TileSubject] = &[
    (
        "tile-find-bar",
        "Tiles/Find bar",
        &["story.tile"],
        &["Rest", "Focus", "Case on", "Long", "Smallest long"],
        Some(".find.case"),
    ),
    (
        "tile-filter",
        "Tiles/Filter",
        &["story.tile"],
        &["Rest", "Empty", "Long", "Smallest long"],
        Some(".filter.line.1"),
    ),
    (
        "tile-notices",
        "Tiles/Notices",
        &["story.tile"],
        &["Rest", "Focus", "Long", "Smallest long"],
        Some(".stale.reload"),
    ),
    (
        "tile-status",
        "Tiles/Status",
        &["story.tile"],
        &["Rest", "Long", "Smallest long"],
        None,
    ),
    (
        "tile-tabs",
        "Tiles/Tabs",
        &["story.tabs"],
        &["Rest", "Long", "Smallest long"],
        Some(".tab.1"),
    ),
    (
        "tile-drag",
        "Tiles/Drag",
        &["story.tile"],
        &["Rest", "Long"],
        None,
    ),
];

const LONG_COUNT: &str = "1,204 of 18,311";
const LONG_LINE: &str = "error[E0308]: mismatched types in attn--ds-chrome-tiles/native/crates/attn-app/src/desktop/view.rs:1325 expected `El`, found `Control`";
const LONG_PEER: &str = "“attn--ds-page-blocks-delegation-appearance-landing-two” on Design system review with the Orchestrator and five Builders";
const LONG_HOST: &str = "attn-linux-landing-runner-with-a-long-hostname";
const LONG_DOCUMENT: &str = "docs/internal/a-very-long-folder-name/trunk-based-development.md";

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    let common = |story: Story<Tiles>| {
        story
            .variant("Long", |s| s.long = true)
            .variant("Smallest long", |s| {
                s.long = true;
                s.smallest = true;
            })
    };
    playground
        .story(common(
            Story::new("Tiles/Find bar", Tiles::default, find_story)
                .variant("Rest", |_| {})
                .variant("Focus", |_| {})
                .variant("Case on", |s| s.on = true),
        ))
        .story(common(
            Story::new("Tiles/Filter", Tiles::default, filter_story)
                .variant("Rest", |_| {})
                .variant("Empty", |s| s.empty = true),
        ))
        .story(common(
            Story::new("Tiles/Notices", Tiles::default, notices_story)
                .variant("Rest", |_| {})
                .variant("Focus", |_| {}),
        ))
        .story(common(
            Story::new("Tiles/Status", Tiles::default, status_story).variant("Rest", |_| {}),
        ))
        .story(common(
            Story::new("Tiles/Tabs", Tiles::default, tabs_story).variant("Rest", |_| {}),
        ))
        .story(
            Story::new("Tiles/Drag", Tiles::default, drag_story)
                .variant("Rest", |_| {})
                .variant("Long", |s| s.long = true),
        )
}

fn tokens(cx: &Cx<Tiles>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

fn pressed(s: &mut Tiles, _: &mut Tx) {
    s.presses += 1;
}

/// A tile's terminal, as wide as the variant's tile, with what lies over it.
fn terminal(s: &Tiles, t: &Tokens, over: Vec<El>) -> El {
    let width = if s.smallest { SMALLEST } else { WIDE };
    let tile = el()
        .key("story.tile")
        .w(width)
        .h(TERMINAL_H)
        .fill(t.bg_tile)
        .border(1.0, t.border)
        .children(over);
    stage(t, tile)
}

fn stage(t: &Tokens, content: El) -> El {
    let content = door::frame(col().child(content), Space::S24, Space::S24, Space::S24);
    el().w_full()
        .h_full()
        .fill(t.bg_panel)
        .child(col().w_full().h_full().center().child(content))
}

fn find_story(s: &Tiles, cx: &mut Cx<Tiles>) -> El {
    let t = tokens(cx);
    let key = "story.tile";
    let field = TextInput::new(&s.field)
        .key(format!("{key}.find.field"))
        .label("Find")
        .placeholder("Find")
        .on_edit(|s: &mut Tiles| &mut s.field);
    let bar = TerminalBar::new(format!("{key}.find"))
        .field(field, 160.0, &t)
        .push(
            TerminalBar::button(format!("{key}.find.case"), "Match case", "Aa", s.on)
                .on_press_tx(pressed)
                .build(&t),
        )
        .count(
            format!("{key}.find.count"),
            if s.long { LONG_COUNT } else { "3 of 12" },
            &t,
        )
        .push(
            TerminalBar::button(
                format!("{key}.find.prev"),
                "Previous match",
                crate::glyph::PREVIOUS,
                false,
            )
            .on_press_tx(pressed)
            .build(&t),
        )
        .push(
            TerminalBar::button(
                format!("{key}.find.next"),
                "Next match",
                crate::glyph::NEXT,
                false,
            )
            .on_press_tx(pressed)
            .build(&t),
        )
        .push(
            TerminalBar::button(
                format!("{key}.find.close"),
                "Close find",
                crate::glyph::TIMES,
                false,
            )
            .on_press_tx(pressed)
            .build(&t),
        )
        .build(&t);
    terminal(s, &t, vec![TerminalBar::corner(vec![bar])])
}

fn filter_story(s: &Tiles, cx: &mut Cx<Tiles>) -> El {
    let t = tokens(cx);
    let key = "story.tile";
    let field = TextInput::new(&s.field)
        .key(format!("{key}.filter.field"))
        .label("Filter block output")
        .placeholder("Filter block output")
        .on_edit(|s: &mut Tiles| &mut s.field);
    let bar = TerminalBar::new(format!("{key}.filter"))
        .field(field, 200.0, &t)
        .push(
            TerminalBar::button(format!("{key}.filter.case"), "Match case", "Aa", false).build(&t),
        )
        .count(
            format!("{key}.filter.count"),
            if s.empty { "0 lines" } else { "3 lines" },
            &t,
        )
        .push(
            TerminalBar::button(
                format!("{key}.filter.close"),
                "Close filter",
                crate::glyph::TIMES,
                false,
            )
            .build(&t),
        )
        .build(&t);
    let wash = t.find.alpha(0.45);
    let lines = if s.empty {
        vec![TerminalBar::none(
            format!("{key}.filter.empty"),
            "No matching lines",
            &t,
        )]
    } else {
        let said = |i: usize| match (s.long, i) {
            (true, _) => LONG_LINE.to_owned(),
            (false, 0) => "error: could not compile `attn-app`".to_owned(),
            (false, 1) => "error[E0308]: mismatched types".to_owned(),
            (false, _) => "error: aborting due to 2 previous errors".to_owned(),
        };
        (0..3)
            .map(|i| {
                TerminalBar::line(
                    format!("{key}.filter.line.{i}"),
                    "Scroll to line",
                    said(i),
                    &[(0, 5)],
                    wash,
                    &t,
                    |s: &mut Tiles| s.presses += 1,
                )
            })
            .collect()
    };
    let list = TerminalBar::list(format!("{key}.filter.results"), 160.0, lines, &t);
    terminal(s, &t, vec![TerminalBar::corner(vec![bar, list])])
}

fn notices_story(s: &Tiles, cx: &mut Cx<Tiles>) -> El {
    let t = tokens(cx);
    let peer = if s.long {
        LONG_PEER
    } else {
        "“reviewer” on this desktop"
    };
    let shared = Notice::warn(
        "story.tile.shared",
        format!(
            "This conversation is also open in {peer}. Two agent processes on one conversation split its history. Close one of them."
        ),
    )
    .build(&t);
    let reload = Button::new("Reload session")
        .look(Look::Plain)
        .size(Size::Compact)
        .on_press_tx(pressed)
        .build(&t)
        .key("story.tile.stale.reload");
    let dismiss = icon_button(
        "story.tile.stale.dismiss",
        "Dismiss older-terminal notice",
        crate::glyph::TIMES,
        IconSize::Regular,
    )
    .on_press_tx(pressed)
    .build(&t);
    let stale = Notice::info(
        "story.tile.stale",
        "This session is running an older terminal. Reload it to bring it up to date. An update normally swaps this out on its own. This one could not.",
    )
    .action(Inline::joined().child(reload.into()).child(dismiss.into()))
    .build(&t);
    terminal(
        s,
        &t,
        vec![terminal_notices(
            "story.tile.notices",
            vec![shared, stale],
            TERMINAL_H - 4.0 * 18.0,
            &t,
        )],
    )
}

fn status_story(s: &Tiles, cx: &mut Cx<Tiles>) -> El {
    let t = tokens(cx);
    let host = if s.long { LONG_HOST } else { "ai-sandbox" };
    let said = vec![
        (
            "story.tile.reach".to_owned(),
            format!("Reconnecting to {host}…"),
        ),
        (
            "story.tile.screen-on".to_owned(),
            format!("The screen is on {host}."),
        ),
        (
            "story.tile.refused".to_owned(),
            "3 keys may not have been sent".to_owned(),
        ),
    ];
    let label = said
        .iter()
        .map(|(_, said)| said.as_str())
        .collect::<Vec<_>>()
        .join(" ");
    let strip = terminal_status("story.tile.status", label, said, &t);
    terminal(
        s,
        &t,
        vec![
            col()
                .pin(Some(0.0), None, Some(0.0), Some(0.0))
                .child(strip),
        ],
    )
}

fn tabs_story(s: &Tiles, cx: &mut Cx<Tiles>) -> El {
    let t = tokens(cx);
    let names = if s.long {
        [LONG_DOCUMENT, "plan.md", "glossary.md"]
    } else {
        ["plan.md", "review.md", "glossary.md"]
    };
    let tabs = names
        .iter()
        .enumerate()
        .fold(
            Tabs::new("story.tabs", "Open review documents"),
            |tabs, (i, name)| tabs.tab(format!("story.tabs.tab.{i}"), *name, i == 0, &t, pressed),
        )
        .build(&t);
    let width = if s.smallest { SMALLEST } else { WIDE };
    stage(&t, col().w(width).child(tabs))
}

fn drag_story(s: &Tiles, cx: &mut Cx<Tiles>) -> El {
    let t = tokens(cx);
    let name = if s.long {
        "attn--ds-page-blocks-delegation-appearance-landing-two"
    } else {
        "reviewer"
    };
    let target = drop_target("story.tile.target", "s1 right", &t)
        .absolute(310.0, 0.0)
        .w(310.0)
        .h(TERMINAL_H);
    let ghost = drag_ghost("story.tile.ghost", name, &t).absolute(120.0, 120.0);
    terminal(s, &t, vec![target, ghost])
}
