//! Settings → Outposts: add an outpost by its ssh destination, and for each one its state,
//! build, identity and Notebook sync, with Re-pair, Remove, an update to the home build, a
//! terminal to answer an ssh refusal in, and a move of the home to it.

use attn_client::{Connection, Reply, Slices};
use attn_protocol::{
    AddOutpostMessage, ConnectOutpostInTerminalMessage, HomeMove, HomeMoveMessage,
    HomeMoveResultMessage, HomeMoveRollbackMessage, HomeMoveStatusMessage, OutpostInfo,
    OutpostNotebook, ProfileActionResultMessage, RemoveOutpostMessage, RepairOutpostMessage,
    UpdateOutpostMessage,
};
use attn_ui::Tokens;
use rune::{El, TextEdit, Tx};
use rune_kit::TextInput;

use attn_ui::blocks::{
    Actions, Block, Facts, Field, Inline, List, Notice, Prose, Section, Stack, Status,
};
use attn_ui::system::{Button, FieldState, ListRow, Look, Size, Tone, Type, line_box, tag, words};

use crate::State;
use crate::action::Action;
use crate::requests::Answer;

const NOT_CONNECTED: &str = "Connect to the home daemon to change outposts.";
const COMMANDS: &[&str] = &[
    "add_outpost",
    "repair_outpost",
    "remove_outpost",
    "update_outpost",
    "connect_outpost_in_terminal",
];

#[derive(Default)]
pub(crate) struct Outposts {
    name: TextEdit,
    destination: TextEdit,
    /// The name and destination of the outpost last added, until a list holds it.
    submitted: Option<(String, String)>,
    /// The outpost whose Remove asks to confirm closing its terminals.
    removing: Option<String>,
    terminal_error: Option<String>,
    /// Why the daemon refused the last outpost command. It outlasts the page.
    command_error: Option<String>,
    moving: Option<Move>,
    /// This machine's latest move, as its daemon last told it.
    last_move: Option<HomeMove>,
    /// The machine the home moved to from this page, until the page is left.
    moved_to: Option<String>,
}

/// A move of the home to one machine, from its checks until it ends.
struct Move {
    id: String,
    name: String,
    step: MoveStep,
}

enum MoveStep {
    Checking,
    Checked(HomeMove),
    Moving,
    Failed(String),
}

fn panel(state: &mut State) -> &mut Outposts {
    &mut state.window.settings.outposts
}

/// Starts the page afresh when it comes into view.
pub(super) fn arrive(state: &mut State) {
    if super::is_open(state) && state.window.settings.section == super::Section::Outposts {
        let outposts = panel(state);
        outposts.removing = None;
        outposts.terminal_error = None;
        outposts.moved_to = None;
    }
}

/// Clears the typed outpost once a list holds the one just added, unless the person typed
/// another since.
pub(super) fn follow(state: &mut State, changed: Slices, tx: &mut Tx) {
    let away = matches!(state.facts.connection, Connection::HomeUnreachable(_));
    if changed.intersects(Slices::CONNECTION) && (remote_home(state) || away) {
        read_last_move(state, tx);
    }
    if !changed.intersects(Slices::OUTPOSTS) {
        return;
    }
    let listed = state.facts.outposts.clone();
    let outposts = panel(state);
    let Some((name, destination)) = outposts.submitted.clone() else {
        return;
    };
    if !listed
        .iter()
        .any(|o| o.name == name && o.destination == destination)
    {
        return;
    }
    let typed_destination = outposts.destination.text.trim().to_owned();
    let typed_name = match outposts.name.text.trim() {
        "" => typed_destination.clone(),
        typed => typed.to_owned(),
    };
    if typed_name == name && typed_destination == destination {
        outposts.name = TextEdit::default();
        outposts.destination = TextEdit::default();
    }
    outposts.submitted = None;
}

