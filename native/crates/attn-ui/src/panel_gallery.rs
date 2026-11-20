//! The gallery's stories for the blocks of panels and drawers: one story per block, a variant per
//! state.

use rune::{Cx, El, KeyEvent, Playground, Story, TextEdit, Tx, col, el};
use rune_kit::TextInput;

use crate::Tokens;
use crate::blocks::inspector::{aside, lines, value};
use crate::blocks::{
    Actions, Block, Facts, Field, Inline, Inspector, List, Notice, Prose, Refusal, Sheet,
};
use crate::colors::scheme_for;
use crate::system::{
    AskChip, Beside, Button, FieldState, IconSize, ListRow, Look, Reveal, Size, Tone, Type,
    ask_mark, icon_button, line_box, press_area, switch, tag, words,
};

/// What a panel story shows: the state its variant sets.
#[derive(Default)]
pub struct Panels {
    pub rings: bool,
    pub long: bool,
    pub size: Drawer,
    pub loading: bool,
    pub failed: bool,
    pub pinned: bool,
    pub empty: bool,
    pub bare: bool,
    pub keys: bool,
    pub on: bool,
    pub several: bool,
    pub line: TextEdit,
}

/// How much room the drawer the story draws has.
#[derive(Default, Clone, Copy)]
pub enum Drawer {
    /// The automations drawer's widest, 640, in a window of a common size.
    #[default]
    Wide,
    /// A drawer's narrowest, 420, in the smallest window.
    Narrow,
    /// The smallest window at 150% text: 800 by 600 shows 533 by 400 points, and a drawer keeps
    /// its narrowest, 420.
    Smallest,
}

impl Drawer {
    fn size(self) -> (f32, f32) {
        match self {
            Drawer::Wide => (640.0, 760.0),
            Drawer::Narrow => (420.0, 760.0),
            Drawer::Smallest => (420.0, 400.0),
        }
    }
}

/// Every panel story, by name, with the keys of what it draws: what the state sheets and the
/// rule tests read.
pub const PANEL_SUBJECTS: &[(&str, &[&str])] = &[
    ("Panels/Sheet", &["sheet"]),
    ("Panels/List", &["list.sheet"]),
    ("Panels/Inspector", &["inspector"]),
    ("Panels/Ask chip", &["askchip.stage"]),
];

/// The variants each panel story has beyond the common ones, as the state sheets draw them.
pub const PANEL_VARIANTS: &[(&str, &[&str])] = &[
    (
        "Panels/Sheet",
        &["Loading", "Failed", "Notice", "Smallest notice"],
    ),
    ("Panels/List", &["Empty", "Failed", "Bare", "Keys"]),
    ("Panels/Inspector", &["Empty"]),
    ("Panels/Ask chip", &["Several"]),
];

/// Room for the list's three rows, the Failed one with its error, so its sheet shows the rows and
/// little else.
const LIST_HEIGHT: f32 = 480.0;

const LONG_TITLE: &str = "Review every pull request the release train touches before it leaves";
const LONG_LINE: &str = "Scheduled — 0 9 * * 1-5 (Europe/Stockholm) · /Users/someone/projects/a-very-long-worktree-basename--with-its-branch";
const LONG_CONFLICT: &str = "This automation changed on disk since you opened it: another window or an agent saved it while you were editing here";
const LONG_ERROR: &str = "automation_runs_get failed: the daemon could not read the run history because the database is locked by another writer; try again in a moment";

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground
        .story(
            common(Story::new("Panels/Sheet", Panels::default, sheet))
                .variant("Loading", |s| s.loading = true)
                .variant("Failed", |s| s.failed = true)
                .variant("Notice", |s| s.pinned = true)
                .variant("Smallest notice", |s| {
                    s.size = Drawer::Smallest;
                    s.long = true;
                    s.pinned = true;
                }),
        )
        .story(
            common(Story::new("Panels/Inspector", Panels::default, inspector))
                .variant("Empty", |s| s.empty = true),
        )
        .story(
            common(Story::new(
                "Panels/Ask chip",
                Panels::default,
                ask_chip_story,
            ))
            .variant("Several", |s| s.several = true),
        )
        .story(
            common(Story::new("Panels/List", Panels::default, list))
                .variant("Empty", |s| s.empty = true)
                .variant("Failed", |s| s.failed = true)
                .variant("Bare", |s| s.bare = true)
                .variant("Keys", |s| s.keys = true),
        )
}

