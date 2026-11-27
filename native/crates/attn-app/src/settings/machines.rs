//! Settings → Machines: every machine of the home, this one and the home's own included, each
//! with its state and one status line. The chosen row holds what can be done with it: move the
//! home there, update it, re-pair or remove it, and its details. A machine is added by its ssh
//! destination, and named by it unless a name is given.

use attn_client::{Connection, Reply, Slices};
use attn_protocol::{
    AddOutpostMessage, ConnectOutpostInTerminalMessage, HomeMove, HomeMoveMessage,
    HomeMoveProgressMessage, HomeMoveResultMessage, HomeMoveRollbackMessage, HomeMoveStatusMessage,
    OutpostInfo, OutpostNotebook, ProfileActionResultMessage, RemoveOutpostMessage,
    RepairOutpostMessage, UpdateOutpostMessage,
};
use attn_ui::Tokens;
use attn_ui::blocks::{
    Actions, Banner, BannerTone, Block, Facts, Field, Inline, List, Notice, Prose, Section, Stack,
    Status,
};
use attn_ui::system::{Button, FieldState, ListRow, Look, Size, Tone, Type, line_box, tag, words};
use rune::{Cx, El, Key, KeyEvent, TextEdit, Tx};
use rune_kit::TextInput;

use crate::State;
use crate::action::Action;
use crate::requests::Answer;

const NOT_CONNECTED: &str = "Connect to the home daemon to change machines.";
const COMMANDS: &[&str] = &[
    "add_outpost",
    "repair_outpost",
    "remove_outpost",
    "update_outpost",
    "connect_outpost_in_terminal",
];
const LIST: &str = "settings.machines.list";
/// The row of the home's machine, wherever the home runs.
const HOME_ROW: &str = "home";

#[derive(Default)]
pub(crate) struct Machines {
    name: TextEdit,
    destination: TextEdit,
    /// The name and destination of the machine last added, until a list holds it.
    submitted: Option<(String, String)>,
    /// The row that holds the page's actions.
    chosen: Option<String>,
    /// The row whose details show.
    details: Option<String>,
    /// The machine whose Remove asks to confirm closing its terminals.
    removing: Option<String>,
    terminal_error: Option<String>,
    /// Why the daemon refused the last machine command. It outlasts the page.
    command_error: Option<String>,
    moving: Option<Move>,
    /// This machine's latest move, as its daemon last told it.
    last_move: Option<HomeMove>,
    /// How the last move from this window ended, until its band is dismissed.
    told: Option<Told>,
}

/// A move of the home to one machine, from its checks until it ends.
struct Move {
    id: String,
    name: String,
    step: MoveStep,
    /// The id of this machine's latest move before this one started, so a record of that move is not read as
    /// this one's outcome.
    before: Option<String>,
}

enum MoveStep {
    Checking,
    Checked(HomeMove),
    /// How many of the move's steps have begun.
    Moving(usize),
    Failed(String),
}

enum Told {
    Moved(String),
    RollingBack,
    RolledBack,
}

/// The steps of a move as its band and its row count them; the daemon names the first four.
const DAEMON_STEPS: [&str; 4] = ["fence", "copy", "build", "commit"];
const STEPS: usize = 6;

fn step_label(step: usize, name: &str) -> String {
    match step {
        1 => "Stop changes".to_owned(),
        2 => format!("Copy to {name}"),
        3 => "Build database".to_owned(),
        4 => "Switch roles".to_owned(),
        5 => format!("Start home on {name}"),
        _ => "Reconnect".to_owned(),
    }
}

fn panel(state: &mut State) -> &mut Machines {
    &mut state.window.settings.machines
}

/// Starts the page afresh when it comes into view.
pub(super) fn arrive(state: &mut State) {
    if super::is_open(state) && state.window.settings.section == super::Section::Machines {
        let machines = panel(state);
        machines.removing = None;
        machines.terminal_error = None;
        machines.details = None;
    }
}

