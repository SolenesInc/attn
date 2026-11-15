use std::time::Duration;

use attn_testkit::{Daemon, Marker, Target, World};

fn facts(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("migration-failure.facts"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn launch(marker: Marker) -> World {
    let daemon = Daemon::scripted();
    daemon.fail_migration(marker);
    World::start(daemon)
}

#[test]
fn a_failed_migration_stops_the_app_on_its_details_instead_of_retrying_the_daemon() {
    let mut world = launch(Marker::Text(
        r#"{"error":"conversion aborted","database_path":"/tmp/attn/attn.db"}"#,
    ));
    let marker = world.daemon.migration_marker().display().to_string();

    assert_eq!(
        world
            .screen()
            .find(Target::key("migration-failure.title"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("attn seems broken after migration")
    );
    assert_eq!(
        facts(&world),
        [
            "Database: /tmp/attn/attn.db".to_owned(),
            "Error: conversion aborted".to_owned(),
            format!("Failure marker: {marker}"),
        ]
    );
    world.advance(Duration::from_secs(60));
    assert_eq!(world.daemon.starts(), 1);
    assert_eq!(world.daemon.attempts(), 0);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_marker_the_app_cannot_read_still_stops_it_and_says_why() {
    let world = launch(Marker::Unreadable);

    let facts = facts(&world);
    assert_eq!(facts.len(), 2, "{facts:?}");
    assert!(
        facts[0].starts_with("Marker could not be read: "),
        "{facts:?}"
    );
    assert_eq!(world.daemon.starts(), 1);
}

#[test]
fn every_fact_shows_known_ones_first_then_the_marker_path() {
    let world = launch(Marker::Text(
        r#"{
            "failed_at": "2026-09-23T10:00:00Z",
            "worker_count": 3,
            "error": "desktop tree for workspace ws-4 is invalid",
            "schema_version_to": 152,
            "schema_version_from": 151,
            "binary_version": "0.42.0",
            "binary_commit": "c1d4d951b",
            "backup_path": "/Users/u/.attn/backups/attn-151.db",
            "database_path": "/Users/u/.attn/attn.db",
            "data_dir": "/Users/u/.attn",
            "daemon_log_path": "/Users/u/.attn/daemon.log"
        }"#,
    ));
    let marker = world.daemon.migration_marker().display().to_string();

    assert_eq!(
        facts(&world),
        [
            "Daemon log: /Users/u/.attn/daemon.log".to_owned(),
            "Data directory: /Users/u/.attn".to_owned(),
            "Database: /Users/u/.attn/attn.db".to_owned(),
            "Backup: /Users/u/.attn/backups/attn-151.db".to_owned(),
            "Schema version before: 151".to_owned(),
            "Schema version after: 152".to_owned(),
            "Error: desktop tree for workspace ws-4 is invalid".to_owned(),
            "attn version: 0.42.0".to_owned(),
            "attn commit: c1d4d951b".to_owned(),
            "Failed at: 2026-09-23T10:00:00Z".to_owned(),
            "worker_count: 3".to_owned(),
            format!("Failure marker: {marker}"),
        ]
    );
    assert!(
        world
            .screen()
            .find(Target::role("button").name("Retry"))
            .is_none()
    );
}

#[test]
fn a_marker_that_is_not_json_shows_verbatim() {
    let world = launch(Marker::Text("conversion aborted before writing JSON"));
    let marker = world.daemon.migration_marker().display().to_string();

    assert_eq!(
        facts(&world),
        [
            "Marker contents (not a JSON object): conversion aborted before writing JSON"
                .to_owned(),
            format!("Failure marker: {marker}"),
        ]
    );
}

#[test]
fn copy_details_puts_every_fact_on_the_clipboard() {
    let mut world = launch(Marker::Text(r#"{"error":"conversion aborted"}"#));
    let marker = world.daemon.migration_marker().display().to_string();

    world.gesture(|app| app.click(Target::key("migration-failure.copy")));

    assert_eq!(
        world.clipboard.text().map(|t| t.to_string()),
        Some(format!(
            "attn failed to migrate its data at startup.\nError: conversion aborted\nFailure marker: {marker}"
        ))
    );
    assert_eq!(
        world
            .screen()
            .find(Target::key("migration-failure.copy"))
            .and_then(|n| n.name)
            .as_deref(),
        Some("Copied")
    );
}