fn common(story: Story<Panels>) -> Story<Panels> {
    story
        .variant("Rest", |_| {})
        .variant("Focus", |s| s.rings = true)
        .variant("Long", |s| s.long = true)
        .variant("Narrow", |s| s.size = Drawer::Narrow)
        .variant("Narrow long", |s| {
            s.size = Drawer::Narrow;
            s.long = true;
        })
        .variant("Smallest long", |s| {
            s.size = Drawer::Smallest;
            s.long = true;
        })
}

fn tokens(cx: &Cx<Panels>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

fn noop(_: &mut Panels, _: &mut Tx) {}

/// `panel` in a drawer of the story's size at the window's right edge, as the dock lays it.
fn stage(s: &Panels, cx: &mut Cx<Panels>, t: &Tokens, tallest: f32, panel: El) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let (w, h) = s.size.size();
    let h = h.min(tallest);
    el().w_full().h_full().fill(t.bg_app).child(
        col().w_full().h_full().items_end().child(
            col()
                .w(w)
                .h(h)
                .fill(t.bg_panel)
                .border_sides([0.0, 0.0, 0.0, 1.0], t.border)
                .child(panel),
        ),
    )
}

fn close(t: &Tokens) -> El {
    icon_button("sheet.close", "Close", crate::glyph::TIMES, IconSize::Large)
        .on_press_tx(noop)
        .build(t)
        .into()
}

fn sheet(s: &Panels, cx: &mut Cx<Panels>) -> El {
    let t = tokens(cx);
    let title = if s.long {
        LONG_TITLE
    } else {
        "Edit automation"
    };
    let sheet = Sheet::new("sheet", title)
        .aside(switch(
            "sheet.enabled",
            "Enabled",
            s.on,
            Beside::Nothing,
            false,
            &t,
            |s: &mut Panels, _: &mut Tx| s.on = !s.on,
        ))
        .close(close(&t))
        .notices(s.pinned.then(|| {
            Notice::warn(
                "sheet.conflict",
                if s.long {
                    LONG_CONFLICT
                } else {
                    "This automation changed since you opened it."
                },
            )
            .action(
                Inline::loose()
                    .child(El::from(
                        Button::new("Reload")
                            .size(Size::Compact)
                            .look(Look::Plain)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("sheet.reload"),
                    ))
                    .child(El::from(
                        Button::new("Overwrite anyway")
                            .size(Size::Compact)
                            .look(Look::Plain)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("sheet.overwrite"),
                    )),
            )
            .build(&t)
        }));
    let sheet = if s.loading {
        sheet.push(
            Prose::lead("sheet.loading", "Loading…")
                .quiet(true)
                .build(&t),
        )
    } else if s.failed {
        sheet.push(
            Notice::error(
                "sheet.load",
                if s.long {
                    LONG_ERROR
                } else {
                    "The automation could not be read."
                },
            )
            .action(
                Button::new("Retry")
                    .on_press_tx(noop)
                    .build(&t)
                    .key("sheet.retry"),
            )
            .build(&t),
        )
    } else {
        let name = TextInput::new(&s.line)
            .key("sheet.name")
            .label("Name")
            .placeholder("Automation name")
            .on_edit(|s: &mut Panels| &mut s.line);
        let mut fields: Vec<Block> = vec![
            Field::new("sheet.name", "Name")
                .control(line_box(name, FieldState::Ready, &t))
                .build(&t),
        ];
        fields.extend((0..6).map(|i| {
            Field::new(format!("sheet.field{i}"), format!("Field {}", i + 1))
                .value(if s.long { LONG_LINE } else { "A value" })
                .hint("What this field changes.")
                .build(&t)
        }));
        sheet
            .push_all(fields)
            .footer(
                Notice::error(
                    "sheet.save",
                    if s.long { LONG_ERROR } else { "Saving failed." },
                )
                .build(&t),
            )
            .footer(
                Actions::new("sheet.actions.block")
                    .push(
                        Button::new("Delete")
                            .look(Look::Danger)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("sheet.delete"),
                    )
                    .then_end()
                    .push(
                        Button::new("Cancel")
                            .on_press_tx(noop)
                            .build(&t)
                            .key("sheet.cancel"),
                    )
                    .push(
                        Button::new("Save")
                            .look(Look::Primary)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("sheet.save-button"),
                    )
                    .build(),
            )
    };
    let panel = sheet.build(&t);
    stage(s, cx, &t, f32::INFINITY, panel)
}

