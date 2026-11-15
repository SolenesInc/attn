use attn_protocol::{
    SeedCheck, SeedCheckEvidence, SeedCheckReport, SeedCheckResultMessage, SeedCheckRollupEntry,
    SeedCheckVerdict, SeedSharedCheck,
};
use attn_testkit::{Request, Target, World};
use serde_json::json;

use super::*;

fn builder() -> ActorView {
    ActorView {
        name: "Builder".into(),
        r#ref: "session:b1".into(),
    }
}

fn check(id: &str, text: &str, state: &str) -> SeedCheck {
    SeedCheck {
        id: id.into(),
        text: text.into(),
        state: state.into(),
        ..SeedCheck::default()
    }
}

fn judged() -> Seed {
    with(seed("s-judge1", "Cache store migration"), |s| {
        s.checks = Some(vec![
            check("C1", "the cache moves", "open"),
            SeedCheck {
                report: Some(SeedCheckReport {
                    summary: "tests pass".into(),
                    evidence: vec![SeedCheckEvidence {
                        url: Some("https://github.com/acme/app/pull/12".into()),
                        ..SeedCheckEvidence::default()
                    }],
                    commit: Some("abcdef1234567890".into()),
                    by: builder(),
                    at: "2026-11-13T09:00:00Z".into(),
                    ..SeedCheckReport::default()
                }),
                ..check("C2", "nothing is lost", "reported")
            },
            SeedCheck {
                verdict: Some(SeedCheckVerdict {
                    confirmed: false,
                    reason: Some("no run on Linux".into()),
                    by: ActorView {
                        name: "the user".into(),
                        r#ref: "user".into(),
                    },
                    at: "2026-11-13T10:00:00Z".into(),
                }),
                ..check("C3", "it runs on Linux", "rejected")
            },
        ])
    })
}

fn moves(world: &World) -> Vec<(String, String, Option<String>)> {
    world
        .daemon
        .sent_of("seed_check")
        .iter()
        .map(|r| {
            (
                r["check_id"].as_str().unwrap().to_owned(),
                r["verb"].as_str().unwrap().to_owned(),
                r["summary"].as_str().map(str::to_owned),
            )
        })
        .collect()
}

fn answer(world: &mut World, error: Option<&str>) {
    let request: Request = world.daemon.sent_of("seed_check").last().unwrap().clone();
    world.daemon.reply_to(
        &request,
        SeedCheckResultMessage {
            request_id: request["request_id"].as_str().unwrap().into(),
            success: error.is_none(),
            error: error.map(str::to_owned),
            ..SeedCheckResultMessage::new()
        },
    );
    world.settle();
}

fn says(world: &World, key: &str) -> Option<String> {
    world.screen().find(Target::key(key)).and_then(|n| n.name)
}

#[test]
fn shows_each_checks_report_and_verdict_and_judges_it_from_the_seed_page() {
    let mut world = open_garden(vec![judged()]);
    open_row(&mut world, "Cache store migration");

    assert_eq!(
        says(&world, "garden.checks.tally").as_deref(),
        Some("Checks: 0 of 3 confirmed · 1 to judge · 1 rejected")
    );
    assert_eq!(
        says(&world, "garden.check.C2.report").as_deref(),
        Some("report: tests pass · PR 12 · @abcdef1 · Builder")
    );
    assert_eq!(
        says(&world, "garden.check.C3.verdict").as_deref(),
        Some("rejected by you: no run on Linux · no report")
    );

    click(&mut world, "Confirm C1");
    answer(&mut world, None);
    click(&mut world, "Reopen C2");
    answer(&mut world, Some("check C2 moved since you looked"));
    assert_eq!(
        says(&world, "garden.check.C2.error").as_deref(),
        Some("check C2 moved since you looked")
    );
    click(&mut world, "Uncheck");
    answer(&mut world, None);
    assert!(
        world
            .screen()
            .find(Target::key("garden.check.C2.error"))
            .is_none()
    );

    assert_eq!(
        moves(&world),
        vec![
            ("C1".into(), "confirm".into(), None),
            ("C2".into(), "reopen".into(), None),
            ("C3".into(), "uncheck".into(), None),
        ]
    );
    assert!(rests(&mut world));
}

#[test]
fn rejecting_a_check_asks_what_would_prove_it_and_escape_puts_the_reason_away() {
    let mut world = open_garden(vec![judged()]);
    open_row(&mut world, "Cache store migration");

    click(&mut world, "Reject C1");
    assert_eq!(
        focused(&world).and_then(|n| n.name).as_deref(),
        Some("Reject C1: what would prove it")
    );
    key(&mut world, Key::Escape);
    assert!(
        world
            .screen()
            .find(Target::key("garden.check.C1.field"))
            .is_none()
    );
    assert_eq!(
        focused(&world).and_then(|n| n.key).as_deref(),
        Some("garden.check.C1.box")
    );

    click(&mut world, "Reject C2");
    key(&mut world, Key::Enter);
    assert_eq!(
        says(&world, "garden.check.C2.error").as_deref(),
        Some("rejecting C2 needs a reason: say what would prove it")
    );
    assert!(moves(&world).is_empty());
    world.gesture(|app| app.type_text("a run on Linux"));
    key(&mut world, Key::Enter);
    assert_eq!(
        moves(&world),
        vec![("C2".into(), "reject".into(), Some("a run on Linux".into()))]
    );
    answer(&mut world, None);

    assert!(
        world
            .screen()
            .find(Target::key("garden.check.C2.field"))
            .is_none()
    );
    assert_eq!(
        focused(&world).and_then(|n| n.key).as_deref(),
        Some("garden.check.C2.box")
    );
    assert!(rests(&mut world));
}

