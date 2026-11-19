use rune::{Cx, El, Playground, Story, TextEdit, Tx, col, el, row};
use rune_kit::{
    Button, ButtonVariant, Field, Kit, List, ListItem, Menu, MenuItem, Popover, TextField,
    TextInput, Toast, ToastRegion, Tooltip,
};

use crate::colors::scheme_for;
use crate::system::{
    Button as Action, Choice, Look, Segment, Segmented, Size, Space, Tone, Type, area_box, arrows,
    choices, disclosure, door, expander, icon_box, line_box, number_box, press_area, stepper,
    switch, tag, words,
};
use crate::{Dialog, DialogFrame, Tokens, kit};

/// What a component story shows: one component, open where it opens, in the stage's
/// appearance, which the playground's own switch sets.
#[derive(Default)]
pub struct Showcase {
    pub field: TextEdit,
    pub dismissed: bool,
}

type View = fn(&Showcase, &mut Cx<Showcase>) -> El;

/// Every shared component, each in attn's colors, by the name its playground story has.
pub const STORIES: &[(&str, View)] = &[
    ("Components/List row", list_row),
    ("Components/Button", button),
    ("Components/Field", field),
    ("Components/Menu", menu),
    ("Components/Dialog", dialog),
    ("Components/Popover", popover),
    ("Components/Toast", toast),
    ("Components/Tooltip", tooltip),
];

/// The app's playground: a story for each shared component, and the library's stories with a
/// variant for each state. It has no shortcut and no bar, so no key of attn's keymap opens it;
/// the View menu and `--playground` do.
pub fn playground<S: 'static>() -> Playground<S> {
    let components = STORIES.iter().fold(
        Playground::new().shortcut(None).bar(false),
        |playground, &(name, view)| playground.story(Story::new(name, Showcase::default, view)),
    );
    library(components)
}

/// What a library story shows: the state its variant sets.
#[derive(Default)]
pub struct Library {
    pub rings: bool,
    pub on: bool,
    pub open: bool,
    pub long: bool,
    pub chosen: usize,
    pub disabled: bool,
    pub failed: bool,
    pub narrow: bool,
    pub line: TextEdit,
    pub number: TextEdit,
    pub area: TextEdit,
}

/// The looks and sizes a button story draws, each keyed `button.{look}.{size}`.
pub const BUTTONS: [(&str, Look); 4] = [
    ("primary", Look::Primary),
    ("plain", Look::Plain),
    ("quiet", Look::Quiet),
    ("danger", Look::Danger),
];
pub const SIZES: [(&str, Size); 2] = [("regular", Size::Regular), ("compact", Size::Compact)];

/// The field boxes a field story draws, by key.
pub const FIELD_BOXES: [&str; 3] = ["field.line", "field.number", "field.area"];

const LONG: &str = "An exceptionally long value that a person pasted without stopping to think about the box it lands in";

/// Every library story, by name, with the keys of the controls it draws: what the state sheets
/// and the rule tests read.
pub const SUBJECTS: &[(&str, &[&str])] = &[
    (
        "Library/Switch",
        &["switch.off", "switch.on", "switch.word"],
    ),
    (
        "Library/Segmented",
        &["segmented.theme", "segmented.agent", "segmented.long"],
    ),
    (
        "Library/Stepper",
        &["stepper.scale", "stepper.match", "stepper.number"],
    ),
    ("Library/Expander", &["expander"]),
    (
        "Library/Tag",
        &["tag.neutral", "tag.warn", "tag.bad", "tag.good"],
    ),
    ("Library/Icon box", &["icon.filled", "icon.dashed"]),
    ("Library/Choice", &["choice.0", "choice.1", "choice.2"]),
    ("Library/Press area", &["press-area"]),
    ("Library/Disclosure", &["disclosure"]),
];

const LONG_NAME: &str = "Same agent, resumed when its session closed on its own";

