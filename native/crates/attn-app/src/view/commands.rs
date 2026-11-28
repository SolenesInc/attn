//! A view's commands as its parts offer them: which run, how each control's last run stands,
//! and the call out that runs one, after the user says yes when the command asks first.

use std::collections::HashMap;

use attn_client::TileKey;
use attn_protocol::{
    ViewCommand, ViewCommandCall, ViewDocument, ViewOwnCommand, ViewRunMessage,
    ViewRunResultMessage,
};
use attn_ui::blocks::Actions;
use attn_ui::system::{Button, Look};
use attn_ui::{Dialog, Tokens};
use rune::{El, TextEdit, Tx};

use super::values::Scope;
use crate::State;

/// The commands door of a part: the view's own commands, whether the user started each, how
/// the runs of its controls stand, and the part and rows the controls are in.
#[derive(Clone)]
pub(crate) struct Commands<'a> {
    place: &'a TileKey,
    view: &'a ViewDocument,
    ran: &'a [ViewCommand],
    typed: &'a HashMap<String, TextEdit>,
    part: i64,
    rows: Vec<String>,
}

/// How a control stands: why it cannot run, or its last run.
pub(crate) enum Phase<'a> {
    Unavailable(String),
    Ready,
    Busy,
    Done,
    Failed(&'a str),
}

/// A command the user asked to run that asks first: the question, and the run a yes sends.
pub(crate) struct Confirming {
    place: TileKey,
    run: ViewRunMessage,
    question: String,
    yes: String,
    back: String,
}

impl<'a> Commands<'a> {
    pub(crate) fn of(
        place: &'a TileKey,
        view: &'a ViewDocument,
        ran: &'a [ViewCommand],
        typed: &'a HashMap<String, TextEdit>,
    ) -> Self {
        Commands {
            place,
            view,
            ran,
            typed,
            part: 0,
            rows: Vec::new(),
        }
    }

    /// The commands of the part on `line`.
    pub(crate) fn at(&self, line: i64) -> Self {
        Commands {
            part: line,
            ..self.clone()
        }
    }

    /// The commands inside the row `row`.
    pub(crate) fn within(&self, row: String) -> Self {
        let mut inner = self.clone();
        inner.rows.push(row);
        inner
    }

    /// The text being typed into the entry keyed `key`.
    pub(crate) fn typed(&self, key: &str) -> Option<&'a TextEdit> {
        self.typed.get(key)
    }

    fn own(&self, name: &str) -> Option<&'a ViewOwnCommand> {
        (self.view.parts.iter())
            .filter_map(|part| part.commands.as_ref())
            .flat_map(|commands| &commands.commands)
            .find(|c| c.name == name)
    }

    fn started(&self, own: &ViewOwnCommand) -> bool {
        (self.view.commands.iter()).any(|c| c.line == own.written && c.state == "started")
    }

    /// Whether `call` can run here now.
    pub(crate) fn runs(&self, call: &ViewCommandCall) -> bool {
        self.own(&call.command).is_some_and(|own| self.started(own))
    }

    /// How the control for `call` stands here.
    pub(crate) fn phase(&self, call: &ViewCommandCall) -> Phase<'a> {
        let Some(own) = self.own(&call.command) else {
            return Phase::Unavailable(format!("{} is not available yet", call.command));
        };
        if !self.started(own) {
            return Phase::Unavailable(format!(
                "{} runs once you start the view's commands: press r",
                call.command
            ));
        }
        let rows = (!self.rows.is_empty()).then_some(&self.rows);
        let last = (self.ran.iter())
            .find(|r| r.command == call.command && r.part == self.part && r.rows.as_ref() == rows);
        match last.map(|r| (r.phase.as_str(), r.error.as_deref())) {
            Some(("busy", _)) => Phase::Busy,
            Some(("done", _)) => Phase::Done,
            Some(("failed", why)) => Phase::Failed(why.unwrap_or("it failed")),
            _ => Phase::Ready,
        }
    }

    /// Runs `call` from here, with `text` when the user typed it, once the user says yes if
    /// the command asks first. `scope` fills the question; `back` is the control the keyboard
    /// returns to after the question.
    pub(crate) fn press(
        &self,
        call: &ViewCommandCall,
        scope: &Scope<'_>,
        back: &str,
    ) -> impl Fn(&mut State, Option<String>, &mut Tx) + 'static {
        let run = ViewRunMessage {
            desktop_id: self.place.desktop.clone(),
            tile_id: self.place.tile.clone(),
            command: call.command.clone(),
            part: self.part,
            rows: (!self.rows.is_empty()).then(|| self.rows.clone()),
            ..ViewRunMessage::new()
        };
        let question = (self.own(&call.command))
            .and_then(|own| own.confirm.as_ref())
            .map(|confirm| scope.text(confirm));
        let (place, yes, back) = (self.place.clone(), named(&call.command), back.to_owned());
        move |state: &mut State, text: Option<String>, tx: &mut Tx| {
            let run = ViewRunMessage {
                text,
                ..run.clone()
            };
            match &question {
                Some(question) => {
                    state.views.confirming = Some(Confirming {
                        place: place.clone(),
                        run,
                        question: question.clone(),
                        yes: yes.clone(),
                        back: back.clone(),
                    });
                    state.views.revision += 1;
                }
                None => send(state, &place, run, tx),
            }
        }
    }
}

