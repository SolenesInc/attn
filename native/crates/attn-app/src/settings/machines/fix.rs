//! A failed move check's fix: a shell on the move's target, under the check, with the command the
//! daemon named typed and not run. The check holds the shell until a check run finds it passing.

use std::collections::{HashMap, HashSet};

use attn_protocol::{
    HomeMoveCheck, SessionPlacement, SpawnSessionMessage, TerminalBinding, UnregisterMessage,
};
use attn_ui::Tokens;
use attn_ui::blocks::{Inline, Stack};
use attn_ui::system::{Button, Look, Size, Type, words};
use rune::{El, Tx, col};

use crate::State;
use crate::action::Action;

const TERMINAL: &str = "settings.machines.fix.terminal";

fn terminal_key(check: &str) -> String {
    format!("{TERMINAL}.{check}")
}

/// Whether the focused element is a fix shell's.
pub(crate) fn focused(key: Option<&str>) -> bool {
    key.is_some_and(|key| key.starts_with(TERMINAL))
}

#[derive(Default)]
pub(crate) struct Fixes {
    /// The session of the fix shell that holds the keyboard, and its check.
    keys: Option<(String, String)>,
    /// The fix shells this window asked for and the home holds no binding of yet, by check.
    starting: HashMap<String, Spawn>,
    /// The fix shells closed from this window, until the home drops their binding.
    closed: HashSet<String>,
    /// The last finished command of each fix terminal that this window heard.
    heard: HashMap<String, u64>,
    /// The fix terminals drawn at the last poll.
    drawing: Vec<String>,
}

enum Spawn {
    Starting(String),
    Refused(String),
}

fn fixes(state: &mut State) -> &mut Fixes {
    &mut state.window.settings.machines.fixes
}

/// The machine whose failed checks show, while Settings shows them.
fn shown_machine(state: &State) -> Option<&str> {
    if !crate::settings::is_open(state)
        || state.window.settings.section != crate::settings::Section::Machines
    {
        return None;
    }
    super::checked_move(state).map(|(machine, _)| machine)
}

fn host_of<'f>(state: &'f State, session: &str) -> Option<&'f str> {
    (state.facts.sessions.iter())
        .find(|s| s.id == session)
        .and_then(|s| s.host.as_deref())
}

/// The shell that the failed check named `check` of a move to `machine` holds.
fn held<'f>(state: &'f State, machine: &str, check: &str) -> Option<&'f TerminalBinding> {
    let fixes = &state.window.settings.machines.fixes;
    (state.facts.held_terminals.iter()).find(|b| {
        b.move_check.as_deref() == Some(check)
            && !fixes.closed.contains(&b.session_id)
            && host_of(state, &b.session_id) == Some(machine)
    })
}

/// The fix shells under the checks Settings shows.
fn shown(state: &State) -> Vec<&TerminalBinding> {
    let Some((machine, checks)) = shown_machine(state).and_then(|_| super::checked_move(state))
    else {
        return Vec::new();
    };
    (checks.iter())
        .filter(|c| !c.ok && c.fix.is_some())
        .filter_map(|c| held(state, machine, &c.name))
        .collect()
}

pub(crate) fn drawn_terminals<'f>(
    state: &State,
    facts: &'f attn_client::Facts,
) -> Vec<crate::terminal::Drawn<'f>> {
    let shown: Vec<String> = shown(state).iter().map(|b| b.terminal_id.clone()).collect();
    (facts.held_terminals.iter())
        .filter(|b| shown.contains(&b.terminal_id))
        .map(|b| crate::terminal::Drawn {
            runtime: &b.terminal_id,
            session: &b.session_id,
            kind: "shell",
        })
        .collect()
}

/// The fix terminal that holds the keyboard, while Settings shows it.
pub(crate) fn keys_terminal(state: &State) -> Option<String> {
    let (keys, _) = state.window.settings.machines.fixes.keys.as_ref()?;
    (shown(state).into_iter())
        .find(|b| b.session_id == *keys)
        .map(|b| b.terminal_id.clone())
}

/// Opens the fix of check `check` of the move to `machine` in a shell there, or shows the one
/// open, and gives it the keyboard.
fn open(state: &mut State, machine: &str, check: &HomeMoveCheck, tx: &mut Tx) {
    let Some(command) = check.fix.clone() else {
        return;
    };
    let name = check.name.clone();
    if let Some(binding) = held(state, machine, &name) {
        let session = binding.session_id.clone();
        return take_keys(state, session, name, tx);
    }
    let session = uuid::Uuid::new_v4().to_string();
    let spawn = SpawnSessionMessage {
        id: session.clone(),
        agent: "shell".into(),
        host: Some(machine.to_owned()),
        cwd: "~".into(),
        typed: Some(command),
        placement: Some(SessionPlacement {
            move_check: Some(name.clone()),
            ..SessionPlacement::default()
        }),
        cols: 80,
        rows: 24,
        profile_id: state.facts.profiles.selected.clone(),
        ..SpawnSessionMessage::new()
    };
    (fixes(state).starting).insert(name.clone(), Spawn::Starting(session.clone()));
    take_keys(state, session.clone(), name.clone(), tx);
    state.ask_as(
        session,
        &spawn,
        move |state: &mut State, answer, _: &mut Tx| {
            if let Err(why) = answer.outcome() {
                fixes(state).starting.insert(name, Spawn::Refused(why));
            }
        },
        tx,
    );
}

