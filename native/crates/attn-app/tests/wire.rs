#[global_allocator]
static ALLOCATOR: attn_testkit::allocations::Counting = attn_testkit::allocations::Counting;

mod wire {
    /// The title bar's leading points macOS's traffic lights take: what a run acting as Linux
    /// gives the title bar beyond a Mac's.
    pub(crate) const MACOS_WINDOW_CONTROLS: f32 = 74.0;

    pub(crate) fn mac() -> bool {
        attn_testkit::mac()
    }

    /// Asserts that the element keyed `key`, mounted by the recorded cause, is opaque and
    /// unclipped on the first frame it is drawn: a refusal the person caused is seen at once.
    pub(crate) fn whole_on_its_first_frame(rec: &rune::motion_gate::Recording, key: &str) {
        let suffix = format!("/{key}");
        let track = rec
            .tracks
            .iter()
            .find(|t| t.path.ends_with(&suffix))
            .unwrap_or_else(|| panic!("{key} is never drawn"));
        let (frame, first) = &track.frames[0];
        assert!(
            first.op > 0.99 && first.rect[3] > 0.0 && (first.vis[3] - first.rect[3]).abs() < 0.5,
            "{key} on its first frame ({frame}): opacity {}, {} of its {} points show",
            first.op,
            first.vis[3],
            first.rect[3]
        );
    }

    /// The room a finder's list leaves between its rows and its edges.
    const FINDER_INSET: f32 = 8.0;

    /// Checks that `active` shows whole inside `list`; returns the room under it.
    fn active_option_shows_whole(
        world: &attn_testkit::World,
        list: &str,
        active: &str,
        step: &str,
    ) -> f32 {
        let elements = world.runtime().elements();
        let find = |k: &str| {
            elements
                .iter()
                .find(|e| e.key.as_deref() == Some(k))
                .unwrap_or_else(|| panic!("{k} is drawn"))
        };
        let view = find(list).visible;
        let row = find(active).rect;
        assert!(
            row[1] >= view[1] - 0.5 && row[1] + row[3] <= view[1] + view[3] + 0.5,
            "{step}: {active} at {row:?} does not show whole in {list}'s {view:?}"
        );
        view[1] + view[3] - (row[1] + row[3])
    }

    /// Types `text` into a field whose `list` the arrows have scrolled, and checks that the
    /// option the field then names shows whole, or that the list shows its top when it names
    /// none.
    pub(crate) fn typing_shows_the_active_option(
        world: &mut attn_testkit::World,
        list: &str,
        text: &str,
    ) {
        let scrolled =
            |world: &attn_testkit::World| world.screen().scrolled(attn_testkit::Target::key(list));
        assert!(scrolled(world) > Some(0.0), "the arrows scrolled {list}");
        world.gesture(|app| app.type_text(text));
        match world.screen().active_option().and_then(|n| n.key) {
            Some(active) => {
                active_option_shows_whole(world, list, &active, &format!("typing {text:?}"));
            }
            None => assert_eq!(
                scrolled(world),
                Some(0.0),
                "typing {text:?}: {list} shows its top"
            ),
        }
    }

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
            active_option_shows_whole(world, list, &key(world), step)
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
            room_under = shown(world, "Down");
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
    mod ask_drafts;
    mod ask_files;
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
    mod harness_logos;
    mod home;
    mod home_pull_requests;
    mod keys;
    mod launcher;
    mod launcher_outposts;
    mod ledger;
    mod machine_terminals;
    mod markdown;
    mod markdown_annotations;
    mod markdown_ids;
    mod migration_failure;
    mod notebook;
    mod notifications;
    mod opener;
    mod outpost_paths;
    mod outpost_updates;
    mod overview;
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
    mod sender_pairing;
    mod settings;
    mod shared_codex;
    mod shelf_drags;
    mod shelf_entries;
    mod shelf_layout;
    mod shelf_peek;
    mod shortcut_editor;
    mod sidebar;
    mod sidebar_automation;
    mod sidebar_width;
    mod terminal;
    mod terminal_annotations;
    mod terminal_blocks;
    mod terminal_filter;
    mod terminal_find;
    mod terminal_fork_offer;
    mod terminal_ids;
    mod terminal_input_latency;
    mod terminal_kitty;
    mod terminal_labels;
    mod terminal_links;
    mod terminal_stale_build;
    mod terminal_view;
    mod tile_header;
    mod tile_overlays;
    mod title_bar;
    mod update_run;
    mod vim_reading;
    mod whats_new;
}
