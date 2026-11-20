//! The gallery's stories for the controls the app's chrome is built from: list rows, icon
//! buttons, chips and group heads. One story per control, a variant per state.

use rune::{Cx, El, Playground, Story, Tx, col, el};

use crate::Tokens;
use crate::colors::scheme_for;
use crate::icon;
use crate::system::{
    GroupHead, IconButton, IconSize, ListRow, Reveal, Size, Space, Tone, count_badge, door,
    icon_button, number_chip, tag,
};

/// What a chrome story shows: the state its variant sets.
#[derive(Default)]
pub struct Chrome {
    pub rings: bool,
    pub lit: bool,
    pub open: bool,
    pub long: bool,
    pub narrow: bool,
    pub disabled: bool,
    pub presses: u32,
}

/// How wide the sidebar's rows are: the sidebar at its default width and at its narrowest, less
/// its inset on both sides.
const WIDE: f32 = 240.0 - 2.0 * 6.0;
const NARROW: f32 = 200.0 - 2.0 * 6.0;

/// The longest names a row meets: a session named after its worktree's folder, and a desktop
/// named by a person.
const LONG_AGENT: &str = "attn--ds-page-blocks-delegation-appearance-landing-two";
const LONG_DESKTOP: &str = "Design system review with the Orchestrator and five Builders";

/// One sheet a state sheet draws from a chrome story: its name, its story, the keys it draws in
/// rows, and its columns as label, variant, gesture (`""`, `"hover"`, `"press"`, `"tab"`) and
/// the suffix of the key the gesture lands on.
pub struct ChromeSheet {
    pub name: &'static str,
    pub story: &'static str,
    pub keys: &'static [&'static str],
    pub columns: &'static [(&'static str, &'static str, &'static str, &'static str)],
}

const PRESSABLE: &[(&str, &str, &str, &str)] = &[
    ("Rest", "Rest", "", ""),
    ("Hover", "Rest", "hover", ""),
    ("Pressed", "Rest", "press", ""),
    ("Focus", "Rest", "tab", ""),
    ("In view", "In view", "", ""),
    ("In view, hover", "In view", "hover", ""),
    ("In view, focus", "In view", "tab", ""),
    ("Long", "Long", "", ""),
    ("Long, hover", "Long", "hover", ""),
    ("Narrow long", "Narrow long", "", ""),
];

pub const CHROME_SHEETS: &[ChromeSheet] = &[
    ChromeSheet {
        name: "chrome-list-row",
        story: "Chrome/List row",
        keys: &["row.agent", "row.nav", "row.tile", "row.two"],
        columns: PRESSABLE,
    },
    ChromeSheet {
        name: "chrome-list-row-kinds",
        story: "Chrome/List row",
        keys: &[
            "row.select",
            "row.keyed",
            "row.quiet",
            "row.crew",
            "row.tree",
            "row.dimmed",
            "row.still",
            "row.wrap",
        ],
        columns: &[
            ("Rest", "Rest", "", ""),
            ("Hover", "Rest", "hover", ""),
            ("Pressed", "Rest", "press", ""),
            ("Focus", "Rest", "tab", ""),
            ("Selected", "In view", "", ""),
            ("Selected, focus", "In view", "tab", ""),
            ("Long", "Long", "", ""),
            ("Narrow long", "Narrow long", "", ""),
            ("Disabled", "Disabled", "", ""),
        ],
    },
    ChromeSheet {
        name: "chrome-icon-button",
        story: "Chrome/Icon button",
        keys: &[
            "icon.18-in-26",
            "icon.mark-18-in-26",
            "icon.16-in-32",
            "icon.18-counted",
            "icon.18-dot",
            "icon.14-keys",
        ],
        columns: &[
            ("Rest", "Rest", "", ""),
            ("Hover", "Rest", "hover", ""),
            ("Pressed", "Rest", "press", ""),
            ("Focus", "Rest", "tab", ""),
            ("Open", "Open", "", ""),
            ("+ hover", "Open", "hover", ""),
            ("+ focus", "Open", "tab", ""),
            ("Disabled", "Disabled", "", ""),
            ("128", "Long", "", ""),
        ],
    },
    ChromeSheet {
        name: "chrome-group-head",
        story: "Chrome/Group head",
        keys: &[
            "head.current",
            "head.named",
            "head.empty",
            "head.section",
            "head.toggle",
        ],
        columns: &[
            ("Rest", "Rest", "", ""),
            ("Hover", "Rest", "hover", ""),
            ("Pressed", "Rest", "press", ""),
            ("Focus", "Rest", "tab", ""),
            ("Open", "Open", "", ""),
            ("Disabled", "Disabled", "", ""),
            ("Long", "Long", "", ""),
            ("Narrow long", "Narrow long", "", ""),
        ],
    },
    ChromeSheet {
        name: "chrome-chip",
        story: "Chrome/Chip",
        keys: &[
            "chip.current",
            "chip.plain",
            "chip.empty",
            "chip.wide",
            "chip.count",
            "chip.count.wide",
            "chip.dots",
            "chip.pill",
            "chip.keys",
        ],
        columns: &[("Rest", "Rest", "", "")],
    },
];

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground
        .story(
            Story::new("Chrome/List row", Chrome::default, list_rows)
                .variant("Rest", |_| {})
                .variant("Disabled", |s| s.disabled = true)
                .variant("In view", |s| s.lit = true)
                .variant("Long", |s| s.long = true)
                .variant("Narrow long", |s| {
                    s.narrow = true;
                    s.long = true;
                }),
        )
        .story(
            Story::new("Chrome/Icon button", Chrome::default, icon_buttons)
                .variant("Rest", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Disabled", |s| s.disabled = true)
                .variant("Long", |s| s.long = true),
        )
        .story(
            Story::new("Chrome/Group head", Chrome::default, group_heads)
                .variant("Rest", |_| {})
                .variant("Open", |s| s.open = true)
                .variant("Disabled", |s| s.disabled = true)
                .variant("Long", |s| s.long = true)
                .variant("Narrow long", |s| {
                    s.narrow = true;
                    s.long = true;
                }),
        )
        .story(Story::new("Chrome/Chip", Chrome::default, chips).variant("Rest", |_| {}))
}

