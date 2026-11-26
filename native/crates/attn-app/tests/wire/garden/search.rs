use attn_testkit::{Target, World};
use rune::Key;

use super::*;

fn garden() -> Vec<Seed> {
    vec![
        plot("s-crown1", "ship the search", ""),
        with(part_of("s-crown1", "s-wire01", "wire the field"), |s| {
            s.body = "the input is a line of type, not a box".into()
        }),
        part_of("s-crown1", "s-rank01", "rank the answers"),
        closed(
            part_of("s-crown1", "s-done01", "draw the field"),
            "harvested",
        ),
        seed("s-else01", "unrelated field work"),
        closed(seed("s-drop01", "a dropped idea"), "withered"),
    ]
}

fn ask(world: &mut World, query: &str) {
    world.gesture(|app| {
        app.click(Target::key("garden.search.field"));
        app.press("mod+a");
        if query.is_empty() {
            app.press("backspace");
        } else {
            app.type_text(query);
        }
    });
}

fn field(world: &World) -> Option<String> {
    world.screen().get(Target::key("garden.search.field")).value
}

fn closed_lens(world: &World) -> Option<(String, bool)> {
    world.screen().find(Target::key("garden.closed")).map(|n| {
        (
            n.name.unwrap_or_default(),
            n.value.as_deref() == Some("pressed"),
        )
    })
}

fn search_inside_the_plot(query: &str) -> World {
    let mut world = open_garden(garden());
    open_row(&mut world, "ship the search");
    ask(&mut world, query);
    world
}

fn offers(world: &World, name: &str) -> bool {
    world
        .screen()
        .find(Target::role("button").name(name))
        .is_some()
}

