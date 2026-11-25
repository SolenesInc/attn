//! The gallery's stories for the blocks the desktop's chrome is built from: the pieces of a tile
//! header's second line, the shelf's entries, the overview's grid of desktops in miniature, and a
//! terminal's screen read as text. One story per block, a variant per state.

use rune::{Cx, El, Playground, Story, Tx, col, el, row};

use crate::Tokens;
use crate::blocks::{
    Card, CountSide, Grid, HeaderPiece, Inline, MemberState, Miniature, Place, Screen, Shelf,
    ShelfCount, ShelfEntry, ShelfMember, Standing,
};
use crate::chrome_gallery::ChromeSheet;
use crate::colors::scheme_for;
use crate::icon;
use crate::system::{Space, door};
use crate::system::{Type, words};

/// What a desk story shows: the state its variant sets.
#[derive(Default)]
pub struct Desk {
    pub rings: bool,
    pub long: bool,
    pub presses: u32,
}

/// The longest words a piece meets: a seed titled at length and a role named in full.
const LONG_SEED: &str = "Chrome: the overview, shelf, peek and seed chips draw through the blocks";
const LONG_ROLE: &str = "Orchestrator of the design system";

const PRESSABLE: &[(&str, &str, &str, &str)] = &[
    ("Rest", "Rest", "", ""),
    ("Hover", "Rest", "hover", ""),
    ("Pressed", "Rest", "press", ""),
    ("Focus", "Rest", "tab", ""),
    ("Long", "Long", "", ""),
    ("Long, focus", "Long", "tab", ""),
];

pub const DESK_SHEETS: &[ChromeSheet] = &[
    ChromeSheet {
        name: "desk-header-piece",
        story: "Desk/Header piece",
        keys: &[
            "piece.seed",
            "piece.plot",
            "piece.icon",
            "piece.role",
            "piece.line",
        ],
        columns: PRESSABLE,
    },
    ChromeSheet {
        name: "desk-shelf",
        story: "Desk/Shelf",
        keys: &[
            "shelf.entry",
            "shelf.entry.stack",
            "shelf.entry.on_screen",
            "shelf.entry.current",
            "shelf.entry.asks",
            "shelf.arrived",
            "shelf.count",
            "shelf.count.needs",
        ],
        columns: PRESSABLE,
    },
    ChromeSheet {
        name: "desk-overview",
        story: "Desk/Overview",
        keys: &["card.current", "card.empty", "card.one"],
        columns: PRESSABLE,
    },
    ChromeSheet {
        name: "desk-screen",
        story: "Desk/Screen",
        keys: &["screen.frame"],
        columns: &[("Rest", "Rest", "", ""), ("Long", "Long", "", "")],
    },
];

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground
        .story(states(Story::new(
            "Desk/Header piece",
            Desk::default,
            header_pieces,
        )))
        .story(states(Story::new("Desk/Shelf", Desk::default, shelf)))
        .story(states(Story::new("Desk/Overview", Desk::default, overview)))
        .story(states(Story::new("Desk/Screen", Desk::default, screen)))
}

fn states(story: Story<Desk>) -> Story<Desk> {
    story
        .variant("Rest", |_| {})
        .variant("Long", |s| s.long = true)
}

