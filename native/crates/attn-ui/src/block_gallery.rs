//! The gallery's stories for the page blocks: one story per block, a variant per state.

use rune::{Cx, El, Playground, Story, TextEdit, Tx, col, el};
use rune_kit::TextInput;

use crate::Tokens;
use crate::blocks::{
    Actions, Block, Choices, Disclosure, Empty, Field, Head, Inline, Notice, Page, Prose, Refusal,
    Section, SettingRow, Stack, Status, SubRow, Table, TableColumns, TableRow, saved, swatches,
};
use crate::colors::scheme_for;
use crate::system::{
    Beside, Button, Choice, Control, FieldState, IconSize, Look, Segment, Segmented, Size, Space,
    Tone, Trigger, TriggerLook, Type, arrows, expander, icon_box, line_box, number_box, stepper,
    switch, tag, words,
};

/// What a block story shows: the state its variant sets.
#[derive(Default)]
pub struct Blocks {
    pub rings: bool,
    pub on: bool,
    pub open: bool,
    pub long: bool,
    pub width: Width,
    pub failed: bool,
    pub disabled: bool,
    pub saving: bool,
    pub chosen: usize,
    pub empty: bool,
    pub line: TextEdit,
    pub number: TextEdit,
}

/// How wide the Settings content is in the story.
#[derive(Default, Clone, Copy)]
pub enum Width {
    /// In a window of a common size.
    #[default]
    Wide,
    /// In the smallest window, 800 wide, less the navigation.
    Narrow,
    /// In the smallest window at 150% text: 800 by 600 shows 533 points, and the Settings page
    /// keeps 163.3 of them for its content.
    Smallest,
    /// As wide as the window, for a check that sweeps the window's width.
    Window,
}

impl Width {
    fn content(self) -> Option<f32> {
        match self {
            Width::Wide => Some(760.0),
            Width::Narrow => Some(430.0),
            Width::Smallest => Some(163.3),
            Width::Window => None,
        }
    }
}

/// Every block story, by name, with the keys of what it draws: what the state sheets and the
/// rule tests read.
pub const BLOCK_SUBJECTS: &[(&str, &[&str])] = &[
    ("Blocks/Page", &["page.body"]),
    ("Blocks/Section", &["section"]),
    (
        "Blocks/Setting row",
        &["row.stepper", "row.number", "row.switch", "row.button"],
    ),
    (
        "Blocks/Field",
        &[
            "field.name.field",
            "field.path.field",
            "field.read.field",
            "field.count.field",
        ],
    ),
    (
        "Blocks/Notice and status",
        &["notice.error", "notice.warn", "notice.info", "status"],
    ),
    ("Blocks/Choices", &["choices"]),
    ("Blocks/Disclosure", &["disclosure.block.part"]),
    ("Blocks/Actions", &["actions"]),
    ("Blocks/Table", &["table"]),
];

