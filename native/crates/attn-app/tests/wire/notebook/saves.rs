use super::*;

fn document_title(world: &World) -> Option<String> {
    world
        .screen()
        .find(Target::key("notebook.document.title"))
        .and_then(|n| n.name.clone())
}

/// The write the app sent last, which the script held, put on disk and answered.
fn land_held_write(world: &mut World, disk: &Disk) -> String {
    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    let (path, content) = (
        held["path"].as_str().unwrap(),
        held["content"].as_str().unwrap(),
    );
    disk.put(path, content);
    world.daemon.reply_to(&held, written(path, content));
    world.settle();
    content.to_owned()
}

#[test]
fn typing_while_the_save_before_opening_another_note_is_in_flight_is_saved_before_it_opens() {
    let disk = Disk::with(&[
        ("knowledge/index.md", INDEX),
        ("journal/2026-11-14.md", "# Friday\n"),
    ]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world
        .daemon
        .answer_in_turn("fs_write", [Answer::Hold, Answer::Hold]);
    type_at_end(&mut world, "One.");
    world.gesture(|d| d.click(Target::role("button").name("Friday")));
    type_at_end(&mut world, " Two.");

    land_held_write(&mut world, &disk);
    assert_eq!(document_title(&world).as_deref(), Some("index"));
    let second = land_held_write(&mut world, &disk);

    assert!(second.contains("One. Two."), "{second}");
    assert_eq!(document_title(&world).as_deref(), Some("2026-11-14"));
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn typing_while_the_save_before_closing_the_notebook_is_in_flight_is_saved_before_it_closes() {
    let disk = Disk::with(&[("knowledge/index.md", INDEX)]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world
        .daemon
        .answer_in_turn("fs_write", [Answer::Hold, Answer::Hold]);
    type_at_end(&mut world, "One.");
    world.gesture(|d| d.click(Target::key("notebook.close")));
    type_at_end(&mut world, " Two.");

    land_held_write(&mut world, &disk);
    assert!(world.screen().find(Target::key("notebook.title")).is_some());
    let second = land_held_write(&mut world, &disk);

    assert!(second.contains("One. Two."), "{second}");
    assert!(world.screen().find(Target::key("notebook.title")).is_none());
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn typing_during_an_autosave_then_closing_the_tile_saves_the_newer_text_and_a_restart_shows_it() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let tile = || editor_tile("n", "plan.md");
    let mut world = start_with(&disk, Some(row(vec![pane("a"), tile()])));
    world
        .daemon
        .answer_in_turn("fs_write", [Answer::Hold, Answer::Hold]);
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("First. ");
    });
    world.advance(Duration::from_millis(700));
    world.gesture(|d| d.type_text("Second. "));
    arrange(&mut world, row(vec![pane("a")]), 2);

    land_held_write(&mut world, &disk);
    let second = land_held_write(&mut world, &disk);
    assert!(second.contains("First. Second. "), "{second}");
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());

    let mut restarted = start_with(&disk, Some(row(vec![pane("a"), tile()])));
    let editor = restarted
        .screen()
        .get(Target::key("notebook.tile.n.editor"));
    assert!(
        editor
            .value
            .as_deref()
            .unwrap_or("")
            .contains("First. Second. "),
        "{editor:?}"
    );
    restarted.advance(Duration::from_secs(1));
    assert!(restarted.runtime_is_at_rest());
}

#[test]
fn a_save_that_fails_after_its_tile_left_the_screen_says_so() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let mut world = start_with(
        &disk,
        Some(row(vec![pane("a"), editor_tile("n", "plan.md")])),
    );
    world.daemon.answer_in_turn("fs_write", [Answer::Hold]);
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.type_text("Lost? ");
    });
    world.advance(Duration::from_millis(700));
    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    arrange(&mut world, row(vec![pane("a")]), 2);

    world.daemon.reply_to(
        &held,
        json!({ "event": "fs_write_result", "success": false, "error": "disk full" }),
    );
    world.settle();

    let notice = world
        .screen()
        .find(Target::key("notice.text"))
        .and_then(|n| n.name);
    assert_eq!(notice.as_deref(), Some("Could not save plan.md: disk full"));
}

