//! Settings → Outposts: add an outpost by its ssh destination, and for each one its state,
//! build, identity and Notebook sync, with Re-pair, Remove, an update to the home build and a
//! terminal to answer an ssh refusal in.

use attn_client::{Connection, Reply, Slices};
use attn_protocol::{
    AddOutpostMessage, ConnectOutpostInTerminalMessage, OutpostInfo, OutpostNotebook,
    ProfileActionResultMessage, RemoveOutpostMessage, RepairOutpostMessage, UpdateOutpostMessage,
};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, TextEdit, Tx, col, row, text};
use rune_kit::{Button, TextInput};

use super::pages::{CAPTION, PATH_FIELD, Tone, block, pill, warning};
use crate::State;
use crate::action::Action;

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
    /// Why the daemon refused the last outpost command. It outlasts the page, as the Tauri
    /// app keeps it with the connection.
    command_error: Option<String>,
}

fn panel(state: &mut State) -> &mut Outposts {
    &mut state.window.settings.outposts
}

/// Starts the page afresh when it comes into view, as the Tauri page mounts.
pub(super) fn arrive(state: &mut State) {
    if super::is_open(state) && state.window.settings.section == super::Section::Outposts {
        let outposts = panel(state);
        outposts.removing = None;
        outposts.terminal_error = None;
    }
}

