#[global_allocator]
static ALLOCATOR: attn_testkit::allocations::Counting = attn_testkit::allocations::Counting;

mod wire {
    pub(crate) fn mac() -> bool {
        rune::KeyPlatform::current() == rune::KeyPlatform::Mac
    }

    /// The room a finder's list leaves between its rows and its edges.
    const FINDER_INSET: f32 = 8.0;

    /// Presses Down until the field's active option is the last of `list`'s, from the first
    /// when none is active, then Up back to the first, and checks that each option shows whole inside the list as it becomes active,
    /// the last with the list's inset under it and the first with the list at its top.
    pub(crate) fn arrows_show_each_option(world: &mut attn_testkit::World, list: &str) {
        let key = |world: &attn_testkit::World| {
            world
                .screen()
                .active_option()
                .and_then(|n| n.key)
                .expect("the field names an active option")
        };
        let options = |world: &attn_testkit::World| -> Vec<String> {
            world
                .screen()
                .descendants(attn_testkit::Target::key(list))
                .into_iter()
                .filter(|n| n.role == "list-box-option")
                .filter_map(|n| n.key)
                .collect()
        };
        let shown = |world: &attn_testkit::World, step: &str| {
            let active = key(world);
            let elements = world.runtime().elements();
            let find = |k: &str| {
                elements
                    .iter()
                    .find(|e| e.key.as_deref() == Some(k))
                    .unwrap_or_else(|| panic!("{k} is drawn"))
            };
            let view = find(list).visible;
            let row = find(&active).rect;
            assert!(
                row[1] >= view[1] - 0.5 && row[1] + row[3] <= view[1] + view[3] + 0.5,
                "{step}: {active} at {row:?} does not show whole in {list}'s {view:?}"
            );
            (active, view[1] + view[3] - (row[1] + row[3]))
        };
        let all = options(world);
        assert!(
            all.len() > 1,
            "{list} has options to walk:\n{}",
            world.screen()
        );
        let press = |world: &mut attn_testkit::World, key: rune::Key| {
            world.gesture(|app| {
                app.key(rune::KeyEvent {
                    key,
                    mods: rune::Mods::default(),
                })
            })
        };
        if world.screen().active_option().is_none() {
            press(world, rune::Key::Down);
        }
        let last = all.last().expect("options").clone();
        let mut room_under = 0.0;
        for _ in 0..all.len() {
            if key(world) == last {
                break;
            }
            press(world, rune::Key::Down);
            (_, room_under) = shown(world, "Down");
        }
        assert_eq!(key(world), last, "Down reaches {list}'s last option");
        assert!(
            room_under + 0.5 >= FINDER_INSET,
            "{last} has {room_under} points under it in {list}, not the list's {FINDER_INSET}"
        );
        let first = options(world).first().expect("options").clone();
        for _ in 0..all.len() {
            if key(world) == first {
                break;
            }
            press(world, rune::Key::Up);
            shown(world, "Up");
        }
        assert_eq!(key(world), first, "Up reaches {list}'s first option");
        assert_eq!(
            world.screen().scrolled(attn_testkit::Target::key(list)),
            Some(0.0),
            "back on the first option, {list} shows its top"
        );
    }

    mod app_actions;
    mod asks;
    mod automation;
    mod automations;
    mod browser;
    mod callouts;
    mod chief;
    mod colors;
    mod connection;
    mod covered;
    mod crew;
    mod crew_start;
    mod daemon_warnings;
    mod deep_link;
    mod desktop_close;
    mod desktop_order;
    mod desktops;
    mod diagnostic_report;
    mod dock;
    mod field_keys;
    mod frame_cost;
    mod garden;
    mod home;
    mod home_pull_requests;
    mod keys;
    mod launcher;
    mod launcher_outposts;
    mod ledger;
    mod markdown;
    mod markdown_annotations;
    mod migration_failure;
    mod notebook;
    mod notifications;
    mod opener;
    mod outpost_paths;
    mod outpost_updates;
    mod palette;
    mod performance;
    mod picker;
    mod presence;
    mod profile;
    mod queue;
    mod quick_capture;
    mod reach;
    mod remote_agents;
    mod remote_shell;
    mod review;
    mod settings;
    mod shared_codex;
    mod shelf_drags;
    mod shelf_layout;
    mod shelf_peek;
    mod shortcut_editor;
    mod sidebar;
    mod sidebar_width;
    mod terminal;
    mod terminal_annotations;
    mod terminal_blocks;
    mod terminal_filter;
    mod terminal_find;
    mod terminal_input_latency;
    mod terminal_kitty;
    mod terminal_links;
    mod terminal_stale_build;
    mod terminal_view;
    mod tile_header;
    mod title_bar;
    mod update_run;
    mod whats_new;
}
