//! The gallery's stories for what floats over the window: the dialog, its blocks and controls,
//! the finder, the surfaces and the keycaps. One story each, a variant per state.

use rune::{Cx, El, Playground, Story, TextEdit, Tx, col, el};
use rune_kit::TextInput;

use crate::Frame;
use crate::Tokens;
use crate::blocks::{Actions, Block, Columns, Dialog, Field, Menu, Notice, Popover, Prose, Stack};
use crate::colors::scheme_for;
use crate::finder::{self, Hint};
use crate::keycap::{self, Keys};
use crate::system::{
    Button, Check, FieldState, IconSize, ListRow, Look, Segment, Segmented, Space, door, file_chip,
    icon_button, image_tile, line_box, pager,
};

/// What a story over the window shows: the state its variant sets.
#[derive(Default)]
pub struct Floating {
    pub case: Case,
    pub long: bool,
    pub smallest: bool,
    pub disabled: bool,
    pub chosen: usize,
    pub field: TextEdit,
}

/// Which dialog the dialog story draws.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Case {
    /// A question, Cancel and one action.
    #[default]
    Prompt,
    /// A field to fill.
    Form,
    /// The field's value was refused.
    Failed,
    /// A sheet with no actions, closed by its ×, its body in columns.
    Sheet,
    /// A finder that lists nothing.
    Empty,
}

/// The window the smallest variants draw in: 800 by 600 at 150% text.
const SMALLEST: (f32, f32) = (533.0, 400.0);

/// Every story of this module, by sheet: its name, its story, the keys of what it draws, its
/// variants in order (the first is its rest), and the key suffix hover and pressed aim at when
/// the pointer goes on a part of it, or `None` for a story that takes no pointer.
pub type FloatingSubject = (
    &'static str,
    &'static str,
    &'static [&'static str],
    &'static [&'static str],
    Option<&'static str>,
);

pub const FLOATING_SUBJECTS: &[FloatingSubject] = &[
    (
        "dialog",
        "Library/Dialog",
        &["story-dialog"],
        &[
            "Prompt",
            "Focus",
            "Form",
            "Failed",
            "Failed focus",
            "Sheet",
            "Sheet smallest",
            "Long",
            "Smallest long",
        ],
        Some(".confirm"),
    ),
    (
        "check",
        "Library/Check",
        &["check.off", "check.on", "check.long"],
        &["Rest", "Focus", "Disabled", "Smallest long"],
        Some(""),
    ),
    (
        "finder",
        "Library/Finder",
        &["finder.panel"],
        &["Rest", "Empty", "Long", "Smallest long"],
        None,
    ),
    (
        "surfaces",
        "Library/Surfaces",
        &[
            "surface.menu",
            "surface.panel",
            "surface.dialog",
            "surface.bar",
        ],
        &["Rest"],
        None,
    ),
    (
        "composer",
        "Library/Composer",
        &["composer"],
        &["Rest", "Filled", "Long", "Smallest long"],
        None,
    ),
    (
        "menu",
        "Library/Menu",
        &["story.menu"],
        &["Rest", "Second chosen", "Form", "Long", "Smallest long"],
        None,
    ),
    (
        "popover",
        "Library/Popover",
        &["story.popover"],
        &["Rest", "Failed", "Long", "Smallest long"],
        Some(".actions"),
    ),
    (
        "pager",
        "Library/Pager",
        &["pager"],
        &["Rest", "Second chosen"],
        None,
    ),
    (
        "attachments",
        "Library/Attachments",
        &["attachments"],
        &["Rest", "Long", "Smallest long"],
        None,
    ),
    (
        "keycaps",
        "Library/Keycaps",
        &[
            "keys.raised",
            "keys.outlined",
            "keys.ledged",
            "keys.alternatives",
        ],
        &["Rest"],
        None,
    ),
];

const LONG_TITLE: &str =
    "Restart shared Codex on attn-linux-landing-runner-with-a-long-hostname now?";