/// Takes a `command_error` for an outpost command no request waits for; whether it was one.
pub(crate) fn refused(state: &mut State, reply: &Reply) -> bool {
    let cmd = reply.body["cmd"].as_str().unwrap_or_default();
    if reply.event != "command_error" || !COMMANDS.contains(&cmd) {
        return false;
    }
    let error = reply.body["error"].as_str().unwrap_or_default();
    panel(state).command_error = Some(match error {
        "daemon_recovering" => "The home daemon is recovering. Try again when it is ready.".into(),
        "" => format!("Could not {}", cmd.replacen('_', " ", 1)),
        error => error.to_owned(),
    });
    true
}

/// Why the daemon refused the last outpost command, or that it could not be sent.
pub(crate) fn command_error(state: &State) -> Option<&str> {
    state.window.settings.outposts.command_error.as_deref()
}

fn connected(state: &State) -> bool {
    state.facts.connection == Connection::Connected
}

/// Sends an outpost command, whose refusal comes back as a `command_error`; whether it left.
fn send(state: &mut State, command: &impl serde::Serialize) -> bool {
    let sent = state.daemon.send(command).is_ok();
    panel(state).command_error = (!sent).then(|| NOT_CONNECTED.to_owned());
    sent
}

fn can_add(state: &State) -> bool {
    connected(state)
        && !state
            .window
            .settings
            .outposts
            .destination
            .text
            .trim()
            .is_empty()
}

fn add(state: &mut State, _tx: &mut Tx) {
    if !can_add(state) {
        return;
    }
    let outposts = panel(state);
    let destination = outposts.destination.text.trim().to_owned();
    let name = match outposts.name.text.trim() {
        "" => destination.clone(),
        name => name.to_owned(),
    };
    let command = AddOutpostMessage {
        name: name.clone(),
        destination: destination.clone(),
        ..AddOutpostMessage::new()
    };
    let known = state
        .facts
        .outposts
        .iter()
        .any(|o| o.destination == destination);
    if send(state, &command) && !known {
        panel(state).submitted = Some((name, destination));
    }
}

fn repair(state: &mut State, id: &str) {
    send(
        state,
        &RepairOutpostMessage {
            id: id.to_owned(),
            ..RepairOutpostMessage::new()
        },
    );
}

pub(crate) fn update(state: &mut State, id: &str) {
    send(
        state,
        &UpdateOutpostMessage {
            id: id.to_owned(),
            ..UpdateOutpostMessage::new()
        },
    );
}

fn terminals(state: &State, id: &str) -> usize {
    state
        .facts
        .sessions
        .iter()
        .filter(|s| s.host.as_deref() == Some(id))
        .count()
}

fn remove(state: &mut State, id: &str) {
    let confirming = panel(state).removing.as_deref() == Some(id);
    if terminals(state, id) > 0 && !confirming {
        panel(state).removing = Some(id.to_owned());
        return;
    }
    send(
        state,
        &RemoveOutpostMessage {
            id: id.to_owned(),
            ..RemoveOutpostMessage::new()
        },
    );
    panel(state).removing = None;
}

/// Opens a terminal that connects to the outpost, then shows it in place of the dialog; a
/// refusal stays in the dialog.
fn connect_in_terminal(state: &mut State, id: &str, tx: &mut Tx) {
    panel(state).terminal_error = None;
    let id = id.to_owned();
    state.ask(
        |request_id| ConnectOutpostInTerminalMessage {
            id,
            request_id,
            ..ConnectOutpostInTerminalMessage::new()
        },
        |state, answer, tx| match answer.outcome() {
            Ok(reply) => {
                let opened = reply.parse::<ProfileActionResultMessage>();
                let tile = opened.and_then(|r| {
                    let desktop = r.desktops?.into_iter().next()?.id;
                    Some((desktop, r.pane_id?))
                });
                super::close(state, tx);
                if let Some((desktop, tile)) = tile {
                    crate::dispatch(state, Action::OpenTile { desktop, tile }, tx);
                }
            }
            Err(why) => panel(state).terminal_error = Some(why),
        },
        tx,
    );
}

fn remote_home(state: &State) -> bool {
    state
        .facts
        .settings
        .get("remote_home_enabled")
        .is_some_and(|on| on == "true")
}

