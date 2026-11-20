//! The gallery's stories for the blocks the desktop's chrome is built from: the pieces of a tile
//! header's second line, the shelf's chips, the overview's grid of desktops in miniature, and a
//! terminal's screen read as text. One story per block, a variant per state.

use rune::{Cx, El, Playground, Story, Tx, col, el, row};

use crate::Tokens;
use crate::blocks::{Card, Grid, HeaderPiece, Inline, Miniature, Screen, Shelf, Standing};
use crate::chrome_gallery::ChromeSheet;
use crate::colors::scheme_for;
use crate::icon;
use crate::system::{DotShape, DotSize, Type, dot, words};
use crate::system::{Space, door};

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
            "shelf.chip",
            "shelf.dot",
            "shelf.dot.current",
            "shelf.cap",
            "shelf.arrived",
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
/// The shelf's chip at its slot's width, as the title bar fits it.
pub const SHELF_CHIP: f32 = 112.0;

/// The shelf's chips: a column off screen with an agent and a tile, columns on screen as their
/// marks, the one on screen filled, a cap of three more, and a chip whose tile just arrived.
fn shelf(s: &Desk, cx: &mut Cx<Desk>) -> El {
    let t = tokens(cx);
    let name = if s.long { LONG_AGENT } else { "reviewer" };
    let marks = |n: usize| {
        Shelf::marks((0..n).map(|i| {
            dot(
                if i == 0 {
                    t.state_waiting
                } else {
                    t.text_secondary
                },
                if i == 0 {
                    DotShape::Filled
                } else {
                    DotShape::Square
                },
                DotSize::Small,
            )
        }))
    };
    let face = |label: &str| {
        Inline::line().w_full().child(marks(2)).child(
            words(label.to_owned(), Type::Small, t.text_secondary)
                .single_line()
                .truncate()
                .grow(1.0),
        )
    };
    let items = vec![
        Shelf::chip("shelf.chip", name, face(name), false, &t).w(SHELF_CHIP - Shelf::gap()),
        Shelf::chip("shelf.dot", "builder", marks(1), false, &t),
        Shelf::chip("shelf.dot.current", "scout", marks(3), true, &t),
        Shelf::chip(
            "shelf.cap",
            "3 more this way",
            words("3 ›", Type::Label, t.warning),
            false,
            &t,
        ),
        Shelf::chip("shelf.arrived", name, face(name), false, &t)
            .w(SHELF_CHIP - Shelf::gap())
            .child(Shelf::flash("shelf.arrived.flash", &t)),
    ]
    .into_iter()
    .map(|chip| chip.on_press_tx(press));
    stage(s, cx, &t, vec![Shelf::row("shelf.row").children(items)])
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
                Inline::line()
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
