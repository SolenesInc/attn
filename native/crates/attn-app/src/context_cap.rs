//! The Tauri app's `SessionContextCapPrompt`: the token budget an agent compacts at, which
//! the daemon applies by reloading the agent.

use attn_protocol::{SessionContextWindowCapResultMessage, SetSessionContextWindowCapMessage};
use attn_ui::{Backdrop, Dialog, DialogFrame, Frame, Tokens};
use rune::{Align, El, Span, TextEdit, TextStyle, Tx, col, text};
use rune_kit::TextInput;

use crate::State;
use crate::sidebar::model::display_name;

const KEY: &str = "context-cap";
const TITLE_TYPE: TextStyle = TextStyle::new(11.0, 600.0).mono().tracking(1.1);
const MESSAGE_TYPE: TextStyle = TextStyle::new(13.0, 400.0).lh(20.0);
const INPUT_TYPE: TextStyle = TextStyle::new(12.0, 400.0).mono();
const ERROR_TYPE: TextStyle = TextStyle::new(12.0, 400.0);

pub(crate) struct ContextCap {
    session: String,
    name: String,
    current: i64,
    edit: TextEdit,
    refused: Option<String>,
    saving: bool,
}

/// Whether `agent` launches with a context-window cap.
pub(crate) fn takes_cap(agent: &str) -> bool {
    matches!(agent.to_lowercase().as_str(), "claude" | "codex")
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
        move |state, answer, _| {
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
                }
            }
        },
        tx,
    );
}

/// `.context-cap-content`: a centred card over the window's overlay.
fn frame(t: &Tokens) -> DialogFrame {
    DialogFrame {
        backdrop: Backdrop::overlay(t),
        surface: Frame {
            fill: t.bg_elevated,
            edge: t.border,
            radius: 12.0,
            shadow: attn_ui::frame::shadow(t, 16.0, 48.0, 0.42),
        },
        header: [20.0, 22.0, 14.0, 22.0],
        ..DialogFrame::prompt(t)
    }
}

pub(crate) fn view(state: &State, t: &Tokens, layers: &mut Vec<El>) {
    let Some(prompt) = &state.window.context_cap else {
        return;
    };
    let before = "Cap ";
    let message = format!(
        "{before}{}’s context window so it compacts at this many tokens. Blank means no cap. \
         Saving reloads the agent to apply it: the conversation is resumed, but anything \
         mid-turn is lost.",
        prompt.name
    );
    let name = Span::new(before.len(), before.len() + prompt.name.len())
        .mono()
        .color(t.brand);
    let lead = col()
        .w_full()
        .gap(11.0)
        .child(
            text("CONTEXT WINDOW CAP")
                .font(TITLE_TYPE)
                .color(t.text_muted)
                .w_full()
                .align(Align::Center),
        )
        .child(
            text(message)
                .key(format!("{KEY}.message"))
                .font(MESSAGE_TYPE)
                .color(t.text_primary)
                .spans(vec![name])
                .w_full()
                .wrap()
                .align(Align::Center),
        );
    let field = TextInput::new(&prompt.edit)
        .key(format!("{KEY}.field"))
        .plain()
        .text_style(INPUT_TYPE)
        .height(INPUT_TYPE.line_px())
        .width(None)
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
        .on_submit(submit)
        .build(attn_ui::kit(t));
    let saving = prompt.saving;
    let body = col()
        .w_full()
        .px(22.0)
        .gap(14.0)
        .child(
            col()
                .w_full()
                .px(10.0)
                .py(8.0)
                .radius(6.0)
                .fill(t.bg_app)
                .border(1.0, t.border_hover)
                .child(field.w_full()),
        )
        .children(prompt.refused.clone().map(|why| {
            text(why)
                .key(format!("{KEY}.refused"))
                .font(ERROR_TYPE)
                .color(t.error)
                .w_full()
                .wrap()
                .align(Align::Center)
        }));
    let actions = rune::row()
        .w_full()
        .gap(8.0)
        .pt(14.0)
        .pb(20.0)
        .justify_center()
        .child(crate::prompt_dialog::button(
            "Cancel",
            false,
            format!("{KEY}.cancel"),
            t,
            |b| b.disabled(saving).on_press_tx(close),
        ))
        .child(crate::prompt_dialog::button(
            if saving { "Saving…" } else { "Save" },
            true,
            format!("{KEY}.save"),
            t,
            |b| b.disabled(saving).on_press_tx(submit),
        ));
    Dialog::new(KEY, "Context window cap", frame(t))
        .lead(lead)
        .width(460.0)
        .on_close_tx(close)
        .child(body)
        .child(actions)
        .build(t, layers);
}
