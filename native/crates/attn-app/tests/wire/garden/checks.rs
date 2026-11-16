use attn_protocol::{
    SeedCheck, SeedCheckEvidence, SeedCheckReport, SeedCheckResultMessage, SeedCheckRollupEntry,
    SeedCheckTally, SeedCheckVerdict, SeedPlotProgress, SeedSharedCheck,
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

#[test]
fn scrolls_from_the_header_tally_to_the_checks_section() {
    let long = (1..=80)
        .map(|n| format!("Step {n} of the plan."))
        .collect::<Vec<_>>()
        .join("\n\n");
    let mut world = open_garden(vec![with(judged(), |s| s.body = long)]);
    open_row(&mut world, "Cache store migration");
    let tally = Target::role("button").name("Checks: 0 of 3 confirmed · 1 to judge · 1 rejected");
    let reader = world
        .screen()
        .bounds(Target::key("garden.viewport"))
        .unwrap();
    let below = |world: &World| {
        world.screen().bounds(Target::key("garden.checks")).unwrap()[1] >= reader[1] + reader[3]
    };
    assert!(below(&world));

    world.gesture(|app| app.click(tally));
    assert!(rests(&mut world));

    assert!(!below(&world));
    assert_eq!(
        world.screen().focused().and_then(|n| n.key).as_deref(),
        Some("garden.checks.heading")
    );
}

#[test]
fn counts_a_fresh_report_as_to_judge_on_child_rows_plot_progress_and_board_cards() {
    let tally = |reported| SeedCheckTally {
        total: 5,
        reported,
        confirmed: 2,
        rejected: 1,
    };
    let crown = |reported| {
        with(plot("s-plot11", "The monitor plot", ""), |s| {
            s.plot_progress = Some(SeedPlotProgress {
                total: 3,
                done: 1,
                growing: 1,
                ready: 1,
                checks: Some(tally(reported)),
                ..SeedPlotProgress::default()
            });
            s.checks = Some(vec![check(
                "C6",
                "The plot glossary entry is written.",
                "open",
            )]);
        })
    };
    let leaf = |c1: &str| {
        with(
            part_of("s-plot11", "s-leaf11", "View and home reactions"),
            |s| {
                s.status = "growing".into();
                s.checks = Some(vec![
                    check("C8", "opens intact", "confirmed"),
                    check("C4", "CI is green", "rejected"),
                    check("C1", "a wire test guards it", c1),
                    check("C5", "a manual run", "open"),
                    check("C2", "the glossary says it", "confirmed"),
                    SeedCheck {
                        removed: true,
                        ..check("C9", "an old statement", "open")
                    },
                ]);
            },
        )
    };
    let table = part_of("s-plot11", "s-tbl111", "Policy and table");
    let mut world = open_garden_sized(
        daemon(vec![leaf("open"), crown(0), table.clone()], Vec::new()),
        THREE_COLUMNS,
    );
    assert!(row_says(&world, "s-plot11", "✓2/5 · 1 rejected"));

    open_row(&mut world, "The monitor plot");
    assert!(row_says(&world, "s-leaf11", "✓2/5 · 1 rejected"));
    assert!(!row_says(
        &world,
        "s-leaf11",
        "✓2/5 · 1 to judge · 1 rejected"
    ));

    push(&mut world, vec![leaf("reported"), crown(1), table]);
    assert!(row_says(
        &world,
        "s-leaf11",
        "✓2/5 · 1 to judge · 1 rejected"
    ));
    let progress: Vec<_> = world
        .screen()
        .descendants(Target::key("garden.head.progress"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        progress.iter().any(|w| w.starts_with("1/3 done")),
        "{progress:?}"
    );
    assert!(
        progress
            .iter()
            .any(|w| w == "Checks: 2 of 5 confirmed · 1 to judge · 1 rejected"),
        "{progress:?}"
    );

    world.gesture(|app| app.key(board()));
    click(&mut world, "board");
    let card = world
        .screen()
        .all(Target::role("button").name("The monitor plot"))
        .into_iter()
        .find_map(|n| n.key.filter(|k| k.starts_with("garden.board.card.")))
        .unwrap();
    assert!(
        world
            .screen()
            .descendants(Target::key(card))
            .iter()
            .any(|n| n.name.as_deref() == Some("✓2/5 · 1 to judge · 1 rejected"))
    );
    assert!(rests(&mut world));
}

fn report_of(world: &World, check: &str) -> String {
    says(world, &format!("garden.check.{check}.report")).unwrap_or_default()
}

#[test]
fn marks_artifact_evidence_the_daemon_says_changed_since_its_report_and_clears_it_after_a_new_report()
 {
    let reported = |changed: Option<bool>, rev: i64| {
        with(seed("s-evid11", "Evidence work"), |s| {
            s.rev = rev;
            s.checks = Some(vec![SeedCheck {
                report: Some(SeedCheckReport {
                    summary: "opens intact".into(),
                    evidence: vec![SeedCheckEvidence {
                        artifact: Some("verify-migration.tar.gz".into()),
                        changed_since_report: changed,
                        ..SeedCheckEvidence::default()
                    }],
                    commit: Some("da2b3b9c0ffee".into()),
                    by: builder(),
                    at: "2026-10-19T21:10:00Z".into(),
                    ..SeedCheckReport::default()
                }),
                ..check("C8", "opens with its sessions intact", "reported")
            }]);
        })
    };
    let world_daemon = Daemon::scripted().state(initial(vec![reported(None, 1)], Vec::new()));
    let document = std::sync::Arc::new(std::sync::Mutex::new(reported(Some(true), 1)));
    let read = document.clone();
    world_daemon.on("seed_document_get", move |r: &Request| {
        document_reply(r, Some(read.lock().unwrap().clone()))
    });
    let mut world = open_garden_with(world_daemon);
    open_row(&mut world, "Evidence work");
    assert!(
        report_of(&world, "C8").contains("verify-migration.tar.gz (changed since this report)"),
        "{}",
        report_of(&world, "C8")
    );

    *document.lock().unwrap() = reported(None, 2);
    push(&mut world, vec![reported(None, 2)]);
    let report = report_of(&world, "C8");
    assert!(
        report.contains("verify-migration.tar.gz · @da2b3b9"),
        "{report}"
    );
    assert!(!report.contains("changed since this report"), "{report}");
    assert!(rests(&mut world));
}

#[test]
fn logs_check_notes_as_ordinary_entries_outside_the_attachment_bookkeeping() {
    let leaf = seed("s-logs11", "Logged work");
    let world_daemon = Daemon::scripted().state(initial(vec![leaf.clone()], Vec::new()));
    world_daemon.on("seed_document_get", move |r: &Request| {
        let note = |id: &str, kind: &str, body: &str, author: ActorView| SeedNote {
            id: id.into(),
            seed_id: "s-logs11".into(),
            kind: kind.into(),
            body: body.into(),
            author,
            created_at: "2026-10-19T21:10:00Z".into(),
            ..SeedNote::default()
        };
        message(SeedDocumentGetResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            document: Some(SeedDocument {
                seed: leaf.clone(),
                notes: vec![
                    note(
                        "n-check",
                        "check",
                        "C4 rejected: That run is on 869dc03.",
                        ActorView {
                            name: "Trellis".into(),
                            r#ref: "member:trellis".into(),
                        },
                    ),
                    note(
                        "n-attach",
                        "attach",
                        "attached run.log",
                        ActorView {
                            name: "the user".into(),
                            r#ref: "user".into(),
                        },
                    ),
                ],
                notes_total: 2,
                ..SeedDocument::default()
            }),
            ..SeedDocumentGetResultMessage::new()
        })
    });
    let mut world = open_garden_with(world_daemon);
    open_row(&mut world, "Logged work");

    let entry: Vec<_> = world
        .screen()
        .descendants(Target::key("garden.log.n-check"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(entry.iter().any(|w| w == "Trellis"), "{entry:?}");
    assert!(entry.iter().any(|w| w == "check"), "{entry:?}");
    assert!(
        world
            .screen()
            .find(Target::key("garden.log.n-attach"))
            .is_none()
    );
    assert!(!shows(&world, "attached run.log"));
    assert!(rests(&mut world));
}

fn no_checks_on_the_page(world: &World) {
    assert!(
        world
            .screen()
            .find(Target::role("heading").name("Checks"))
            .is_none()
    );
    assert!(
        world
            .screen()
            .all(Target::role("button"))
            .iter()
            .all(|n| !n.name.as_deref().is_some_and(|w| w.starts_with("Checks:")))
    );
}

#[test]
fn counts_a_plots_descendants_without_a_link_when_the_plot_has_no_checks_section_of_its_own() {
    let bare = with(plot("s-plot11", "The monitor plot", ""), |s| {
        s.plot_progress = Some(SeedPlotProgress {
            total: 3,
            done: 1,
            growing: 1,
            ready: 1,
            checks: Some(SeedCheckTally {
                total: 5,
                reported: 1,
                confirmed: 2,
                rejected: 1,
            }),
            ..SeedPlotProgress::default()
        })
    });
    let mut world = open_garden(vec![
        bare,
        part_of("s-plot11", "s-tbl111", "Policy and table"),
    ]);
    open_row(&mut world, "The monitor plot");

    no_checks_on_the_page(&world);
    let progress: Vec<_> = world
        .screen()
        .descendants(Target::key("garden.head.progress"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect();
    assert!(
        progress
            .iter()
            .any(|w| w == "✓2/5 · 1 to judge · 1 rejected"),
        "{progress:?}"
    );
    assert!(rests(&mut world));
}

#[test]
fn renders_no_checks_section_and_no_tally_for_a_seed_without_checks() {
    let mut world = open_garden(vec![with(seed("s-plain1", "A plain seed"), |s| {
        s.status = "growing".into()
    })]);
    assert!(
        row_words(&world, "s-plain1")
            .iter()
            .all(|w| !w.contains('✓'))
    );

    open_row(&mut world, "A plain seed");

    no_checks_on_the_page(&world);
    assert!(rests(&mut world));
}