/// Reads this machine's move again whenever the connection changes, and clears the typed
/// destination once a list holds the machine just added, unless the person typed another since.
pub(super) fn follow(state: &mut State, changed: Slices, tx: &mut Tx) {
    let away = matches!(state.facts.connection, Connection::HomeUnreachable(_));
    if changed.intersects(Slices::CONNECTION) {
        follow_connection(state);
        if remote_home(state) || away {
            read_last_move(state, tx);
        }
    }
    if !changed.intersects(Slices::OUTPOSTS) {
        return;
    }
    let listed = state.facts.outposts.clone();
    let machines = panel(state);
    let Some((name, destination)) = machines.submitted.clone() else {
        return;
    };
    if !listed.iter().any(|o| o.destination == destination) {
        return;
    }
    let typed = machines.name.text.trim();
    let unchanged =
        (typed.is_empty() || typed == name) && machines.destination.text.trim() == destination;
    if unchanged {
        machines.name = TextEdit::default();
        machines.destination = TextEdit::default();
    }
    machines.submitted = None;
}

/// Takes a `command_error` for a machine command no request waits for; whether it was one.
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

/// Takes a step the daemon began of the move this window runs; whether the reply was one.
pub(crate) fn progressed(state: &mut State, reply: &Reply) -> bool {
    if reply.event != "home_move_progress" {
        return false;
    }
    let begun = reply
        .parse::<HomeMoveProgressMessage>()
        .and_then(|p| DAEMON_STEPS.iter().position(|s| *s == p.step))
        .map(|i| i + 1);
    if let (Some(begun), Some(moving)) = (begun, panel(state).moving.as_mut())
        && let MoveStep::Moving(steps) = &mut moving.step
    {
        *steps = (*steps).max(begun);
    }
    true
}

/// Why the daemon refused the last machine command, or that it could not be sent.
pub(crate) fn command_error(state: &State) -> Option<&str> {
    state.window.settings.machines.command_error.as_deref()
}

fn connected(state: &State) -> bool {
    state.facts.connection == Connection::Connected
}

/// Sends a machine command, whose refusal comes back as a `command_error`; whether it left.
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
            .machines
            .destination
            .text
            .trim()
            .is_empty()
}

fn add(state: &mut State, _tx: &mut Tx) {
    if !can_add(state) {
        return;
    }
    let machines = panel(state);
    let destination = machines.destination.text.trim().to_owned();
    let name = match machines.name.text.trim() {
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
    let machines = panel(state);
    machines.told = None;
    let before = machines.last_move.as_ref().and_then(|m| m.id.clone());
    machines.moving = Some(Move {
        id: id.to_owned(),
        name: name.to_owned(),
        step: MoveStep::Checking,
        before,
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
    moving.step = MoveStep::Moving(0);
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
            let Some(moving) = moving_to(state, &id) else {
                return;
            };
            moving.step = match move_answer(&answer) {
                Ok(moved) if moved.step == "committed" => MoveStep::Moving(5),
                Ok(HomeMove { error, step, .. }) => MoveStep::Failed(error.unwrap_or(step)),
                Err(why) => MoveStep::Failed(why),
            };
        },
        tx,
    );
}

/// Ends the move this window runs once this window has the home again: its record on this machine says how it
/// ended, and a move with no record past the one before it stopped before it committed.
fn settle_move(state: &mut State, latest: &HomeMove) {
    let back = connected(state);
    let machines = panel(state);
    if matches!(machines.told, Some(Told::RollingBack)) && latest.step == "rolled-back" {
        machines.told = Some(Told::RolledBack);
    }
    let Some(moving) = machines
        .moving
        .take_if(|m| back && matches!(m.step, MoveStep::Moving(_)))
    else {
        return;
    };
    let ours = latest.id.is_some() && latest.id != moving.before;
    let why = match (ours, latest.step.as_str()) {
        (true, "committed" | "applied" | "done") => {
            machines.told = Some(Told::Moved(moving.name));
            return;
        }
        (true, "aborted" | "rolled-back") => {
            latest.error.clone().unwrap_or_else(|| latest.step.clone())
        }
        (true, _) => {
            machines.moving = Some(moving);
            return;
        }
        (false, _) => "it stopped before it committed, so the home did not move".to_owned(),
    };
    machines.moving = Some(Move {
        step: MoveStep::Failed(why),
        ..moving
    });
}

/// Counts the steps after the commit from this window's connection: its daemon restarts while the home starts on
/// the other machine, then dials it.
fn follow_connection(state: &mut State) {
    let after = match state.facts.connection {
        Connection::Connecting | Connection::Reconnecting { .. } => 5,
        Connection::HomeUnreachable(_) => 6,
        _ => return,
    };
    if let Some(Move {
        step: MoveStep::Moving(steps),
        ..
    }) = panel(state).moving.as_mut()
    {
        *steps = (*steps).max(after);
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
    let latest = state.window.settings.machines.last_move.as_ref()?;
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
            Ok(latest) => {
                let machines = panel(state);
                machines.moving = None;
                machines.told = Some(Told::RollingBack);
                machines.last_move = Some(latest);
            }
            Err(_) if matches!(answer, Answer::Dropped) => {}
            Err(why) => panel(state).command_error = Some(why),
        },
        tx,
    );
}