fn library<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground
        .story(
            Story::new("Library/Switch", Library::default, switches)
                .variant("Rest", |_| {})
                .variant("Focus", |s| s.rings = true),
        )
        .story(
            Story::new("Library/Segmented", Library::default, segmented)
                .variant("Rest", |_| {})
                .variant("Second chosen", |s| s.chosen = 1)
                .variant("Focus", |s| s.rings = true)
                .variant("Narrow", |s| s.narrow = true),
        )
        .story(
            Story::new("Library/Stepper", Library::default, steppers)
                .variant("Rest", |_| {})
                .variant("Focus", |s| s.rings = true),
        )
        .story(
            Story::new("Library/Expander", Library::default, expanders)
                .variant("Closed", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Focus", |s| s.rings = true),
        )
        .story(
            Story::new("Library/Tag", Library::default, tags)
                .variant("Rest", |_| {})
                .variant("Long", |s| s.long = true),
        )
        .story(Story::new("Library/Icon box", Library::default, icon_boxes).variant("Rest", |_| {}))
        .story(
            Story::new("Library/Choice", Library::default, choice_list)
                .variant("First chosen", |_| {})
                .variant("Second chosen", |s| s.chosen = 1)
                .variant("Focus", |s| s.rings = true)
                .variant("Narrow", |s| s.narrow = true),
        )
        .story(
            Story::new("Library/Press area", Library::default, press_areas)
                .variant("Closed", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Focus", |s| s.rings = true)
                .variant("Long", |s| s.long = true),
        )
        .story(
            Story::new("Library/Disclosure", Library::default, disclosures)
                .variant("Closed", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Failed", |s| s.failed = true)
                .variant("Focus", |s| s.rings = true),
        )
        .story(
            Story::new("Library/Button", Library::default, buttons)
                .variant("Rest", |_| {})
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true),
        )
        .story(
            Story::new("Library/Field box", Library::default, field_boxes)
                .variant("Empty", |_| {})
                .variant("Filled", |s| {
                    s.line = TextEdit::new("api-review");
                    s.number = TextEdit::new("600");
                    s.area = TextEdit::new(
                        "Review the change against the plan.\nReport P0 and P1 only.",
                    );
                })
                .variant("Failed", |s| {
                    s.failed = true;
                    s.line = TextEdit::new("api review");
                })
                .variant("Disabled", |s| {
                    s.disabled = true;
                    s.line = TextEdit::new("api-review");
                })
                .variant("Long text", |s| {
                    s.line = TextEdit::new(LONG);
                    s.number = TextEdit::new("1234567");
                    s.area = TextEdit::new(LONG);
                })
                .variant("Narrow", |s| {
                    s.narrow = true;
                    s.line = TextEdit::new(LONG);
                }),
        )
}

fn library_tokens(cx: &Cx<Library>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

fn library_stage(t: &Tokens, content: El) -> El {
    el().w_full()
        .h_full()
        .fill(t.bg_panel)
        .child(col().w_full().h_full().center().child(content))
}

fn buttons(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let rows = SIZES.iter().map(|&(size_key, size)| {
        door::gap(row().items_center(), Space::S16).children(BUTTONS.iter().map(
            |&(look_key, look)| {
                let shown = match look {
                    Look::Primary => "Save",
                    Look::Plain => "Browse",
                    Look::Quiet => "Undo",
                    Look::Danger => "Remove",
                };
                Action::new(shown)
                    .look(look)
                    .size(size)
                    .disabled(s.disabled)
                    .on_press_tx(|_: &mut Library, _: &mut Tx| {})
                    .build(&t)
                    .key(format!("button.{look_key}.{size_key}"))
                    .into()
            },
        ))
    });
    library_stage(&t, door::gap(col(), Space::S24).children(rows))
}

fn field_boxes(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let line = TextInput::new(&s.line)
        .key("field.line.input")
        .label("Name")
        .placeholder("Automation name")
        .invalid(s.failed)
        .disabled(s.disabled)
        .on_edit(|s: &mut Library| &mut s.line);
    let number = TextInput::new(&s.number)
        .key("field.number.input")
        .label("Sidebar width")
        .placeholder("240")
        .disabled(s.disabled)
        .on_edit(|s: &mut Library| &mut s.number);
    let area = rune_kit::TextArea::new(&s.area)
        .key("field.area.input")
        .label("Prompt")
        .placeholder("What the agent does each run")
        .rows(3)
        .disabled(s.disabled)
        .on_edit(|s: &mut Library| &mut s.area);
    let column = door::gap(col(), Space::S16)
        .w(if s.narrow { 160.0 } else { 280.0 })
        .child(line_box(line, &t).key("field.line").into())
        .child(number_box(number, &t).key("field.number").into())
        .child(area_box(area, &t).key("field.area").into());
    library_stage(&t, column)
}

fn stage(cx: &Cx<Showcase>, body: impl FnOnce(&Kit, &mut Vec<El>) -> El) -> El {
    let t = &scheme_for(None).colors(cx.system_dark()).ui;
    let k = kit(t);
    let mut layer = Vec::new();
    let content = body(k, &mut layer);
    el().w_full()
        .h_full()
        .fill(t.bg_app)
        .child(col().w_full().h_full().center().child(content))
        .children(layer)
}

fn list_row(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        List::new()
            .item(ListItem::new("api-review").selected(true))
            .item(ListItem::new("docs-pass"))
            .item(ListItem::new("build-shell"))
            .build(k)
            .w(240.0)
    })
}

