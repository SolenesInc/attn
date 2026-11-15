use attn_protocol::SettingsUpdatedMessage;
use attn_testkit::{Target, World};
use rune::{FileDialogKind, Key};

use super::{accepting, click, field, keeping, named, open, press, rest, retype, value, writes};

const EFFECTIVE: (&str, &str) = ("notebook.root.effective", "/Users/me/attn-notebook");

fn on_desktop(world: &mut World) {
    open(world);
    click(world, "settings.nav.desktop");
}

fn pills(world: &World) -> Vec<String> {
    world
        .screen()
        .children(Target::key("settings.pills"))
        .into_iter()
        .filter_map(|n| n.name)
        .collect()
}

fn checked(world: &World, setting: &str) -> bool {
    world
        .screen()
        .get(Target::key(format!("settings.switch.{setting}")))
        .is("checked")
}

#[test]
fn the_notebook_folder_shows_the_profile_field_and_the_folder_it_resolves_to() {
    let (daemon, _) = accepting(&[
        ("notebook.root", "~/work-notes"),
        ("notebook.root.effective", "/Users/me/work-notes"),
    ]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    let notebook = field("notebook.root");
    assert_eq!(value(&world, &notebook), "~/work-notes");
    assert_eq!(named(&world, &notebook).as_deref(), Some("Notebook folder"));
    assert_eq!(
        named(&world, "settings.notebook.effective").as_deref(),
        Some("This profile uses: /Users/me/work-notes")
    );
    rest(&mut world);
}

#[test]
fn the_notebook_field_is_empty_while_the_profile_uses_the_default_folder() {
    let (daemon, _) = accepting(&[EFFECTIVE]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    assert_eq!(value(&world, &field("notebook.root")), "");
    assert_eq!(
        named(&world, "settings.notebook.effective").as_deref(),
        Some("This profile uses: /Users/me/attn-notebook")
    );
    rest(&mut world);
}

#[test]
fn the_notebook_folder_saves_when_focus_leaves_and_an_empty_value_goes_back_to_the_default() {
    let (daemon, _) = accepting(&[EFFECTIVE]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    let notebook = field("notebook.root");

    retype(&mut world, &notebook, "/Users/me/work");
    click(&mut world, "settings.heading");
    retype(&mut world, &notebook, "");
    click(&mut world, "settings.heading");

    assert_eq!(
        writes(&world),
        [
            ("notebook.root".to_owned(), "/Users/me/work".to_owned()),
            ("notebook.root".to_owned(), String::new()),
        ]
    );
    rest(&mut world);
}

#[test]
fn a_browsed_notebook_folder_the_daemon_refuses_shows_why_and_keeps_the_folder_in_use() {
    let refusal = "profile Side's Notebook folder /Users/me/attn-notebook/side is inside the default Notebook folder /Users/me/attn-notebook";
    let (daemon, _) = keeping(&[EFFECTIVE], move |key, _| {
        (key == "notebook.root").then(|| refusal.to_owned())
    });
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    click(&mut world, "settings.browse.notebook.root");
    assert_eq!(
        world.file_dialog(),
        Some((FileDialogKind::Folder, "Select Notebook Folder".to_owned()))
    );
    world.answer_file_dialog(Some("/Users/me/attn-notebook/browsed"));

    assert_eq!(
        writes(&world),
        [(
            "notebook.root".to_owned(),
            "/Users/me/attn-notebook/browsed".to_owned()
        )]
    );
    assert_eq!(
        value(&world, &field("notebook.root")),
        "/Users/me/attn-notebook/browsed"
    );
    assert_eq!(
        named(&world, "settings.refusal.notebook.root").as_deref(),
        Some(refusal)
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.saved.notebook.root"))
            .is_none()
    );
    assert_eq!(
        named(&world, "settings.notebook.effective").as_deref(),
        Some("This profile uses: /Users/me/attn-notebook")
    );
    rest(&mut world);
}

#[test]
fn a_cancelled_folder_dialog_saves_nothing() {
    let (daemon, _) = accepting(&[("projects_directory", "/Users/me/projects")]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);

    click(&mut world, "settings.browse.projects_directory");
    assert_eq!(
        world.file_dialog(),
        Some((
            FileDialogKind::Folder,
            "Select Projects Directory".to_owned()
        ))
    );
    world.answer_file_dialog(None);

    assert!(writes(&world).is_empty());
    assert_eq!(
        value(&world, &field("projects_directory")),
        "/Users/me/projects"
    );
    rest(&mut world);
}

#[test]
fn the_projects_directory_pill_says_edited_until_the_path_saves_and_browse_saves_the_chosen_folder()
{
    let (daemon, stored) = accepting(&[("projects_directory", "/Users/me/projects")]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    let projects = field("projects_directory");
    assert_eq!(
        named(&world, &projects).as_deref(),
        Some("Projects directory")
    );
    assert_eq!(pills(&world), ["project path saved", "sent files open"]);

    retype(&mut world, &projects, "/Users/me/src");
    assert_eq!(pills(&world)[0], "project path edited");
    press(&mut world, Key::Enter);
    assert_eq!(
        stored.lock().unwrap()["projects_directory"],
        "/Users/me/src"
    );
    assert_eq!(pills(&world)[0], "project path saved");

    click(&mut world, "settings.browse.projects_directory");
    world.answer_file_dialog(Some("/Users/me/code"));
    assert_eq!(
        writes(&world).last().unwrap(),
        &("projects_directory".to_owned(), "/Users/me/code".to_owned())
    );
    assert_eq!(value(&world, &projects), "/Users/me/code");
    assert_eq!(pills(&world)[0], "project path saved");
    rest(&mut world);
}

#[test]
fn the_editor_saves_on_enter() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    let editor = field("editor_executable");
    assert_eq!(named(&world, &editor).as_deref(), Some("Editor"));

    retype(&mut world, &editor, "zed --wait");
    press(&mut world, Key::Enter);

    assert_eq!(
        writes(&world),
        [("editor_executable".to_owned(), "zed --wait".to_owned())]
    );
    assert!(
        world
            .screen()
            .find(Target::key("settings.saved.editor_executable"))
            .is_some()
    );
    rest(&mut world);
}

#[test]
fn sent_files_open_by_default_and_the_switch_saves_off_and_back_on() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    assert!(checked(&world, "open_sent_files_enabled"));

    click(&mut world, "settings.switch.open_sent_files_enabled");
    assert!(!checked(&world, "open_sent_files_enabled"));
    assert_eq!(pills(&world)[1], "sent files ignored");
    click(&mut world, "settings.switch.open_sent_files_enabled");
    assert!(checked(&world, "open_sent_files_enabled"));

    assert_eq!(
        writes(&world),
        [
            ("open_sent_files_enabled".to_owned(), "false".to_owned()),
            ("open_sent_files_enabled".to_owned(), "true".to_owned()),
        ]
    );
    rest(&mut world);
}

#[test]
fn the_worktree_sweep_switch_follows_a_change_from_the_shell_and_saves() {
    let (daemon, _) = accepting(&[]);
    let mut world = World::start(daemon);
    on_desktop(&mut world);
    assert!(checked(&world, "worktree_sweep_enabled"));

    world.daemon.emit(SettingsUpdatedMessage {
        settings: Some([("worktree_sweep_enabled".to_owned(), "false".to_owned())].into()),
        ..SettingsUpdatedMessage::new()
    });
    world.settle();
    assert!(!checked(&world, "worktree_sweep_enabled"));

    click(&mut world, "settings.switch.worktree_sweep_enabled");
    assert_eq!(
        writes(&world),
        [("worktree_sweep_enabled".to_owned(), "true".to_owned())]
    );
    assert!(checked(&world, "worktree_sweep_enabled"));
    rest(&mut world);
}
