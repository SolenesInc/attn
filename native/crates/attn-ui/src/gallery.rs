use rune::{Cx, El, Playground, Story, TextEdit, Tx, col, el, row};
use rune_kit::{
    Button, ButtonVariant, Field, Kit, List, ListItem, Menu, MenuItem, Popover, TextField,
    TextInput, Toast, ToastRegion, Tooltip,
};

use crate::blocks::{Banner, BannerTone};
use crate::colors::scheme_for;
use crate::system::{
    Beside, Button as Action, Choice, FieldState, IconSize, Look, Segment, Segmented, Size, Space,
    Tone, Trigger, TriggerLook, Type, area_box, arrows, checks, choices, code_box, disclosure,
    door, expander, icon, icon_box, line_box, number_box, press_area, stepper, switch, tag, words,
};
use crate::{Tokens, kit};

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
    crate::reading_gallery::stories(crate::floating_gallery::stories(
        crate::panel_gallery::stories(crate::chrome_gallery::stories(
            crate::settings_gallery::stories(crate::block_gallery::stories(library(components))),
        )),
    ))
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
    pub many: bool,
    pub banner: BannerCase,
    pub line: TextEdit,
    pub code: TextEdit,
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
pub const FIELD_BOXES: [&str; 4] = ["field.line", "field.code", "field.number", "field.area"];

const LONG: &str = "An exceptionally long value that a person pasted without stopping to think about the box it lands in";

/// Every library story, by name, with the keys of the controls it draws: what the state sheets
/// and the rule tests read.
pub const SUBJECTS: &[(&str, &[&str])] = &[
    (
        "Library/Switch",
        &["switch.off", "switch.on", "switch.word", "switch.caption"],
    ),
    (
        "Library/Segmented",
        &[
            "segmented.theme",
            "segmented.agent",
            "segmented.long",
            "segmented.start",
        ],
    ),
    (
        "Library/Stepper",
        &["stepper.scale", "stepper.match", "stepper.number"],
    ),
    ("Library/Expander", &["expander"]),
    (
        "Library/Tag",
        &[
            "tag.neutral",
            "tag.warn",
            "tag.bad",
            "tag.good",
            "tag.accent",
        ],
    ),
    ("Library/Icon box", &["icon.filled", "icon.dashed"]),
    ("Library/Choice", &["choices"]),
    ("Library/Press area", &["press-area"]),
    ("Library/Disclosure", &["disclosure"]),
    ("Library/Trigger", &["trigger.field", "trigger.quiet"]),
];

/// Two branches to start from that differ only at their ends, the second longer than any line.
const START_POINTS: [&str; 2] = [
    "Start from release/customer-platform-2026.12",
    "Start from release/customer-platform-2026.21-with-the-hotfix-for-the-tokyo-region-rollout",
];
const LONG_NAME: &str = "Same agent, resumed when its session closed on its own";