const OTHER_NOTEBOOK: &str = "/home/me/other-notebook";

/// Two notebook folders: a request without a root reads the one the setting names now.
#[derive(Clone)]
struct Notebooks {
    current: Arc<Mutex<String>>,
    folders: BTreeMap<String, Disk>,
}

impl Notebooks {
    fn serve(&self, daemon: &Daemon) {
        let at = |notebooks: &Notebooks, req: &Value| {
            let root = req["root"]
                .as_str()
                .map_or_else(|| notebooks.current.lock().unwrap().clone(), str::to_owned);
            notebooks.folders[&root].clone()
        };
        let notebooks = self.clone();
        daemon.on("fs_read", move |req| {
            let path = req["path"].as_str().unwrap_or("");
            match at(&notebooks, req).get(path) {
                Some(content) => json!({ "event": "fs_read_result", "success": true,
                    "result": { "path": path, "content": content, "hash": hash(&content) } }),
                None => {
                    json!({ "event": "fs_read_result", "success": false, "error": "not found" })
                }
            }
        });
        let notebooks = self.clone();
        daemon.on("fs_write", move |req| {
            let disk = at(&notebooks, req);
            let path = req["path"].as_str().unwrap_or("").to_owned();
            let content = req["content"].as_str().unwrap_or("").to_owned();
            let current = disk.get(&path).map(|c| hash(&c));
            if req.get("base_hash").and_then(Value::as_str).map(str::to_owned) != current {
                return json!({ "event": "fs_write_result", "success": true,
                    "result": { "path": path, "conflict": true, "current_hash": current.unwrap_or_default() } });
            }
            disk.put(&path, &content);
            json!({ "event": "fs_write_result", "success": true,
                "result": { "path": path, "conflict": false, "hash": hash(&content) } })
        });
    }

    fn switch_to(&self, world: &mut World, root: &str) {
        *self.current.lock().unwrap() = root.to_owned();
        world.daemon.emit(attn_protocol::SettingsUpdatedMessage {
            settings: Some(BTreeMap::from([(
                "notebook.root.effective".to_owned(),
                root.to_owned(),
            )])),
            ..attn_protocol::SettingsUpdatedMessage::new()
        });
        world.settle();
    }
}

#[test]
fn an_edit_open_when_the_notebook_moves_is_saved_into_the_folder_it_was_read_from() {
    let first = Disk::with(&[("knowledge/index.md", INDEX)]);
    let second = Disk::with(&[("knowledge/index.md", INDEX)]);
    let notebooks = Notebooks {
        current: Arc::new(Mutex::new(NOTEBOOK.to_owned())),
        folders: BTreeMap::from([
            (NOTEBOOK.to_owned(), first.clone()),
            (OTHER_NOTEBOOK.to_owned(), second.clone()),
        ]),
    };
    let mut world = start(&first);
    notebooks.serve(&world.daemon);
    open_fullscreen(&mut world);
    type_at_end(&mut world, "Mine.");

    notebooks.switch_to(&mut world, OTHER_NOTEBOOK);
    world.advance(Duration::from_millis(700));

    assert_eq!(second.get("knowledge/index.md").as_deref(), Some(INDEX));
    let saved = first.get("knowledge/index.md").unwrap();
    assert!(saved.contains("Mine."), "{saved}");
    let editor = editor_text(&world, "notebook.editor");
    assert!(!editor.contains("Mine."), "{editor}");
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn undoing_back_to_the_file_while_its_save_is_in_flight_saves_the_file_back() {
    let disk = Disk::with(&[("knowledge/index.md", INDEX)]);
    let mut world = start(&disk);
    open_fullscreen(&mut world);
    world
        .daemon
        .answer_in_turn("fs_write", [Answer::Hold, Answer::Hold]);
    type_at_end(&mut world, "Oops.");
    world.advance(Duration::from_millis(700));
    world.gesture(|d| d.press("mod+z"));

    land_held_write(&mut world, &disk);
    world.advance(Duration::from_millis(700));
    let second = land_held_write(&mut world, &disk);

    assert_eq!(second, INDEX);
    world.advance(Duration::from_secs(3));
    assert!(world.runtime_is_at_rest());
}