fn row(s: &Panels, t: &Tokens, i: usize, name: &str, selected: bool) -> (ListRow, Vec<Block>) {
    let title = if s.long { LONG_TITLE } else { name };
    let controls: Vec<El> = vec![
        switch(
            format!("list.toggle{i}"),
            "Enabled",
            true,
            Beside::Nothing,
            false,
            t,
            noop,
        )
        .into(),
        Button::new("Run now")
            .size(Size::Compact)
            .on_press_tx(noop)
            .build(t)
            .key(format!("list.run{i}"))
            .into(),
        Button::new("Edit")
            .size(Size::Compact)
            .look(Look::Quiet)
            .on_press_tx(noop)
            .build(t)
            .key(format!("list.edit{i}"))
            .into(),
    ];
    let mut row = ListRow::new(format!("list.row{i}"), title)
        .title(title.to_owned())
        .subtitle(
            if s.long {
                LONG_LINE
            } else {
                "Manual · Desktop 2"
            },
            t,
        )
        .selected(selected)
        .select(format!("list.select{i}"), title, noop)
        .controls(controls, Reveal::Always);
    let mut under = Vec::new();
    if s.failed && i == 1 {
        row = row.tag(tag(format!("list.failed{i}"), "Failed", Tone::Bad, t));
        under.push(Block::custom(
            format!("list.error{i}.block"),
            Refusal::new(format!("list.error{i}"), LONG_ERROR).build(t),
        ));
        under.push(Block::custom(
            format!("list.guard{i}.block"),
            words(
                "Guard: failed · checked 2 minutes ago · 3 checks since the last run",
                Type::Small,
                t.text_tertiary,
            )
            .key(format!("list.note{i}"))
            .wrap(),
        ));
    }
    (row, under)
}

fn list(s: &Panels, cx: &mut Cx<Panels>) -> El {
    let t = tokens(cx);
    let rows = if s.empty {
        Vec::new()
    } else {
        vec![
            row(s, &t, 0, "Review the queue", false),
            row(s, &t, 1, "Morning digest", true),
            row(s, &t, 2, "Weekly release notes", false),
        ]
    };
    let list = rows
        .into_iter()
        .fold(
            if s.bare {
                List::new("list", "Automations")
            } else {
                List::new("list", "Automations").card()
            },
            |list, (row, under)| list.row_with(row, under),
        )
        .empty(
            "list.empty",
            "No automations yet.",
            Some(
                Button::new("New automation")
                    .size(Size::Compact)
                    .on_press_tx(noop)
                    .build(&t)
                    .key("list.new")
                    .into(),
            ),
        );
    let list = if s.keys {
        Block::custom(
            "list.column",
            list.on_key(|_: &mut Panels, _: &KeyEvent, _: &mut Tx| false)
                .scrolling(&t),
        )
    } else {
        list.build(&t)
    };
    let panel = Sheet::new("list.sheet", "Automations")
        .close(close(&t))
        .push(list)
        .build(&t);
    stage(s, cx, &t, LIST_HEIGHT, panel)
}