/// The band of the move this window runs: its step while it runs, and how it ended.
pub(crate) fn banner(state: &State, t: &Tokens, cx: &Cx<State>) -> Option<El> {
    let banner = said(state)?.details(crate::shell::opens_details(crate::shell::Band::HomeMove));
    Some(banner.build(t, cx))
}

/// The message the band shows.
pub(crate) fn identity(state: &State) -> Option<String> {
    if let Some(to) = failed_move(state) {
        return Some(format!("failed:{to}"));
    }
    let machines = &state.window.settings.machines;
    match (&machines.moving, &machines.told) {
        (
            Some(Move {
                name,
                step: MoveStep::Moving(steps),
                ..
            }),
            _,
        ) => Some(format!("moving:{name}:{steps}")),
        (_, Some(Told::Moved(to))) => Some(format!("moved:{to}")),
        (_, Some(Told::RollingBack)) => Some("rolling-back".to_owned()),
        (_, Some(Told::RolledBack)) => Some("rolled-back".to_owned()),
        _ => None,
    }
}

/// The machine a move from this one went to while its home did not start there: this machine
/// lost the home it handed over, and has not reached it since. A move this window runs is still
/// starting the home until the dial fails.
fn failed_move(state: &State) -> Option<&str> {
    let to = undoable_move(state)?;
    let away = crate::connection::lost_home(state)?;
    let starting = state
        .window
        .settings
        .machines
        .moving
        .as_ref()
        .is_some_and(|m| matches!(m.step, MoveStep::Moving(_)));
    (!starting || away.reason.is_some()).then_some(to)
}

pub(crate) fn said(state: &State) -> Option<Banner> {
    if let Some(to) = failed_move(state) {
        return Some(
            Banner::new("home-move", "Home move", BannerTone::Bad)
                .says(
                    "home-move.text",
                    format!("Move failed: {to}’s home didn’t start."),
                )
                .note("home-move.why", crate::connection::rollback_said(to))
                .action("home-move.roll-back", Button::new("Roll back"), roll_back),
        );
    }
    let machines = &state.window.settings.machines;
    let dismiss = |banner: Banner| {
        banner.dismiss("home-move.dismiss", "Dismiss", |state: &mut State| {
            panel(state).told = None;
        })
    };
    if let Some(Move {
        name,
        step: MoveStep::Moving(steps),
        ..
    }) = &machines.moving
    {
        let step = (*steps).clamp(1, STEPS);
        return Some(
            Banner::new("home-move", "Home move", BannerTone::Calm)
                .says("home-move.text", format!("Moving home to {name}"))
                .note(
                    "home-move.step",
                    format!("{step}/{STEPS} {}", step_label(step, name)),
                ),
        );
    }
    Some(match machines.told.as_ref()? {
        Told::Moved(to) => dismiss(
            Banner::new("home-move", "Home move", BannerTone::Good)
                .says("home-move.text", format!("Home moved to {to}.")),
        ),
        Told::RollingBack => Banner::new("home-move", "Home move", BannerTone::Calm)
            .says("home-move.text", "Rolling back"),
        Told::RolledBack => dismiss(
            Banner::new("home-move", "Home move", BannerTone::Good).says(
                "home-move.text",
                format!("Rolled back. The home is on {}.", this_machine()),
            ),
        ),
    })
}