fn library<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground
        .story(
            Story::new("Library/Switch", Library::default, switches)
                .variant("Rest", |_| {})
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true),
        )
        .story(
            Story::new("Library/Segmented", Library::default, segmented)
                .variant("Rest", |_| {})
                .variant("Second chosen", |s| s.chosen = 1)
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true)
                .variant("Narrow", |s| s.narrow = true),
        )
        .story(
            Story::new("Library/Stepper", Library::default, steppers)
                .variant("Rest", |_| {})
                .variant("Wide value", |s| s.number = TextEdit::new("1000000"))
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true),
        )
        .story(
            Story::new("Library/Expander", Library::default, expanders)
                .variant("Closed", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true),
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
                .variant("Disabled", |s| s.disabled = true)
                .variant("Narrow", |s| s.narrow = true)
                .variant("Recommended", |s| s.open = true)
                .variant("Many", |s| s.many = true),
        )
        .story(
            Story::new("Library/Press area", Library::default, press_areas)
                .variant("Closed", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true)
                .variant("Long", |s| s.long = true),
        )
        .story(
            Story::new("Library/Disclosure", Library::default, disclosures)
                .variant("Closed", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Failed", |s| s.failed = true)
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true),
        )
        .story(
            Story::new("Library/Trigger", Library::default, triggers)
                .variant("Rest", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Unset", |s| s.failed = true)
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true)
                .variant("Long", |s| s.long = true),
        )
        .story(
            Story::new("Library/Button", Library::default, buttons)
                .variant("Rest", |_| {})
                .variant("Focus", |s| s.rings = true)
                .variant("Disabled", |s| s.disabled = true)
                .variant("Long label", |s| s.long = true)
                .variant("Narrow", |s| {
                    s.long = true;
                    s.narrow = true;
                }),
        )
        .story(
            Story::new("Library/Banner", Library::default, banners)
                .variant("Rest", |_| {})
                .variant("No action", |s| s.banner = BannerCase::NoAction)
                .variant("Sentence only", |s| s.banner = BannerCase::SentenceOnly)
                .variant("Notes", |s| s.banner = BannerCase::Notes)
                .variant("Countdown", |s| s.banner = BannerCase::Countdown)
                .variant("Two actions", |s| s.banner = BannerCase::TwoActions)
                .variant("Inline controls", |s| s.banner = BannerCase::InlineControls)
                .variant("Long", |s| s.banner = BannerCase::Long)
                .variant("Focus", |s| {
                    s.rings = true;
                    s.banner = BannerCase::Notes;
                })
                .variant("Long link, smallest window", |s| {
                    s.banner = BannerCase::LongLink;
                    s.narrow = true;
                })
                .variant("Long failure, smallest window", |s| {
                    s.banner = BannerCase::LongFailure;
                    s.narrow = true;
                })
                .variant("Many failures, smallest window", |s| {
                    s.banner = BannerCase::ManyFailures;
                    s.narrow = true;
                }),
        )
        .story(
            Story::new("Library/Baseline", Library::default, baseline).variant("Rest", |s| {
                s.line = TextEdit::new("api-review");
                s.number = TextEdit::new("600");
            }),
        )
        .story(
            Story::new("Library/Field box", Library::default, field_boxes)
                .variant("Empty", |_| {})
                .variant("Filled", |s| {
                    s.line = TextEdit::new("api-review");
                    s.code = TextEdit::new("0 9 * * 1-5");
                    s.number = TextEdit::new("600");
                    s.area = TextEdit::new(
                        "Review the change against the plan.\nReport P0 and P1 only.",
                    );
                })
                .variant("Failed", |s| {
                    s.failed = true;
                    s.line = TextEdit::new("api review");
                    s.code = TextEdit::new("0 9 * * 8");
                    s.number = TextEdit::new("6O0");
                    s.area = TextEdit::new("Review the change against the plan.");
                })
                .variant("Disabled", |s| {
                    s.disabled = true;
                    s.line = TextEdit::new("api-review");
                    s.code = TextEdit::new("0 9 * * 1-5");
                    s.number = TextEdit::new("600");
                    s.area = TextEdit::new(
                        "Review the change against the plan.\nReport P0 and P1 only.",
                    );
                })
                .variant("Disabled empty", |s| s.disabled = true)
                .variant("Selected text", |s| {
                    s.line = TextEdit::new("api-review");
                    s.line.set_selection(0, 3);
                    s.line.focused = true;
                })
                .variant("Long text", |s| {
                    s.line = TextEdit::new(LONG);
                    s.code = TextEdit::new(LONG);
                    s.number = TextEdit::new("123456789012");
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
        let line = if s.long { col() } else { row().items_center() };
        door::gap(line, Space::S16).children(BUTTONS.iter().map(|&(look_key, look)| {
            let shown = match (look, s.long) {
                (Look::Primary, false) => "Save",
                (Look::Plain, false) => "Browse",
                (Look::Quiet, false) => "Undo",
                (Look::Danger, false) => "Remove",
                (Look::Primary, true) => "Save and restart every agent",
                (Look::Plain, true) => "Browse for a folder to watch",
                (Look::Quiet, true) => "Undo the last three changes",
                (Look::Danger, true) => "Remove this automation for good",
            };
            let button: El = Action::new(shown)
                .look(look)
                .size(size)
                .disabled(s.disabled)
                .on_press_tx(|_: &mut Library, _: &mut Tx| {})
                .build(&t)
                .key(format!("button.{look_key}.{size_key}"))
                .into();
            if s.narrow {
                el().w(NARROW_BUTTON).child(button)
            } else {
                button
            }
        }))
    });
    library_stage(&t, door::gap(col(), Space::S24).children(rows))
}

