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

/// Pops editor tile n out and holds the save of what it types there, brings the tile back,
/// types in the main window's tile until that edit saves and then once more, and answers the
/// held save with the conflict the main window's save made. Every later write is held.
fn conflicted_pop_out_under_an_edited_tile(disk: &Disk) -> World {
    let (mut world, window) = popped_out_editor(disk);
    world
        .daemon
        .answer_in_turn("fs_write", std::iter::repeat_with(|| Answer::Hold).take(5));
    type_in_tile_window(&mut world, &window, "Popped-out draft.");
    world.advance(Duration::from_millis(700));
    let popped = world.daemon.sent_of("fs_write").pop().unwrap();

    arrange(
        &mut world,
        row(vec![pane("a"), editor_tile("n", "plan.md")]),
        3,
    );
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.press("mod+down");
        d.type_text(" Main saved.");
    });
    world.advance(Duration::from_millis(700));
    land_held_write(&mut world, disk);
    world.gesture(|d| {
        d.click(Target::key("notebook.tile.n.editor"));
        d.press("mod+down");
        d.type_text(" Main unsaved.");
    });
    world.daemon.reply_to(
        &popped,
        json!({ "event": "fs_write_result", "success": true,
            "result": { "path": "plan.md", "conflict": true,
                "current_hash": hash(&disk.get("plan.md").unwrap()) } }),
    );
    world.settle();
    assert!(main_tile_editor(&world).contains("Main unsaved."));
    world
}

fn shows_conflict(world: &World) -> bool {
    world
        .screen()
        .find(Target::key("notebook.tile.n.document.conflict"))
        .is_some()
}

#[test]
fn a_conflicted_pop_out_draft_waiting_under_an_edited_tile_survives_the_tile_leaving_and_shows_when_it_returns()
 {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let mut world = conflicted_pop_out_under_an_edited_tile(&disk);

    arrange(&mut world, row(vec![pane("a")]), 4);
    assert!(land_held_write(&mut world, &disk).contains("Main unsaved."));
    arrange(
        &mut world,
        row(vec![pane("a"), editor_tile("n", "plan.md")]),
        5,
    );

    assert!(
        main_tile_editor(&world).contains("Popped-out draft."),
        "{}",
        world.screen()
    );
    assert!(shows_conflict(&world));
    world.gesture(|d| d.click(Target::role("button").name("Overwrite anyway")));
    assert!(land_held_write(&mut world, &disk).contains("Popped-out draft."));
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest() && world.tile_windows_are_at_rest());
}

#[test]
fn a_conflicted_pop_out_draft_waiting_under_an_edited_tile_shows_there_once_that_edit_saves() {
    let disk = Disk::with(&[("plan.md", PLAN)]);
    let mut world = conflicted_pop_out_under_an_edited_tile(&disk);
    assert!(!shows_conflict(&world));

    world.advance(Duration::from_millis(700));
    assert!(land_held_write(&mut world, &disk).contains("Main unsaved."));

    assert!(
        main_tile_editor(&world).contains("Popped-out draft."),
        "{}",
        world.screen()
    );
    assert!(shows_conflict(&world));
    world.advance(Duration::from_secs(7));
    assert!(world.runtime_is_at_rest() && world.tile_windows_are_at_rest());
}