fn tokens(cx: &Cx<Chrome>) -> Tokens {
    scheme_for(None).colors(cx.system_dark()).ui
}

fn press(s: &mut Chrome, _: &mut Tx) {
    s.presses += 1;
}

/// The controls in a column as wide as the sidebar's rows, on the sidebar's surface.
fn stage(s: &Chrome, cx: &mut Cx<Chrome>, t: &Tokens, items: Vec<El>) -> El {
    if s.rings {
        cx.force_focus_rings();
    }
    let width = if s.narrow { NARROW } else { WIDE };
    el().key("stage.presses")
        .label(s.presses.to_string())
        .w_full()
        .h_full()
        .fill(t.bg_panel)
        .child(
            door::inset(col(), Space::S24, Space::S24)
                .child(door::gap(col(), Space::S12).w(width).children(items)),
        )
}

fn dot(t: &Tokens) -> El {
    door::corner(
        el().size(8.0, 8.0).fill(t.state_waiting),
        crate::system::Radius::Round,
    )
}

fn row_controls(key: &str) -> Vec<IconButton> {
    vec![
        icon_button(
            format!("{key}.rename"),
            "Rename",
            &icon::PENCIL,
            IconSize::Small,
        )
        .on_press_tx(press),
        icon_button(
            format!("{key}.close"),
            "Close",
            crate::glyph::TIMES,
            IconSize::Small,
        )
        .on_press_tx(press),
    ]
}