fn move_answer(answer: &Answer) -> Result<HomeMove, String> {
    let result = answer
        .parse::<HomeMoveResultMessage>()
        .ok_or_else(|| "the daemon did not answer".to_owned())?;
    match (result.success, result.home_move) {
        (true, Some(home_move)) => Ok(home_move),
        _ => Err(result.error.unwrap_or_else(|| "the move failed".to_owned())),
    }
}

fn moving_to<'a>(state: &'a mut State, id: &str) -> Option<&'a mut Move> {
    panel(state).moving.as_mut().filter(|m| m.id == id)
}

fn check_move(state: &mut State, id: &str, name: &str, tx: &mut Tx) {
    panel(state).moving = Some(Move {
        id: id.to_owned(),
        name: name.to_owned(),
        step: MoveStep::Checking,
    });
    let machine = id.to_owned();
    let id = id.to_owned();
    state.ask(
        |request_id| HomeMoveMessage {
            machine,
            check: Some(true),
            request_id: Some(request_id),
            ..HomeMoveMessage::new()
        },
        move |state, answer, _| {
            if let Some(moving) = moving_to(state, &id) {
                moving.step = match move_answer(&answer) {
                    Ok(checked) => MoveStep::Checked(checked),
                    Err(why) => MoveStep::Failed(why),
                };
            }
        },
        tx,
    );
}

fn start_move(state: &mut State, id: &str, tx: &mut Tx) {
    let Some(moving) = moving_to(state, id) else {
        return;
    };
    moving.step = MoveStep::Moving;
    let machine = id.to_owned();
    let id = id.to_owned();
    state.ask(
        |request_id| HomeMoveMessage {
            machine,
            request_id: Some(request_id),
            ..HomeMoveMessage::new()
        },
        move |state, answer, _| {
            if matches!(answer, Answer::Dropped) {
                return;
            }
            match move_answer(&answer) {
                Ok(moved) if moved.step == "committed" => settle_move(state, &moved),
                Ok(HomeMove { error, step, .. }) => {
                    if let Some(moving) = moving_to(state, &id) {
                        moving.step = MoveStep::Failed(error.unwrap_or(step));
                    }
                }
                Err(why) => {
                    if let Some(moving) = moving_to(state, &id) {
                        moving.step = MoveStep::Failed(why);
                    }
                }
            }
        },
        tx,
    );
}

/// Ends the move this page runs once this machine's daemon says how it ended.
fn settle_move(state: &mut State, latest: &HomeMove) {
    let outposts = panel(state);
    let Some(moving) = outposts
        .moving
        .take_if(|m| matches!(m.step, MoveStep::Moving))
    else {
        return;
    };
    match latest.step.as_str() {
        "committed" | "applied" | "done" => outposts.moved_to = Some(moving.name),
        "aborted" | "rolled-back" => {
            let why = latest.error.clone().unwrap_or_else(|| latest.step.clone());
            outposts.moving = Some(Move {
                step: MoveStep::Failed(why),
                ..moving
            });
        }
        _ => outposts.moving = Some(moving),
    }
}

fn read_last_move(state: &mut State, tx: &mut Tx) {
    state.ask_here(
        |request_id| HomeMoveStatusMessage {
            request_id: Some(request_id),
            ..HomeMoveStatusMessage::new()
        },
        |state, answer, _| {
            if let Ok(latest) = move_answer(&answer) {
                settle_move(state, &latest);
                panel(state).last_move = Some(latest);
            }
        },
        tx,
    );
}

/// The machine this one moved the home to, while that move can still be rolled back: the
/// new home has not taken over, as this machine has not bridged to it.
pub(crate) fn undoable_move(state: &State) -> Option<&str> {
    let latest = state.window.settings.outposts.last_move.as_ref()?;
    let undoable = latest.side.as_deref() == Some("from")
        && matches!(latest.step.as_str(), "committed" | "applied");
    undoable.then_some(latest.to.as_deref()).flatten()
}

pub(crate) fn roll_back(state: &mut State, tx: &mut Tx) {
    state.ask_here(
        |request_id| HomeMoveRollbackMessage {
            request_id: Some(request_id),
            ..HomeMoveRollbackMessage::new()
        },
        |state, answer, _| match move_answer(&answer) {
            Ok(latest) => panel(state).last_move = Some(latest),
            Err(_) if matches!(answer, Answer::Dropped) => {}
            Err(why) => panel(state).command_error = Some(why),
        },
        tx,
    );
}