#[derive(Clone, Copy)]
enum Failure {
    Conflict,
    WriteError(&'static str),
}

const DISK_FULL: Failure = Failure::WriteError("disk full");

/// A real daemon's error for a note in a deeply nested folder of Latin and CJK names.
const NESTED_PATH: Failure = Failure::WriteError(
    "open /tmp/notebook-save-review.WzYy6o/notebook-2693475008/engineering-platform-and-infrastructure/production-rollout-and-deployment/customer-migration-and-backfill/architecture-review-and-acceptance/europe-stockholm-hardware-replacement/dependencies-and-migration-state/release-coordination-and-communications/team-ownership-and-followup/本番環境のクラスタにおけるバックアップと復旧手順/内部インフラのリリース運用変更の検証タスク一覧/東京リージョン向けの障害復旧と段階的な移行作業/operator-guides-and-incident-reports/shift-handoff-and-investigation-notes/.index.md.tmp.1716.1795179264032749842: permission denied",
);

/// The index, a long note, open in `document`, scrolled far down with the caret at the end of
/// the line a third down the view, typed into, and its save held: the line the caret is on.
fn typed_far_down(world_of: impl FnOnce(&Disk) -> World, document: &str) -> (Disk, World, String) {
    typed_far_down_at(world_of, document, 1.0 / 3.0)
}

/// [`typed_far_down`] with the caret on the first whole line `down` of the way down the view.
fn typed_far_down_at(
    world_of: impl FnOnce(&Disk) -> World,
    document: &str,
    down: f32,
) -> (Disk, World, String) {
    let disk = Disk::with(&[(NOTE, &twice_titled_note())]);
    let mut world = world_of(&disk);
    let scroll = Target::key(format!("{document}.scroll"));
    let [x, y, w, h] = world.screen().bounds(scroll.clone()).unwrap();
    world.gesture(|d| d.runtime.wheel(x + w / 2.0, y + h / 2.0, -1400.0));
    let screen = world.screen();
    let line = screen
        .descendants(scroll)
        .into_iter()
        .filter_map(|n| n.key)
        .filter(|key| key.starts_with(&format!("{document}.editor.line.")))
        .find(|key| {
            screen
                .bounds(Target::key(key))
                .is_some_and(|[_, top, _, _]| top >= y + h * down)
        })
        .expect("a line that far down the view");
    world
        .daemon
        .answer_in_turn("fs_write", [Answer::Hold, Answer::Hold]);
    world.gesture(|d| {
        d.click(Target::key(&line));
        d.press("end");
        d.type_text(" Far down.");
    });
    world.advance(Duration::from_millis(700));
    (disk, world, line)
}

const NOTE: &str = "knowledge/index.md";

fn fullscreen(window: Window) -> impl FnOnce(&Disk) -> World {
    move |disk| {
        let mut world = start_in(disk, None, window);
        open_fullscreen(&mut world);
        world
    }
}

fn tile(window: Window) -> impl FnOnce(&Disk) -> World {
    move |disk| {
        let note = editor_tile("n", r#"{"path":"knowledge/index.md"}"#);
        start_in(disk, Some(row(vec![note])), window)
    }
}

/// Answers `held`, a save the script held, with `failure`.
fn fail(daemon: &Daemon, held: &Value, disk: &Disk, failure: &Failure) {
    let reply = match failure {
        Failure::Conflict => {
            disk.put(NOTE, "changed elsewhere\n");
            json!({ "event": "fs_write_result", "success": true,
                "result": { "path": NOTE, "conflict": true, "current_hash": hash("changed elsewhere\n") } })
        }
        Failure::WriteError(error) => {
            json!({ "event": "fs_write_result", "success": false, "error": error })
        }
    };
    daemon.reply_to(held, reply);
}

fn alert(document: &str, failure: &Failure) -> String {
    match failure {
        Failure::Conflict => format!("{document}.document.conflict"),
        Failure::WriteError(_) => "notebook.save-error".to_owned(),
    }
}

/// Where `line` was drawn on each frame of `rec` from its cause on, top edges in window points.
fn tops(rec: &rune::motion_gate::Recording, line: &str) -> Vec<f32> {
    let from = rec.inputs[0].0;
    rec.tracks
        .iter()
        .find(|t| t.path.ends_with(&format!("/{line}")))
        .map(|t| {
            t.frames
                .iter()
                .filter(|(k, _)| *k >= from)
                .map(|(_, s)| s.rect[1])
                .collect()
        })
        .unwrap_or_default()
}

/// `line` stood at `top` on every frame `rec` drew, and stands inside the note's view.
fn stays_where_it_shows(
    world: &World,
    rec: &rune::motion_gate::Recording,
    document: &str,
    line: &str,
    top: f32,
) {
    let drawn = tops(rec, line);
    assert!(!drawn.is_empty(), "{line} was not drawn");
    assert!(
        drawn.iter().all(|y| (y - top).abs() < 0.5),
        "the caret's line left {top}: {drawn:?}"
    );
    let [_, y, _, h] = world
        .screen()
        .bounds(Target::key(format!("{document}.scroll")))
        .unwrap();
    let [_, line_y, _, line_h] = world.screen().bounds(Target::key(line)).unwrap();
    assert!(
        line_y >= y && line_y + line_h <= y + h,
        "the caret's line at {line_y} is outside the note's view {:?}",
        [y, h]
    );
}

/// A note scrolled far down whose save fails: the failure, with each control that recovers
/// from it, stands inside the document and outside what scrolls, and the line the caret is on
/// stays where it shows from the first frame the failure draws, and again when it goes.
fn a_failed_save_far_down_a_long_note_keeps_the_caret_where_it_shows(
    document: &str,
    world_of: impl FnOnce(&Disk) -> World,
    failure: Failure,
) {
    let (disk, mut world, line) = typed_far_down(world_of, document);
    let editor = format!("{document}.editor");
    let top = top_of(&world, &line);
    let selection = world.screen().get(Target::key(&editor)).selection;

    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    let arrived = world.record(60.0, 0.5, |daemon, _| fail(daemon, &held, &disk, &failure));
    stays_where_it_shows(&world, &arrived, document, &line, top);

    let screen = world.screen();
    let alert = alert(document, &failure);
    assert_eq!(screen.get(Target::key(&alert)).role, "alert");
    let mut shown = vec![alert];
    if let Failure::Conflict = failure {
        shown.push(format!("{document}.document.reload"));
        shown.push(format!("{document}.document.overwrite"));
    }
    let [dx, dy, dw, dh] = screen
        .bounds(Target::key(format!("{document}.document")))
        .unwrap();
    let scrolled = screen.descendants(Target::key(format!("{document}.scroll")));
    for key in &shown {
        let [x, y, w, h] = screen.bounds(Target::key(key)).unwrap();
        assert!(
            x >= dx && y >= dy && x + w <= dx + dw && y + h <= dy + dh,
            "{key} at {:?} is not inside the document at {:?}",
            [x, y, w, h],
            [dx, dy, dw, dh]
        );
        assert!(
            !scrolled
                .iter()
                .any(|n| n.key.as_deref() == Some(key.as_str())),
            "{key} scrolls with the note"
        );
    }
    assert_eq!(screen.get(Target::key(&editor)).selection, selection);

    match failure {
        Failure::Conflict => {
            world
                .daemon
                .answer_in_turn("fs_write", [Answer::Reply(written(NOTE, "overwritten"))]);
            let overwrite = Target::key(format!("{document}.document.overwrite"));
            let gone = world.record(60.0, 0.5, |_, app| app.click(overwrite));
            stays_where_it_shows(&world, &gone, document, &line, top);
        }
        Failure::WriteError(_) => {
            world.advance(Duration::from_millis(700));
            assert!(
                world
                    .screen()
                    .find(Target::key("notebook.save-error"))
                    .is_none()
            );
            assert!(
                (top_of(&world, &line) - top).abs() < 0.5,
                "the caret's line left {top}"
            );
        }
    }
}

#[test]
fn a_conflict_far_down_a_long_note_keeps_the_caret_where_it_shows() {
    a_failed_save_far_down_a_long_note_keeps_the_caret_where_it_shows(
        "notebook",
        fullscreen(WIDE),
        Failure::Conflict,
    );
}

#[test]
fn a_write_error_far_down_a_long_note_keeps_the_caret_where_it_shows() {
    a_failed_save_far_down_a_long_note_keeps_the_caret_where_it_shows(
        "notebook",
        fullscreen(WIDE),
        DISK_FULL,
    );
}

#[test]
fn a_conflict_far_down_a_long_note_in_an_editor_tile_keeps_the_caret_where_it_shows() {
    a_failed_save_far_down_a_long_note_keeps_the_caret_where_it_shows(
        "notebook.tile.n",
        tile(WIDE),
        Failure::Conflict,
    );
}

#[test]
fn a_write_error_far_down_a_long_note_in_an_editor_tile_keeps_the_caret_where_it_shows() {
    a_failed_save_far_down_a_long_note_keeps_the_caret_where_it_shows(
        "notebook.tile.n",
        tile(WIDE),
        DISK_FULL,
    );
}

/// In the smallest window at the largest text, each failure keeps its actions whole first, then
/// three whole lines of the note in view, however long its sentence: one too long for its band
/// scrolls inside it. The note gives up room only for the actions: a conflict in the fullscreen
/// Notebook leaves it less than three lines' room, which still shows three whole lines where
/// this note is scrolled.
fn a_failed_save_keeps_its_actions_whole_then_three_lines_in_the_smallest_window<W>(
    document: &str,
    world_of: fn(Window) -> W,
) where
    W: FnOnce(&Disk) -> World,
{
    for (failure, gives_way) in [
        (Failure::Conflict, false),
        (DISK_FULL, false),
        (NESTED_PATH, true),
    ] {
        let (disk, mut world, _) = typed_far_down(world_of(SMALLEST), document);
        let held = world.daemon.sent_of("fs_write").pop().unwrap();
        fail(&world.daemon, &held, &disk, &failure);
        world.settle();
        let screen = world.screen();
        let scroll = Target::key(format!("{document}.scroll"));
        let [_, y, _, h] = screen.bounds(scroll.clone()).unwrap();
        let [_, wy, _, wh] = screen.bounds(Target::key("window")).unwrap();
        assert!(
            y >= wy && y + h <= wy + wh + 0.5,
            "the note's view at {:?} runs out of the window {:?}",
            [y, h],
            [wy, wh]
        );
        let whole = whole_lines_in_view(&world, document);
        let alert = alert(document, &failure);
        assert!(
            whole >= 3,
            "{alert} leaves {whole} whole lines in a view {h} tall"
        );
        let [_, ay, _, ah] = screen.bounds(Target::key(&alert)).unwrap();
        let [_, by, _, bh] = screen
            .bounds(Target::key(format!("{document}.document.sheet.notices")))
            .unwrap();
        assert!(
            ay >= by && ay + ah <= by + bh,
            "{alert} at {:?} runs out of its band",
            [ay, ah]
        );
        let sentence = Target::key(format!("{alert}.sentence"));
        let [_, sy, _, sh] = screen.bounds(sentence.clone()).unwrap();
        assert!(
            sh > 0.0 && sy >= ay && sy + sh <= ay + ah,
            "{alert}'s sentence shows at {:?}, out of its notice",
            [sy, sh]
        );
        if gives_way {
            world.gesture(|d| {
                d.runtime.accessibility_action(
                    &format!("{alert}.sentence"),
                    rune::accesskit::Action::ScrollDown,
                    None,
                );
            });
            world.settle();
            assert!(
                world.screen().scrolled(sentence).is_some_and(|y| y > 0.0),
                "{alert}'s sentence does not scroll inside its band"
            );
        }
        if let Failure::Conflict = failure {
            for action in ["document.reload", "document.overwrite"] {
                let [_, y, _, h] = screen
                    .bounds(Target::key(format!("{document}.{action}")))
                    .unwrap();
                assert!(
                    y >= ay && y + h <= ay + ah,
                    "{action} at {:?} runs out of its notice at {:?}",
                    [y, h],
                    [ay, ah]
                );
            }
        }
    }
}

#[test]
fn in_the_smallest_window_a_failed_save_keeps_its_actions_whole_then_the_note_three_lines() {
    a_failed_save_keeps_its_actions_whole_then_three_lines_in_the_smallest_window(
        "notebook", fullscreen,
    );
}

#[test]
fn in_the_smallest_window_a_failed_save_keeps_its_actions_whole_then_a_tiles_note_three_lines() {
    a_failed_save_keeps_its_actions_whole_then_three_lines_in_the_smallest_window(
        "notebook.tile.n",
        tile,
    );
}

/// How many of the note's lines show whole in its view.
fn whole_lines_in_view(world: &World, document: &str) -> usize {
    let screen = world.screen();
    let scroll = Target::key(format!("{document}.scroll"));
    let [_, y, _, h] = screen.bounds(scroll.clone()).unwrap();
    screen
        .descendants(scroll)
        .into_iter()
        .filter_map(|n| n.key)
        .filter(|key| key.starts_with(&format!("{document}.editor.line.")))
        .filter(|key| {
            screen
                .bounds(Target::key(key))
                .is_some_and(|[_, top, _, lh]| top >= y - 0.5 && top + lh <= y + h + 0.5)
        })
        .count()
}

/// A write error too long for its band reads to its end from the keyboard: from Find in note,
/// Shift-Tab reaches its sentence, and Page Down scrolls the sentence, the note staying where it
/// was.
fn a_long_write_error_reads_to_its_end_from_the_keyboard<W>(
    document: &str,
    world_of: fn(Window) -> W,
) where
    W: FnOnce(&Disk) -> World,
{
    let (disk, mut world, line) = typed_far_down(world_of(SMALLEST), document);
    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    fail(&world.daemon, &held, &disk, &NESTED_PATH);
    world.settle();
    let editor = format!("{document}.editor");
    let note = Target::key(format!("{document}.scroll"));
    let (top, scrolled, selection) = (
        top_of(&world, &line),
        world.screen().scrolled(note.clone()),
        world.screen().get(Target::key(&editor)).selection,
    );
    let sentence = "notebook.save-error.sentence";
    world.gesture(|d| d.press("mod+f"));
    let mut path = vec![keyboard_at(&world)];
    while path.len() < 12 && path.last().map(String::as_str) != Some(sentence) {
        world.gesture(|d| d.press("shift+tab"));
        path.push(keyboard_at(&world));
    }
    assert_eq!(
        path.last().map(String::as_str),
        Some(sentence),
        "Shift-Tab from Find in note passes {path:?}"
    );
    world.gesture(|d| d.press("pagedown"));
    world.settle();
    let screen = world.screen();
    assert!(
        screen
            .scrolled(Target::key(sentence))
            .is_some_and(|y| y > 0.0),
        "Page Down leaves the sentence at its start"
    );
    assert_eq!(screen.scrolled(note), scrolled, "Page Down moved the note");
    assert!(
        (top_of(&world, &line) - top).abs() < 0.5,
        "the caret's line left {top}"
    );
    assert_eq!(screen.get(Target::key(&editor)).selection, selection);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_long_write_error_reads_to_its_end_from_the_keyboard_in_the_smallest_window() {
    a_long_write_error_reads_to_its_end_from_the_keyboard("notebook", fullscreen);
}

#[test]
fn a_long_write_error_in_an_editor_tile_reads_to_its_end_from_the_keyboard() {
    a_long_write_error_reads_to_its_end_from_the_keyboard("notebook.tile.n", tile);
}

/// An editor tile stacked over an agent, with its file tree open, at the largest text.
fn stacked_tile_with_its_tree_open(disk: &Disk) -> World {
    const STACKED: Window = Window {
        size: (1200.0, 800.0),
        text: 1.5,
    };
    let note = editor_tile("n", r#"{"path":"knowledge/index.md"}"#);
    let mut world = start_in(disk, Some(row(vec![below(note, pane("a"))])), STACKED);
    let fold = Target::key("notebook.tile.n.fold.tree");
    if world.screen().get(fold.clone()).name.as_deref() == Some("Show file tree") {
        world.gesture(|d| d.click(fold));
    }
    world
}

/// A conflict in a tile too short for its notice and three lines keeps a whole line of the
/// draft in view, so the person sees what Overwrite anyway would write, and both recovery
/// actions show when Shift-Tab from Find in note reaches them.
#[test]
fn a_conflict_in_a_short_stacked_editor_tile_keeps_a_line_of_the_draft_and_its_recovery() {
    let document = "notebook.tile.n";
    let (disk, mut world, _) = typed_far_down(stacked_tile_with_its_tree_open, document);
    assert_eq!(
        world
            .screen()
            .get(Target::key("notebook.tile.n.fold.tree"))
            .name
            .as_deref(),
        Some("Hide file tree")
    );
    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    fail(&world.daemon, &held, &disk, &Failure::Conflict);
    world.settle();
    let whole = whole_lines_in_view(&world, document);
    assert!(
        whole >= 1,
        "the conflict leaves {whole} whole lines of the draft"
    );
    world.gesture(|d| d.press("mod+f"));
    for action in ["document.overwrite", "document.reload"] {
        world.gesture(|d| d.press("shift+tab"));
        world.settle();
        let key = format!("{document}.{action}");
        assert_eq!(keyboard_at(&world), key);
        let screen = world.screen();
        let [_, y, _, h] = screen.bounds(Target::key(&key)).unwrap();
        let [_, by, _, bh] = screen
            .bounds(Target::key(format!("{document}.document.sheet.notices")))
            .unwrap();
        assert!(
            y >= by - 0.5 && y + h <= by + bh + 0.5,
            "{key} at {:?} is out of view in its band at {:?}",
            [y, h],
            [by, bh]
        );
    }
    assert!(whole_lines_in_view(&world, document) >= 1);
    world.advance(Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

/// What has the keyboard: its key, else its name, else its role.
fn keyboard_at(world: &World) -> String {
    world
        .screen()
        .focused()
        .map(|n| n.key.or(n.name).unwrap_or(n.role))
        .unwrap_or_default()
}

/// The stops Tab passes from Reload from disk to the note's editor.
fn tab_path_from_reload(world: &mut World, document: &str) -> Vec<String> {
    let reload = format!("{document}.document.reload");
    world.gesture(|d| {
        assert!(
            d.runtime
                .accessibility_action(&reload, rune::accesskit::Action::Focus, None)
        );
    });
    let mut path = vec![keyboard_at(world)];
    while path.len() < 20 && path.last().map(String::as_str) != Some(&format!("{document}.editor"))
    {
        world.gesture(|d| d.press("tab"));
        path.push(keyboard_at(world));
    }
    path
}

/// Tab reaches the conflict's recovery after the title line and before Find in note, as it
/// did when the notice stood in the note's header, then the note.
fn tab_reaches_a_conflicts_recovery_before_find_in_note_and_the_note(
    document: &str,
    world_of: impl FnOnce(&Disk) -> World,
) {
    let (disk, mut world, _) = typed_far_down(world_of, document);
    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    fail(&world.daemon, &held, &disk, &Failure::Conflict);
    world.settle();
    let (reload, overwrite, find, editor) = (
        format!("{document}.document.reload"),
        format!("{document}.document.overwrite"),
        format!("{document}.editor.find"),
        format!("{document}.editor"),
    );
    assert_eq!(
        tab_path_from_reload(&mut world, document),
        [reload.as_str(), &overwrite, &editor]
    );
    world.gesture(|d| d.press("mod+f"));
    assert_eq!(keyboard_at(&world), find);
    assert_eq!(
        tab_path_from_reload(&mut world, document),
        [
            reload.as_str(),
            &overwrite,
            &find,
            "Previous",
            "Next",
            "Close",
            &editor
        ]
    );
}

#[test]
fn tab_reaches_a_conflicts_recovery_before_find_in_note() {
    tab_reaches_a_conflicts_recovery_before_find_in_note_and_the_note("notebook", fullscreen(WIDE));
}

#[test]
fn tab_reaches_a_conflicts_recovery_before_find_in_note_in_an_editor_tile() {
    tab_reaches_a_conflicts_recovery_before_find_in_note_and_the_note(
        "notebook.tile.n",
        tile(WIDE),
    );
}

/// The caret's line at the top of the view, where a conflict's notice comes to stand: it
/// shows just under the notice, at the view's new top, from the first frame.
#[test]
fn a_conflict_over_the_caret_s_line_leaves_it_just_under_the_notice() {
    let (disk, mut world, line) = typed_far_down_at(fullscreen(WIDE), "notebook", 0.0);
    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    let arrived = world.record(60.0, 0.5, |daemon, _| {
        fail(daemon, &held, &disk, &Failure::Conflict)
    });
    let [_, view_top, _, _] = world
        .screen()
        .bounds(Target::key("notebook.scroll"))
        .unwrap();
    stays_where_it_shows(&world, &arrived, "notebook", &line, view_top);
}

const FILLER_LINES: usize = 40;

/// A note whose one long paragraph, after a title, a blank line and [`FILLER_LINES`] short
/// lines, wraps over many lines in the smallest window.
fn wrapped_note() -> String {
    let filler: String = (0..FILLER_LINES).map(|i| format!("Line {i}.\n")).collect();
    let paragraph =
        "A long paragraph that wraps over many lines in the smallest window. ".repeat(16);
    format!("# Plan\n\n{filler}{paragraph}\n{filler}")
}

fn bounds(world: &World, key: &str) -> [f32; 4] {
    world
        .screen()
        .bounds(Target::key(key))
        .unwrap_or_else(|| panic!("{key} is not on screen"))
}

/// In the smallest window, the long paragraph scrolled so its first lines are above the view,
/// the caret placed on its line at the view's top, and a letter typed there, its save held.
fn typed_at_the_top_inside_a_wrapped_paragraph(
    world_of: impl FnOnce(&Disk) -> World,
    document: &str,
) -> (Disk, World) {
    let disk = Disk::with(&[(NOTE, &wrapped_note())]);
    let mut world = world_of(&disk);
    let [x, y, w, h] = bounds(&world, &format!("{document}.scroll"));
    let paragraph = format!("{document}.editor.line.{}", FILLER_LINES + 2);
    while world
        .screen()
        .bounds(Target::key(&paragraph))
        .is_none_or(|[_, top, _, tall]| top > y - 2.0 * h / 10.0 || top + tall < y + h)
    {
        world.gesture(|d| d.runtime.wheel(x + w / 2.0, y + h / 2.0, -60.0));
    }
    world.daemon.answer_in_turn("fs_write", [Answer::Hold]);
    world.gesture(|d| {
        d.runtime.pointer_down(x + w / 2.0, y + 12.0);
        d.runtime.pointer_up(x + w / 2.0, y + 12.0);
        d.type_text("x");
    });
    world.advance(Duration::from_millis(700));
    (disk, world)
}

/// A write error too long for its band, over a caret near the top of the view inside a wrapped
/// paragraph whose first lines are scrolled away: the band takes the room the caret was in, and
/// the caret shows whole just under it.
fn a_long_write_error_leaves_its_caret_whole(
    world_of: impl FnOnce(&Disk) -> World,
    document: &str,
) {
    let (disk, mut world) = typed_at_the_top_inside_a_wrapped_paragraph(world_of, document);
    let caret = format!("{document}.editor.caret");
    let [_, was, _, _] = bounds(&world, &caret);
    let held = world.daemon.sent_of("fs_write").pop().unwrap();
    fail(&world.daemon, &held, &disk, &NESTED_PATH);
    world.settle();
    let [_, y, _, h] = bounds(&world, &format!("{document}.scroll"));
    let [_, top, _, tall] = bounds(&world, &caret);
    assert!(
        top >= y - 0.5 && top + tall <= y + h + 0.5,
        "the caret, at {was} before the error, is at {:?} in a view at {:?}",
        [top, tall],
        [y, h]
    );
}

#[test]
fn a_long_write_error_leaves_a_caret_inside_a_wrapped_paragraph_whole() {
    a_long_write_error_leaves_its_caret_whole(fullscreen(SMALLEST), "notebook");
}

#[test]
fn a_long_write_error_leaves_a_caret_inside_a_wrapped_paragraph_in_an_editor_tile_whole() {
    a_long_write_error_leaves_its_caret_whole(tile(SMALLEST), "notebook.tile.n");
}

/// In the smallest window with the file tree or the context rail open beside the note, a
/// conflict's actions wrap onto two lines and stay whole inside their notice.
#[test]
fn with_a_pane_open_in_the_smallest_window_a_conflict_keeps_its_wrapped_actions_whole() {
    for pane in ["tree", "rail"] {
        let (disk, mut world, _) = typed_far_down(fullscreen(SMALLEST), "notebook");
        let held = world.daemon.sent_of("fs_write").pop().unwrap();
        fail(&world.daemon, &held, &disk, &Failure::Conflict);
        world.settle();
        world.gesture(|d| d.click(Target::key(format!("notebook.fold.{pane}"))));
        world.settle();
        let [_, ay, _, ah] = bounds(&world, "notebook.document.conflict");
        let [_, reload, _, _] = bounds(&world, "notebook.document.reload");
        for action in ["notebook.document.reload", "notebook.document.overwrite"] {
            let [_, y, w, h] = bounds(&world, action);
            assert!(
                w > 0.0 && y >= ay && y + h <= ay + ah,
                "with the {pane} open, {action} at {:?} runs out of its notice at {:?}",
                [y, w, h],
                [ay, ah]
            );
        }
        assert!(
            bounds(&world, "notebook.document.overwrite")[1] > reload,
            "with the {pane} open, the actions fit one line: widen the pane or the text so they wrap"
        );
    }
}