fn triggers(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let model = match (s.failed, s.long) {
        (true, _) => "Choose a model",
        (false, true) => "claude-opus-4-5-20251101-with-a-very-long-provider-suffix",
        (false, false) => "Opus 4.5",
    };
    let ink = if s.failed { t.warning } else { t.text_primary };
    let shown = |key: &str, look: TriggerLook| -> El {
        let content = door::gap(row().items_center().min_w(0.0), Space::S8)
            .child(words("Claude", Type::Small, t.text_secondary))
            .child(words(model, Type::Body, ink).truncate().shrink().min_w(0.0))
            .child(words("High", Type::Body, t.accent));
        Trigger::new(key, "Model", content)
            .look(look)
            .value(format!("Claude, {model}, High"))
            .open(s.open)
            .unset(s.failed)
            .disabled(s.disabled)
            .build(&t, |s: &mut Library, _: &mut Tx| s.open = !s.open)
            .into()
    };
    let column = door::gap(col(), Space::S16)
        .w(320.0)
        .child(shown("trigger.field", TriggerLook::Field))
        .child(shown("trigger.quiet", TriggerLook::Quiet));
    library_stage(&t, column)
}

/// The narrowest column a button is drawn in: a sidebar's footer.
const NARROW_BUTTON: f32 = 120.0;

/// One row of every control that sits on a line with others, so their edges and words line up.
fn baseline(s: &Library, cx: &mut Cx<Library>) -> El {
    let t = library_tokens(cx);
    let line = TextInput::new(&s.line)
        .key("baseline.line.input")
        .label("Name")
        .on_edit(|s: &mut Library| &mut s.line);
    let number = TextInput::new(&s.number)
        .key("baseline.number.input")
        .label("Width")
        .on_edit(|s: &mut Library| &mut s.number);
    let noop = |_: &mut Library, _: &mut Tx| {};
    let row = door::gap(row().key("baseline").items_center(), Space::S8)
        .child(
            Action::new("Save")
                .look(Look::Primary)
                .on_press_tx(noop)
                .build(&t)
                .key("baseline.primary")
                .into(),
        )
        .child(
            Action::new("Browse")
                .on_press_tx(noop)
                .build(&t)
                .key("baseline.plain")
                .into(),
        )
        .child(
            Action::new("Undo")
                .look(Look::Quiet)
                .on_press_tx(noop)
                .build(&t)
                .key("baseline.quiet")
                .into(),
        )
        .child(
            el().w(160.0).child(
                line_box(line, FieldState::Ready, &t)
                    .key("baseline.line")
                    .into(),
            ),
        )
        .child(
            number_box(number, FieldState::Ready, &t)
                .key("baseline.number")
                .into(),
        )
        .child(tag("baseline.tag", "Running", Tone::Good, &t).into());
    library_stage(&t, row)
}

fn field_boxes(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let field = match (s.disabled, s.failed) {
        (true, _) => FieldState::Disabled,
        (false, failed) => FieldState::failed_if(failed),
    };
    let line = TextInput::new(&s.line)
        .key("field.line.input")
        .label("Name")
        .placeholder("Automation name")
        .on_edit(|s: &mut Library| &mut s.line);
    let code = TextInput::new(&s.code)
        .key("field.code.input")
        .label("Cron")
        .placeholder("0 9 * * 1-5")
        .on_edit(|s: &mut Library| &mut s.code);
    let number = TextInput::new(&s.number)
        .key("field.number.input")
        .label("Sidebar width")
        .placeholder("240")
        .on_edit(|s: &mut Library| &mut s.number);
    let area = rune_kit::TextArea::new(&s.area)
        .key("field.area.input")
        .label("Prompt")
        .placeholder("What the agent does each run")
        .rows(3)
        .on_edit(|s: &mut Library| &mut s.area);
    let column = door::gap(col(), Space::S16)
        .w(if s.narrow { 160.0 } else { 280.0 })
        .child(line_box(line, field, &t).key("field.line").into())
        .child(code_box(code, field, &t).key("field.code").into())
        .child(number_box(number, field, &t).key("field.number").into())
        .child(area_box(area, field, &t).key("field.area").into());
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
        door::gap(rune::row(), Space::S8)
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

/// What a banner story's variant puts in each banner.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum BannerCase {
    #[default]
    Rest,
    NoAction,
    SentenceOnly,
    Notes,
    Countdown,
    TwoActions,
    InlineControls,
    Long,
    /// A restart waiting on two agents named by a worktree's basename and by a 40-character
    /// rename in kanji and kana.
    LongLink,
    /// A self-update that failed to stage one outpost with a long host name.
    LongFailure,
    /// A self-update that failed to stage three outposts.
    ManyFailures,
}