fn take_keys(state: &mut State, session: String, check: String, tx: &mut Tx) {
    tx.focus(terminal_key(&check), false);
    fixes(state).keys = Some((session, check));
}

fn check_key(state: &State, check: &str, name: &str) -> Option<String> {
    let (machine, _) = super::checked_move(state)?;
    Some(format!(
        "{}.move.check.{check}.{name}",
        super::row_key(machine)
    ))
}

/// Whether a fix shell holds the keyboard, or will once it starts.
pub(crate) fn holds(state: &State) -> bool {
    state.window.settings.machines.fixes.keys.is_some()
}

/// The Settings chord hands the keyboard back to the checks, at the fix's Close terminal.
pub(crate) fn hand_back(state: &mut State, tx: &mut Tx) -> bool {
    let Some((_, check)) = fixes(state).keys.take() else {
        return false;
    };
    if let Some(close) = check_key(state, &check, "close") {
        tx.focus(close, false);
    }
    true
}

fn end(state: &mut State, session: String, tx: &mut Tx) {
    if state
        .daemon
        .send(&UnregisterMessage {
            id: session.clone(),
            ..UnregisterMessage::new()
        })
        .is_err()
    {
        state.window.notify(
            "Could not end that terminal: not connected to the daemon".into(),
            tx,
        );
        return;
    }
    let fixes = fixes(state);
    if fixes
        .keys
        .as_ref()
        .is_some_and(|(keys, _)| *keys == session)
    {
        fixes.keys = None;
    }
    fixes.closed.insert(session);
}

/// Ends the fix shell of check `check` of the move to `machine`.
fn close(state: &mut State, machine: &str, check: &str, tx: &mut Tx) {
    let starting = match fixes(state).starting.remove(check) {
        Some(Spawn::Starting(session)) => Some(session),
        _ => None,
    };
    let held = held(state, machine, check).map(|b| b.session_id.clone());
    for session in held.into_iter().chain(starting) {
        end(state, session, tx);
    }
    if let Some(open) = check_key(state, check, "open") {
        tx.focus(open, false);
    }
}

/// Ends every fix shell of a move to `machine`, as its Cancel does.
pub(super) fn close_all(state: &mut State, machine: &str, tx: &mut Tx) {
    let held: Vec<String> = (state.facts.held_terminals.iter())
        .filter(|b| b.move_check.is_some() && host_of(state, &b.session_id) == Some(machine))
        .map(|b| b.session_id.clone())
        .collect();
    let starting: Vec<String> = (fixes(state).starting.drain())
        .filter_map(|(_, spawn)| match spawn {
            Spawn::Starting(session) => Some(session),
            Spawn::Refused(_) => None,
        })
        .collect();
    for session in held.into_iter().chain(starting) {
        end(state, session, tx);
    }
}

/// Keeps the fix terminals Settings shows attached, forgets what the home's bindings settled,
/// and checks again once a fix's command finishes cleanly; whether anything changed.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) -> bool {
    let bound: Vec<(String, String)> = (state.facts.held_terminals.iter())
        .filter(|b| b.move_check.is_some())
        .map(|b| (b.session_id.clone(), b.terminal_id.clone()))
        .collect();
    let is_bound = |session: &str| bound.iter().any(|(s, _)| s == session);
    let kept = fixes(state);
    let before = (kept.starting.len(), kept.closed.len());
    (kept.starting).retain(|_, spawn| !matches!(spawn, Spawn::Starting(s) if is_bound(s)));
    kept.closed.retain(|session| is_bound(session));
    kept.heard
        .retain(|terminal, _| bound.iter().any(|(_, t)| t == terminal));
    let mut changed = before != (kept.starting.len(), kept.closed.len());
    let drawing: Vec<String> = shown(state).iter().map(|b| b.terminal_id.clone()).collect();
    if fixes(state).drawing != drawing {
        fixes(state).drawing = drawing.clone();
        crate::follow_terminals(state, tx);
        changed = true;
    }
    let mut finished = false;
    for terminal in drawing {
        let Some((block, exit)) = state.terminals.last_exit(&terminal) else {
            continue;
        };
        if fixes(state).heard.insert(terminal, block) != Some(block) {
            changed = true;
            finished |= exit == Some(0);
        }
    }
    if finished && let Some((machine, name)) = super::checked_machine(state) {
        super::check_move(state, &machine, &name, tx);
    }
    changed
}