fn list_rows(s: &Chrome, cx: &mut Cx<Chrome>) -> El {
    let t = tokens(cx);
    let name = if s.long { LONG_AGENT } else { "api-review" };
    let rows = vec![
        ListRow::new("row.agent", format!("Open {name}"))
            .lead(dot(&t))
            .title(name)
            .aside(crate::system::words(
                "5m",
                crate::system::Type::Small,
                t.text_tertiary,
            ))
            .badge(tag("row.agent.tag", "Builder", Tone::Neutral, &t))
            .hover_controls(row_controls("row.agent"))
            .in_view(s.lit)
            .disabled(s.disabled)
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.nav", "Home")
            .lead(crate::system::icon(
                &icon::HOME,
                IconSize::Regular,
                t.text_secondary,
            ))
            .quiet_title(if s.long {
                "Desktop overview of every desktop"
            } else {
                "Home"
            })
            .keys(Some("⌘⇧H".to_owned()), &t)
            .current(s.lit)
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.tile", "Open notes.md")
            .lead(crate::system::icon(
                &icon::MARKDOWN,
                IconSize::Regular,
                t.text_tertiary,
            ))
            .title(if s.long { LONG_DESKTOP } else { "notes.md" })
            .aside(tag("row.tile.tag", "Doc", Tone::Neutral, &t))
            .hover_controls(row_controls("row.tile"))
            .in_view(s.lit)
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.keyed", format!("Open {name}"))
            .lead(dot(&t))
            .title(name)
            .hover_controls([
                icon_button("row.keyed.reload", "Reload", &icon::PENCIL, IconSize::Small)
                    .keys(Some("⌘⇧R".to_owned()))
                    .on_press_tx(press),
                icon_button("row.keyed.asks", "Asks", &icon::ASKS, IconSize::Small)
                    .count(Some("3".to_owned()))
                    .on_press_tx(press),
            ])
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.two", "Open the delegation chain")
            .lead(dot(&t))
            .title(if s.long { LONG_AGENT } else { "chrome" })
            .subtitle(
                if s.long {
                    LONG_DESKTOP
                } else {
                    "Builder · `ai-sandbox`"
                },
                &t,
            )
            .hover_controls(row_controls("row.two"))
            .in_view(s.lit)
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.select", "Nightly triage")
            .lead(dot(&t))
            .title(if s.long {
                LONG_DESKTOP
            } else {
                "Nightly triage"
            })
            .subtitle(
                if s.long {
                    LONG_AGENT
                } else {
                    "Every day at 09:00"
                },
                &t,
            )
            .tag(tag("row.select.tag", "Failed", Tone::Bad, &t))
            .controls(
                [crate::system::Button::new("Run now")
                    .size(Size::Compact)
                    .on_press_tx(press)
                    .build(&t)
                    .key("row.select.run")
                    .into()],
                Reveal::Always,
            )
            .hover_controls(row_controls("row.select"))
            .select("row.select.open", "Open Nightly triage", press)
            .selected(s.lit)
            .disabled(s.disabled)
            .build(&t),
        ListRow::new("row.quiet", "Session api-review")
            .title(if s.long { LONG_AGENT } else { "api-review" })
            .subtitle("`~/projects/victor/attn--api-review`", &t)
            .controls(
                row_controls("row.quiet")
                    .into_iter()
                    .map(|b| b.build(&t).into()),
                Reveal::Quiet,
            )
            .selected(s.lit)
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.tree", "Open notes")
            .depth(2)
            .lead(crate::system::icon(
                &icon::FOLDER,
                IconSize::Regular,
                t.text_tertiary,
            ))
            .title(if s.long { LONG_DESKTOP } else { "notes" })
            .selected(s.lit)
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.crew", "Start Archivist")
            .lead(dot(&t))
            .title(if s.long { LONG_AGENT } else { "Archivist" })
            .hover_controls([
                icon_button("row.crew.actions", "Actions", "•••", IconSize::Small)
                    .on_press_tx(press),
            ])
            .controls(
                [
                    icon_button("row.crew.start", "Start", &icon::CREW_SUN, IconSize::Small)
                        .on_press_tx(press)
                        .build(&t)
                        .into(),
                ],
                Reveal::Always,
            )
            .selected(s.lit)
            .build(&t)
            .on_press_tx(press),
        ListRow::new("row.still", "Split right")
            .lead(dot(&t))
            .title("Split right")
            .subtitle(
                if s.long {
                    "Opens the focused tile's terminal beside it on the same desktop"
                } else {
                    "Beside the focused tile"
                },
                &t,
            )
            .aside(crate::system::keycaps(["⌘", "D"], &t))
            .still()
            .build(&t),
        ListRow::new("row.wrap", "docs-pass")
            .lead(dot(&t).key("row.wrap.lead"))
            .title("docs-pass")
            .subtitle(
                if s.long {
                    "Turn running. Gets up to 60s to finish, then it is stopped."
                } else {
                    "Idle."
                },
                &t,
            )
            .still()
            .wrap()
            .build(&t),
        ListRow::new("row.dimmed", "Closed session docs-pass")
            .lead(dot(&t))
            .title(if s.long { LONG_AGENT } else { "docs-pass" })
            .aside(crate::system::words(
                "2h",
                crate::system::Type::Small,
                t.text_tertiary,
            ))
            .dimmed(true)
            .selected(s.lit)
            .build(&t)
            .on_press_tx(press),
    ];
    stage(s, cx, &t, rows)
}