const LONG_LEAD: &str = "Every Codex tile in this profile goes offline until 0.142.0-alpha.7 is up, including the ones that run on a schedule and the ones another agent delegates to. What you typed stays.";
const LONG_MESSAGE: &str = "Ship the cache fix tonight, then check that the landing runner picked up the new pin before anyone starts the release, and tell me which tests it ran.";

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    let common = |story: Story<Floating>| {
        story
            .variant("Long", |s| s.long = true)
            .variant("Smallest long", |s| {
                s.long = true;
                s.smallest = true;
            })
    };
    playground
        .story(common(
            Story::new("Library/Dialog", Floating::default, dialog)
                .variant("Prompt", |_| {})
                .variant("Focus", |_| {})
                .variant("Form", |s| s.case = Case::Form)
                .variant("Failed", |s| s.case = Case::Failed)
                .variant("Sheet", |s| s.case = Case::Sheet)
                .variant("Sheet smallest", |s| {
                    s.case = Case::Sheet;
                    s.smallest = true;
                }),
        ))
        .story(common(
            Story::new("Library/Check", Floating::default, checks)
                .variant("Rest", |_| {})
                .variant("Focus", |_| {})
                .variant("Disabled", |s| s.disabled = true),
        ))
        .story(common(
            Story::new("Library/Finder", Floating::default, finder_story)
                .variant("Rest", |_| {})
                .variant("Empty", |s| s.case = Case::Empty),
        ))
        .story(Story::new("Library/Surfaces", Floating::default, surfaces).variant("Rest", |_| {}))
        .story(Story::new("Library/Keycaps", Floating::default, keycaps).variant("Rest", |_| {}))
        .story(common(
            Story::new("Library/Menu", Floating::default, menu_story)
                .variant("Rest", |_| {})
                .variant("Second chosen", |s| s.chosen = 1)
                .variant("Form", |s| s.case = Case::Form),
        ))
        .story(common(
            Story::new("Library/Popover", Floating::default, popover_story)
                .variant("Rest", |_| {})
                .variant("Failed", |s| s.case = Case::Failed),
        ))
        .story(
            Story::new("Library/Pager", Floating::default, pager_story)
                .variant("Rest", |_| {})
                .variant("Second chosen", |s| s.chosen = 1),
        )
        .story(common(
            Story::new("Library/Attachments", Floating::default, attachments_story)
                .variant("Rest", |_| {}),
        ))
        .story(common(
            Story::new("Library/Composer", Floating::default, composer_story)
                .variant("Rest", |_| {})
                .variant("Filled", |s| s.field = TextEdit::new("Ship the cache fix")),
        ))
}