fn holds_keys(state: &State, session: &str) -> bool {
    (state.window.settings.machines.fixes.keys.as_ref()).is_some_and(|(keys, _)| keys == session)
}

fn hint(state: &State, binding: &TerminalBinding) -> String {
    let exit = (state.terminals.last_exit(&binding.terminal_id)).map(|(_, exit)| exit);
    let holds = holds_keys(state, &binding.session_id);
    match exit {
        Some(Some(0)) => "Done.".to_owned(),
        Some(Some(code)) => format!("It exited with {code}."),
        _ if holds => match state.keymap.binding(&Action::UiOpenSettings) {
            Some(chord) => format!(
                "Press Enter to run it. {} goes back to the checks.",
                state.keymap.label(chord)
            ),
            None => "Press Enter to run it.".to_owned(),
        },
        _ => "Click the terminal to type in it.".to_owned(),
    }
}

/// What a failed check with a fix shows beside and under its line: Open in terminal, or the fix
/// shell with its place, what to do next and Close terminal.
pub(super) fn under(
    state: &State,
    machine: &str,
    key: &str,
    check: &HomeMoveCheck,
    scale: f32,
    t: &Tokens,
) -> Option<(Option<El>, Option<El>)> {
    check.fix.as_ref()?;
    let part = |name: &str| format!("{key}.check.{}.{name}", check.name);
    let binding = held(state, machine, &check.name);
    let starting = state
        .window
        .settings
        .machines
        .fixes
        .starting
        .get(&check.name);
    let screen = match (binding, starting) {
        (Some(binding), _) => holding_keys(
            crate::desktop::ask_terminal(
                state,
                (&binding.terminal_id, &binding.session_id),
                holds_keys(state, &binding.session_id),
                scale,
                t,
            ),
            binding.session_id.clone(),
            check.name.clone(),
        ),
        (None, Some(Spawn::Starting(session))) => holding_keys(
            words("Starting a shell…", Type::Small, t.text_tertiary).key(part("starting")),
            session.clone(),
            check.name.clone(),
        ),
        (None, Some(Spawn::Refused(why))) => {
            let said = format!(
                "Could not open a shell on {}: {why}",
                super::machine_title(state, machine)
            );
            let refused = (words(said.clone(), Type::Small, t.warning))
                .key(part("refused"))
                .label(said)
                .wrap();
            return Some((
                Some(open_button(machine, check, part("open"), t)),
                Some(refused),
            ));
        }
        (None, None) => return Some((Some(open_button(machine, check, part("open"), t)), None)),
    };
    let (machine, name) = (machine.to_owned(), check.name.clone());
    let place = binding
        .and_then(|b| state.facts.sessions.iter().find(|s| s.id == b.session_id))
        .map(|s| {
            format!(
                "{} · {}",
                super::machine_title(state, &machine),
                s.directory
            )
        })
        .unwrap_or_default();
    let bar = Inline::loose()
        .key(part("bar"))
        .w_full()
        .child(
            words(place, Type::Mono, t.text_tertiary)
                .key(part("place"))
                .min_w(0.0)
                .shrink()
                .truncate(),
        )
        .children(binding.map(|binding| {
            words(hint(state, binding), Type::Small, t.text_secondary)
                .key(part("hint"))
                .min_w(0.0)
                .shrink()
        }))
        .child(El::from(
            Button::new("Close terminal")
                .size(Size::Compact)
                .look(Look::Quiet)
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    close(state, &machine, &name, tx)
                })
                .build(t)
                .key(part("close")),
        ));
    let shell = Stack::tight()
        .key(part("shell"))
        .w_full()
        .child(col().w_full().h(320.0).border(1.0, t.border).child(screen))
        .child(bar);
    Some((None, Some(shell)))
}

fn open_button(machine: &str, check: &HomeMoveCheck, key: String, t: &Tokens) -> El {
    let (machine, check) = (machine.to_owned(), check.clone());
    Button::new("Open in terminal")
        .size(Size::Compact)
        .on_press_tx(move |state: &mut State, tx: &mut Tx| open(state, &machine, &check, tx))
        .build(t)
        .key(key)
        .into()
}

/// The element that holds the keyboard for a fix shell: focused, every key but the Settings
/// chord goes to the shell, and a press in it takes the keyboard.
fn holding_keys(shown: El, session: String, check: String) -> El {
    col()
        .key(terminal_key(&check))
        .role("group")
        .label("Terminal")
        .focusable(true)
        .no_focus_ring()
        .on_key(|state: &mut State, ev: &rune::KeyEvent, tx: &mut Tx| {
            crate::keys::on_key(state, ev, tx) || !matches!(ev.key, rune::Key::Modifier { .. })
        })
        .on_press_within(move |state: &mut State, tx: &mut Tx| {
            take_keys(state, session.clone(), check.clone(), tx)
        })
        .w_full()
        .grow(1.0)
        .min_h(0.0)
        .child(shown.w_full().grow(1.0).min_h(0.0))
}
