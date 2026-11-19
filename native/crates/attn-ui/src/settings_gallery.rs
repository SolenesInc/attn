//! The gallery's stories for the blocks the Settings pages added: one story per block, a variant
//! per state.

use rune::{Cx, El, Playground, Story};

use crate::block_gallery::{Blocks, common, noop, stage, tokens};
use crate::blocks::{Facts, Prose, Section, SettingRow};
use crate::system::{Type, switch, words};

/// Every Settings block story, by name, with the keys of what it draws: what the state sheets
/// and the rule tests read.
pub const SETTINGS_SUBJECTS: &[(&str, &[&str])] = &[
    ("Blocks/Facts", &["facts"]),
    ("Blocks/Section with aside", &["aside.section"]),
];

/// The variants each story adds to the common ones.
pub const SETTINGS_VARIANTS: &[(&str, &[&str])] = &[
    ("Blocks/Facts", &[]),
    ("Blocks/Section with aside", &["One on"]),
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
}

fn aside(s: &Blocks, cx: &mut Cx<Blocks>) -> El {
    let t = tokens(cx);
    let on = usize::from(s.on);
    let row =
        |key: &str, name: &'static str, summary: &'static str, note: Option<&str>, on: bool| {
            SettingRow::new(
                name,
                summary,
                switch(format!("{key}.switch"), name, on, false, false, &t, noop),
            )
            .key(key.to_owned())
            .note(note.map(|n| words(n, Type::Small, t.text_muted).wrap()))
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
        Facts::new()
            .key("facts")
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
            .build(&t),
        Prose::hint("facts.hint", "Add hosts with `gh auth login --hostname <host>`.").build(&t),
    ];
    stage(s, cx, &t, blocks)
}
