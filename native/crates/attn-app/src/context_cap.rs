//! The context cap prompt: the token budget an agent compacts at, which
//! the daemon applies by reloading the agent.

use std::collections::BTreeMap;

use attn_protocol::{SessionContextWindowCapResultMessage, SetSessionContextWindowCapMessage};
use attn_ui::blocks::{Actions, Field};
use attn_ui::system::{Button, FieldState, Look, Part, Said, line_box};
use attn_ui::{Dialog, Tokens};
use rune::{El, TextEdit, Tx};
use rune_kit::TextInput;

use crate::State;
use crate::sidebar::model::display_name;

const KEY: &str = "context-cap";

pub(crate) struct ContextCap {
    session: String,
    name: String,
    current: i64,
    edit: TextEdit,
    refused: Option<String>,
    saving: bool,
}

/// Whether `agent` launches with a context-window cap.
pub(crate) fn takes_cap(settings: &BTreeMap<String, String>, agent: &str) -> bool {
    crate::settings::harnesses::has(settings, &agent.to_lowercase(), "context_window_cap")
}

pub(crate) fn open(state: &mut State, session: &str, tx: &mut Tx) {
    let Some(found) = state.facts.sessions.iter().find(|s| s.id == session) else {
        return;
    };
    let current = found.context_window_cap.unwrap_or(0);
    let mut edit = TextEdit::new(if current > 0 {
        current.to_string()
    } else {
        String::new()
    });
    edit.anchor = 0;
    state.window.context_cap = Some(ContextCap {
        session: session.to_owned(),
        name: display_name(found),
        current,
        edit,
        refused: None,
        saving: false,
    });
    tx.focus(format!("{KEY}.field"), true);
}

pub(crate) fn follow(state: &mut State) {
    let gone = state
        .window
        .context_cap
        .as_ref()
        .is_some_and(|c| !state.facts.sessions.iter().any(|s| s.id == c.session));
    if gone {
        state.window.context_cap = None;
    }
}

fn close(state: &mut State, _: &mut Tx) {
    if state.window.context_cap.as_ref().is_some_and(|c| !c.saving) {
        state.window.context_cap = None;
    }
}

fn submit(state: &mut State, tx: &mut Tx) {
    let Some(prompt) = state.window.context_cap.as_mut().filter(|c| !c.saving) else {
        return;
    };
    let typed = prompt.edit.text.trim();
    let Some(cap) = (if typed.is_empty() {
        Some(0)
    } else {
        typed.parse::<i64>().ok().filter(|cap| *cap >= 0)
    }) else {
        prompt.refused = Some("Enter a whole number of tokens, or leave blank for no cap".into());
        show_refused(tx);
        return;
    };
    if cap == prompt.current {
        state.window.context_cap = None;
        return;
    }
    prompt.saving = true;
    prompt.refused = None;
    let session = prompt.session.clone();
    let command = SetSessionContextWindowCapMessage {
        session_id: session.clone(),
        cap,
        ..SetSessionContextWindowCapMessage::new()
    };
    state.ask_as(
        attn_client::context_cap_id(&session),
        &command,
        move |state, answer, tx| {
            let refused = match answer.parse::<SessionContextWindowCapResultMessage>() {
                Some(r) if r.success => None,
                r => Some(
                    r.and_then(|r| r.error)
                        .filter(|e| !e.is_empty())
                        .unwrap_or_else(|| "Setting the cap failed".into()),
                ),
            };
            let Some(prompt) = state
                .window
                .context_cap
                .as_mut()
                .filter(|c| c.session == session && c.saving)
            else {
                return;
            };
            match refused {
                None => state.window.context_cap = None,
                Some(why) => {
                    prompt.saving = false;
                    prompt.refused = Some(why);
                    show_refused(tx);
                }
            }
        },
        tx,
    );
}

fn show_refused(tx: &mut Tx) {
    let at = tx.focused().map(str::to_owned);
    attn_ui::blocks::show_refusal(
        tx,
        &format!("{KEY}.body"),
        format!("{KEY}.field.error"),
        at.as_deref(),
    );
}

pub(crate) fn view(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some(prompt) = &state.window.context_cap else {
        return;
    };
    let lead = Said::parts([
        Part::Text("Cap ".to_owned()),
        Part::Code(prompt.name.clone()),
        Part::Text(
            "’s context window so it compacts at this many tokens. Blank means no cap. \
             Saving reloads the agent to apply it: the conversation is resumed, but anything \
             mid-turn is lost."
                .to_owned(),
        ),
    ]);
    let saving = prompt.saving;
    let input = TextInput::new(&prompt.edit)
        .key(format!("{KEY}.field"))
        .label("Context window cap in tokens")
        .placeholder("e.g. 800000 — blank for no cap")
        .on_edit(|state: &mut State| {
            &mut state
                .window
                .context_cap
                .as_mut()
                .expect("the cap field shows only while the prompt is open")
                .edit
        })
        .on_change(|state: &mut State, _: &mut Tx| {
            if let Some(prompt) = state.window.context_cap.as_mut() {
                prompt.refused = None;
            }
        })
        .on_submit(submit);
    let field = if saving {
        FieldState::Disabled
    } else {
        FieldState::failed_if(prompt.refused.is_some())
    };
    Dialog::new(KEY, "Context window cap")
        .lead(lead)
        .on_close_tx(close)
        .push(
            Field::new(format!("{KEY}.field"), "Cap in tokens")
                .control(line_box(input, field, t))
                .error(prompt.refused.clone())
                .build(t),
        )
        .actions(
            Actions::new(format!("{KEY}.actions"))
                .then_end()
                .push(
                    Button::new("Cancel")
                        .disabled(saving)
                        .on_press_tx(close)
                        .build(t)
                        .key(format!("{KEY}.cancel")),
                )
                .push(
                    Button::new(if saving { "Saving…" } else { "Save" })
                        .look(Look::Primary)
                        .disabled(saving)
                        .on_press_tx(submit)
                        .build(t)
                        .key(format!("{KEY}.save")),
                ),
        )
        .build(t, layers);
}
