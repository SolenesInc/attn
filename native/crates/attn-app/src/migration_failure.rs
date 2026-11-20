//! The screen the app opens on when the daemon cannot start because a data migration failed:
//! the marker's facts, to hand to an agent outside attn, and a way to copy them.

use attn_client::MigrationMarker;
use attn_ui::Tokens;
use attn_ui::blocks::{Block, Head, Inline, Page, Stack};
use attn_ui::system::{Button, Size, Type, words};
use rune::{El, Tx};

use crate::State;

const KNOWN_FACTS: [(&str, &str); 10] = [
    ("daemon_log_path", "Daemon log"),
    ("data_dir", "Data directory"),
    ("database_path", "Database"),
    ("backup_path", "Backup"),
    ("schema_version_from", "Schema version before"),
    ("schema_version_to", "Schema version after"),
    ("error", "Error"),
    ("binary_version", "attn version"),
    ("binary_commit", "attn commit"),
    ("failed_at", "Failed at"),
];

/// The marker's facts, known ones first in a fixed order, then any others, then the marker's
/// path. A marker that cannot be read or parsed shows as it is rather than hiding.
fn facts(marker: &MigrationMarker) -> Vec<(String, String)> {
    let mut facts = match &marker.contents {
        Err(why) => vec![("Marker could not be read".to_owned(), why.clone())],
        Ok(text) => {
            match serde_json::from_str::<serde_json::Map<String, serde_json::Value>>(text) {
                Err(_) => vec![(
                    "Marker contents (not a JSON object)".to_owned(),
                    text.clone(),
                )],
                Ok(record) => {
                    let value = |v: &serde_json::Value| match v {
                        serde_json::Value::String(s) => s.clone(),
                        other => other.to_string(),
                    };
                    let known = KNOWN_FACTS.iter().filter_map(|(key, label)| {
                        record.get(*key).map(|v| ((*label).to_owned(), value(v)))
                    });
                    let others = record
                        .iter()
                        .filter(|(key, _)| !KNOWN_FACTS.iter().any(|(k, _)| k == key))
                        .map(|(key, v)| (key.clone(), value(v)));
                    known.chain(others).collect()
                }
            }
        }
    };
    facts.push(("Failure marker".to_owned(), marker.path.clone()));
    facts
}

fn report(marker: &MigrationMarker) -> String {
    std::iter::once("attn failed to migrate its data at startup.".to_owned())
        .chain(
            facts(marker)
                .iter()
                .map(|(label, value)| format!("{label}: {value}")),
        )
        .collect::<Vec<_>>()
        .join("\n")
}

pub(crate) fn screen(state: &State, marker: &MigrationMarker, t: &Tokens) -> El {
    let copied = state.window.migration_details_copied;
    let report = report(marker);
    let facts = Stack::tight()
        .key("migration-failure.facts")
        .role("list")
        .label("Details")
        .w_full()
        .children(
            facts(marker)
                .into_iter()
                .enumerate()
                .map(|(i, (label, value))| {
                    Inline::loose()
                        .key(format!("migration-failure.fact.{i}"))
                        .role("listitem")
                        .label(format!("{label}: {value}"))
                        .w_full()
                        .items_start()
                        .child(
                            words(label, Type::Label, t.text_tertiary)
                                .w(LABEL_W)
                                .wrap()
                                .a11y_hidden(),
                        )
                        .child(
                            words(value, Type::Mono, t.text_primary)
                                .grow_from(1.0, COPY_FLOOR)
                                .min_w(0.0)
                                .wrap()
                                .selectable(true)
                                .a11y_hidden(),
                        )
                }),
        );
    let copy = Button::new(if copied { "Copied" } else { "Copy details" })
        .size(Size::Compact)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
            tx.copy(report.clone());
            state.window.migration_details_copied = true;
        })
        .build(t)
        .key("migration-failure.copy");
    Page::new()
        .head(
            Head::new(
                "migration-failure.title",
                "attn seems broken after migration",
            )
            .lead(
                "Ask an agent outside attn to investigate, preferably one running from within \
                 the attn codebase. Give it the details below.",
            ),
        )
        .push(Block::custom("migration-failure.facts.block", facts))
        .push(Block::control("migration-failure.copy.block", copy))
        .build("migration-failure", t)
        .role("main")
        .label("attn seems broken after migration")
}

/// A fact's name: "Schema version before", the longest known one, measures 172.2 in `Label`.
const LABEL_W: f32 = 200.0;

/// The narrowest a fact's value gets beside its name before it moves under it.
const COPY_FLOOR: f32 = 240.0;