/// The longest names the shelf and the overview meet: a session named after its worktree's
/// folder, and a desktop named by a person.
const LONG_AGENT: &str = "attn--ds-page-blocks-delegation-appearance-landing-two";
const LONG_DESKTOP: &str = "Design system review with the Orchestrator and five Builders";
/// The shelf of a row that has gone off screen at both ends: a count, a column of one, a stack
/// whose second tile waits, the two columns on screen with a stack in use, a document no one has
/// read and an agent that asks, and a count with an approval in it.
fn shelf(s: &Desk, cx: &mut Cx<Desk>) -> El {
    let t = tokens(cx);
    let name = if s.long { LONG_AGENT } else { "reviewer" };
    let approval = member("docs", &icon::AGENT, Some(MemberState::Approval));
    let arrived = entry(
        &[(name, &icon::MARKDOWN, Some(MemberState::Waiting))],
        0,
        Place::OffScreen,
        0,
    );
    let arrived_name = arrived.name_width();
    let entries = [
        (
            "shelf.entry",
            entry(
                &[(name, &icon::AGENT, Some(MemberState::Idle))],
                0,
                Place::OffScreen,
                0,
            ),
        ),
        (
            "shelf.entry.stack",
            entry(
                &[
                    ("builder", &icon::AGENT, Some(MemberState::Working)),
                    (name, &icon::AGENT, Some(MemberState::Waiting)),
                    ("shell", &icon::TERMINAL, None),
                ],
                0,
                Place::OffScreen,
                0,
            ),
        ),
        (
            "shelf.entry.on_screen",
            entry(&[("plan.md", &icon::MARKDOWN, None)], 0, Place::OnScreen, 0),
        ),
        (
            "shelf.entry.current",
            entry(
                &[
                    ("builder", &icon::AGENT, Some(MemberState::Idle)),
                    (name, &icon::AGENT, Some(MemberState::Working)),
                    ("docs", &icon::AGENT, Some(MemberState::Approval)),
                    ("shell", &icon::TERMINAL, None),
                ],
                1,
                Place::Current,
                0,
            ),
        ),
        (
            "shelf.entry.asks",
            entry(
                &[("scout", &icon::AGENT, Some(MemberState::Waiting))],
                0,
                Place::OffScreen,
                1,
            ),
        ),
    ];
    let before = count(CountSide::Before, 2, vec![]);
    let after = count(CountSide::After, 3, vec![approval]);
    let mut x = Shelf::START + before.width() + Shelf::gap();
    let mut framed = (x, 0.0);
    for (key, entry) in &entries {
        if *key == "shelf.entry.on_screen" {
            framed.0 = x;
        }
        x += entry.width(entry.name_width());
        if *key == "shelf.entry.current" {
            framed.1 = x;
        }
        x += Shelf::gap();
    }
    let reach = Shelf::FRAME_REACH;
    let (frame_x, frame_width) = (framed.0 - reach, framed.1 - framed.0 + 2.0 * reach);
    let row = Shelf::row("shelf.row")
        .child(Shelf::frame(frame_x, frame_width, &t))
        .child(Shelf::frame_edge(frame_x, frame_width, &t))
        .child(
            before
                .build("shelf.count", "2 more this way", false, &t)
                .on_press_tx(press),
        )
        .children(entries.iter().map(|(key, entry)| {
            entry
                .build(key, &entry.members()[0].name, entry.name_width(), &t)
                .on_press_tx(press)
        }))
        .child(
            arrived
                .build("shelf.arrived", name, arrived_name, &t)
                .child(Shelf::flash("shelf.arrived.flash", &t))
                .on_press_tx(press),
        )
        .child(
            after
                .build("shelf.count.needs", "3 more this way", false, &t)
                .on_press_tx(press),
        );
    stage(s, cx, &t, vec![row])
}

fn member(name: &str, icon: &'static rune::Icon, state: Option<MemberState>) -> ShelfMember {
    ShelfMember {
        name: name.to_owned(),
        icon: icon.into(),
        state,
    }
}

fn count(side: CountSide, columns: usize, marks: Vec<ShelfMember>) -> ShelfCount {
    ShelfCount {
        side,
        columns,
        needs_user: !marks.is_empty(),
        marks,
    }
}