fn button(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        rune::row()
            .gap(8.0)
            .child(
                Button::new("New session")
                    .variant(ButtonVariant::Submit)
                    .build(k),
            )
            .child(Button::new("Cancel").build(k))
    })
}

fn field(s: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        Field::new()
            .label("Session name")
            .control(
                TextField::new("Session name", &s.field)
                    .placeholder("api-review")
                    .hide_label()
                    .on_edit(|s: &mut Showcase| &mut s.field)
                    .build(k),
            )
            .build(k)
            .w(280.0)
    })
}

fn menu(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, layer| {
        Menu::new("story.menu")
            .standard()
            .label("Session")
            .trigger(Button::new("Session").build(k))
            .item(MenuItem::new("Rename").shortcut("⌘R"))
            .item(MenuItem::new("Snooze"))
            .separator()
            .item(MenuItem::new("Close").danger(true))
            .open(true)
            .build(k, layer)
    })
}

fn dialog(s: &Showcase, cx: &mut Cx<Showcase>) -> El {
    let t = scheme_for(None).colors(cx.system_dark()).ui;
    stage(cx, |k, layer| {
        if !s.dismissed {
            Dialog::new(
                "story.dialog",
                "Close this session?",
                DialogFrame::prompt(&t),
            )
            .subtitle("The agent stops and its terminal closes.")
            .child(
                rune::row()
                    .gap(10.0)
                    .justify_end()
                    .w_full()
                    .px(24.0)
                    .pb(22.0)
                    .child(Button::new("Cancel").build(k))
                    .child(
                        Button::new("Close session")
                            .variant(ButtonVariant::Danger)
                            .build(k),
                    ),
            )
            .on_close(|s: &mut Showcase| s.dismissed = true)
            .build(&t, layer);
        }
        el()
    })
}

fn popover(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, layer| {
        Popover::new("story.popover")
            .trigger(Button::new("Pull request").build(k))
            .title("#123 Route every key")
            .description("Checks passed · 2 reviews")
            .open(true)
            .build(k, layer)
    })
}

fn toast(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, _| {
        ToastRegion::new()
            .toast(
                Toast::new("story.toast", "Session closed")
                    .description("api-review stopped.")
                    .build(k),
            )
            .build(k)
    })
}

fn tooltip(_: &Showcase, cx: &mut Cx<Showcase>) -> El {
    stage(cx, |k, layer| {
        Tooltip::new("story.tooltip")
            .trigger(Button::new("Split").build(k))
            .text(k, "New shell beside")
            .open(true)
            .build(k, layer)
    })
}

fn noop(_: &mut Library, _: &mut Tx) {}

fn stack(t: &Tokens, children: impl IntoIterator<Item = El>) -> El {
    library_stage(
        t,
        door::gap(col().items_start(), Space::S24).children(children),
    )
}

fn switches(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    stack(
        &t,
        [
            switch("switch.off", "Delegation", false, false, &t, noop).into(),
            switch("switch.on", "Delegation", true, false, &t, noop).into(),
            switch(
                "switch.word",
                "Telemetry",
                s.on,
                true,
                &t,
                |s: &mut Library, _: &mut Tx| s.on = !s.on,
            )
            .into(),
        ],
    )
}

fn segmented(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let pick = |i: usize| move |s: &mut Library, _: &mut Tx| s.chosen = i;
    let track = |key: &str, label: &str, names: &[&str]| -> El {
        Segmented::new(key, label)
            .options(names.iter().enumerate().map(|(i, name)| {
                Segment::new(format!("segment{i}"), *name)
                    .chosen(i == s.chosen)
                    .on_press_tx(pick(i))
            }))
            .build(&t)
            .into()
    };
    let agent = Segmented::new("segmented.agent", "Default session agent")
        .option(
            Segment::new("segment0", "Claude")
                .chosen(s.chosen == 0)
                .on_press_tx(pick(0)),
        )
        .option(
            Segment::new("segment1", "Codex")
                .chosen(s.chosen == 1)
                .on_press_tx(pick(1)),
        )
        .option(
            Segment::new("segment2", "Pi")
                .disabled(true)
                .help("Pi CLI not found in PATH"),
        )
        .option(
            Segment::new("segment3", "Copilot")
                .trailing(words("…", Type::Control, t.text_tertiary))
                .help("Checking for Copilot on this machine"),
        )
        .build(&t)
        .into();
    let column = door::gap(col().items_start(), Space::S24)
        .w(if s.narrow { 220.0 } else { 520.0 })
        .child(track(
            "segmented.theme",
            "Theme",
            &["Dark", "Light", "System"],
        ))
        .child(agent)
        .child(track(
            "segmented.long",
            "Agent",
            &["New each run", LONG_NAME],
        ));
    library_stage(&t, column)
}