fn icon_buttons(s: &Chrome, cx: &mut Cx<Chrome>) -> El {
    let t = tokens(cx);
    let count = if s.long { "128" } else { "3" };
    let button = |key: &str, glyph: crate::Glyph, size: IconSize| {
        icon_button(key, key, glyph, size)
            .expanded(s.open)
            .disabled(s.disabled)
            .on_press_tx(press)
    };
    let items = vec![
        button("icon.18-in-26", (&icon::BELL).into(), IconSize::Large)
            .build(&t)
            .into(),
        button(
            "icon.mark-18-in-26",
            crate::glyph::TIMES.into(),
            IconSize::Large,
        )
        .build(&t)
        .into(),
        button("icon.16-in-32", (&icon::PENCIL).into(), IconSize::Regular)
            .size(Size::Regular)
            .build(&t)
            .into(),
        button("icon.18-counted", (&icon::ASKS).into(), IconSize::Large)
            .count(Some(count.to_owned()))
            .build(&t)
            .into(),
        button("icon.18-dot", (&icon::BELL).into(), IconSize::Large)
            .dot(Some(if s.long { t.error } else { t.accent }))
            .build(&t)
            .into(),
        button("icon.14-keys", (&icon::PENCIL).into(), IconSize::Small)
            .keys(Some(if s.long { "⌘⇧⌫" } else { "R" }.to_owned()))
            .build(&t)
            .into(),
    ];
    stage(
        s,
        cx,
        &t,
        items.into_iter().map(|e: El| e.self_start()).collect(),
    )
}

fn group_heads(s: &Chrome, cx: &mut Cx<Chrome>) -> El {
    let t = tokens(cx);
    let name = if s.long { LONG_DESKTOP } else { "Reviews" };
    let heads = vec![
        GroupHead::new("head.current", "Open Desktop 1")
            .mark(number_chip(1, true, false, &t))
            .keys(Some("⌃1".to_owned()))
            .build(&t)
            .on_press_tx(press),
        GroupHead::new("head.named", format!("Open {name}"))
            .mark(number_chip(2, false, false, &t))
            .name(name)
            .keys(Some("⌃2".to_owned()))
            .build(&t)
            .on_press_tx(press),
        GroupHead::new("head.empty", "Open Desktop 12")
            .mark(number_chip(12, false, true, &t))
            .controls(row_controls("head.empty"))
            .disabled(s.disabled)
            .build(&t)
            .on_press_tx(press),
        GroupHead::new("head.section", "Crew")
            .heading()
            .section("Crew")
            .trailing(
                crate::system::Button::new("manage")
                    .look(crate::system::Look::Quiet)
                    .size(Size::Compact)
                    .on_press_tx(press)
                    .build(&t)
                    .into(),
            )
            .build(&t),
        GroupHead::new("head.toggle", "2 more agents")
            .open(s.open)
            .section(if s.long {
                LONG_DESKTOP
            } else {
                "2 more agents"
            })
            .keys(Some("⌘⇧A".to_owned()))
            .disabled(s.disabled)
            .build(&t)
            .on_press_tx(press),
    ];
    stage(s, cx, &t, heads)
}

/// Every state dot: filled, hollow and square, small and regular.
fn dots(t: &Tokens) -> El {
    use crate::system::{DotShape, DotSize, dot};
    door::gap(rune::row().items_center(), Space::S8).children(
        [DotSize::Small, DotSize::Regular]
            .into_iter()
            .flat_map(|size| {
                [DotShape::Filled, DotShape::Hollow, DotShape::Square]
                    .map(|shape| dot(t.state_working, shape, size))
            }),
    )
}

fn chips(s: &Chrome, cx: &mut Cx<Chrome>) -> El {
    let t = tokens(cx);
    let items = vec![
        number_chip(1, true, false, &t).key("chip.current"),
        number_chip(2, false, false, &t).key("chip.plain"),
        number_chip(3, false, true, &t).key("chip.empty"),
        number_chip(12, false, false, &t).key("chip.wide"),
        count_badge(3, t.warning, crate::warning_ink(&t)).key("chip.count"),
        count_badge(128, t.warning, crate::warning_ink(&t)).key("chip.count.wide"),
        dots(&t).key("chip.dots"),
        crate::system::pill("chip.pill", "Set up ⏎", &t),
        crate::system::keys_hint(["⌘", "⇧", "K"], "Open the palette", &t).key("chip.keys"),
    ];
    stage(
        s,
        cx,
        &t,
        items.into_iter().map(|e| e.self_start()).collect(),
    )
}
