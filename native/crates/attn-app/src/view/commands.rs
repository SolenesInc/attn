//! A view's commands as its parts offer them: which run, how each control's last run stands,
//! the letter each control answers, and the call out that runs one, after the user says yes
//! when the command asks first.

use std::collections::HashMap;

use attn_client::TileKey;
use attn_protocol::go_values::VIEW_STORE_COMMANDS;
use attn_protocol::{
    ViewCommand, ViewCommandCall, ViewDocument, ViewOwnCommand, ViewPart, ViewRunMessage,
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
    letters: Vec<(String, char)>,
    part: i64,
    rows: Vec<String>,
}

/// What a key does to the view's commands, once the state it read is let go.
pub(crate) type Keyed = Box<dyn Fn(&mut State, &mut Tx)>;

/// The commands the keys reach in `parts`, in order: those of the view's own `:::entry` lines, of
/// `:::controls` and of the rows of each list, inside `::::each` too. A `::::one-by-one` item's
/// rows are never highlighted, so its controls take no letter.
fn keyed_calls(parts: &[ViewPart], outermost: bool, out: &mut Vec<ViewCommandCall>) {
    for part in parts {
        if outermost {
            out.extend(part.entry.iter().map(|entry| entry.command.clone()));
        }
        out.extend(
            part.controls
                .iter()
                .flat_map(|c| c.controls.iter().cloned()),
        );
        out.extend(
            (part.list.iter())
                .flat_map(|l| l.slots.controls.iter().flatten())
                .cloned(),
        );
        if let Some(each) = &part.each {
            keyed_calls(&each.blocks, false, out);
        }
    }
}

/// The keys a view's tile already answers, which no control's letter takes.
const TILE_KEYS: &str = "jkresfx";