/// The tones a banner story draws, each keyed `banner.{tone}`, with its sentence at
/// `banner.{tone}.text`, its first note at `banner.{tone}.note.0`, its first action at
/// `banner.{tone}.action` and its dismiss at `banner.{tone}.dismiss`.
pub const BANNERS: [(&str, BannerTone); 4] = [
    ("warn", BannerTone::Warn),
    ("calm", BannerTone::Calm),
    ("good", BannerTone::Good),
    ("bad", BannerTone::Bad),
];

/// The smallest main window's width, which a top banner spans.
const BANNER_WIDTH: f32 = 800.0;

/// The largest text size the app offers, at which the smallest window lays out
/// `BANNER_WIDTH / LARGEST_TEXT` points across.
const LARGEST_TEXT: f32 = 1.5;

const CODEX_WAITING: &str = "Codex 0.163.0 installed. Restarts when agents are idle.";
const WORKTREE_BASENAME: &str =
    "attn--fix-native-terminal-focus-when-reopening-an-old-codex-session-in-another-desktop";
const LONG_RENAME: &str =
    "Überprüfung-der-Freigabeanfragen-nach-dem-Neustart-des-gemeinsamen-Codex-Agenten";
const LONG_HOST: &str = "build-linux-primary.us-east-1.example.dev";
const NO_SPACE: &str = "cat: write error: No space left on device";
const UPDATE_LOG: &str = "Log: /Users/sam/.attn/update/run-20261119.log";
const MANY_FAILURES: [(&str, &str); 3] = [
    (LONG_HOST, NO_SPACE),
    (
        "ci-runner-07.build-and-validation.platform.example.org",
        "ssh: connect to host ci-runner-07.build-and-validation.platform.example.org port 22: \
         Connection timed out",
    ),
    (
        "studio",
        "attn --version exited with status 127: zsh:1: command not found: attn",
    ),
];

const LONG_SENTENCE: &str = "Codex 0.163.0 didn’t start: the app server exited with status 1 \
    before it answered its first request, after loading a configuration it could not read. Still \
    on 0.162.1, nothing was interrupted.";

/// A self-update's failure to stage `outposts`, each with why, as the daemon words it.
fn failed(outposts: &[(&str, &str)]) -> String {
    let each: Vec<String> = outposts
        .iter()
        .map(|(host, why)| format!("{host}: {why}"))
        .collect();
    format!(
        "could not stage {}. attn did not restart. {UPDATE_LOG}",
        each.join("; ")
    )
}

