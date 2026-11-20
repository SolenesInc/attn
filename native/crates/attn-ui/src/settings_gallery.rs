//! The gallery's stories for the blocks the Settings pages added: one story per block, a variant
//! per state.

use rune::{Cx, El, Playground, Story};

use crate::block_gallery::{Blocks, common, noop, stage, tokens};
use crate::blocks::{Block, Facts, Inline, Prose, Section, SettingRow, pill};
use crate::system::{Beside, Button, Look, Size, Tone, Type, switch, words};

/// Every Settings block story, by name, with the keys of what it draws: what the state sheets
/// and the rule tests read.
pub const SETTINGS_SUBJECTS: &[(&str, &[&str])] = &[
    ("Blocks/Facts", &["facts", "facts.tiles"]),
    ("Blocks/Section with aside", &["aside.section"]),
    ("Blocks/Pill", &["pill.route", "pill.short"]),
];

/// The variants each story adds to the common ones.
pub const SETTINGS_VARIANTS: &[(&str, &[&str])] = &[
    ("Blocks/Facts", &[]),
    ("Blocks/Section with aside", &["One on"]),
    ("Blocks/Pill", &[]),
];

const LONG_PATH: &str = "/Users/you/projects/victor/attn--a-worktree-with-a-long-name/native/target/release/attn-daemon";

pub fn stories<S: 'static>(playground: Playground<S>) -> Playground<S> {
    playground
        .story(common(Story::new("Blocks/Facts", Blocks::default, facts)))
        .story(
            common(Story::new(
                "Blocks/Section with aside",
                Blocks::default,
                aside,
            ))
            .variant("One on", |s| s.on = true),
        )
        .story(common(Story::new("Blocks/Pill", Blocks::default, pills)))
}

fn pills(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let model = if s.long {
        "anthropic.claude-opus-5-5-20261101-v1:0-with-extended-thinking-eu-north-1"
    } else {
        "Opus 5.5"
    };
    let route = pill(
        [
            words("Claude Code", Type::Small, t.text_secondary),
            words(model, Type::Small, t.text_primary)
                .wrap_anywhere()
                .shrink()
                .min_w(0.0),
            words("· deep", Type::Small, t.text_tertiary),
            words("high", Type::Small, t.accent),
        ],
        &t,
    )
    .key("pill.route");
    let short = pill(
        [
            words("Codex", Type::Small, t.text_secondary),
            words("gpt-6-sol", Type::Small, t.text_primary),
        ],
        &t,
    )
    .key("pill.short");
    let blocks = vec![
        Block::custom("pill.route.slot", Inline::loose().child(route)),
        Block::custom("pill.short.slot", Inline::loose().child(short)),
    ];
    stage(s, cx, &t, blocks)
}

fn aside(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let on = usize::from(s.on);
    let row =
        |key: &str, name: &'static str, summary: &'static str, note: Option<&str>, on: bool| {
            SettingRow::new(
                key.to_owned(),
                name,
                summary,
                switch(
                    format!("{key}.switch"),
                    name,
                    on,
                    Beside::Nothing,
                    false,
                    &t,
                    noop,
                ),
            )
            .note(note.map(|n| words(n.to_owned(), Type::Small, t.text_muted).wrap()))
            .build(&t)
        };
    let blocks = vec![
        Section::new("aside.section", if s.long { "Connections between this home and the machines it reaches" } else { "Connections" })
            .aside(format!("{on} of 2 on"))
            .push(row(
                "aside.outposts",
                "Outposts",
                "Connect this home to attn on other computers.",
                None,
                s.on,
            ))
            .push(row(
                "aside.phones",
                "Phone access",
                "Let phones you pair see this home's agents.",
                Some(if s.long {
                    "Turning it off disconnects every phone, including the ones paired through the relay and the ones waiting to finish pairing."
                } else {
                    "Turning it off disconnects every phone."
                }),
                false,
            ))
            .build(&t),
    ];
    stage(s, cx, &t, blocks)
}

fn facts(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let build = if s.long {
        LONG_PATH
    } else {
        "0.13.0 (abcdef123)"
    };
    let blocks = vec![
        Facts::new("facts")
            .fact("facts.install", "Installed as", "Release app")
            .code("facts.build", "Build", build)
            .fact("facts.host", "Shared terminal host", "Not installed")
            .fact(
                "facts.available",
                "Available",
                if s.long {
                    "Unknown. The last check failed: could not reach the release feed at https://example.com/releases/latest.json"
                } else {
                    "Up to date"
                },
            )
            .item(
                "facts.path",
                "Log",
                Inline::loose()
                    .child(words(if s.long { LONG_PATH } else { "/tmp/run-1.log" }, Type::Mono, t.text_primary).wrap_anywhere().shrink())
                    .child(Button::new("Copy").size(Size::Compact).on_press_tx(noop).build(&t).key("facts.path.copy").into()),
            )
            .control(
                "facts.folder",
                "Folder",
                Inline::loose()
                    .child(Button::new("~/projects/reviewer").look(Look::Quiet).on_press_tx(noop).build(&t).key("facts.folder.copy").into()),
            )
            .build(&t),
        Facts::new("facts.tiles")
            .tiles()
            .code("facts.tiles.events", "Events", if s.long { "1,048,576" } else { "1,204" })
            .code("facts.tiles.weight", "Weight", "3.2 MB")
            .code(
                "facts.tiles.seq",
                "Seq",
                if s.long { "1,048,576–1,049,000" } else { "1–1,204" },
            )
            .code("facts.tiles.oldest", "Oldest", "2 days")
            .code("facts.tiles.unreachable", "Unreachable", "1 of 3")
            .tone(Some(Tone::Warn))
            .build(&t),
        Prose::hint("facts.hint", "Add hosts with `gh auth login --hostname <host>`.").build(&t),
        Prose::hint("facts.code", "This profile uses:")
            .code(if s.long { LONG_PATH } else { "/Users/me/notes `draft`" })
            .build(&t),
    ];
    stage(s, cx, &t, blocks)
}
