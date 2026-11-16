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

    let changed = garden()
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
    push(&mut world, changed);

    assert!(!rows(&world).iter().any(|r| r.contains("wire the")));
    assert!(has_row(&world, "unrelated field work"));
    ask(&mut world, "quartz line");
    assert!(has_row(&world, "wire the quartz"));
    ask(&mut world, "line of type");
    assert!(!has_row(&world, "wire the quartz"));
    assert!(rests(&mut world));
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
    assert!(rests(&mut world));
}

#[test]
fn stands_down_when_the_query_names_a_lens_of_its_own() {
    let mut world = open_garden(garden());
    assert!(closed_lens(&world).is_some());

    ask(&mut world, "field is:closed");
    assert_eq!(closed_lens(&world), None);

    ask(&mut world, "is:ready");
    assert_eq!(closed_lens(&world), None);
    assert!(rests(&mut world));
}

#[test]
fn searches_the_plot_you_are_standing_in_and_says_what_the_garden_holds() {
    let mut world = search_inside_the_plot("field");

    assert_eq!(rows(&world), ["wire the field"]);
    assert!(offers(&world, "+1 in the whole garden"));
    assert!(rests(&mut world));
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
    assert!(rests(&mut world));
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
    assert!(rests(&mut world));
}