fn tokens(cx: &Cx<Floating>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

fn noop(_: &mut Floating, _: &mut Tx) {}

/// The story's window: the whole stage, or the smallest window's points in its corner.
fn window(s: &Floating, t: &Tokens, content: El, layer: Vec<El>) -> El {
    let inner = el()
        .key("stage.window")
        .fill(t.bg_app)
        .child(content)
        .children(layer);
    let inner = if s.smallest {
        inner.w(SMALLEST.0).h(SMALLEST.1)
    } else {
        inner.w_full().h_full()
    };
    el().w_full().h_full().fill(t.bg_panel).child(inner)
}

/// Controls one under another, centred on the stage.
fn column(s: &Floating, t: &Tokens, children: Vec<El>) -> El {
    let width = if s.smallest { SMALLEST.0 } else { 560.0 };
    let content = door::frame(
        door::gap(col().w(width), Space::S24).children(children),
        Space::S24,
        Space::S24,
        Space::S24,
    );
    el().w_full()
        .h_full()
        .fill(t.bg_panel)
        .child(col().w_full().h_full().center().child(content))
}

fn dialog(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let (title, lead) = if s.long {
        (LONG_TITLE, LONG_LEAD)
    } else {
        (
            "Restart shared Codex now?",
            "Every Codex tile in this profile goes offline until 0.142.0 is up. What you typed stays.",
        )
    };
    let key = "story";
    let mut dialog = Dialog::new(key, title)
        .lead(lead)
        .on_close(|_: &mut Floating| {});
    dialog = match s.case {
        Case::Form | Case::Failed => dialog.push(
            Field::new("story.name", "Name")
                .control(line_box(
                    TextInput::new(&s.field)
                        .key("story.name.input")
                        .label("Name")
                        .placeholder("Desktop 3")
                        .on_edit(|s: &mut Floating| &mut s.field),
                    FieldState::failed_if(s.case == Case::Failed),
                    &t,
                ))
                .hint("On ⌃3.")
                .error((s.case == Case::Failed).then(|| {
                    "A desktop with this name is already open on this machine.".to_owned()
                }))
                .build(&t),
        ),
        Case::Sheet => dialog.size(crate::blocks::DialogSize::Sheet).push(
            Columns::new("story.columns")
                .column(vec![sheet_items("Sessions", s, &t)])
                .column(vec![sheet_items("Panes", s, &t)])
                .build(),
        ),
        Case::Prompt | Case::Empty => dialog,
    };
    if s.case != Case::Sheet {
        dialog = dialog
            .push_all(s.long.then(|| {
                Notice::error(
                    "story.error",
                    "The daemon refused: attn-linux-landing-runner is unreachable over SSH (exit 255).",
                )
                .build(&t)
            }))
            .actions(
                Actions::new("story-dialog.actions")
                    .then_end()
                    .push(Button::new("Cancel").build(&t).key("story-dialog.cancel"))
                    .push(
                        Button::new(if s.long { "Restart every Codex tile now" } else { "Restart now" })
                            .look(Look::Primary)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("story-dialog.confirm"),
                    ),
            );
    } else {
        dialog =
            dialog.push(Prose::lead("story.prose", "Press a shortcut's keys to run it.").build(&t));
    }
    let mut layer = Vec::new();
    dialog.build(&t, &mut layer);
    window(s, &t, el(), layer)
}

fn sheet_items(title: &str, s: &Floating, t: &Tokens) -> crate::blocks::Block {
    let name = if s.long {
        "Open the new-session dialog on the desktop the pointer is over"
    } else {
        "New session"
    };
    Block::custom(
        format!("story.{title}.block"),
        Stack::tight()
            .key(format!("story.{title}"))
            .w_full()
            .child(
                crate::system::GroupHead::new(format!("story.{title}.heading"), title)
                    .section(title)
                    .heading()
                    .build(t),
            )
            .child(
                ListRow::new(format!("story.{title}.a"), name)
                    .still()
                    .title(name)
                    .aside(Keys::raised(&["⌘", "N"]).build(t))
                    .build(t),
            )
            .child(
                ListRow::new(format!("story.{title}.b"), "Close")
                    .still()
                    .title("Close")
                    .subtitle("Asks first when the tile runs an agent.", t)
                    .aside(Keys::raised(&["⌘", "W"]).build(t))
                    .build(t),
            ),
    )
}

fn checks(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let check = |key: &str, name: &str, note: &str, on: bool| -> El {
        Check::new(key, name)
            .note(note)
            .checked(on)
            .disabled(s.disabled)
            .build(&t, noop)
            .into()
    };
    let long_name = if s.long {
        "Agent p2 in worktree attn--ds-dialogs-palette-with-a-long-branch-name"
    } else {
        "Agent p2"
    };
    column(
        s,
        &t,
        vec![
            check("check.off", "Agent p1", "waiting_input agent", false),
            check("check.on", "Agent a", "working agent", true),
            check(
                "check.long",
                long_name,
                "pending_approval agent, not currently mounted",
                true,
            ),
        ],
    )
}

fn finder_story(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let input = finder::field(
        TextInput::new(&s.field)
            .key("finder.input")
            .plain()
            .width(None)
            .label("Search")
            .placeholder("Open a markdown file…")
            .on_edit(|s: &mut Floating| &mut s.field),
    )
    .build(crate::kit(&t));
    let list = finder::list("finder.list");
    let list = if s.case == Case::Empty {
        list.child(finder::empty(
            &t,
            "finder.empty",
            "No markdown files match “plna”",
        ))
    } else {
        let path = if s.long {
            "~/projects/victor/attn--ds-dialogs-palette/docs/internal/a-very-long-folder-name/plan.md"
        } else {
            "~/projects/victor/attn/docs/plan.md"
        };
        list.children((0..3).map(|i| {
            finder::path_option(
                &t,
                format!("finder.option.{i}"),
                ["plan.md", "glossary.md", "native-app.md"][i],
                path,
                i == 0,
            )
        }))
    };
    let footer = finder::footer(
        &t,
        "finder",
        vec![
            Hint::keys("", "↑↓", " to move"),
            Hint::keys("", "↵", " to open"),
            Hint::keys("", "esc", " to close"),
        ],
    );
    let panel = finder::panel(
        &t,
        col().key("finder.panel").w_full(),
        finder::input_row(None, input, None),
        list,
        Some(footer),
    );
    window(
        s,
        &t,
        el(),
        vec![finder::layer(
            &t,
            "finder.layer",
            panel,
            finder::FinderSize::Narrow,
        )],
    )
}

fn surfaces(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let surface = |key: &str, frame: Frame| -> El { frame.draw(el().key(key).w(200.0).h(80.0)) };
    column(
        s,
        &t,
        vec![
            surface("surface.menu", Frame::menu(&t)),
            surface("surface.panel", Frame::panel(&t)),
            surface("surface.dialog", Frame::dialog(&t)),
            surface("surface.bar", Frame::terminal_bar(&t)),
        ],
    )
}

fn keycaps(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    column(
        s,
        &t,
        vec![
            Keys::raised(&["⌘", "⇧", "K"]).build(&t).key("keys.raised"),
            Keys::outlined(&["⌘", "K"]).build(&t).key("keys.outlined"),
            Keys::ledged(&["esc"]).build(&t).key("keys.ledged"),
            keycap::alternatives(
                vec![Keys::raised(&["⌘", "↑"]), Keys::raised(&["⌘", "↓"])],
                &t,
            )
            .key("keys.alternatives"),
        ],
    )
}

fn composer_story(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let edit = if s.long {
        TextEdit::new(LONG_MESSAGE)
    } else {
        s.field.clone()
    };
    column(
        s,
        &t,
        vec![
            crate::system::composer(&edit, "Message Chief", &t)
                .key("composer")
                .label("Message")
                .return_breaks_line(),
        ],
    )
}

fn menu_story(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let names = if s.long {
        [
            "work",
            "attn-linux-landing-runner-with-a-long-hostname",
            "side",
        ]
    } else {
        ["work", "home", "side"]
    };
    let menu = Menu::new("story.menu", "Switch profile");
    let menu = if s.case == Case::Form {
        menu.form([Field::new("story.menu.name", "New profile name")
            .control(line_box(
                TextInput::new(&s.field)
                    .key("story.menu.name")
                    .label("New profile name")
                    .on_edit(|s: &mut Floating| &mut s.field),
                FieldState::Ready,
                &t,
            ))
            .build(&t)])
            .footer(
                Actions::new("story.menu.form.actions")
                    .push(
                        Button::new("Create and switch")
                            .look(Look::Primary)
                            .build(&t),
                    )
                    .push(Button::new("Back").look(Look::Quiet).build(&t)),
            )
    } else {
        menu.rows(names.iter().enumerate().map(|(i, name)| {
            ListRow::new(format!("story.menu.{i}"), *name)
                .title(*name)
                .selected(i == s.chosen)
                .controls(
                    [El::from(
                        icon_button(
                            format!("story.menu.{i}.rename"),
                            "Rename",
                            &crate::icon::PENCIL,
                            IconSize::Regular,
                        )
                        .build(&t),
                    )],
                    crate::system::Reveal::Always,
                )
                .select_role("menuitem")
                .select(format!("story.menu.{i}.choose"), *name, noop)
                .build(&t)
        }))
        .footer(
            Actions::new("story.menu.actions")
                .push(Button::new("New profile").look(Look::Quiet).build(&t)),
        )
    };
    column(s, &t, vec![menu.build(&t)])
}

fn popover_story(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let mut popover = Popover::new(
        "story.popover",
        if s.long {
            "Set up attn-linux-landing-runner-with-a-long-hostname"
        } else {
            "Set up Captain"
        },
    )
    .aside(
        Button::new("esc")
            .look(Look::Quiet)
            .size(crate::system::Size::Compact)
            .build(&t),
    )
    .push(
        Field::new("story.popover.harness", "Harness")
            .control(
                Segmented::new("story.popover.harness.choice", "Harness")
                    .option(Segment::new("story.popover.claude", "Claude Code").chosen(true))
                    .option(Segment::new("story.popover.codex", "Codex"))
                    .build(&t),
            )
            .build(&t),
    );
    if s.case == Case::Failed {
        popover = popover.push(
            Notice::error(
                "story.popover.alert",
                "Cannot start here: the folder is not writable.",
            )
            .build(&t),
        );
    }
    let popover = popover.actions(
        Actions::new("story.popover.actions").then_end().push(
            Button::new("Start Captain")
                .look(Look::Primary)
                .build(&t)
                .key("story.popover.actions"),
        ),
    );
    column(s, &t, vec![popover.build(&t)])
}

fn pager_story(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    column(
        s,
        &t,
        vec![pager(
            ("pager", "pager.dot"),
            3,
            s.chosen,
            |i| format!("Step {}", i + 1),
            &t,
            |s: &mut Floating, i: usize, _: &mut Tx| s.chosen = i,
        )],
    )
}

fn attachments_story(s: &Floating, cx: &mut Cx<Floating>) -> El {
    let t = tokens(cx);
    let name = if s.long {
        "Screenshot 2026-11-19 at 21.47.03 of the launcher at the smallest window.png"
    } else {
        "notes.md"
    };
    let remove = |key: &str| -> El {
        icon_button(
            key.to_owned(),
            "Remove",
            crate::glyph::TIMES,
            IconSize::Small,
        )
        .build(&t)
        .into()
    };
    column(
        s,
        &t,
        vec![
            crate::blocks::Inline::loose()
                .key("attachments")
                .child(file_chip(
                    "attachments.file",
                    name,
                    "MD",
                    "2.1 KiB",
                    Some(remove("attachments.file.remove")),
                    &t,
                ))
                .child(file_chip(
                    "attachments.busy",
                    "trace.json",
                    "JSON",
                    "Attaching…",
                    None,
                    &t,
                ))
                .child(image_tile(
                    "attachments.image",
                    "screenshot.png",
                    None,
                    (84.0, 58.0),
                    Some(remove("attachments.image.remove")),
                    &t,
                )),
        ],
    )
}