/// What the row of the machine a move goes to shows under it: the checks, the paths that
/// change and what can be done next.
fn move_blocks(state: &State, outpost: &OutpostInfo, t: &Tokens) -> Option<(Vec<Block>, Vec<El>)> {
    let moving = state
        .window
        .settings
        .outposts
        .moving
        .as_ref()
        .filter(|m| m.id == outpost.id)?;
    let key = format!("settings.outpost.{}.move", outpost.id);
    let part = |name: &str| format!("{key}.{name}");
    let name = machine_name(state, outpost);
    let online = connected(state);
    let cancel = || {
        action(part("cancel"), "Cancel", false, t, |state, _| {
            panel(state).moving = None;
        })
    };
    let again = || {
        let (id, name) = (outpost.id.clone(), outpost.name.clone());
        action(
            part("again"),
            "Check again",
            !online,
            t,
            move |state, tx| {
                check_move(state, &id, &name, tx);
            },
        )
    };
    Some(match &moving.step {
        MoveStep::Checking => (
            vec![Status::new(part("checking"), format!("Checking {name}…"), true).build(t)],
            vec![cancel()],
        ),
        MoveStep::Moving => (
            vec![Status::new(part("moving"), format!("Moving the home to {name}…"), true).build(t)],
            vec![],
        ),
        MoveStep::Failed(why) => (
            vec![Notice::error(part("failed"), format!("Move failed: {why}")).build(t)],
            vec![again(), cancel()],
        ),
        MoveStep::Checked(checked) => {
            let mut under = vec![checks(&key, checked, t)];
            if !checked.remaps.is_empty() {
                let facts = checked.remaps.iter().enumerate().fold(
                    Facts::new(part("paths")),
                    |facts, (i, remap)| {
                        facts.code(
                            part(&format!("path.{i}")),
                            "Notebook",
                            format!("{} → {}", remap.from, remap.to),
                        )
                    },
                );
                under.push(facts.build(t));
            }
            if checked.sleeps == Some(true) {
                under.push(
                    Notice::warn(
                        part("sleeps"),
                        format!("While {name} sleeps, the home is down."),
                    )
                    .build(t),
                );
            }
            let ready = checked.step == "checked";
            let id = outpost.id.clone();
            let go = Button::new(format!("Move home to {name}"))
                .look(Look::Primary)
                .size(Size::Compact)
                .disabled(!online || !ready)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| start_move(state, &id, tx))
                .build(t)
                .key(part("go"));
            (under, vec![go.into(), again(), cancel()])
        }
    })
}

fn checks(key: &str, checked: &HomeMove, t: &Tokens) -> Block {
    let passed = checked.checks.iter().filter(|c| c.ok).count();
    let noun = if passed == 1 { "check" } else { "checks" };
    let summary = format!("✓ {passed} {noun} passed");
    let failed = checked.checks.iter().filter(|c| !c.ok).map(|c| {
        let said = match c.detail.as_deref() {
            Some(detail) => format!("✗ {}: {detail}", c.name),
            None => format!("✗ {}", c.name),
        };
        words(said.clone(), Type::Small, t.warning)
            .key(format!("{key}.check.{}", c.name))
            .label(said)
            .role("alert")
            .wrap()
    });
    Block::custom(
        format!("{key}.checks.block"),
        Stack::tight()
            .key(format!("{key}.checks"))
            .role("status")
            .child(
                words(summary.clone(), Type::Small, t.text_tertiary)
                    .key(format!("{key}.checks.passed"))
                    .label(summary),
            )
            .children(failed),
    )
}

/// The machine's name as this window says it: this Mac for the machine the window runs on.
fn machine_name(state: &State, outpost: &OutpostInfo) -> String {
    if outpost.id == state.facts.machine.id {
        this_machine().to_owned()
    } else {
        outpost.name.clone()
    }
}

pub(crate) fn this_machine() -> &'static str {
    if rune::Platform::current() == rune::Platform::Mac {
        "this Mac"
    } else {
        "this machine"
    }
}