#[test]
fn finds_a_changed_seed_by_its_new_text_and_a_seed_the_change_did_not_include_keeps_its_hit() {
    let mut world = open_garden(garden());
    ask(&mut world, "field");
    assert!(has_row(&world, "wire the field"));
    assert!(has_row(&world, "unrelated field work"));

    let changed: Vec<Seed> = garden()
        .into_iter()
        .map(|s| {
            if s.id == "s-wire01" {
                with(s, |s| {
                    s.title = "wire the quartz".into();
                    s.body = "a quartz line".into();
                    s.rev = 2;
                })
            } else {
                s
            }
        })
        .collect();
    let search_changed = changed.clone();
    world.daemon.on("garden_search", move |r: &Request| {
        search_reply(r, &search_changed, &[], &[])
    });
    push(&mut world, changed);

    assert!(!rows(&world).iter().any(|r| r.contains("wire the")));
    assert!(has_row(&world, "unrelated field work"));
    ask(&mut world, "quartz line");
    assert!(has_row(&world, "wire the quartz"));
    ask(&mut world, "line of type");
    assert!(!has_row(&world, "wire the quartz"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn toggles_the_closed_lens_by_writing_it_into_the_query_without_flattening_the_plots() {
    let mut world = open_garden(garden());
    assert_eq!(closed_lens(&world), Some(("1 closed".into(), false)));

    click(&mut world, "1 closed");
    assert_eq!(field(&world).as_deref(), Some("is:any"));
    assert!(has_row(&world, "a dropped idea"));
    assert!(!has_row(&world, "wire the field"));
    assert!(!has_row(&world, "draw the field"));
    assert_eq!(closed_lens(&world), Some(("hide 1 closed".into(), true)));

    click(&mut world, "hide 1 closed");
    assert!(!has_row(&world, "a dropped idea"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn stands_down_when_the_query_names_a_lens_of_its_own() {
    let mut world = open_garden(garden());
    assert!(closed_lens(&world).is_some());

    ask(&mut world, "field is:closed");
    assert_eq!(closed_lens(&world), None);

    ask(&mut world, "is:ready");
    assert_eq!(closed_lens(&world), None);
    assert!(world.runtime_is_at_rest());
}

#[test]
fn searches_the_plot_you_are_standing_in_and_says_what_the_garden_holds() {
    let world = search_inside_the_plot("field");

    assert_eq!(rows(&world), ["wire the field"]);
    assert!(offers(&world, "+1 in the whole garden"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn keeps_the_trail_when_the_search_widens_out_of_the_plot_and_goes_back_to_the_plot_when_cleared() {
    let mut world = search_inside_the_plot("field");

    click(&mut world, "+1 in the whole garden");
    assert_eq!(rows(&world).len(), 2);
    assert_eq!(trail(&world), ["Garden"]);
    assert!(offers(&world, "1 in this plot"));

    ask(&mut world, "");
    assert!(has_row(&world, "rank the answers"));
    assert!(!has_row(&world, "unrelated field work"));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn starts_a_new_question_at_its_best_answer() {
    let mut world = open_garden(garden());
    ask(&mut world, "field");
    key(&mut world, Key::Down);
    assert_eq!(
        active_answer(&world).as_deref(),
        Some("unrelated field work")
    );

    world.gesture(|app| app.type_text(" w"));

    assert_eq!(field(&world).as_deref(), Some("field w"));
    assert_eq!(active_answer(&world).as_deref(), Some("wire the field"));
    assert!(world.runtime_is_at_rest());
}

fn twins() -> Vec<Seed> {
    let twin = |plot: &str, id: &str| {
        with(part_of(plot, id, "Verify the rollout"), |s| {
            s.body = "Run the smoke test".into()
        })
    };
    vec![
        plot("s-cache1", "Roll out the cache", ""),
        plot("s-queue1", "Roll out the queue", ""),
        twin("s-cache1", "s-twinc1"),
        twin("s-queue1", "s-twinq1"),
    ]
}

/// The garden in the smallest window at the largest text, holding `seeds`.
fn smallest(seeds: Vec<Seed>, expanded: bool) -> World {
    let daemon = daemon(seeds, Vec::new()).rewriting(|message| {
        if message["event"] == "initial_state" {
            message["settings"]["uiScale"] = serde_json::json!("1.5");
        }
    });
    let mut world = open_garden_sized(daemon, (800.0, 600.0));
    if expanded {
        click(&mut world, "Expand the garden");
    }
    world
}

/// The laid-out box of the text that says exactly `said`, and the box the clip leaves of it.
fn text_box(world: &World, said: &str) -> Option<([f32; 4], [f32; 4])> {
    world
        .runtime()
        .elements()
        .into_iter()
        .find(|e| e.text.as_deref() == Some(said))
        .map(|e| (e.rect, e.visible))
}

fn tells_apart(world: &World, frame: &str) {
    assert_eq!(shown(world), frame);
    assert_eq!(rows(world), ["Verify the rollout", "Verify the rollout"]);
    for (id, plot) in [
        ("s-twinc1", "Roll out the cache"),
        ("s-twinq1", "Roll out the queue"),
    ] {
        let words = row_words(world, id);
        let case = format!("{frame}, {id}: {words:?}");
        assert!(words.iter().any(|w| w == id), "{case}");
        assert!(words.iter().any(|w| w == &format!("in {plot}")), "{case}");
        assert!(words.iter().any(|w| w == "Run the smoke test"), "{case}");
        let description = world
            .screen()
            .get(Target::key(format!("garden.row.{id}")))
            .description
            .unwrap_or_default();
        assert!(
            description.contains(id) && description.contains(plot),
            "{case}: {description}"
        );
    }
}

#[test]
fn tells_apart_two_body_answers_by_their_ids_and_plots_docked_and_expanded() {
    let mut world = open_garden(twins());
    ask(&mut world, "smoke");
    tells_apart(&world, "dock");

    click(&mut world, "Expand the garden");
    tells_apart(&world, "full");
    assert!(world.runtime_is_at_rest());
}

fn far_plots() -> Vec<Seed> {
    let child = |plot: &str, id: &str| {
        with(part_of(plot, id, "Verify the rollout"), |s| {
            s.body = "Run the smoke test".into();
            s.tender = Some(attn_protocol::PartyView {
                name: "Deployment verification Builder".into(),
                r#ref: "session:builder".into(),
                session_id: Some("builder".into()),
            });
        })
    };
    vec![
        plot("s-tokyo1", TOKYO, ""),
        plot("s-osaka1", OSAKA, ""),
        child("s-tokyo1", "s-twint1"),
        child("s-osaka1", "s-twino1"),
    ]
}

const TOKYO: &str = "Roll out the cache to the production region in Tokyo 東京本番環境";
const OSAKA: &str = "Roll out the cache to the production region in Osaka 大阪本番環境";

#[test]
fn shows_the_whole_plot_name_of_two_body_answers_whose_plots_differ_past_their_opening_words() {
    for expanded in [false, true] {
        let mut world = smallest(far_plots(), expanded);
        ask(&mut world, "smoke");
        let natural = {
            let mut wide = open_garden_sized(daemon(far_plots(), Vec::new()), (4000.0, 1600.0));
            ask(&mut wide, "smoke");
            [TOKYO, OSAKA].map(|plot| text_box(&wide, &format!("in {plot}")).map(|(r, _)| r))
        };
        for (plot, natural) in [TOKYO, OSAKA].into_iter().zip(natural) {
            let case = format!("{}, {plot}", shown(&world));
            let said = format!("in {plot}");
            let (rect, visible) =
                text_box(&world, &said).unwrap_or_else(|| panic!("{case}: no {said:?}"));
            let natural =
                natural.unwrap_or_else(|| panic!("{case}: no {said:?} in the wide window"));
            assert!(
                rect[3] > natural[3] + 0.5 || rect[2] + 0.5 >= natural[2],
                "{case}: {rect:?} is cut short of its natural {natural:?}"
            );
            assert_eq!(rect, visible, "{case}: a clip cuts the plot's name");
        }
        assert!(world.runtime_is_at_rest());
    }
}

/// Twelve answers in plots with short names, and one in the middle with a 250-character title
/// in a plot with a 175-character CJK name, taller than the docked results at the smallest size.
fn many_answers() -> Vec<Seed> {
    let mut seeds = vec![
        plot("s-cache1", "Roll out the cache", ""),
        plot("s-queue1", "Roll out the queue", ""),
        plot(
            "s-tall1",
            &"東京本番環境へのキャッシュ展開".repeat(12)[..175 * 3],
            "",
        ),
    ];
    for n in 0..12 {
        let (parent, title) = match n {
            6 => (
                "s-tall1",
                "Verify the rollout of the cache to every production region ".repeat(5)[..250]
                    .to_string(),
            ),
            _ if n % 2 == 0 => ("s-cache1", "Verify the rollout".to_string()),
            _ => ("s-queue1", "Verify the rollout".to_string()),
        };
        seeds.push(with(
            part_of(parent, &format!("s-many{n:02}"), &title),
            |s| {
                s.body = "Before the rollout each region drains its old workers. Run the smoke \
                          test and read error rates and cold-start latency before anyone widens \
                          the rollout."
                    .into()
            },
        ));
    }
    seeds
}

#[test]
fn shows_the_answer_the_arrows_reach_whole_or_from_its_title_on_the_frame_they_reach_it() {
    for expanded in [false, true] {
        let mut world = smallest(many_answers(), expanded);
        let frame = shown(&world);
        ask(&mut world, "smoke");
        for (step, down) in [true; 13].into_iter().chain([false; 13]).enumerate() {
            let mut first_frame = None;
            let mut results = None;
            world.gesture(|app| {
                app.key(KeyEvent {
                    key: if down { Key::Down } else { Key::Up },
                    mods: Mods::default(),
                });
                app.runtime.frame(0.0);
                first_frame = app
                    .runtime
                    .elements()
                    .into_iter()
                    .find(|e| {
                        e.item_selected
                            && e.key
                                .as_deref()
                                .is_some_and(|k| k.starts_with("garden.row."))
                    })
                    .map(|e| (e.key, e.rect, e.visible));
                results = app
                    .runtime
                    .elements()
                    .into_iter()
                    .find(|e| e.key.as_deref() == Some("garden.results"))
                    .map(|e| e.visible);
            });
            let case = format!("{frame}, step {step}");
            let (key, rect, visible) =
                first_frame.unwrap_or_else(|| panic!("{case}: no active answer"));
            assert_eq!(
                key,
                world.screen().active_option().and_then(|n| n.key),
                "{case}: the first frame shows the answer the field names"
            );
            let list = results.unwrap_or_else(|| panic!("{case}: no results list"));
            if rect[3] <= list[3] {
                assert_eq!(rect, visible, "{case}: the active answer is cut or hidden");
            } else {
                assert_eq!(
                    rect[1], visible[1],
                    "{case}: a tall answer does not show from its title"
                );
                assert!(visible[3] > 0.0, "{case}: the tall answer is hidden");
            }
        }
        assert!(world.runtime_is_at_rest(), "{frame}");
    }
}

#[test]
fn searches_archived_log_entries_pages_results_and_opens_archived_hits_with_ancestor_trail() {
    let mut world = open_garden(vec![seed("s-open01", "current work")]);
    let archived_plot = closed(plot("s-aplt01", "Archived Plot", ""), "harvested");
    let archived_child_row = closed(
        part_of("s-aplt01", "s-akid01", "Archived log hit"),
        "harvested",
    );
    let archived_child_full = with(archived_child_row.clone(), |s| {
        s.body = "Full body loaded from daemon document.".into();
    });
    let mut page1_hits = vec![GardenSearchHit {
        seed: archived_child_row.clone(),
        r#where: "log".into(),
        id_hit: false,
        title_ranges: None,
        snippet: Some("Calibrated the hyperdrive coils.".into()),
        snippet_ranges: Some(vec![GardenSearchRange { start: 15, end: 25 }]),
        plot_title: Some("Archived Plot".into()),
    }];
    page1_hits.extend((0..29).map(|i| GardenSearchHit {
        seed: closed(
            seed(&format!("s-sh{i:04}"), &format!("Hyperdrive hit {i:02}")),
            "harvested",
        ),
        r#where: "title".into(),
        id_hit: false,
        title_ranges: None,
        snippet: None,
        snippet_ranges: None,
        plot_title: None,
    }));
    let second_hit = closed(seed("s-akid02", "Second archived hit"), "withered");
    let (h1, h2) = (page1_hits, second_hit.clone());
    world.daemon.on("garden_search", move |r: &Request| {
        let offset = r["offset"].as_i64().unwrap_or(0);
        let (hits, next_offset) = match offset {
            0 => (h1.clone(), Some(30)),
            30 => (
                vec![GardenSearchHit {
                    seed: h2.clone(),
                    r#where: "title".into(),
                    id_hit: false,
                    title_ranges: None,
                    snippet: None,
                    snippet_ranges: None,
                    plot_title: None,
                }],
                None,
            ),
            _ => (Vec::new(), None),
        };
        message(GardenSearchResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            hits,
            total: 31,
            next_offset,
            ..GardenSearchResultMessage::new()
        })
    });
    let (doc_plot, doc_child) = (archived_plot.clone(), archived_child_full.clone());
    world.daemon.on("seed_document_get", move |r: &Request| {
        if r["seed_id"] == "s-akid01" {
            return message(SeedDocumentGetResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                document: Some(SeedDocument {
                    seed: doc_child.clone(),
                    ancestors: Some(vec![doc_plot.clone()]),
                    ..SeedDocument::default()
                }),
                ..SeedDocumentGetResultMessage::new()
            });
        }
        document_reply(r, None)
    });

    ask(&mut world, "hyperdrive is:any");

    assert!(has_row(&world, "Archived log hit"));
    assert!(row_says(&world, "s-akid01", "in Archived Plot"));
    assert!(row_says(&world, "s-akid01", "in log"));
    assert!(row_says(
        &world,
        "s-akid01",
        "Calibrated the hyperdrive coils."
    ));
    assert!(
        world
            .screen()
            .find(Target::key("garden.search.more"))
            .is_some()
    );
    let searches = world.daemon.sent_of("garden_search");
    let final_first_page = searches.last().expect("garden_search sent");
    assert_eq!(final_first_page["terms"], serde_json::json!(["hyperdrive"]));
    assert_eq!(final_first_page["is"], serde_json::json!(["any"]));
    assert_eq!(final_first_page["offset"], 0);
    assert_eq!(final_first_page["limit"], 50);
    assert!(
        !searches.iter().any(|r| r["offset"] == 30),
        "page 2 must not eager-fetch before scrolling or clicking load-more"
    );
    assert!(world.runtime_is_at_rest());

    bring_into_view(&mut world, &Target::key("garden.search.more"));
    let searches = world.daemon.sent_of("garden_search");
    let page2_reqs: Vec<_> = searches.iter().filter(|r| r["offset"] == 30).collect();
    assert_eq!(page2_reqs.len(), 1);
    assert_eq!(page2_reqs[0]["terms"], serde_json::json!(["hyperdrive"]));
    assert_eq!(page2_reqs[0]["is"], serde_json::json!(["any"]));
    assert_eq!(page2_reqs[0]["limit"], 50);
    assert!(
        world
            .screen()
            .find(Target::key("garden.search.more"))
            .is_none()
    );

    if let Some([x, y, w, h]) = world.screen().bounds(Target::key("garden.viewport")) {
        world.gesture(|app| {
            app.runtime
                .wheel_event(rune::WheelEvent::new(0.0, 20_000.0).at(x + w / 2.0, y + h / 2.0))
        });
    }
    open_row(&mut world, "Archived log hit");
    assert_eq!(heading(&world).as_deref(), Some("Archived log hit"));
    assert_eq!(trail(&world), ["Garden", "Archived Plot"]);
    assert!(
        world
            .screen()
            .descendants(Target::key("garden.related"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Archived Plot")),
        "part-of relation must render from document.ancestors even when document.related is None"
    );
    assert!(
        world
            .screen()
            .descendants(Target::role("document").name("Plan"))
            .iter()
            .any(|n| n.name.as_deref() == Some("Full body loaded from daemon document."))
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn discards_out_of_order_search_replies_and_retries_failed_page_at_same_offset() {
    let mut world = open_garden(vec![seed("s-open01", "current work")]);
    world.daemon.answer_in_turn(
        "garden_search",
        [
            attn_testkit::Answer::Hold,
            attn_testkit::Answer::Hold,
            attn_testkit::Answer::Reply(message(GardenSearchResultMessage {
                success: false,
                error: Some("search page failed".into()),
                ..GardenSearchResultMessage::new()
            })),
            attn_testkit::Answer::Reply(message(GardenSearchResultMessage {
                success: true,
                hits: vec![GardenSearchHit {
                    seed: seed("s-ab0099", "AB recovered page 2"),
                    r#where: "title".into(),
                    id_hit: false,
                    title_ranges: None,
                    snippet: None,
                    snippet_ranges: None,
                    plot_title: None,
                }],
                total: 31,
                next_offset: None,
                ..GardenSearchResultMessage::new()
            })),
        ],
    );
    world.gesture(|app| {
        app.click(Target::key("garden.search.field"));
        app.type_text("a");
    });
    assert!(shows(&world, "Searching…"));
    world.gesture(|app| app.type_text("b"));

    let searches = world.daemon.sent_of("garden_search");
    assert_eq!(searches.len(), 2);
    assert_eq!(searches[0]["terms"], serde_json::json!(["a"]));
    assert_eq!(searches[1]["terms"], serde_json::json!(["ab"]));

    let first_page: Vec<GardenSearchHit> = (0..30)
        .map(|i| GardenSearchHit {
            seed: seed(&format!("s-ab{i:04}"), &format!("AB hit {i:02}")),
            r#where: "title".into(),
            id_hit: false,
            title_ranges: None,
            snippet: None,
            snippet_ranges: None,
            plot_title: None,
        })
        .collect();
    world.daemon.reply_to(
        &searches[1],
        message(GardenSearchResultMessage {
            request_id: searches[1]["request_id"].as_str().unwrap().into(),
            success: true,
            hits: first_page,
            total: 31,
            next_offset: Some(30),
            ..GardenSearchResultMessage::new()
        }),
    );
    world.daemon.reply_to(
        &searches[0],
        message(GardenSearchResultMessage {
            request_id: searches[0]["request_id"].as_str().unwrap().into(),
            success: true,
            hits: vec![GardenSearchHit {
                seed: seed("s-stale1", "Stale A hit"),
                r#where: "title".into(),
                id_hit: false,
                title_ranges: None,
                snippet: None,
                snippet_ranges: None,
                plot_title: None,
            }],
            total: 1,
            ..GardenSearchResultMessage::new()
        }),
    );
    world.settle();

    assert!(has_row(&world, "AB hit 00"));
    assert!(!has_row(&world, "Stale A hit"));
    assert!(world.runtime_is_at_rest());

    bring_into_view(&mut world, &Target::key("garden.search.more"));
    assert!(
        world
            .screen()
            .find(Target::key("garden.search.more.retry"))
            .is_some()
    );
    assert!(world.runtime_is_at_rest());

    bring_into_view(&mut world, &Target::key("garden.search.more.retry"));
    world.gesture(|app| app.click(Target::key("garden.search.more.retry")));
    let searches = world.daemon.sent_of("garden_search");
    assert_eq!(searches.len(), 4);
    assert_eq!(searches[2]["offset"], 30);
    assert_eq!(searches[3]["offset"], 30);
    assert!(
        world
            .screen()
            .find(Target::key("garden.search.more"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());
}

#[test]
fn fullscreen_columns_name_hidden_closed_children_and_keep_the_closed_toggle_for_an_archived_hit() {
    let archived_plot = with(
        closed(plot("s-aplot1", "Batch plot 0", ""), "harvested"),
        |s| {
            s.plot_progress = Some(attn_protocol::SeedPlotProgress {
                done: 2,
                total: 2,
                ..attn_protocol::SeedPlotProgress::default()
            });
        },
    );
    let archived_child = closed(
        part_of("s-aplot1", "s-akid01", "Archived task 0000"),
        "harvested",
    );
    let archived_sibling = closed(
        part_of("s-aplot1", "s-akid02", "Archived task 0001"),
        "harvested",
    );
    let live = vec![seed("s-open01", "Open root")];
    let d = daemon(live, Vec::new()).rewriting(|m| {
        if m["event"] == "initial_state" {
            m["seeds_total"] = serde_json::json!(4);
            m["garden_closed_counts"] = serde_json::json!({
                "all": { "harvested": 1, "withered": 0 },
                "unassigned": { "harvested": 1, "withered": 0 },
                "automations": { "harvested": 0, "withered": 0 },
                "by_bed": [],
            });
        }
    });
    let search_hit = archived_child.clone();
    d.on("garden_search", move |r: &Request| {
        message(GardenSearchResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            hits: vec![GardenSearchHit {
                seed: search_hit.clone(),
                r#where: "log".into(),
                id_hit: false,
                title_ranges: None,
                snippet: Some("Archived log entry mentioning hyperdrive.".into()),
                snippet_ranges: None,
                plot_title: Some("Batch plot 0".into()),
            }],
            total: 1,
            ..GardenSearchResultMessage::new()
        })
    });
    let (doc_plot, doc_child) = (archived_plot.clone(), archived_child.clone());
    d.on("seed_document_get", move |r: &Request| {
        if r["seed_id"] == "s-akid01" {
            return message(SeedDocumentGetResultMessage {
                request_id: r["request_id"].as_str().unwrap().into(),
                success: true,
                document: Some(SeedDocument {
                    seed: doc_child.clone(),
                    ancestors: Some(vec![doc_plot.clone()]),
                    ..SeedDocument::default()
                }),
                ..SeedDocumentGetResultMessage::new()
            });
        }
        document_reply(r, None)
    });
    let (page_plot, page_child, page_sib) = (
        archived_plot.clone(),
        archived_child.clone(),
        archived_sibling.clone(),
    );
    d.on("garden_seeds_page", move |r: &Request| {
        let seeds = match r["plot_id"].as_str() {
            Some("s-aplot1") => vec![page_sib.clone(), page_child.clone()],
            None => vec![page_plot.clone()],
            _ => Vec::new(),
        };
        let total = seeds.len() as i64;
        message(attn_protocol::GardenSeedsPageResultMessage {
            request_id: r["request_id"].as_str().unwrap().into(),
            success: true,
            seeds,
            total,
            harvested: total,
            ..attn_protocol::GardenSeedsPageResultMessage::new()
        })
    });

    let mut world = open_garden_sized(d, TWO_COLUMNS);
    click(&mut world, "Expand the garden");
    ask(&mut world, "hyperdrive is:any");
    open_row(&mut world, "Archived task 0000");

    assert_eq!(heading(&world).as_deref(), Some("Archived task 0000"));
    assert_eq!(trail(&world), ["Garden", "Batch plot 0"]);
    assert!(!shows(&world, "Nothing here yet."));
    assert!(shows(
        &world,
        "Nothing open here. 2 closed seeds are behind the closed toggle above."
    ));
    assert_eq!(closed_lens(&world), Some(("3 closed".into(), false)));

    click(&mut world, "3 closed");
    assert_eq!(closed_lens(&world), Some(("hide 3 closed".into(), true)));
    assert!(has_row(&world, "Batch plot 0"));
    assert!(has_row(&world, "Archived task 0000"));
    assert!(has_row(&world, "Archived task 0001"));
    assert!(world.runtime_is_at_rest());
}