/// Clears the typed outpost once a list holds the one just added, unless the person typed
/// another since.
pub(super) fn follow(state: &mut State, changed: Slices) {
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

fn update(state: &mut State, id: &str) {
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

/// Whether the outpost runs another build than home, or lacks the shared terminal host.
fn needs_update(row: &OutpostInfo, home_build: &str) -> bool {
    !home_build.is_empty()
        && (row.host_missing == Some(true)
            || match row.build.as_deref().filter(|b| !b.is_empty()) {
                Some(build) => build != home_build,
                None => row.state == "refused",
            })
}

fn updating(row: &OutpostInfo) -> bool {
    row.update
        .as_ref()
        .is_some_and(|u| u.phase != "done" && u.phase != "failed")
}

fn update_text(row: &OutpostInfo) -> Option<String> {
    let update = row.update.as_ref()?;
    if let Some(outcome) = update.outcome.as_ref().filter(|o| !o.is_empty()) {
        return Some(outcome.clone());
    }
    Some(
        match update.phase.as_str() {
            "connecting" => "Connecting.",
            "building" => "Building attn for Linux. The first build takes about a minute.",
            "downloading" => "Downloading attn for Linux.",
            "sending" => "Copying it over.",
            "verifying" => "Checking it runs.",
            "restarting" => "Restarting.",
            "done" => "Up to date.",
            "failed" => "Update failed.",
            other => other,
        }
        .to_owned(),
    )
}

fn state_tone(state: &str) -> Tone {
    match state {
        "connected" => Tone::Good,
        "stalled" => Tone::Warn,
        "lost" | "refused" => Tone::Bad,
        _ => Tone::Plain,
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

fn notebook(key: &str, notebook: &OutpostNotebook, t: &Tokens) -> El {
    let said = notebook_text(notebook);
    let line = if notebook.state == "error" {
        warning(format!("{key}.notebook.state"), said, t)
    } else {
        text(said.clone())
            .key(format!("{key}.notebook.state"))
            .label(said)
            .font(CAPTION)
            .color(t.text_tertiary)
            .wrap()
    };
    let conflicts = notebook.conflicts.unwrap_or(0);
    col()
        .key(format!("{key}.notebook"))
        .role("status")
        .gap(space::S1)
        .child(line)
        .children((conflicts > 0).then(|| {
            let copies = if conflicts == 1 { "copy" } else { "copies" };
            warning(
                format!("{key}.notebook.conflicts"),
                format!("{conflicts} conflict {copies}"),
                t,
            )
        }))
        .children(notebook.previous_root.as_ref().map(|root| {
            let said = format!("Previous folder saved at {root}");
            text(said.clone())
                .key(format!("{key}.notebook.previous"))
                .label(said)
                .font(CAPTION)
                .color(t.text_tertiary)
                .wrap()
        }))
}

fn meta(key: String, label: &str, value: String, t: &Tokens) -> El {
    row()
        .key(key)
        .label(format!("{label} {value}"))
        .gap(space::S3)
        .child(
            text(label.to_owned())
                .font(CAPTION)
                .color(t.text_tertiary)
                .w(140.0),
        )
        .child(
            text(value)
                .font(type_::MONO_CAPTION)
                .color(t.text_primary)
                .wrap()
                .grow(1.0)
                .min_w(0.0),
        )
}

fn status(key: String, said: String, t: &Tokens) -> El {
    text(said.clone())
        .key(key)
        .role("status")
        .label(said)
        .font(CAPTION)
        .color(t.text_tertiary)
        .wrap()
}

fn action(
    key: String,
    label: &str,
    disabled: bool,
    t: &Tokens,
    press: impl Fn(&mut State, &mut Tx) + 'static,
) -> El {
    Button::new(label)
        .disabled(disabled)
        .on_press_tx(press)
        .build(attn_ui::kit(t))
        .key(key)
}

fn card(state: &State, outpost: &OutpostInfo, t: &Tokens) -> El {
    let page = &state.window.settings.outposts;
    let online = connected(state);
    let id = outpost.id.clone();
    let key = format!("settings.outpost.{id}");
    let part = |name: &str| format!("{key}.{name}");
    let count = terminals(state, &id);
    let confirming = count > 0 && page.removing.as_deref() == Some(id.as_str());
    let noun = if count == 1 { "terminal" } else { "terminals" };
    let reachable = outpost.reach == "reachable";
    let mut details = vec![
        meta(part("since"), "Since", timestamp(&outpost.state_since), t),
        meta(
            part("build"),
            "Build",
            outpost
                .build
                .clone()
                .filter(|b| !b.is_empty())
                .unwrap_or_else(|| "Not yet known".into()),
            t,
        ),
    ];
    if outpost.host_missing == Some(true) {
        details.push(meta(
            part("host"),
            "Shared terminal host",
            "Not installed".into(),
            t,
        ));
    }
    if let Some(daemon) = outpost.daemon_id.clone().filter(|d| !d.is_empty()) {
        details.push(meta(part("daemon"), "Daemon", daemon, t));
    }
    if let Some(fingerprint) = outpost
        .host_key_fingerprint
        .clone()
        .filter(|f| !f.is_empty())
    {
        details.push(meta(part("host-key"), "Host key", fingerprint, t));
    }
    if let Some(error) = outpost.last_error.clone().filter(|e| !e.is_empty()) {
        details.push(
            col()
                .key(part("error"))
                .gap(space::S1)
                .child(warning(part("error.text"), error, t))
                .children(
                    outpost
                        .last_error_at
                        .as_deref()
                        .map(|at| text(timestamp(at)).font(CAPTION).color(t.text_tertiary)),
                ),
        );
    }
    if let Some(nb) = &outpost.notebook {
        details.push(notebook(&key, nb, t));
    }
    if let Some(said) = update_text(outpost) {
        details.push(status(part("update"), said, t));
    }
    let run = state.facts.update_run.as_ref().as_ref();
    let in_run = run
        .filter(|run| crate::update_run::is_open(run))
        .into_iter()
        .flat_map(|run| run.outposts.iter())
        .chain(run.into_iter().flat_map(crate::update_run::failed_outposts))
        .filter(|o| o.id == id);
    details.extend(in_run.map(|o| {
        status(
            part("run"),
            crate::update_run::outpost_step(o).to_owned(),
            t,
        )
    }));

    let mut actions: Vec<El> = Vec::new();
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
    if needs_update(outpost, &state.facts.home_build) {
        let busy = updating(outpost);
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
            &remove_label,
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
    let unreachable_note = (confirming && !reachable).then(|| {
        status(
            part("remove-note"),
            format!(
                "{} isn't connected. If ssh cannot stop it, its daemon and shells keep running there. Run this instance's attn enrollment leave and attn daemon stop commands there.",
                outpost.name
            ),
            t,
        )
    });
    col()
        .key(key.clone())
        .role("listitem")
        .label(outpost.name.clone())
        .w_full()
        .gap(space::S2)
        .padding(space::S3)
        .radius(6.0)
        .fill(t.bg_panel)
        .border(1.0, t.border_subtle)
        .child(
            row()
                .w_full()
                .gap(space::S3)
                .items_center()
                .child(
                    col()
                        .grow(1.0)
                        .min_w(0.0)
                        .gap(space::S1)
                        .child(
                            text(outpost.name.clone())
                                .font(type_::LABEL)
                                .color(t.text_primary),
                        )
                        .child(
                            text(outpost.destination.clone())
                                .font(CAPTION)
                                .color(t.text_tertiary),
                        ),
                )
                .child(pill(
                    part("state"),
                    outpost.state.clone(),
                    state_tone(&outpost.state),
                    t,
                )),
        )
        .children(details)
        .child(
            row()
                .key(part("actions"))
                .gap(space::S2)
                .items_center()
                .children(actions),
        )
        .children(unreachable_note)
}

fn add_form(state: &State, t: &Tokens) -> El {
    let k = attn_ui::kit(t);
    let page = &state.window.settings.outposts;
    let destination = page.destination.text.trim().to_owned();
    let name_placeholder = if destination.is_empty() {
        "Optional".to_owned()
    } else {
        destination
    };
    let field = |label: &str, input: El| {
        row()
            .w_full()
            .gap(space::S3)
            .items_center()
            .child(
                text(label.to_owned())
                    .font(type_::LABEL)
                    .color(t.text_primary)
                    .w(140.0),
            )
            .child(input)
    };
    col()
        .key("settings.outposts.add")
        .role("form")
        .label("Add an outpost")
        .gap(space::S2)
        .child(field(
            "Name",
            TextInput::new(&page.name)
                .key("settings.outposts.name")
                .label("Name")
                .placeholder(name_placeholder)
                .on_edit(|state: &mut State| &mut panel(state).name)
                .on_submit(add)
                .width(Some(PATH_FIELD))
                .build(k),
        ))
        .child(field(
            "ssh destination",
            TextInput::new(&page.destination)
                .key("settings.outposts.destination")
                .label("ssh destination")
                .placeholder("user@host")
                .on_edit(|state: &mut State| &mut panel(state).destination)
                .on_submit(add)
                .width(Some(PATH_FIELD))
                .build(k),
        ))
        .child(row().child(action(
            "settings.outposts.submit".into(),
            "Add outpost",
            !can_add(state),
            t,
            add,
        )))
}

pub(super) fn page(state: &State, t: &Tokens) -> Vec<El> {
    let page = &state.window.settings.outposts;
    let mut shown = vec![block(
        "outposts.add",
        "Add an outpost",
        "Add the destination you use with ssh. attn installs your home's build there and pins its identity on first contact.",
        t,
        vec![add_form(state, t)],
    )];
    if let Some(error) = page.terminal_error.as_ref().or(page.command_error.as_ref()) {
        shown.push(warning("settings.outposts.error".into(), error.clone(), t));
    }
    if !connected(state) {
        shown.push(status(
            "settings.outposts.offline".into(),
            NOT_CONNECTED.into(),
            t,
        ));
    }
    let listed = &state.facts.outposts;
    let list = if listed.is_empty() {
        text("No outposts added.")
            .key("settings.outposts.empty")
            .label("No outposts added.")
            .font(type_::BODY)
            .color(t.text_tertiary)
    } else {
        col()
            .key("settings.outposts.list")
            .role("list")
            .label("Outposts")
            .w_full()
            .gap(space::S2)
            .children(listed.iter().map(|outpost| card(state, outpost, t)))
    };
    shown.push(block(
        "outposts.list",
        "Outposts",
        "Re-pair forgets the pinned host key and daemon identity, then connects again. Remove closes its terminals across all profiles, releases enrollment and stops its daemon over ssh, then forgets this outpost. Its binary, data and Notebook stay. If ssh cannot reach it, run this instance's attn enrollment leave and attn daemon stop commands there.",
        t,
        vec![list],
    ));
    shown
}