fn state_tone(state: &str) -> Tone {
    match state {
        "connected" => Tone::Good,
        "stalled" => Tone::Warn,
        "lost" | "refused" | "error" => Tone::Bad,
        _ => Tone::Neutral,
    }
}

fn timestamp(at: &str) -> String {
    crate::sidebar::queue::epoch_ms(at).map_or_else(|| at.to_owned(), crate::calendar::date_time)
}

fn notebook_text(notebook: &OutpostNotebook) -> String {
    if notebook.state == "error" {
        return format!(
            "Notebook sync error: {}",
            notebook.error.as_deref().unwrap_or_default()
        );
    }
    let bytes = notebook.need_bytes.unwrap_or(0);
    let items = notebook.need_items.unwrap_or(0);
    let label = match notebook.state.as_str() {
        "first_sync" => "first sync",
        "syncing" => "syncing",
        "in_sync" => "in sync",
        other => other,
    };
    if bytes <= 0 && items <= 0 {
        return format!("Notebook: {label}");
    }
    const KB: i64 = 1024;
    let (unit, divisor) = match bytes {
        b if b >= KB * KB * KB => ("GB", KB * KB * KB),
        b if b >= KB * KB => ("MB", KB * KB),
        b if b >= KB => ("kB", KB),
        _ => ("B", 1),
    };
    let size = if divisor == 1 {
        bytes.to_string()
    } else {
        format!("{:.1}", bytes as f64 / divisor as f64)
    };
    let noun = if items == 1 { "item" } else { "items" };
    format!("Notebook: {label} · {size} {unit} and {items} {noun} left")
}

/// What an outpost's Notebook is doing, and what it kept aside, as lines under its row.
fn notebook(key: &str, notebook: &OutpostNotebook, t: &Tokens) -> Block {
    let said = notebook_text(notebook);
    let line = |key: String, said: String, alert: bool| {
        let ink = if alert { t.warning } else { t.text_tertiary };
        let line = words(said.clone(), Type::Small, ink)
            .key(key)
            .label(said)
            .wrap();
        if alert { line.role("alert") } else { line }
    };
    let conflicts = notebook.conflicts.unwrap_or(0);
    Block::custom(
        format!("{key}.notebook.block"),
        Stack::tight()
            .key(format!("{key}.notebook"))
            .role("status")
            .child(line(
                format!("{key}.notebook.state"),
                said,
                notebook.state == "error",
            ))
            .children((conflicts > 0).then(|| {
                let copies = if conflicts == 1 { "copy" } else { "copies" };
                line(
                    format!("{key}.notebook.conflicts"),
                    format!("{conflicts} conflict {copies}"),
                    true,
                )
            }))
            .children(notebook.previous_root.as_ref().map(|root| {
                line(
                    format!("{key}.notebook.previous"),
                    format!("Previous folder saved at {root}"),
                    false,
                )
            })),
    )
}

fn action(
    key: String,
    label: impl Into<attn_ui::system::Said>,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(label)
        .size(Size::Compact)
        .disabled(disabled)
        .on_press_tx(press)
        .build(t)
        .key(key)
        .into()
}