const LONG_COPY: &str = "Every agent attn starts on this machine, including the ones that run on a schedule and the ones another agent delegates to, reads this before its first turn, so a long value here reaches all of them.";
const LONG_TITLE: &str = "Narrowest tile width before tiles stack into a column (columns)";

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground
        .story(
            common(Story::new("Blocks/Page", Blocks::default, page))
                .variant("Saving", |s| s.saving = true)
                .variant("Refused", |s| s.failed = true)
                .variant("Off", |s| s.disabled = true),
        )
        .story(common(Story::new(
            "Blocks/Section",
            Blocks::default,
            section,
        )))
        .story(common(Story::new(
            "Blocks/Setting row",
            Blocks::default,
            setting_rows,
        )))
        .story(
            common(Story::new("Blocks/Field", Blocks::default, fields))
                .variant("Saved", |s| s.saving = true)
                .variant("Failed", |s| s.failed = true)
                .variant("Disabled", |s| s.disabled = true),
        )
        .story(common(Story::new(
            "Blocks/Notice and status",
            Blocks::default,
            notices,
        )))
        .story(
            common(Story::new("Blocks/Choices", Blocks::default, choice_list))
                .variant("Second chosen", |s| s.chosen = 1)
                .variant("Disabled", |s| s.disabled = true),
        )
        .story(
            common(Story::new(
                "Blocks/Disclosure",
                Blocks::default,
                disclosures,
            ))
            .variant("Open", |s| s.open = true)
            .variant("Failed", |s| s.failed = true)
            .variant("Disabled", |s| s.disabled = true),
        )
        .story(common(Story::new(
            "Blocks/Actions",
            Blocks::default,
            actions,
        )))
        .story(
            common(Story::new("Blocks/Table", Blocks::default, table))
                .variant("Open", |s| s.open = true)
                .variant("Sub-row open", |s| {
                    s.open = true;
                    s.chosen = 1;
                })
                .variant("Off", |s| s.disabled = true)
                .variant("Open, focus", |s| {
                    s.open = true;
                    s.rings = true;
                })
                .variant("Off, focus", |s| {
                    s.disabled = true;
                    s.rings = true;
                })
                .variant("Off, open, focus", |s| {
                    s.disabled = true;
                    s.open = true;
                    s.rings = true;
                })
                .variant("Empty", |s| s.empty = true)
                .variant("Empty off", |s| {
                    s.empty = true;
                    s.disabled = true;
                })
                .variant("Empty smallest long", |s| {
                    s.empty = true;
                    s.width = Width::Smallest;
                    s.long = true;
                })
                .variant("Empty off smallest long", |s| {
                    s.empty = true;
                    s.disabled = true;
                    s.width = Width::Smallest;
                    s.long = true;
                })
                .variant("Open, window wide", |s| {
                    s.open = true;
                    s.width = Width::Window;
                })
                .variant("Open narrow", |s| {
                    s.open = true;
                    s.chosen = 1;
                    s.width = Width::Narrow;
                }),
        )
}

pub(crate) fn common(story: Story<Blocks>) -> Story<Blocks> {
    story
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
        })
}