/// A machine as the page lists it.
struct Row<'a> {
    id: String,
    name: String,
    this: bool,
    home: bool,
    outpost: Option<&'a OutpostInfo>,
}

/// The machines of the home: the home's machine first, then this machine, then the others.
fn rows(state: &State) -> Vec<Row<'_>> {
    let here = state.facts.machine.id.as_str();
    let mut rows = vec![match &state.facts.machine.home {
        Some(home) => Row {
            id: HOME_ROW.to_owned(),
            name: home.clone(),
            this: false,
            home: true,
            outpost: None,
        },
        None => Row {
            id: HOME_ROW.to_owned(),
            name: this_machine_title(),
            this: true,
            home: true,
            outpost: None,
        },
    }];
    let mut outposts: Vec<&OutpostInfo> = state.facts.outposts.iter().collect();
    outposts.sort_by_key(|o| o.id != here);
    rows.extend(outposts.into_iter().map(|outpost| {
        let this = outpost.id == here;
        Row {
            id: outpost.id.clone(),
            name: if this {
                this_machine_title()
            } else {
                outpost.name.clone()
            },
            this,
            home: false,
            outpost: Some(outpost),
        }
    }));
    rows
}

fn row_key(id: &str) -> String {
    format!("settings.machine.{id}")
}

fn chosen(state: &State) -> Option<String> {
    let rows = rows(state);
    let machines = &state.window.settings.machines;
    machines
        .chosen
        .clone()
        .filter(|id| rows.iter().any(|r| &r.id == id))
        .or_else(|| rows.first().map(|r| r.id.clone()))
}

fn choose(state: &mut State, step: isize) {
    let ids: Vec<String> = rows(state).into_iter().map(|r| r.id).collect();
    let at = chosen(state)
        .and_then(|id| ids.iter().position(|i| *i == id))
        .unwrap_or(0);
    let next = at
        .saturating_add_signed(step)
        .min(ids.len().saturating_sub(1));
    panel(state).chosen = ids.get(next).cloned();
}

/// The keys of the list: the arrows choose a row, M makes it the home, U updates it and D
/// shows its details.
fn list_key(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    let Some(id) = chosen(state) else {
        return false;
    };
    match &ev.key {
        Key::Down => choose(state, 1),
        Key::Up => choose(state, -1),
        Key::Text(typed) => match typed.as_str() {
            "j" => choose(state, 1),
            "k" => choose(state, -1),
            "m" | "M" => {
                let Some(name) = can_make_home(state, &id) else {
                    return false;
                };
                check_move(state, &id, &name, tx);
            }
            "u" | "U" => {
                let needs = state.facts.outposts.iter().any(|o| {
                    o.id == id && crate::outpost::needs_update(o, &state.facts.home_build)
                });
                if !needs || !connected(state) {
                    return false;
                }
                update(state, &id);
            }
            "d" | "D" => toggle_details(state, &id),
            _ => return false,
        },
        _ => return false,
    }
    true
}

fn toggle_details(state: &mut State, id: &str) {
    let machines = panel(state);
    machines.details = if machines.details.as_deref() == Some(id) {
        None
    } else {
        Some(id.to_owned())
    };
}

/// The name a move to the machine `id` says, when the home can move there now.
fn can_make_home(state: &State, id: &str) -> Option<String> {
    let outpost = state.facts.outposts.iter().find(|o| o.id == id)?;
    let ready = remote_home(state)
        && connected(state)
        && outpost.state == "connected"
        && state.window.settings.machines.moving.is_none();
    ready.then(|| machine_name(state, outpost))
}