/// An outpost's row, and what is under it: its details, its trouble and what can be done.
fn card(state: &State, outpost: &OutpostInfo, t: &Tokens) -> (ListRow, Vec<Block>) {
    let page = &state.window.settings.outposts;
    let online = connected(state);
    let id = outpost.id.clone();
    let key = format!("settings.outpost.{id}");
    let part = |name: &str| format!("{key}.{name}");
    let count = terminals(state, &id);
    let confirming = count > 0 && page.removing.as_deref() == Some(id.as_str());
    let noun = if count == 1 { "terminal" } else { "terminals" };
    let reachable = outpost.reach == "reachable";
    let mut facts = Facts::new(part("details"))
        .fact(part("since"), "Since", timestamp(&outpost.state_since))
        .code(
            part("build"),
            "Build",
            outpost
                .build
                .clone()
                .filter(|b| !b.is_empty())
                .unwrap_or_else(|| "Not yet known".into()),
        );
    if outpost.host_missing == Some(true) {
        facts = facts.fact(part("host"), "Shared terminal host", "Not installed");
    }
    if let Some(daemon) = outpost.daemon_id.clone().filter(|d| !d.is_empty()) {
        facts = facts.code(part("daemon"), "Daemon", daemon);
    }
    if let Some(fingerprint) = outpost
        .host_key_fingerprint
        .clone()
        .filter(|f| !f.is_empty())
    {
        facts = facts.code(part("host-key"), "Host key", fingerprint);
    }
    let mut under = vec![facts.build(t)];
    if let Some(error) = outpost.last_error.clone().filter(|e| !e.is_empty()) {
        under.push(Notice::warn(part("error.text"), error).build(t));
        under.extend(
            outpost
                .last_error_at
                .as_deref()
                .map(|at| Prose::hint(part("error"), format!("At {}", timestamp(at))).build(t)),
        );
    }
    if let Some(nb) = &outpost.notebook {
        under.push(notebook(&key, nb, t));
    }
    if let Some(said) = crate::outpost::update_text(outpost) {
        under.push(Status::new(part("update"), said, false).build(t));
    }
    let run = state.facts.update_run.as_ref().as_ref();
    let in_run = run
        .filter(|run| crate::update_run::is_open(run))
        .into_iter()
        .flat_map(|run| run.outposts.iter())
        .chain(run.into_iter().flat_map(crate::update_run::failed_outposts))
        .filter(|o| o.id == id);
    under.extend(in_run.map(|o| {
        Status::new(
            part("run"),
            crate::update_run::outpost_step(o).to_owned(),
            false,
        )
        .build(t)
    }));

    let mut actions: Vec<El> = Vec::new();
    if let Some((blocks, moves)) = move_blocks(state, outpost, t) {
        under.extend(blocks);
        under.push(
            moves
                .into_iter()
                .fold(Actions::new(part("move.actions")), Actions::push)
                .build(),
        );
    } else if remote_home(state) && outpost.state == "connected" && page.moving.is_none() {
        let (id, name) = (id.clone(), outpost.name.clone());
        actions.push(action(
            part("move"),
            "Move home here",
            !online,
            t,
            move |state, tx| check_move(state, &id, &name, tx),
        ));
    }
    if outpost.state == "refused" && outpost.connectable == Some(true) {
        let id = id.clone();
        actions.push(action(
            part("connect"),
            "Connect in a terminal",
            !online,
            t,
            move |state, tx| connect_in_terminal(state, &id, tx),
        ));
    }
    if run.is_some_and(|run| {
        crate::update_run::failed_outposts(run)
            .iter()
            .any(|o| o.id == id)
    }) {
        let id = id.clone();
        actions.push(action(
            part("without"),
            "Update without it",
            !online,
            t,
            move |state, tx| crate::update_run::start(state, Some(vec![id.clone()]), tx),
        ));
    }
    if crate::outpost::needs_update(outpost, &state.facts.home_build) {
        let busy = crate::outpost::updating(outpost);
        let label = if busy {
            "Updating…"
        } else if outpost.host_missing == Some(true) {
            "Install shared terminal host"
        } else {
            "Update to home build"
        };
        let id = id.clone();
        actions.push(action(
            part("update-build"),
            label,
            !online || busy,
            t,
            move |state, _| update(state, &id),
        ));
    }
    {
        let id = id.clone();
        actions.push(action(
            part("repair"),
            "Re-pair",
            !online,
            t,
            move |state, _| repair(state, &id),
        ));
    }
    let remove_label = match (confirming, reachable) {
        (false, _) => "Remove".to_owned(),
        (true, true) => format!("Remove and close {count} {noun}"),
        (true, false) => format!("Remove and forget {count} {noun}"),
    };
    {
        let id = id.clone();
        actions.push(action(
            part("remove"),
            remove_label,
            !online,
            t,
            move |state, _| remove(state, &id),
        ));
    }
    if confirming {
        let focus = part("remove");
        actions.push(action(
            part("cancel"),
            "Cancel",
            false,
            t,
            move |state, tx| {
                tx.focus(focus.clone(), false);
                panel(state).removing = None;
            },
        ));
    }
    under.push(
        actions
            .into_iter()
            .fold(Actions::new(part("actions")), Actions::push)
            .build(),
    );
    under.extend((confirming && !reachable).then(|| {
        Notice::info(
            part("remove-note"),
            format!(
                "{} isn't connected. If ssh cannot stop it, its daemon and shells keep running there. Run this instance's attn enrollment leave and attn daemon stop commands there.",
                outpost.name
            ),
        )
        .build(t)
    }));
    let row = ListRow::new(key.clone(), outpost.name.clone())
        .still()
        .wrap()
        .title(outpost.name.clone())
        .subtitle_el(
            Inline::tight()
                .child(
                    El::from(tag(
                        part("state"),
                        &outpost.state,
                        state_tone(&outpost.state),
                        t,
                    ))
                    .role("status"),
                )
                .child(
                    words(outpost.destination.clone(), Type::Small, t.text_tertiary)
                        .wrap_anywhere()
                        .shrink()
                        .min_w(0.0),
                ),
        );
    (row, under)
}

