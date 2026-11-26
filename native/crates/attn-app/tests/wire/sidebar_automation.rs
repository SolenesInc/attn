use attn_protocol::AutomationProvenance;
use attn_testkit::{Daemon, Target, World};

use super::sidebar::{desktop, pane, session, state};

fn automation_desktop(with_shell: bool) -> World {
    let work = session("manual-work", "working");
    let mut run = session("run", "working");
    run.automation = Some(AutomationProvenance {
        definition_id: 10,
        definition_name: "Review main".into(),
        run_id: "r-review".into(),
        ..Default::default()
    });
    let mut delegate = session("delegate", "working");
    delegate.dispatcher_session_id = Some(run.id.clone());
    let mut review_panes = vec![
        pane("review", "p-run", &run.id),
        pane("review", "p-delegate", &delegate.id),
    ];
    let work_desktop = desktop("work", "a", "Work", vec![pane("work", "p-work", &work.id)]);
    let mut sessions = vec![work, run, delegate];
    if with_shell {
        let mut shell = session("debug-shell", "idle");
        shell.agent = "shell".into();
        shell.parent_session_id = Some("delegate".into());
        let mut shell_pane = pane("review", "p-debug-shell", &shell.id);
        shell_pane.kind = "shell".into();
        review_panes.push(shell_pane);
        sessions.push(shell);
    }
    let initial = state(
        sessions,
        vec![work_desktop, desktop("review", "b", "Review", review_panes)],
    );
    let mut world = World::start(Daemon::scripted().state(initial));
    world.open_desktop();
    world
}

#[test]
fn dropping_on_an_automation_delegate_moves_the_tile_to_its_desktop() {
    let mut world = automation_desktop(false);
    for action in ["desktop_set_current", "desktop_move_leaf"] {
        world.daemon.on(action, move |_| {
            serde_json::json!({
                "event": "profile_action_result",
                "action": action,
                "success": true,
            })
        });
    }
    let [x, y, width, height] = world
        .screen()
        .bounds(Target::key("sidebar.session.delegate"))
        .expect("the delegate is visible in Automations");
    world.gesture(|app| {
        app.drag(
            Target::key("sidebar.session.manual-work"),
            (x + width / 2.0, y + height / 2.0),
        );
    });
    let moves = world.daemon.sent_of("desktop_move_leaf");
    assert_eq!(moves.len(), 1, "{moves:?}");
    assert_eq!(moves[0]["source_desktop_id"], "work");
    assert_eq!(moves[0]["leaf_id"], "p-work");
    assert_eq!(moves[0]["target_desktop_id"], "review");
    world.advance(std::time::Duration::from_secs(1));
    assert!(world.runtime_is_at_rest());
}

#[test]
fn a_live_split_shell_stays_visible_under_automations_when_off_screen() {
    let world = automation_desktop(true);
    assert!(
        world
            .screen()
            .descendants(Target::key("sidebar.automations"))
            .into_iter()
            .any(|row| row.key.as_deref() == Some("sidebar.session.debug-shell"))
    );
    assert!(
        world
            .screen()
            .find(Target::key("sidebar.automations.finished"))
            .is_none()
    );
    assert!(world.runtime_is_at_rest());
}