/// What the row of the machine a move goes to shows under it: the checks, the paths that
/// change, the steps while it runs, and what can be done next.
fn move_blocks(state: &State, row: &Row, t: &Tokens) -> Option<(Vec<Block>, Vec<El>)> {
    let moving = state
        .window
        .settings
        .machines
        .moving
        .as_ref()
        .filter(|m| m.id == row.id)?;
    let key = format!("{}.move", row_key(&row.id));
    let part = |name: &str| format!("{key}.{name}");
    let name = moving.name.clone();
    let online = connected(state);
    let cancel = || {
        action(part("cancel"), "Cancel", false, t, |state, _| {
            panel(state).moving = None;
        })
    };
    let again = || {
        let (id, name) = (row.id.clone(), name.clone());
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
        MoveStep::Moving(steps) => (vec![move_steps(&key, *steps, &name, t)], vec![]),
        MoveStep::Failed(why) => (
            vec![Notice::error(part("failed"), format!("Move failed: {why}")).build(t)],
            vec![again(), cancel()],
        ),
        MoveStep::Checked(checked) => {
            let mut under = vec![checks(&key, checked, t)];
            if !checked.remaps.is_empty() {
                under.push(remaps(state, &key, checked, t));
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
            let id = row.id.clone();
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

fn move_steps(key: &str, steps: usize, name: &str, t: &Tokens) -> Block {
    let lines = (1..=STEPS).map(|step| {
        let (mark, color) = match step.cmp(&steps.max(1)) {
            std::cmp::Ordering::Less => ("✓", t.text_secondary),
            std::cmp::Ordering::Equal => ("…", t.text_primary),
            std::cmp::Ordering::Greater => ("○", t.text_tertiary),
        };
        let said = format!("{mark} {}", step_label(step, name));
        words(said.clone(), Type::Small, color)
            .key(format!("{key}.step.{step}"))
            .label(said)
    });
    Block::custom(
        format!("{key}.steps.block"),
        Stack::tight()
            .key(format!("{key}.steps"))
            .role("status")
            .children(lines),
    )
}

fn remaps(state: &State, key: &str, checked: &HomeMove, t: &Tokens) -> Block {
    let mut facts = Facts::new(format!("{key}.paths"));
    for (i, remap) in checked.remaps.iter().enumerate() {
        facts = facts.code(
            format!("{key}.path.{i}"),
            "Notebook",
            format!("{} → {}", remap.from, remap.to),
        );
    }
    let crew: Vec<&str> = state
        .facts
        .crew
        .iter()
        .filter(|member| {
            checked
                .remaps
                .iter()
                .any(|remap| member.home_dir.starts_with(&format!("{}/", remap.from)))
        })
        .map(|member| member.name.as_str())
        .collect();
    if !crew.is_empty() {
        facts = facts.fact(
            format!("{key}.crew"),
            "Crew homes",
            format!("{} move with it", crew.join(", ")),
        );
    }
    facts.build(t)
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

fn this_machine_title() -> String {
    if rune::Platform::current() == rune::Platform::Mac {
        "This Mac".to_owned()
    } else {
        "This machine".to_owned()
    }
}

fn state_tone(state: &str) -> Tone {
    match state {
        "connected" => Tone::Good,
        "stalled" | "moving" | "connecting" => Tone::Warn,
        _ => Tone::Bad,
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

/// The state a row's tag says: the outpost's own, or for the home and this machine, whether
/// this window has the home.
fn row_state(state: &State, row: &Row) -> String {
    let moving = state
        .window
        .settings
        .machines
        .moving
        .as_ref()
        .is_some_and(|m| m.id == row.id && matches!(m.step, MoveStep::Moving(_)));
    if moving {
        return "moving".to_owned();
    }
    if let Some(outpost) = row.outpost {
        return outpost.state.clone();
    }
    match state.facts.connection {
        Connection::Connected => "connected",
        Connection::HomeUnreachable(_) => "unreachable",
        _ => "connecting",
    }
    .to_owned()
}

/// The one line a row says after its tags.
fn status_line(state: &State, row: &Row) -> Option<String> {
    let moving = state.window.settings.machines.moving.as_ref();
    if let Some(moving) = moving.filter(|m| m.id == row.id) {
        return Some(match &moving.step {
            MoveStep::Checking => "checking".to_owned(),
            MoveStep::Checked(_) => "checked".to_owned(),
            MoveStep::Moving(steps) => {
                step_label((*steps).clamp(1, STEPS), &moving.name).to_lowercase()
            }
            MoveStep::Failed(_) => "move failed".to_owned(),
        });
    }
    let version = |build: &str| build.split_whitespace().next().map(str::to_owned);
    let Some(outpost) = row.outpost else {
        return version(&state.facts.home_build).filter(|_| row.home && row.this);
    };
    let run = state.facts.update_run.as_ref().as_ref();
    let step = run
        .filter(|run| crate::update_run::is_open(run))
        .into_iter()
        .flat_map(|run| run.outposts.iter())
        .chain(run.into_iter().flat_map(crate::update_run::failed_outposts))
        .find(|o| o.id == outpost.id)
        .map(|o| crate::update_run::outpost_step(o).to_owned());
    step.or_else(|| crate::outpost::update_text(outpost).map(|said| said.to_string()))
        .or_else(|| outpost.last_error.clone().filter(|e| !e.is_empty()))
        .or_else(|| outpost.build.as_deref().and_then(version))
}

/// What a row's details hold: everything the row does not say.
fn details(state: &State, row: &Row, t: &Tokens) -> Vec<Block> {
    let key = row_key(&row.id);
    let part = |name: &str| format!("{key}.{name}");
    let Some(outpost) = row.outpost else {
        let build = state.facts.home_build.clone();
        let facts = Facts::new(part("details"));
        let facts = if build.is_empty() {
            facts
        } else {
            facts.code(part("build"), "Build", build)
        };
        return vec![facts.build(t)];
    };
    let mut facts = Facts::new(part("details"))
        .fact(
            part("destination"),
            "ssh destination",
            outpost.destination.clone(),
        )
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
    if !row.this {
        under.push(
            Prose::hint(
                part("explain"),
                "Re-pair forgets the pinned host key and daemon identity, then connects again. Remove closes its terminals, releases its enrollment and stops its daemon over ssh. Its binary, data and Notebook stay.",
            )
            .build(t),
        );
    }
    under
}

/// The buttons of the chosen row.
fn actions(state: &State, row: &Row, t: &Tokens) -> Vec<El> {
    let page = &state.window.settings.machines;
    let online = connected(state);
    let key = row_key(&row.id);
    let part = |name: &str| format!("{key}.{name}");
    let id = row.id.clone();
    let mut actions: Vec<El> = Vec::new();
    if let Some(name) = can_make_home(state, &id) {
        let id = id.clone();
        actions.push(action(
            part("move"),
            "Make home",
            false,
            t,
            move |state, tx| check_move(state, &id, &name, tx),
        ));
    }
    let run = state.facts.update_run.as_ref().as_ref();
    if let Some(outpost) = row.outpost {
        if crate::outpost::needs_update(outpost, &state.facts.home_build) {
            let busy = crate::outpost::updating(outpost);
            let label = if busy {
                "Updating…"
            } else if outpost.host_missing == Some(true) {
                "Install shared terminal host"
            } else {
                "Update"
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
        if !row.this {
            let count = terminals(state, &id);
            let confirming = count > 0 && page.removing.as_deref() == Some(id.as_str());
            let noun = if count == 1 { "terminal" } else { "terminals" };
            let remove_label = match (confirming, outpost.reach == "reachable") {
                (false, _) => "Remove".to_owned(),
                (true, true) => format!("Remove and close {count} {noun}"),
                (true, false) => format!("Remove and forget {count} {noun}"),
            };
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
        }
    }
    let shown = page.details.as_deref() == Some(id.as_str());
    let id = id.clone();
    actions.push(action(
        part("details-toggle"),
        if shown { "Hide details" } else { "Details" },
        false,
        t,
        move |state, _| toggle_details(state, &id),
    ));
    actions
}

/// A machine's row, and what is under it while it is chosen or a move goes to it.
fn machine_row(state: &State, row: &Row, chosen: bool, t: &Tokens) -> (ListRow, Vec<Block>) {
    let page = &state.window.settings.machines;
    let key = row_key(&row.id);
    let part = |name: &str| format!("{key}.{name}");
    let mut under = Vec::new();
    if let Some((blocks, moves)) = move_blocks(state, row, t) {
        under.extend(blocks);
        if !moves.is_empty() {
            under.push(
                moves
                    .into_iter()
                    .fold(Actions::new(part("move.actions")), Actions::push)
                    .build(),
            );
        }
    } else if chosen {
        under.push(
            actions(state, row, t)
                .into_iter()
                .fold(Actions::new(part("actions")), Actions::push)
                .build(),
        );
    }
    if page.details.as_deref() == Some(row.id.as_str()) {
        under.extend(details(state, row, t));
    }
    if let Some(outpost) = row
        .outpost
        .filter(|o| page.removing.as_deref() == Some(o.id.as_str()) && o.reach != "reachable")
    {
        under.push(
            Notice::info(
                part("remove-note"),
                format!(
                    "{} isn't connected. If ssh cannot stop it, its daemon and shells keep running there. Run this instance's attn enrollment leave and attn daemon stop commands there.",
                    outpost.name
                ),
            )
            .build(t),
        );
    }
    let row_state = row_state(state, row);
    let tags = Inline::tight()
        .children(
            row.home
                .then(|| El::from(tag(part("home"), "home", Tone::Neutral, t))),
        )
        .child(El::from(tag(part("state"), &row_state, state_tone(&row_state), t)).role("status"))
        .children(status_line(state, row).map(|said| {
            words(said.clone(), Type::Small, t.text_tertiary)
                .key(part("status"))
                .label(said)
                .wrap_anywhere()
                .shrink()
                .min_w(0.0)
        }));
    let id = row.id.clone();
    let list_row = ListRow::new(key.clone(), row.name.clone())
        .wrap()
        .title(row.name.clone())
        .subtitle_el(tags)
        .in_view(chosen)
        .on_press_tx(move |state: &mut State, _: &mut Tx| panel(state).chosen = Some(id.clone()));
    (list_row, under)
}

fn add_form(state: &State, t: &Tokens) -> Block {
    let page = &state.window.settings.machines;
    Section::new("settings.machines.add", "Add a machine")
        .form()
        .push(
            Field::new("settings.machines.name", "Name")
                .control(line_box(
                    TextInput::new(&page.name)
                        .key("settings.machines.name")
                        .label("Name")
                        .placeholder("Optional")
                        .on_edit(|state: &mut State| &mut panel(state).name)
                        .on_submit(add),
                    FieldState::Ready,
                    t,
                ))
                .build(t),
        )
        .push(
            Field::new("settings.machines.destination", "ssh destination")
                .control(line_box(
                    TextInput::new(&page.destination)
                        .key("settings.machines.destination")
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
            Actions::new("settings.machines.add.actions")
                .push(
                    Button::new("Add")
                        .look(Look::Primary)
                        .disabled(!can_add(state))
                        .on_press_tx(add)
                        .build(t)
                        .key("settings.machines.submit"),
                )
                .build(),
        )
        .build(t)
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<Block> {
    let page = &state.window.settings.machines;
    let mut shown = Vec::new();
    if let Some(error) = page.terminal_error.as_ref().or(page.command_error.as_ref()) {
        shown.push(Notice::error("settings.machines.error", error.clone()).build(t));
    }
    if !connected(state) {
        shown.push(Notice::info("settings.machines.offline", NOT_CONNECTED).build(t));
    }
    let chosen = chosen(state);
    let chooser = chosen.as_deref().map(row_key);
    let rows = rows(state);
    let list = rows
        .iter()
        .map(|row| machine_row(state, row, chosen.as_deref() == Some(row.id.as_str()), t))
        .fold(
            List::new(LIST, "Machines")
                .chooser(chooser)
                .on_key(list_key),
            |list, (row, under)| list.row_with(row, under),
        )
        .build(t);
    shown.push(
        Section::new("settings.block.machines.list", "Machines")
            .push(list)
            .build(t),
    );
    shown.push(add_form(state, t));
    shown
}