fn add_form(state: &State, t: &Tokens) -> Block {
    let page = &state.window.settings.outposts;
    let destination = page.destination.text.trim().to_owned();
    let name_placeholder = if destination.is_empty() {
        "Optional".to_owned()
    } else {
        destination
    };
    Section::new("settings.outposts.add", "Add an outpost")
        .form()
        .description("Add the destination you use with ssh. attn installs your home's build there and pins its identity on first contact.")
        .push(
            Field::new("settings.outposts.name", "Name")
                .control(line_box(
                    TextInput::new(&page.name)
                        .key("settings.outposts.name")
                        .label("Name")
                        .placeholder(name_placeholder)
                        .on_edit(|state: &mut State| &mut panel(state).name)
                        .on_submit(add),
                    FieldState::Ready,
                    t,
                ))
                .build(t),
        )
        .push(
            Field::new("settings.outposts.destination", "ssh destination")
                .control(line_box(
                    TextInput::new(&page.destination)
                        .key("settings.outposts.destination")
                        .label("ssh destination")
                        .placeholder("user@host")
                        .on_edit(|state: &mut State| &mut panel(state).destination)
                        .on_submit(add),
                    FieldState::Ready,
                    t,
                ))
                .build(t),
        )
        .push(
            Actions::new("settings.outposts.add.actions")
                .push(
                    Button::new("Add outpost")
                        .look(Look::Primary)
                        .disabled(!can_add(state))
                        .on_press_tx(add)
                        .build(t)
                        .key("settings.outposts.submit"),
                )
                .build(),
        )
        .build(t)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let page = &state.window.settings.outposts;
    let mut shown = vec![add_form(state, t)];
    if let Some(error) = page.terminal_error.as_ref().or(page.command_error.as_ref()) {
        shown.push(Notice::error("settings.outposts.error", error.clone()).build(t));
    }
    if !connected(state) {
        shown.push(Notice::info("settings.outposts.offline", NOT_CONNECTED).build(t));
    }
    if let Some(to) = &page.moved_to {
        shown.push(
            Notice::info(
                "settings.outposts.moved",
                format!("The home moved to {to}."),
            )
            .build(t),
        );
    }
    let listed = &state.facts.outposts;
    let list = if listed.is_empty() {
        Prose::empty("settings.outposts.empty", "No outposts added.").build(t)
    } else {
        listed
            .iter()
            .map(|outpost| card(state, outpost, t))
            .fold(
                List::new("settings.outposts.list", "Outposts"),
                |list, (row, under)| list.row_with(row, under),
            )
            .build(t)
    };
    shown.push(
        Section::new("settings.block.outposts.list", "Outposts")
            .description("Re-pair forgets the pinned host key and daemon identity, then connects again. Remove closes its terminals across all profiles, releases enrollment and stops its daemon over ssh, then forgets this outpost. Its binary, data and Notebook stay. If ssh cannot reach it, run this instance's attn enrollment leave and attn daemon stop commands there.")
            .push(list)
            .build(t),
    );
    shown
}