fn inspector(s: &Panels, cx: &mut Cx<Panels>) -> El {
    let t = tokens(cx);
    let shown = if s.empty {
        Inspector::new("inspector", "Nothing selected")
            .push(aside("Pick a row to read it here.", &t))
            .build(&t)
    } else {
        let path = if s.long {
            "/home/someone/projects/InternationalEngineeringObservability--a-very-long-worktree-basename"
        } else {
            "~/projects/reviewer"
        };
        Inspector::new("inspector", if s.long { LONG_TITLE } else { "reviewer" })
            .kicker(words(
                "waiting_input · claude",
                Type::Small,
                t.text_tertiary,
            ))
            .block(
                Facts::new("inspector.facts")
                    .fact("inspector.profile", "Profile", "Work")
                    .item(
                        "inspector.directory",
                        "Directory",
                        lines([value(path, &t), aside("Clean, on main", &t)]),
                    )
                    .control(
                        "inspector.seed",
                        "Seed",
                        press_area(
                            "inspector.seed.open",
                            "Colors in one place",
                            false,
                            false,
                            words("Colors in one place", Type::Body, t.accent),
                            &t,
                            noop,
                        )
                        .into(),
                    )
                    .fact(
                        "inspector.last-seen",
                        "Last seen",
                        "Tue, Nov 17 at 09:58 (2 hours ago)",
                    )
                    .build(&t),
            )
            .push(
                Button::new("Focus")
                    .size(Size::Compact)
                    .look(Look::Primary)
                    .on_press_tx(noop)
                    .build(&t)
                    .key("inspector.focus"),
            )
            .build(&t)
    };
    stage(
        s,
        cx,
        &t,
        f32::INFINITY,
        rune::row().w_full().h_full().child(shown),
    )
}

const ASK_SUBJECT: &str = "Cache store";
const LONG_ASK_SUBJECT: &str =
    "Which store should keep the cache once deploys stop emptying it on every release";
/// The width a tile header leaves a cut subject, as the desktop gives it.
const ASK_SUBJECT_CUT: f32 = 120.0;

/// The ask chip a variant draws: its subject whole in a wide header, cut in a narrow one, and
/// its mark alone in the smallest window.
pub fn ask_chip(s: &Panels) -> AskChip {
    let subject = if s.long {
        LONG_ASK_SUBJECT
    } else {
        ASK_SUBJECT
    };
    let more = if s.several { 2 } else { 0 };
    let chip = AskChip::new(format!("Open ask: {subject}"));
    match s.size {
        Drawer::Wide => chip.subject(subject, None).more(more),
        Drawer::Narrow => chip.subject(subject, Some(ASK_SUBJECT_CUT)).more(more),
        Drawer::Smallest => chip,
    }
}

/// The chip in a tile header's line, and the mark an agent's row carries for one ask and for
/// three.
fn ask_chip_story(s: &Panels, cx: &mut Cx<Panels>) -> El {
    let t = tokens(cx);
    if s.rings {
        cx.force_focus_rings();
    }
    let chip = ask_chip(s).build(&t).key("askchip").on_press_tx(noop);
    let stage = Inline::loose()
        .key("askchip.stage")
        .child(chip)
        .child(ask_mark(1, &t).key("askchip.mark"))
        .child(ask_mark(if s.several { 3 } else { 12 }, &t).key("askchip.marks"));
    el().w_full().h_full().fill(t.bg_app).child(
        col()
            .w(s.size.size().0)
            .h_full()
            .justify_center()
            .items_center()
            .fill(t.bg_panel)
            .child(stage),
    )
}