fn banners(s: &Library, cx: &mut Cx<Library>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let t = library_tokens(cx);
    let drawn = BANNERS.iter().map(|&(name, tone)| {
        let (sentence, action, notes): (&str, &str, &[&str]) = match tone {
            BannerTone::Warn => (
                "ai-sandbox runs a different build than home.",
                "Update outposts",
                &[
                    "ai-sandbox: Connecting.",
                    "studio: Building attn for Linux.",
                ],
            ),
            BannerTone::Calm => (
                "Updating outposts.",
                "Updating…",
                &["ai-sandbox: Copying it over.", "studio: Checking it runs."],
            ),
            BannerTone::Good => (
                "Updated ai-sandbox and studio.",
                "Show what changed",
                &["Both outposts run the home’s build."],
            ),
            BannerTone::Bad => (
                "Couldn’t update ai-sandbox.",
                "Retry",
                &["ssh: connect to host ai-sandbox port 22: Connection timed out"],
            ),
        };
        let case = s.banner;
        let key = format!("banner.{name}");
        let (sentence, action) = match case {
            BannerCase::Long => (LONG_SENTENCE.to_owned(), action),
            BannerCase::LongLink => (CODEX_WAITING.to_owned(), "Restart now"),
            BannerCase::LongFailure => (failed(&[(LONG_HOST, NO_SPACE)]), "Retry"),
            BannerCase::ManyFailures => (failed(&MANY_FAILURES), "Retry"),
            _ => (sentence.to_owned(), action),
        };
        let mut banner = Banner::new(key.clone(), format!("Banner {name}"), tone)
            .says(format!("{key}.text"), sentence)
            .details(|_: &mut Library, _: &mut rune::Tx| {});
        if matches!(
            case,
            BannerCase::Notes | BannerCase::TwoActions | BannerCase::Long
        ) {
            for (i, note) in notes.iter().enumerate() {
                banner = banner.note(format!("{key}.note.{i}"), *note);
            }
        }
        if case == BannerCase::InlineControls {
            banner = banner
                .note(format!("{key}.waiting"), "Waiting on")
                .link(
                    format!("{key}.go.a"),
                    "working agent",
                    "Go to working agent",
                    noop,
                )
                .note(format!("{key}.reason.a"), "(working),")
                .item()
                .link(
                    format!("{key}.go.b"),
                    "docs-sweep",
                    "Go to docs-sweep",
                    noop,
                )
                .note(format!("{key}.reason.b"), "(waiting on an approval),")
                .item()
                .link(
                    format!("{key}.go.c"),
                    "plan-cache",
                    "Go to plan-cache",
                    noop,
                )
                .note(
                    format!("{key}.reason.c"),
                    "(still starting or asking whether to trust its folder).",
                )
                .note_button(
                    format!("{key}.relaunch"),
                    "Relaunch waiting_input agent",
                    Action::new("Relaunch"),
                    noop,
                );
        }
        if case == BannerCase::LongLink {
            banner = banner
                .note(format!("{key}.waiting"), "Waiting on")
                .link(
                    format!("{key}.go.a"),
                    WORKTREE_BASENAME,
                    format!("Go to {WORKTREE_BASENAME}"),
                    noop,
                )
                .note(
                    format!("{key}.reason.a"),
                    "(still starting or asking whether to trust its folder),",
                )
                .note_button(
                    format!("{key}.relaunch"),
                    format!("Relaunch {WORKTREE_BASENAME}"),
                    Action::new("Relaunch"),
                    noop,
                )
                .item()
                .link(
                    format!("{key}.go.b"),
                    LONG_RENAME,
                    format!("Go to {LONG_RENAME}"),
                    noop,
                )
                .note(format!("{key}.reason.b"), "(working).");
        }
        let acts = !matches!(
            case,
            BannerCase::NoAction | BannerCase::SentenceOnly | BannerCase::Countdown
        );
        if acts {
            banner = banner.action(
                format!("{key}.action"),
                Action::new(action).disabled(tone == BannerTone::Calm),
                noop,
            );
        }
        let second = match case {
            BannerCase::TwoActions => Some("Update without ai-sandbox".to_owned()),
            BannerCase::LongFailure => Some(format!("Update without {LONG_HOST}")),
            BannerCase::ManyFailures => {
                Some(format!("Update without {} outposts", MANY_FAILURES.len()))
            }
            _ => None,
        };
        if let Some(second) = second {
            banner = banner.action(format!("{key}.second"), Action::new(second), noop);
        }
        if case == BannerCase::Countdown {
            banner = banner.countdown(format!("{key}.countdown"), 5);
        }
        if case != BannerCase::SentenceOnly {
            banner = banner.dismiss(
                format!("{key}.dismiss"),
                format!("Dismiss {name}"),
                |_: &mut Library| {},
            );
        }
        banner.build(&t, cx)
    });
    let width = if s.narrow {
        BANNER_WIDTH / LARGEST_TEXT
    } else {
        BANNER_WIDTH
    };
    library_stage(&t, door::gap(col().w(width), Space::S16).children(drawn))
}

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
            switch(
                "switch.off",
                "Delegation",
                false,
                Beside::Nothing,
                s.disabled,
                &t,
                noop,
            )
            .into(),
            switch(
                "switch.on",
                "Delegation",
                true,
                Beside::Nothing,
                s.disabled,
                &t,
                noop,
            )
            .into(),
            switch(
                "switch.word",
                "Telemetry",
                s.on,
                Beside::State,
                s.disabled,
                &t,
                |s: &mut Library, _: &mut Tx| s.on = !s.on,
            )
            .into(),
            switch(
                "switch.caption",
                "YOLO",
                s.on,
                Beside::Label,
                s.disabled,
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
    let track = |key: &str, label: &str, names: &[&'static str]| -> El {
        Segmented::new(key, label)
            .options(names.iter().enumerate().map(|(i, name)| {
                Segment::new(format!("segment{i}"), *name)
                    .chosen(i == s.chosen)
                    .on_press_tx(pick(i))
            }))
            .disabled(s.disabled)
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
            Segment::new("segment4", "Gemini")
                .missing(true)
                .help("Gemini is not installed on build-and-release")
                .on_press_tx(pick(4)),
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
        .disabled(s.disabled)
        .build(&t)
        .into();
    let column = door::gap(col().key("segmented.column").items_start(), Space::S24)
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
        ))
        .child(
            Segmented::new("segmented.start", "Start from")
                .stacked()
                .options(START_POINTS.iter().enumerate().map(|(i, name)| {
                    Segment::new(format!("segment{i}"), *name)
                        .cut_middle()
                        .chosen(i == s.chosen)
                        .on_press_tx(pick(i))
                }))
                .disabled(s.disabled)
                .build(&t)
                .into(),
        )
        .child(
            door::gap(
                row().key("segmented.row").w_full().items_center(),
                Space::S16,
            )
            .child(
                words(
                    "Used for new sessions and opening PRs.",
                    Type::Small,
                    t.text_tertiary,
                )
                .key("segmented.description")
                .wrap()
                .grow(1.0)
                .min_w(0.0),
            )
            .child(track(
                "segmented.beside",
                "Default agent",
                &["Claude", "Codex"],
            )),
        );
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
                "100%",
                s.disabled,
                &t,
                noop,
                noop,
            )
            .into(),
            stepper(
                "stepper.match",
                "terminal font size",
                "Match app",
                s.disabled,
                &t,
                noop,
                noop,
            )
            .into(),
            number_box(number, FieldState::disabled_if(s.disabled), &t)
                .key("stepper.number")
                .into(),
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
            s.disabled,
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
            tag("tag.accent", said("Recommended"), Tone::Accent, &t).into(),
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
            El::from(icon_box(
                icon(&crate::icon::GARDEN, IconSize::Regular, t.text_primary),
                false,
                &t,
            ))
            .key("icon.glyph"),
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
    let group = if s.many {
        checks("choices", "Agent")
    } else {
        choices("choices", "Agent")
    };
    let list =
        group
            .w(if s.narrow { 220.0 } else { 420.0 })
            .children(options.iter().enumerate().map(|(i, (name, note))| {
                let choice = Choice::new(format!("choice.{i}"), *name)
                    .note(*note)
                    .many(s.many)
                    .chosen(if s.many { i != 1 } else { i == s.chosen })
                    .disabled(s.disabled);
                let choice = if s.open && i == 0 {
                    choice.tag(tag(
                        "choice.0.recommended",
                        "Recommended",
                        Tone::Neutral,
                        &t,
                    ))
                } else {
                    choice
                };
                choice
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
        s.disabled,
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
        vec![
            crate::system::Said::from("Worktree"),
            crate::system::Said::from("2 env"),
        ]
    };
    stack(
        &t,
        [disclosure(
            "disclosure",
            "Advanced",
            s.open,
            None,
            &chips,
            s.failed,
            s.disabled,
            &t,
            |s: &mut Library, _: &mut Tx| s.open = !s.open,
        )
        .into()],
    )
}