fn entry(
    members: &[(&str, &'static rune::Icon, Option<MemberState>)],
    shown: usize,
    place: Place,
    asking: usize,
) -> ShelfEntry {
    let members = members
        .iter()
        .map(|(name, icon, state)| member(name, icon, *state))
        .collect();
    ShelfEntry::new(members, shown, place, asking)
}

/// Three desktops in miniature on the overview's grid: the current one picked, an empty one, and
/// a desktop of four tiles.
fn overview(s: &Desk, cx: &mut Cx<Desk>) -> El {
    let t = tokens(cx);
    let name = if s.long { LONG_DESKTOP } else { "work" };
    let tile = |label: &str, on: bool| Miniature::tile(label.to_owned(), on, &t);
    let split = |a: El, b: El, across: bool| {
        if across { row() } else { col() }
            .w_full()
            .h_full()
            .min_w(0.0)
            .min_h(0.0)
            .child(a.grow(1.0).w(0.0).min_w(0.0))
            .child(b.grow(1.0).w(0.0).min_w(0.0))
    };
    let four = split(
        tile(if s.long { LONG_AGENT } else { "reviewer" }, true),
        split(
            tile("shell", false),
            split(tile("plan.md", false), tile("scout", false), false),
            false,
        ),
        true,
    );
    let card = |key: &str, title: &str, layout: Option<El>, standing: Standing| {
        Card::new(key)
            .control("button", title)
            .standing(standing)
            .push(
                Inline::joined()
                    .w_full()
                    .child(words("⌘1", Type::Mono, t.text_secondary))
                    .child(
                        words(title.to_owned(), Type::Body, t.text_primary)
                            .single_line()
                            .truncate()
                            .shrink(),
                    ),
            )
            .push(Miniature::ground(layout, 16.0 / 9.0, &t))
            .build(&t)
            .on_press_tx(press)
            .min_w(0.0)
    };
    let grid = Grid::cards(
        "grid",
        3,
        [
            card("card.current", name, Some(four), Standing::Picked),
            card("card.empty", "notes", None, Standing::Resting),
            card(
                "card.one",
                "review",
                Some(tile("reviewer", false)),
                Standing::Resting,
            ),
        ],
    );
    stage(s, cx, &t, vec![grid.w(OVERVIEW_W)])
}

/// The overview's grid at the width the overview's dialog gives it.
const OVERVIEW_W: f32 = 572.0;

/// A terminal's screen: a prompt and its output, and with Long a line wider than the screen.
fn screen(s: &Desk, cx: &mut Cx<Desk>) -> El {
    let t = tokens(cx);
    let mut shown = String::from(
        "$ cargo test -p attn-ui\n   Compiling attn-ui v0.1.0\n    Finished test in 4.2s\n",
    );
    if s.long {
        shown.push_str(&"running 128 tests ".repeat(12));
    }
    let frame = col()
        .key("screen.frame")
        .w(420.0)
        .h(160.0)
        .fill(t.bg_app)
        .child(Screen::showing("screen", shown, &t));
    stage(s, cx, &t, vec![frame])
}

/// Each of `items` on the panel's surface, one under another.
fn stage(s: &Desk, cx: &mut Cx<Desk>, t: &Tokens, items: Vec<El>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    el().key("stage.presses")
        .label(s.presses.to_string())
        .w_full()
        .h_full()
        .fill(t.bg_panel)
        .child(door::inset(col(), Space::S24, Space::S24).child(
            door::gap(col(), Space::S12).children(items.into_iter().map(|e| e.self_start())),
        ))
}

fn tokens(cx: &Cx<Desk>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

fn press(s: &mut Desk, _: &mut Tx) {
    s.presses += 1;
}

/// The pieces a tile header's second line holds: a seed the agent tends with its state, a plot
/// with its counts, a seed as its mark alone, a role, and a line of words with a link.
pub fn header_pieces_of(long: bool, t: &Tokens) -> Vec<(&'static str, HeaderPiece)> {
    let seed = if long {
        LONG_SEED
    } else {
        "Seed chips on blocks"
    };
    let role = if long { LONG_ROLE } else { "Builder" };
    vec![
        (
            "piece.seed",
            HeaderPiece::new()
                .mark(icon::seed("growing"), t.success)
                .words_keyed("piece.seed.label", seed, t.text_secondary, None)
                .state("Growing", t.success),
        ),
        (
            "piece.plot",
            HeaderPiece::new()
                .mark(icon::seed("growing"), t.text_secondary)
                .words_keyed("piece.plot.label", seed, t.text_secondary, Some(84.0))
                .count(icon::seed("harvested"), t.warning, "12", "12 harvested")
                .count(icon::seed("growing"), t.success, "3", "3 growing")
                .words("12/15", t.text_tertiary),
        ),
        (
            "piece.icon",
            HeaderPiece::new().mark(icon::seed("planted"), t.warning),
        ),
        (
            "piece.role",
            HeaderPiece::new().mark("B", t.accent).words(role, t.accent),
        ),
        (
            "piece.line",
            HeaderPiece::new()
                .words("⎇ PR", t.text_tertiary)
                .link(
                    "piece.line.link",
                    "Pull request details",
                    "attn #812 ↗",
                    t.text_secondary,
                    |e| e,
                )
                .words("checks running", t.warning),
        ),
    ]
}

fn header_pieces(s: &Desk, cx: &mut Cx<Desk>) -> El {
    let t = tokens(cx);
    let pieces = header_pieces_of(s.long, &t)
        .into_iter()
        .map(|(key, piece)| {
            if key == "piece.line" {
                piece.build(&t).key(key)
            } else {
                piece.button(key, key, &t).on_press_tx(press)
            }
        })
        .collect();
    stage(s, cx, &t, pieces)
}