/// The letter of each command the view's controls run, in the order the view first offers it:
/// the first letter of its name that the tile does not already answer and no command before
/// it took. A command whose name has no such letter has none.
fn letters(view: &ViewDocument) -> Vec<(String, char)> {
    let mut calls = Vec::new();
    keyed_calls(&view.parts, true, &mut calls);
    let mut letters: Vec<(String, char)> = Vec::new();
    for call in calls {
        if letters.iter().any(|(name, _)| name == &call.command) {
            continue;
        }
        let free = call
            .command
            .chars()
            .map(|c| c.to_ascii_lowercase())
            .find(|c| {
                c.is_ascii_lowercase()
                    && !TILE_KEYS.contains(*c)
                    && !letters.iter().any(|(_, taken)| taken == c)
            });
        if let Some(letter) = free {
            letters.push((call.command.clone(), letter));
        }
    }
    letters
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
    back: Option<String>,
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
            letters: letters(view),
            part: 0,
            rows: Vec::new(),
        }
    }

    /// The letter that runs `command` from a control.
    pub(crate) fn letter(&self, command: &str) -> Option<char> {
        (self.letters.iter())
            .find(|(name, _)| name == command)
            .map(|(_, letter)| *letter)
    }

    /// Which of `calls` the letter `typed` runs.
    pub(crate) fn lettered<'c>(
        &self,
        calls: &'c [ViewCommandCall],
        typed: &str,
    ) -> Option<&'c ViewCommandCall> {
        calls.iter().find(|call| {
            self.letter(&call.command)
                .is_some_and(|l| typed == l.to_string())
        })
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

    /// Why `command` cannot run here now, or nothing when it can. A store's command runs once
    /// the view's own store holds its items; a view's own command once the user started it.
    fn unavailable(&self, command: &str) -> Option<String> {
        if VIEW_STORE_COMMANDS.contains(&command) {
            let store =
                (self.view.sources.iter()).find(|s| s.kind == "store" && !s.id.contains('/'));
            return match store {
                Some(s) if s.state == "ok" => None,
                Some(s) => Some(
                    s.notice
                        .clone()
                        .unwrap_or_else(|| format!("{command} waits for the view's store")),
                ),
                None => Some(format!(
                    "{command} changes a store of the view's own, and it has none"
                )),
            };
        }
        match self.own(command) {
            None => Some(format!("{command} is not available yet")),
            Some(own) if !self.started(own) => Some(format!(
                "{command} runs once you start the view's commands: press r"
            )),
            Some(_) => None,
        }
    }

    /// Whether `call` can run here now.
    pub(crate) fn runs(&self, call: &ViewCommandCall) -> bool {
        self.unavailable(&call.command).is_none()
    }

    /// How the control for `call` stands here.
    pub(crate) fn phase(&self, call: &ViewCommandCall) -> Phase<'a> {
        if let Some(why) = self.unavailable(&call.command) {
            return Phase::Unavailable(why);
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

    /// What a key that names `call` does here: runs it as a press would, says why it cannot
    /// run, or nothing while it runs.
    pub(crate) fn keyed(&self, call: &ViewCommandCall, scope: &Scope<'_>) -> Keyed {
        match self.phase(call) {
            Phase::Unavailable(why) => {
                let tile = self.place.tile.clone();
                Box::new(move |state: &mut State, _: &mut Tx| {
                    state.views.refused(&tile, Some(why.clone()))
                })
            }
            Phase::Busy => Box::new(|_: &mut State, _: &mut Tx| {}),
            _ => {
                let press = self.press(call, scope, None);
                Box::new(move |state: &mut State, tx: &mut Tx| press(state, None, tx))
            }
        }
    }

    /// Runs `call` from here, with `text` when the user typed it, once the user says yes if
    /// the command asks first. `scope` fills the question. `from` is the control pressed,
    /// which the keyboard returns to after the question, and for an entry the field whose text
    /// goes once the home takes the run.
    pub(crate) fn press(
        &self,
        call: &ViewCommandCall,
        scope: &Scope<'_>,
        from: Option<&str>,
    ) -> impl Fn(&mut State, Option<String>, &mut Tx) + 'static {
        let run = ViewRunMessage {
            desktop_id: self.place.desktop.clone(),
            tile_id: self.place.tile.clone(),
            command: call.command.clone(),
            args: Some(call.args.clone()),
            part: self.part,
            rows: (!self.rows.is_empty()).then(|| self.rows.clone()),
            ..ViewRunMessage::new()
        };
        let question = (self.own(&call.command))
            .and_then(|own| own.confirm.as_ref())
            .map(|confirm| scope.text(confirm));
        let (place, yes, back) = (
            self.place.clone(),
            named(&call.command),
            from.map(str::to_owned),
        );
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
                    tx.focus("view.confirm.yes".to_owned(), true);
                }
                None => send(state, &place, (run, back.clone()), tx),
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

/// Sends `run` to the home, which keeps how it goes on the tile. Once the home takes a run
/// with typed text, the entry `from` clears if it still holds that text.
fn send(
    state: &mut State,
    place: &TileKey,
    (run, from): (ViewRunMessage, Option<String>),
    tx: &mut Tx,
) {
    let tile = place.tile.clone();
    let sent = from.zip(run.text.clone());
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
            if refused.is_none()
                && let Some((field, text)) = &sent
                && let Some(edit) = state.views.entries.get_mut(field)
                && &edit.text == text
            {
                edit.clear();
                state.views.revision += 1;
            }
            state.views.refused(&tile, refused);
        },
        tx,
    );
}

/// Closes the question and gives the keyboard back to the control that asked it.
fn end_confirming(state: &mut State, tx: &mut Tx) -> Option<Confirming> {
    let confirming = state.views.confirming.take()?;
    state.views.revision += 1;
    if let Some(back) = &confirming.back {
        tx.focus(back.clone(), true);
    }
    Some(confirming)
}

fn confirm(state: &mut State, tx: &mut Tx) {
    if let Some(Confirming {
        place, run, back, ..
    }) = end_confirming(state, tx)
    {
        send(state, &place, (run, back), tx);
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