#[test]
fn a_plots_shared_check_opens_to_the_seeds_that_prove_it_and_removed_checks_wait_behind_a_toggle() {
    let crown = with(plot("s-plot11", "Native app", ""), |s| {
        s.checks = Some(vec![SeedCheck {
            removed: true,
            ..check("C1", "an old promise", "open")
        }]);
        s.shared_checks = Some(vec![SeedSharedCheck {
            id: "C2".into(),
            text: "lands green".into(),
            removed: false,
            seeds: vec![
                SeedCheckRollupEntry {
                    seed_id: "s-kid111".into(),
                    state: "confirmed".into(),
                    has_report: true,
                },
                SeedCheckRollupEntry {
                    seed_id: "s-kid222".into(),
                    state: "open".into(),
                    has_report: false,
                },
            ],
        }]);
    });
    let mut world = open_garden(vec![
        crown,
        part_of("s-plot11", "s-kid111", "Garden unit"),
        part_of("s-plot11", "s-kid222", "Settings unit"),
    ]);
    open_row(&mut world, "Native app");
    click(&mut world, "Expand the garden");

    assert!(
        world
            .screen()
            .find(Target::key("garden.check.C1"))
            .is_none()
    );
    click(&mut world, "1 removed");
    assert_eq!(
        says(&world, "garden.check.C1").as_deref(),
        Some("C1 an old promise · open")
    );
    assert!(
        world
            .screen()
            .find(Target::role("button").name("Confirm C1"))
            .is_none()
    );

    click(&mut world, "C2 lands green · 1 of 2 confirmed");
    click(&mut world, "Settings unit · planted · open");
    assert_eq!(heading(&world).as_deref(), Some("Settings unit"));
    assert!(rests(&mut world));
}

#[test]
fn opens_each_piece_of_evidence_where_the_seeds_artifacts_open_it_and_leaves_unknown_names_as_words()
 {
    let evidence = |artifact: &str| SeedCheckEvidence {
        artifact: Some(artifact.into()),
        ..SeedCheckEvidence::default()
    };
    let reported = with(judged(), |s| {
        let report = s.checks.as_mut().unwrap()[1].report.as_mut().unwrap();
        report.evidence = vec![
            evidence("verify-migration.tar.gz"),
            evidence("notes.md"),
            evidence("https://ci.example.test/run/9"),
            evidence("/repo/plan.md"),
            evidence("gone.log"),
            SeedCheckEvidence {
                url: Some("https://github.com/acme/app/pull/12".into()),
                ..SeedCheckEvidence::default()
            },
        ];
    });
    let held = reported.clone();
    let daemon = daemon(vec![reported], Vec::new());
    daemon.on("seed_document_get", move |r: &Request| {
        let kept = |name: &str| attn_protocol::SeedArtifact {
            filename: name.into(),
            relative_target: name.into(),
            size: 1,
            modified_at: "2026-11-13T09:00:00Z".into(),
        };
        let linked = |kind: &str, url: Option<&str>, path: Option<&str>| {
            attn_protocol::SeedArtifactReference {
                kind: kind.into(),
                url: url.map(str::to_owned),
                path: path.map(str::to_owned),
                ..attn_protocol::SeedArtifactReference::default()
            }
        };
        message(SeedDocumentGetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            document: Some(SeedDocument {
                seed: held.clone(),
                artifacts: vec![kept("verify-migration.tar.gz"), kept("notes.md")],
                references: vec![
                    linked("url", Some("https://ci.example.test/run/9"), None),
                    linked("markdown_file", None, Some("/repo/plan.md")),
                ],
                ..SeedDocument::default()
            }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    daemon.on("seed_artifact_target", |r: &Request| {
        json!({
            "event": "seed_artifact_target_result",
            "request_id": r["request_id"],
            "success": true,
            "result": {
                "relative_target": r["relative_target"],
                "path": format!("/notebook/seeds/s-judge1/{}", r["relative_target"].as_str().unwrap()),
            },
        })
    });
    let mut world = open_garden_with(daemon);
    open_row(&mut world, "Cache store migration");

    let press = |world: &mut World, n: usize| {
        world.gesture(|app| app.click(Target::key(format!("garden.check.C2.evidence.{n}"))))
    };
    press(&mut world, 0);
    assert_eq!(
        world.revealed_paths(),
        vec!["/notebook/seeds/s-judge1/verify-migration.tar.gz".to_owned()]
    );
    press(&mut world, 1);
    press(&mut world, 2);
    press(&mut world, 5);
    assert_eq!(
        world.opened_links(),
        [
            "file:///notebook/seeds/s-judge1/notes.md",
            "https://ci.example.test/run/9",
            "https://github.com/acme/app/pull/12",
        ]
    );
    press(&mut world, 3);
    assert_eq!(
        world.daemon.sent_of("open_markdown")[0]["path"],
        "/repo/plan.md"
    );
    assert!(
        world
            .screen()
            .find(Target::key("garden.check.C2.evidence.4"))
            .is_none()
    );
    assert!(rests(&mut world));
}