fn steppers(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let number = TextInput::new(&s.number)
        .key("stepper.number.input")
        .label("Sidebar width")
        .placeholder("240")
        .trailing(arrows(
            "stepper.number",
            &t,
            |_: &mut Library, _: bool, _: &mut Tx| {},
        ))
        .on_edit(|s: &mut Library| &mut s.number);
    stack(
        &t,
        [
            stepper(
                "stepper.scale",
                "app font size",
                "100%".into(),
                &t,
                noop,
                noop,
            )
            .into(),
            stepper(
                "stepper.match",
                "terminal font size",
                "Match app".into(),
                &t,
                noop,
                noop,
            )
            .into(),
            number_box(number, &t).key("stepper.number").into(),
        ],
    )
}

fn expanders(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    stack(
        &t,
        [expander(
            "expander",
            "+2 alternatives",
            s.open,
            &t,
            |s: &mut Library, _: &mut Tx| s.open = !s.open,
        )
        .into()],
    )
}

fn tags(s: &Library, cx: &mut Cx<Library>) -> El {
    let t = library_tokens(cx);
    let said = |short: &'static str| {
        if s.long {
            "Needs a model before it can run"
        } else {
            short
        }
    };
    stack(
        &t,
        [
            tag("tag.neutral", said("Attn"), Tone::Neutral, &t).into(),
            tag("tag.warn", said("Needs a model"), Tone::Warn, &t).into(),
            tag("tag.bad", said("Failed"), Tone::Bad, &t).into(),
            tag("tag.good", said("Live"), Tone::Good, &t).into(),
        ],
    )
}

fn icon_boxes(_: &Library, cx: &mut Cx<Library>) -> El {
    let t = library_tokens(cx);
    stack(
        &t,
        [
            El::from(icon_box(words("B", Type::Name, t.text_primary), false, &t))
                .key("icon.filled"),
            El::from(icon_box(words("+", Type::Name, t.text_tertiary), true, &t))
                .key("icon.dashed"),
        ],
    )
}

fn choice_list(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let options = [
        ("New each run", "Every run starts a new agent."),
        ("Same agent", "Each run continues the last one's session."),
        (
            LONG_NAME,
            "Resumes the agent when its session ends without a person closing it.",
        ),
    ];
    let n = options.len();
    let list = choices("choices", "Agent")
        .w(if s.narrow { 220.0 } else { 420.0 })
        .children(options.iter().enumerate().map(|(i, (name, note))| {
            Choice::new(format!("choice.{i}"), *name)
                .note(*note)
                .chosen(i == s.chosen)
                .build((i, n), &t, move |s: &mut Library, _: &mut Tx| s.chosen = i)
                .into()
        }));
    library_stage(&t, list)
}

fn press_areas(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let name = if s.long { LONG_NAME } else { "Reviewer" };
    let content = door::gap(col(), Space::S2)
        .child(words(name, Type::Name, t.text_primary).wrap())
        .child(
            words(
                "Reviews a change against its plan.",
                Type::Small,
                t.text_tertiary,
            )
            .wrap(),
        );
    let area = press_area(
        "press-area",
        name,
        s.open,
        content,
        &t,
        |s: &mut Library, _: &mut Tx| s.open = !s.open,
    );
    library_stage(&t, col().w(320.0).child(area.into()))
}

fn disclosures(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let chips = if s.open {
        Vec::new()
    } else {
        vec!["Worktree".to_owned(), "2 env".to_owned()]
    };
    stack(
        &t,
        [disclosure(
            "disclosure",
            "Advanced",
            s.open,
            &chips,
            s.failed,
            &t,
            |s: &mut Library, _: &mut Tx| s.open = !s.open,
        )
        .into()],
    )
}
