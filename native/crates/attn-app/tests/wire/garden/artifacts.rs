use attn_protocol::{
    FsExistsResult, FsExistsResultMessage, SeedArtifact, SeedArtifactReference,
    SeedArtifactTargetResult, SeedArtifactTargetResultMessage, SeedArtifactTransferResult,
    SeedArtifactTransferResultMessage,
};
use attn_testkit::{Request, Target, World};
use rune::FileDialogKind;
use serde_json::json;

use super::*;

const SEED: &str = "Ship the report";
const AT: &str = "2026-08-29T20:00:00Z";

fn kept(filename: &str, size: i64) -> SeedArtifact {
    SeedArtifact {
        filename: filename.into(),
        relative_target: filename.into(),
        size,
        modified_at: AT.into(),
    }
}

fn linked(kind: &str, f: impl FnOnce(&mut SeedArtifactReference)) -> SeedArtifactReference {
    let mut reference = SeedArtifactReference {
        kind: kind.into(),
        ..SeedArtifactReference::default()
    };
    f(&mut reference);
    reference
}

fn file(path: &str) -> SeedArtifactReference {
    linked("markdown_file", |r| r.path = Some(path.into()))
}

/// The garden open on a seed that keeps `artifacts` and links to `references`, over a daemon
/// that says each path in `missing` is not on disk and refuses every transfer with `refusal`.
fn opened(
    artifacts: Vec<SeedArtifact>,
    references: Vec<SeedArtifactReference>,
    missing: &'static [&'static str],
    refusal: Option<&'static str>,
) -> World {
    let held = seed("s-ship11", SEED);
    let daemon = daemon(vec![held.clone()], Vec::new());
    daemon.on("seed_document_get", move |r: &Request| {
        message(SeedDocumentGetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            document: Some(SeedDocument {
                seed: held.clone(),
                artifacts: artifacts.clone(),
                references: references.clone(),
                ..SeedDocument::default()
            }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    daemon.on("fs_exists", move |r: &Request| {
        let path = format!(
            "{}/{}",
            r["root"].as_str().unwrap(),
            r["path"].as_str().unwrap()
        );
        message(FsExistsResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            result: Some(FsExistsResult {
                exists: !missing.contains(&path.as_str()),
                path,
            }),
            ..FsExistsResultMessage::new()
        })
    });
    daemon.on("seed_artifact_target", |r: &Request| {
        let target = r["relative_target"].as_str().unwrap();
        message(SeedArtifactTargetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            result: Some(SeedArtifactTargetResult {
                relative_target: target.into(),
                path: Some(format!("/notebook/seeds/s-ship11/{target}")),
                ..SeedArtifactTargetResult::default()
            }),
            ..SeedArtifactTargetResultMessage::new()
        })
    });
    daemon.on("seed_artifact_transfer", move |r: &Request| {
        message(SeedArtifactTransferResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: refusal.is_none(),
            error: refusal.map(str::to_owned),
            result: refusal.is_none().then(|| SeedArtifactTransferResult {
                operation: r["operation"].as_str().unwrap().into(),
                seed_id: "s-ship11".into(),
                ..SeedArtifactTransferResult::default()
            }),
            ..SeedArtifactTransferResultMessage::new()
        })
    });
    let mut world = open_garden_with(daemon);
    open_row(&mut world, SEED);
    world
}

fn row(world: &World, part: &str) -> Node {
    world
        .screen()
        .get(Target::key(format!("garden.{part}.row")))
}

fn press(world: &mut World, part: &str, action: &str) {
    world.gesture(|app| app.click(Target::key(format!("garden.{part}.{action}"))));
}

fn transfers(world: &World) -> Vec<Value> {
    world
        .daemon
        .sent_of("seed_artifact_transfer")
        .into_iter()
        .map(|mut sent| {
            let fields = sent.as_object_mut().unwrap();
            fields.remove("request_id");
            fields.remove("cmd");
            sent
        })
        .collect()
}

#[test]
fn opens_reveals_and_moves_out_a_kept_document_where_the_user_chooses() {
    let mut world = opened(vec![kept("report.pdf", 1234)], Vec::new(), &[], None);

    let item = row(&world, "artifact.report.pdf");
    assert_eq!(
        (item.role.as_str(), item.name.as_deref()),
        ("list-item", Some("report.pdf · 1,234 bytes"))
    );

    press(&mut world, "artifact.report.pdf", "open");
    assert_eq!(
        world.daemon.sent_of("seed_artifact_target")[0]["purpose"],
        "artifact"
    );
    assert_eq!(
        world.opened_links(),
        vec!["file:///notebook/seeds/s-ship11/report.pdf".to_owned()]
    );

    press(&mut world, "artifact.report.pdf", "reveal");
    assert_eq!(
        world.revealed_paths(),
        vec!["/notebook/seeds/s-ship11/report.pdf".to_owned()]
    );

    press(&mut world, "artifact.report.pdf", "move-out");
    assert_eq!(
        world.file_dialog(),
        Some((
            FileDialogKind::Save,
            "Move artifact out of this seed".into()
        ))
    );
    world.answer_file_dialog(Some("/tmp/out/report.pdf"));
    assert_eq!(
        transfers(&world),
        vec![json!({
            "seed_id": "s-ship11",
            "operation": "detach",
            "filename": "report.pdf",
            "destination_path": "/tmp/out/report.pdf",
        })]
    );
    assert!(rests(&mut world));
}

#[test]
fn only_reveals_a_kept_file_the_system_would_run() {
    let mut world = opened(vec![kept("setup.command", 12)], Vec::new(), &[], None);

    assert!(
        world
            .screen()
            .find(Target::key("garden.artifact.setup.command.open"))
            .is_none()
    );
    press(&mut world, "artifact.setup.command", "reveal");
    assert_eq!(
        world.revealed_paths(),
        vec!["/notebook/seeds/s-ship11/setup.command".to_owned()]
    );
    press(&mut world, "artifact.setup.command", "move-out");
    world.answer_file_dialog(None);
    assert!(transfers(&world).is_empty());
    assert!(rests(&mut world));
}

#[test]
fn keeps_a_missing_linked_file_listed_and_brings_it_in_only_when_asked() {
    let mut world = opened(
        Vec::new(),
        vec![file("/gone/legacy.md")],
        &["/gone/legacy.md"],
        None,
    );

    assert_eq!(
        row(&world, "reference.0").name.as_deref(),
        Some("linked file legacy.md · /gone · not on disk")
    );
    assert!(transfers(&world).is_empty());
    press(&mut world, "reference.0", "copy-in");
    assert_eq!(
        transfers(&world),
        vec![json!({
            "seed_id": "s-ship11",
            "operation": "copy",
            "source_path": "/gone/legacy.md",
            "legacy_reference": {"kind": "markdown_file", "path": "/gone/legacy.md"},
        })]
    );
    assert!(rests(&mut world));
}

#[test]
fn asks_which_file_a_repository_relative_link_means_before_bringing_it_in() {
    let mut world = opened(Vec::new(), vec![file("docs/legacy.md")], &[], None);

    press(&mut world, "reference.0", "move-in");
    assert_eq!(
        world.file_dialog(),
        Some((
            FileDialogKind::Open,
            "Choose the linked file to bring into this seed".into()
        ))
    );
    world.answer_file_dialog(Some("/chosen/legacy.md"));
    assert_eq!(
        transfers(&world),
        vec![json!({
            "seed_id": "s-ship11",
            "operation": "move",
            "source_path": "/chosen/legacy.md",
            "legacy_reference": {"kind": "markdown_file", "path": "docs/legacy.md"},
        })]
    );
    assert!(rests(&mut world));
}

#[test]
fn leaves_a_linked_file_listed_and_says_why_when_bringing_it_in_is_refused() {
    let mut world = opened(
        Vec::new(),
        vec![file("/tmp/legacy.md")],
        &[],
        Some("destination already exists"),
    );

    press(&mut world, "reference.0", "move-in");
    let error = world.screen().get(Target::key("garden.reference.0.error"));
    assert_eq!(
        (error.role.as_str(), error.name.as_deref()),
        ("alert", Some("destination already exists"))
    );
    assert_eq!(
        row(&world, "reference.0").name.as_deref(),
        Some("linked file legacy.md · /tmp")
    );
    assert!(rests(&mut world));
}

#[test]
fn removes_a_link_through_a_detach_note() {
    let mut world = opened(Vec::new(), vec![file("/repo/plan.md")], &[], None);
    world.daemon.on("seed_note", |r: &Request| {
        json!({"event": "seed_note_result", "request_id": r["request_id"], "success": true})
    });

    press(&mut world, "reference.0", "remove");
    let sent = &world.daemon.sent_of("seed_note")[0];
    assert_eq!(
        (&sent["kind"], &sent["body"], &sent["artifact"]),
        (
            &json!("detach"),
            &json!(""),
            &json!({"kind": "markdown_file", "path": "/repo/plan.md"})
        )
    );
    assert!(rests(&mut world));
}

#[test]
fn lists_linked_files_notebook_documents_and_pages_and_opens_each_where_it_belongs() {
    let mut world = opened(
        Vec::new(),
        vec![
            file("/repo/current.md"),
            linked("notebook", |r| {
                r.notebook_document_id = Some("nb-plan-7".into())
            }),
            linked("url", |r| r.url = Some("https://example.test/pr/1".into())),
            linked("url", |r| {
                r.url = Some("https://github.com/acme/app/pull/736".into())
            }),
        ],
        &[],
        None,
    );

    let names: Vec<_> = (0..4)
        .map(|n| {
            row(&world, &format!("reference.{n}"))
                .name
                .unwrap_or_default()
        })
        .collect();
    assert_eq!(
        names,
        [
            "linked file current.md · /repo",
            "notebook nb-plan-7",
            "link example.test · /pr/1",
            "pull request #736 · acme/app"
        ]
    );
    assert_eq!(
        world
            .screen()
            .get(Target::key("garden.reference.2.open"))
            .role,
        "link"
    );

    press(&mut world, "reference.0", "open");
    assert_eq!(
        world.daemon.sent_of("open_markdown")[0]["path"],
        "/repo/current.md"
    );
    press(&mut world, "reference.3", "open");
    assert_eq!(
        world.opened_links(),
        vec!["https://github.com/acme/app/pull/736".to_owned()]
    );
    assert!(rests(&mut world));
}
