//! The screen the app opens on when the daemon cannot start because a data migration failed:
//! the marker's facts, to hand to an agent outside attn, and a way to copy them.

use attn_client::MigrationMarker;
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, ControlSize};

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
    let k = attn_ui::kit(t);
    let copied = state.window.migration_details_copied;
    let report = report(marker);
    col()
        .key("migration-failure")
        .role("main")
        .label("attn seems broken after migration")
        .max_w(640.0)
        .gap(space::S3)
        .padding(space::S6)
        .child(
            text("attn seems broken after migration")
                .key("migration-failure.title")
                .role("heading")
                .level(1)
                .font(type_::TITLE)
                .color(t.text_primary),
        )
        .child(
            text("Ask an agent outside attn to investigate, preferably one running from within the attn codebase. Give it the details below.")
                .font(type_::BODY)
                .color(t.text_secondary)
                .wrap(),
        )
        .child(
            col()
                .key("migration-failure.facts")
                .role("list")
                .label("Details")
                .w_full()
                .gap(space::S2)
                .children(facts(marker).into_iter().enumerate().map(|(i, (label, value))| {
                    row()
                        .key(format!("migration-failure.fact.{i}"))
                        .role("listitem")
                        .label(format!("{label}: {value}"))
                        .w_full()
                        .gap(space::S3)
                        .child(
                            text(label)
                                .font(type_::LABEL)
                                .color(t.text_tertiary)
                                .w(160.0)
                                .wrap()
                                .a11y_hidden(),
                        )
                        .child(
                            text(value)
                                .font(type_::MONO_CAPTION)
                                .color(t.text_primary)
                                .grow(1.0)
                                .min_w(0.0)
                                .wrap()
                                .selectable(true)
                                .a11y_hidden(),
                        )
                })),
        )
        .child(
            Button::new(if copied { "Copied" } else { "Copy details" })
                .variant(ButtonVariant::Submit)
                .control_size(ControlSize::Small)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    tx.copy(report.clone());
                    state.window.migration_details_copied = true;
                })
                .build(k)
                .key("migration-failure.copy")
                .self_start(),
        )
}