/// A command's name as a control says it: `rerun-job` is Rerun job.
pub(crate) fn named(command: &str) -> String {
    (command.chars().enumerate())
        .map(|(i, c)| match (i, c) {
            (0, c) => c.to_ascii_uppercase(),
            (_, '-' | '_') => ' ',
            (_, c) => c,
        })
        .collect()
}

/// Sends `run` to the home, which keeps how it goes on the tile.
fn send(state: &mut State, place: &TileKey, run: ViewRunMessage, tx: &mut Tx) {
    let tile = place.tile.clone();
    state.ask(
        move |request_id| ViewRunMessage {
            request_id,
            ..run.clone()
        },
        move |state, answer, _| {
            let refused = match answer.parse::<ViewRunResultMessage>() {
                Some(r) if r.success => None,
                Some(r) => Some(
                    r.error
                        .unwrap_or_else(|| "the daemon refused the command".into()),
                ),
                None => Some("the daemon did not answer, so the command may not have run".into()),
            };
            state.views.refused(&tile, refused);
        },
        tx,
    );
}

/// Closes the question and gives the keyboard back to the control that asked it.
fn end_confirming(state: &mut State, tx: &mut Tx) -> Option<Confirming> {
    let confirming = state.views.confirming.take()?;
    state.views.revision += 1;
    tx.focus(confirming.back.clone(), true);
    Some(confirming)
}

fn confirm(state: &mut State, tx: &mut Tx) {
    if let Some(Confirming { place, run, .. }) = end_confirming(state, tx) {
        send(state, &place, run, tx);
    }
}

fn cancel(state: &mut State, tx: &mut Tx) {
    end_confirming(state, tx);
}

/// The question a command that asks first puts over the window, while it waits for the user.
pub(crate) fn confirmation(state: &State, t: &Tokens) -> Vec<El> {
    let Some(Confirming { question, yes, .. }) = &state.views.confirming else {
        return Vec::new();
    };
    let mut layer = Vec::new();
    Dialog::new("view.confirm", question.clone())
        .on_close_tx(cancel)
        .actions(
            Actions::new("view.confirm.actions")
                .then_end()
                .push(
                    Button::new("Cancel")
                        .on_press_tx(cancel)
                        .build(t)
                        .key("view.confirm.cancel"),
                )
                .push(
                    Button::new(yes.clone())
                        .look(Look::Primary)
                        .on_press_tx(confirm)
                        .build(t)
                        .key("view.confirm.yes"),
                ),
        )
        .build(t, &mut layer);
    layer
}
