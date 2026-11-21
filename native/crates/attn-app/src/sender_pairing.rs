//! The Tauri app's `SenderPairingPrompt`: a program that ran `attn send pair` waits for the
//! user to allow or deny it, with the code it shows, over everything in the main window until
//! the daemon says it no longer waits.

use attn_protocol::{SenderPairDecideMessage, SenderPairingRequest, SendersGetMessage};
use attn_ui::blocks::{Actions, MatchCode, Notice};
use attn_ui::system::{Button, Look};
use attn_ui::{Dialog, Tokens};
use rune::{El, Key, KeyEvent, Mods, Tx};

use crate::State;

const KEY: &str = "sender-pairing";
const DENY: &str = "sender-pairing.deny";
const ALLOW: &str = "sender-pairing.allow";

/// The answer the window gave the request it shows, while the daemon decides it, and why the
/// last one failed.
#[derive(Default)]
pub(crate) struct SenderPairing {
    shown: Option<String>,
    deciding: bool,
    refused: Option<String>,
}

fn waiting(state: &State) -> Option<&SenderPairingRequest> {
    state
        .window
        .tile
        .is_none()
        .then(|| state.facts.sender_pairing.as_ref().as_ref())
        .flatten()
}

/// Asks on each connect for the program waiting now, as the daemon tells only of changes.
pub(crate) fn connected(state: &mut State) {
    let _ = state.daemon.send(&SendersGetMessage::new());
}

/// Starts each request the daemon asks about afresh, with the keyboard on Deny.
pub(crate) fn follow(state: &mut State, tx: &mut Tx) {
    let request = waiting(state).map(|r| r.id.clone());
    if state.window.sender_pairing.shown == request {
        return;
    }
    let focus = request.is_some();
    state.window.sender_pairing = SenderPairing {
        shown: request,
        ..SenderPairing::default()
    };
    if focus {
        tx.focus(DENY, true);
    }
}

fn decide(state: &mut State, allow: bool, tx: &mut Tx) {
    let Some(pairing) = state.window.sender_pairing.shown.clone() else {
        return;
    };
    let prompt = &mut state.window.sender_pairing;
    if prompt.deciding {
        return;
    }
    prompt.deciding = true;
    prompt.refused = None;
    let answered = pairing.clone();
    state.ask(
        |request_id| SenderPairDecideMessage {
            request_id,
            pairing_id: pairing,
            allow,
            ..SenderPairDecideMessage::new()
        },
        move |state, answer, tx| {
            let prompt = &mut state.window.sender_pairing;
            if prompt.shown.as_deref() != Some(answered.as_str()) {
                return;
            }
            prompt.deciding = false;
            prompt.refused = answer.outcome().err();
            if prompt.refused.is_some() {
                let at = tx.focused().map(str::to_owned);
                attn_ui::blocks::show_refusal(
                    tx,
                    &format!("{KEY}.body"),
                    format!("{KEY}.error"),
                    at.as_deref(),
                );
            } else {
                tx.instant();
            }
        },
        tx,
    );
}

/// Return presses the focused answer, as it does in the Tauri app.
fn press_focused(state: &mut State, ev: &KeyEvent, tx: &mut Tx) -> bool {
    if ev.key != Key::Enter || ev.mods != Mods::default() {
        return false;
    }
    let allow = match tx.focused() {
        Some(DENY) => false,
        Some(ALLOW) => true,
        _ => return false,
    };
    decide(state, allow, tx);
    true
}

fn profile_name(state: &State) -> Option<&str> {
    let profiles = &state.facts.profiles;
    profiles
        .all
        .iter()
        .find(|p| p.id == profiles.selected)
        .map(|p| p.name.as_str())
}

pub(crate) fn view(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some(request) = waiting(state) else {
        return;
    };
    let prompt = &state.window.sender_pairing;
    let deciding = prompt.deciding;
    let to_profile = profile_name(state).map_or_else(String::new, |name| {
        format!(", to the sessions of profile “{name}”")
    });
    let mut dialog = Dialog::new(
        KEY,
        format!("Allow “{}” to send messages as you?", request.name),
    )
    .lead(format!(
        "This program will be able to send files and messages that agents receive as your own{to_profile}."
    ))
    .click_outside(false)
    .on_key(press_focused)
    .on_close_tx(|_: &mut State, _: &mut Tx| {})
    .push(
        MatchCode::new(
            format!("{KEY}.match"),
            request.code.clone(),
            "Check that the program shows the same code.",
        )
        .build(t),
    );
    if let Some(refused) = &prompt.refused {
        dialog = dialog.push(Notice::error(format!("{KEY}.error"), refused.clone()).build(t));
    }
    dialog
        .actions(
            Actions::new(format!("{KEY}.actions"))
                .then_end()
                .push(
                    Button::new("Deny")
                        .disabled(deciding)
                        .on_press_tx(|state: &mut State, tx: &mut Tx| decide(state, false, tx))
                        .build(t)
                        .key(DENY),
                )
                .push(
                    Button::new("Allow")
                        .look(Look::Primary)
                        .disabled(deciding)
                        .on_press_tx(|state: &mut State, tx: &mut Tx| decide(state, true, tx))
                        .build(t)
                        .key(ALLOW),
                ),
        )
        .build(t, layers);
}