pub(crate) fn tokens(cx: &Cx<Blocks>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

/// `page` at the story's width: its content and the page's inset on both sides.
fn sized(s: &Blocks, page: El) -> El {
    match s.width.content() {
        Some(content) => page.w(content + 2.0 * Space::S32.pt()),
        None => page.w_full(),
    }
}

/// The story's blocks on a page of the story's width, as Settings lays them.
pub(crate) fn stage(s: &Blocks, cx: &mut Cx<Blocks>, t: &Tokens, blocks: Vec<Block>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    el().w_full().h_full().fill(t.bg_panel).child(
        col().w_full().h_full().items_center().child(
            sized(s, col()).h_full().child(
                Page::new()
                    .push_all(blocks)
                    .group("stage", "Stage")
                    .build("stage.page", t),
            ),
        ),
    )
}

pub(crate) fn noop(_: &mut Blocks, _: &mut Tx) {}

fn copy(s: &Blocks, short: &'static str) -> &'static str {
    if s.long { LONG_COPY } else { short }
}

fn title(s: &Blocks, short: &'static str) -> &'static str {
    if s.long { LONG_TITLE } else { short }
}

fn number_field(s: &Blocks, key: &str, t: &Tokens) -> El {
    let input = TextInput::new(&s.number)
        .key(format!("{key}.input"))
        .label("Sidebar width")
        .placeholder("240")
        .invalid(s.failed)
        .trailing(arrows(key, t, |_: &mut Blocks, _: bool, _: &mut Tx| {}))
        .on_edit(|s: &mut Blocks| &mut s.number);
    let refusal = s.failed.then(|| {
        Refusal::new(
            format!("{key}.refusal"),
            "sidebar_width must be between 200 and 600",
        )
        .retry(
            Button::new("Retry")
                .on_press_tx(noop)
                .build(t)
                .key(format!("{key}.retry")),
        )
        .build(t)
    });
    Stack::tight()
        .items_end()
        .child(
            Inline::loose()
                .child(
                    number_box(input, FieldState::failed_if(s.failed), t)
                        .key(key.to_owned())
                        .into(),
                )
                .children(s.saving.then(|| saved(format!("{key}.saved"), t))),
        )
        .children(refusal)
}

fn rows(s: &Blocks, t: &Tokens) -> Vec<Block> {
    vec![
        SettingRow::new(
            "row.stepper",
            title(s, "App"),
            copy(s, "The whole interface, including terminals."),
            Inline::loose()
                .child(
                    stepper(
                        "row.stepper.control",
                        "app font size",
                        "110%",
                        s.disabled,
                        t,
                        noop,
                        noop,
                    )
                    .into(),
                )
                .child(
                    Button::new("Reset")
                        .look(Look::Quiet)
                        .on_press_tx(noop)
                        .build(t)
                        .key("row.stepper.reset")
                        .into(),
                ),
        )
        .build(t),
        SettingRow::new(
            "row.number",
            title(s, "Sidebar width (pixels)"),
            copy(s, "The open sidebar's width, from 200 to 600."),
            number_field(s, "row.number.control", t),
        )
        .build(t),
        SettingRow::new(
            "row.switch",
            title(s, "Sent files open"),
            copy(s, "Open a file an agent sends as soon as it arrives."),
            switch(
                "row.switch.control",
                "Sent files open",
                s.on,
                Beside::Nothing,
                s.disabled,
                t,
                |s: &mut Blocks, _: &mut Tx| s.on = !s.on,
            ),
        )
        .build(t),
        SettingRow::new(
            "row.button",
            title(s, "Custom colors"),
            copy(s, "Your own colors replace parts of the scheme."),
            Button::new("Reset")
                .disabled(s.disabled)
                .on_press_tx(noop)
                .build(t)
                .key("row.button.control"),
        )
        .build(t),
    ]
}

fn setting_rows(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let rows = rows(s, &t);
    stage(s, cx, &t, rows)
}

fn theme(s: &Blocks, t: &Tokens) -> Block {
    Block::control(
        "section.theme.block",
        Segmented::new("section.theme", "Theme")
            .options(
                ["Dark", "Light", "System"]
                    .iter()
                    .enumerate()
                    .map(|(i, name)| {
                        Segment::new(format!("segment{i}"), *name)
                            .chosen(i == s.chosen)
                            .on_press_tx(move |s: &mut Blocks, _: &mut Tx| s.chosen = i)
                    }),
            )
            .build(t),
    )
}

fn section(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let sections = vec![
        Section::new("section", "Theme")
            .description(copy(
                s,
                "Choose the appearance and the color scheme of attn and its terminals.",
            ))
            .push(theme(s, &t))
            .build(&t),
        Section::new("section.second", title(s, "Font Size"))
            .description(copy(s, "Scale text across attn."))
            .control(
                Button::new("Reset")
                    .look(Look::Quiet)
                    .size(Size::Compact)
                    .on_press_tx(noop)
                    .build(&t)
                    .key("section.second.reset"),
            )
            .push_all(rows(s, &t).into_iter().take(2))
            .build(&t),
    ];
    stage(s, cx, &t, sections)
}

fn page(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    if s.rings {
        cx.force_focus_rings();
    }
    let mut head = Head::new("page.heading", title(s, "Delegation"))
        .lead(copy(
            s,
            "Choose the agents attn starts when an agent hands work to another.",
        ))
        .note(
            words(
                "Changes apply to the next delegation.",
                Type::Small,
                t.text_muted,
            )
            .wrap(),
        )
        .aside(switch(
            "page.switch",
            "Delegation",
            !s.disabled,
            Beside::State,
            false,
            &t,
            noop,
        ));
    if s.long {
        head = head.aside(
            Inline::loose()
                .key("page.pills")
                .child(El::from(tag(
                    "page.pill.saved",
                    "project path saved",
                    Tone::Good,
                    &t,
                )))
                .child(El::from(tag(
                    "page.pill.sent",
                    "sent files ignored",
                    Tone::Neutral,
                    &t,
                ))),
        );
    }
    let said = if s.saving { "Saving…" } else { "" };
    let mut parts: Vec<El> = Vec::new();
    if s.saving {
        parts.push(words(said, Type::Small, t.text_tertiary));
    }
    if s.failed {
        parts.push(
            Refusal::new(
                "page.refusal",
                "The daemon refused the theme: unknown value",
            )
            .retry(
                Button::new("Retry")
                    .on_press_tx(noop)
                    .build(&t)
                    .key("page.retry"),
            )
            .build(&t),
        );
    }
    let blocks = vec![
        Prose::lead("page.prose", copy(s, "Roles run in the order listed.")).build(&t),
        Section::new("page.section", "Font Size")
            .description(copy(s, "Scale text across attn."))
            .push_all(rows(s, &t).into_iter().take(2))
            .build(&t),
        Section::new("page.layout", "Layout")
            .description("How wide tiles and the sidebar get.")
            .push_all(rows(s, &t).into_iter().skip(2))
            .build(&t),
    ];
    let page = Page::new()
        .head(head)
        .status("page.status", said, parts)
        .push_all(blocks)
        .group("page", "Delegation")
        .dimmed(s.disabled)
        .build("page.body", &t);
    el().w_full().h_full().fill(t.bg_panel).child(
        col()
            .w_full()
            .h_full()
            .items_center()
            .child(sized(s, col()).h_full().child(page)),
    )
}

fn fields(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let name = TextInput::new(&s.line)
        .key("field.name")
        .label("Name")
        .placeholder("Automation name")
        .on_edit(|s: &mut Blocks| &mut s.line);
    let path = TextInput::new(&s.number)
        .key("field.path")
        .label("Folder")
        .placeholder("~/projects")
        .on_edit(|s: &mut Blocks| &mut s.number);
    let fields = vec![
        Field::new("field.name", title(s, "Name"))
            .note(copy(s, "required"))
            .control(line_box(name, field_state(s), &t).key("field.name.box"))
            .hint(copy(s, "Shown in the sidebar and the ledger."))
            .error(s.failed.then(|| "A name can't hold a space.".to_owned()))
            .build(&t),
        Field::new("field.path", title(s, "Folder"))
            .note(copy(s, "optional"))
            .mark(s.saving.then(|| saved("field.path.saved", &t)))
            .control(line_box(path, path_state(s), &t).key("field.path.box"))
            .action(
                Button::new("Browse")
                    .disabled(s.disabled)
                    .on_press_tx(noop)
                    .build(&t)
                    .key("field.path.browse"),
            )
            .refusal((s.failed || s.long).then(|| {
                Refusal::new(
                    "field.path.refusal",
                    if s.long {
                        "cannot create directory /Users/you/projects/platform/InternationalEngineeringObservability/worktrees: permission denied"
                    } else {
                        "cannot create directory: permission denied"
                    },
                )
                .retry(
                    Button::new("Retry")
                        .on_press_tx(noop)
                        .build(&t)
                        .key("field.path.retry"),
                )
                .build(&t)
            }))
            .build(&t),
        Field::new("field.read", "Model")
            .value(if s.long {
                "claude-opus-4-5-20251101-with-a-very-long-provider-suffix-that-wraps"
            } else {
                "Opus 4.5"
            })
            .build(&t),
        Field::new("field.count", "Wait before counting down (seconds)")
            .control(
                number_box(
                    TextInput::new(&s.number)
                        .key("field.count")
                        .label("Wait before counting down (seconds)")
                        .trailing(arrows("field.count", &t, |_: &mut Blocks, _: bool, _: &mut Tx| {}))
                        .on_edit(|s: &mut Blocks| &mut s.number),
                    FieldState::Ready,
                    &t,
                )
                .key("field.count.box"),
            )
            .hint(copy(
                s,
                "How long the agent must keep working before anything starts.",
            ))
            .build(&t),
    ];
    stage(s, cx, &t, fields)
}

fn path_state(s: &Blocks) -> FieldState {
    if s.disabled {
        FieldState::Disabled
    } else {
        FieldState::failed_if(s.failed)
    }
}

fn notices(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let said = |short: &'static str| copy(s, short);
    let retry = |key: &str| {
        Button::new("Retry")
            .on_press_tx(noop)
            .build(&t)
            .key(key.to_owned())
    };
    let blocks = vec![
        Notice::error("notice.error", said("Couldn't read the roles."))
            .action(retry("notice.error.retry"))
            .build(&t),
        Notice::warn("notice.warn", said("Two roles need a model.")).build(&t),
        Notice::info("notice.info", said("Delegation is off.")).build(&t),
        Status::new("status", said("3 roles, 2 on"), true)
            .aside("Saved")
            .build(&t),
    ];
    stage(s, cx, &t, blocks)
}

fn choice_list(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let schemes: [(&str, [rune::Color; 4]); 3] = [
        ("attn", [t.bg_app, t.bg_panel, t.text_primary, t.accent]),
        (
            "Catppuccin Frappé",
            [t.bg_panel, t.bg_elevated, t.warning, t.success],
        ),
        (
            if s.long {
                "A scheme whose name runs on well past where a name usually ends"
            } else {
                "Solarized"
            },
            [t.bg_elevated, t.text_secondary, t.error, t.brand],
        ),
    ];
    let n = schemes.len();
    let choices = Choices::new("choices", "Color scheme")
        .options(schemes.into_iter().enumerate().map(|(i, (name, colors))| {
            Choice::new(format!("choice.{i}"), name)
                .leading(swatches(colors, &t))
                .chosen(i == s.chosen)
                .disabled(s.disabled)
                .build((i, n), &t, move |s: &mut Blocks, _: &mut Tx| s.chosen = i)
        }))
        .build();
    stage(s, cx, &t, vec![choices])
}

fn disclosures(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let tags = if s.open {
        Vec::new()
    } else {
        vec!["Worktree".to_owned(), "2 env".to_owned()]
    };
    let block = Disclosure::new(
        "disclosure.block",
        "Advanced",
        s.open,
        |s: &mut Blocks, _: &mut Tx| s.open = !s.open,
    )
    .tags(tags)
    .failed(s.failed)
    .disabled(s.disabled)
    .push(rows(s, &t).remove(1))
    .push(rows(s, &t).remove(2))
    .build(&t);
    stage(s, cx, &t, vec![block])
}

fn actions(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let button = |key: &str, said: &'static str, look: Look| {
        Button::new(if s.long {
            "Save and restart every agent now"
        } else {
            said
        })
        .look(look)
        .size(Size::Regular)
        .on_press_tx(noop)
        .build(&t)
        .key(key.to_owned())
    };
    let block = Actions::new("actions")
        .push(button("actions.save", "Save", Look::Primary))
        .push(button("actions.cancel", "Cancel", Look::Plain))
        .then_end()
        .push(button("actions.delete", "Delete", Look::Danger))
        .build();
    stage(s, cx, &t, vec![block])
}

fn field_state(s: &Blocks) -> FieldState {
    if s.disabled {
        FieldState::Disabled
    } else {
        FieldState::failed_if(s.failed)
    }
}

/// The fixed column of the table story: a model chip as Delegation shows one.
const RUNS_ON: f32 = 280.0;

fn model(key: &str, said: &str, unset: bool, t: &Tokens) -> Control {
    let ink = if unset { t.warning } else { t.text_primary };
    Trigger::new(
        key,
        format!("Model for {key}"),
        words(said.to_owned(), Type::Body, ink).wrap(),
    )
    .look(TriggerLook::Quiet)
    .value(said)
    .unset(unset)
    .build(t, noop)
}

fn mark(initial: &str, t: &Tokens) -> Control {
    icon_box(
        words(initial.to_owned(), Type::Label, t.text_secondary),
        false,
        t,
    )
}

fn table(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let mut columns = TableColumns::new("Role");
    let runs_on = columns.fixed("Runs on", RUNS_ON);
    let toggle = |s: &mut Blocks, _: &mut Tx| s.open = !s.open;
    let fallback = TableRow::new("table.fallback", "Anything else", noop)
        .leading(icon_box(
            words("→", Type::Label, t.text_secondary),
            true,
            &t,
        ))
        .subtitle("When no role fits the work.")
        .control(
            runs_on,
            model("table.fallback.model", "Claude · Sonnet", false, &t),
        )
        .more("Anything else details");
    if s.empty {
        let empty = Empty::new(
            "table.empty",
            "No roles yet",
            "Start with Attn's maintained roles, then pick a model for each.",
            [
                El::from(
                    Button::new("Add Attn roles")
                        .look(Look::Primary)
                        .on_press_tx(noop)
                        .build(&t),
                )
                .key("table.adopt"),
                El::from(
                    Button::new("+ Custom role")
                        .look(Look::Plain)
                        .on_press_tx(noop)
                        .build(&t),
                )
                .key("table.add"),
            ],
        );
        let block = Table::new("table", columns)
            .empty(empty)
            .row(fallback)
            .dimmed(s.disabled)
            .build(&t);
        return stage(s, cx, &t, vec![block]);
    }
    let name = if s.long { LONG_TITLE } else { "Builder" };
    let alternative = |key: &str, title: &'static str, when: &'static str, open: bool| {
        let sub = SubRow::new(
            format!("table.alt.{key}"),
            title,
            |s: &mut Blocks, _: &mut Tx| {
                s.chosen = if s.chosen == 1 { 0 } else { 1 };
            },
        )
        .note("when", when, when.is_empty())
        .control(
            runs_on,
            model(
                &format!("table.alt.{key}.model"),
                "Codex · gpt-5",
                false,
                &t,
            ),
        )
        .trailing(
            Button::mark("×", IconSize::Large)
                .look(Look::Quiet)
                .size(Size::Compact)
                .on_press_tx(noop)
                .build(&t)
                .key(format!("table.alt.{key}.remove"))
                .label(format!("Remove {title}")),
        )
        .open(open);
        if !open {
            return sub;
        }
        sub.details(
            Field::new(format!("table.alt.{key}.name"), "Name")
                .value(title.to_owned())
                .build(&t),
        )
        .details(
            Actions::new(format!("table.alt.{key}.actions"))
                .push(
                    Button::new("Make default")
                        .look(Look::Plain)
                        .on_press_tx(noop)
                        .build(&t)
                        .key(format!("table.alt.{key}.default")),
                )
                .build(),
        )
    };
    let mut builder = TableRow::new("table.builder", name, toggle)
        .leading(mark("B", &t))
        .tag("Attn", Tone::Neutral)
        .subtitle(copy(s, "Builds an agreed brief or plan."))
        .control(
            runs_on,
            model("table.builder.model", "Claude · Opus · high", false, &t),
        )
        .more("Builder details")
        .open(s.open);
    if s.open {
        builder = builder
            .details(
                Field::new("table.builder.instructions", "Instructions")
                    .note("maintained by Attn")
                    .value(copy(s, "Implement the brief. Report what you verified.").to_owned())
                    .build(&t),
            )
            .sub_row(alternative(
                "fast",
                "Fast",
                "Small edits with a clear answer",
                s.chosen == 1,
            ))
            .sub_row(alternative("unnamed", "Unnamed alternative", "", false))
            .sub_footer(
                runs_on,
                Button::new("+ Alternative model")
                    .look(Look::Quiet)
                    .size(Size::Compact)
                    .on_press_tx(noop)
                    .build(&t)
                    .key("table.builder.add-alternative"),
            )
            .actions(
                Actions::new("table.builder.actions")
                    .push(
                        Button::new("Make an editable copy")
                            .look(Look::Plain)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("table.builder.copy"),
                    )
                    .then_end()
                    .push(
                        Button::new("Turn off")
                            .look(Look::Quiet)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("table.builder.turn"),
                    )
                    .push(
                        Button::new("Delete")
                            .look(Look::Danger)
                            .on_press_tx(noop)
                            .build(&t)
                            .key("table.builder.delete"),
                    ),
            );
    } else {
        builder = builder.control(
            runs_on,
            expander(
                "table.builder.alternatives",
                "2 alternatives",
                false,
                false,
                &t,
                toggle,
            ),
        );
    }
    let orchestrator = TableRow::new("table.orchestrator", "Orchestrator", noop)
        .leading(mark("O", &t))
        .tag("Needs a model", Tone::Warn)
        .subtitle("Runs an agreed plan through Builders.")
        .control(
            runs_on,
            model("table.orchestrator.model", "Choose a model", true, &t),
        )
        .more("Orchestrator details");
    let reviewer = TableRow::new("table.reviewer", "Reviewer", noop)
        .leading(mark("R", &t))
        .tag("Off", Tone::Neutral)
        .subtitle("Reviews a change against its plan.")
        .control(
            runs_on,
            model("table.reviewer.model", "Claude · Opus", false, &t),
        )
        .more("Reviewer details")
        .off(true);
    let block = Table::new("table", columns)
        .rows([builder, orchestrator, reviewer, fallback])
        .footer(
            "table.footer",
            Actions::new("table.footer.actions")
                .push(
                    Button::new("+ Custom role")
                        .look(Look::Quiet)
                        .on_press_tx(noop)
                        .build(&t)
                        .key("table.add"),
                )
                .push(
                    Button::new("+ Scout")
                        .look(Look::Quiet)
                        .on_press_tx(noop)
                        .build(&t)
                        .key("table.adopt"),
                ),
        )
        .dimmed(s.disabled)
        .build(&t);
    stage(s, cx, &t, vec![block])
}
