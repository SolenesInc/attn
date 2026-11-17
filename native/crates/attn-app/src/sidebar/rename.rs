use attn_protocol::{DesktopRenameMessage, RenameResultMessage, RenameSessionMessage};
use attn_ui::type_;
use attn_ui::{Frame, Tokens};
use rune::tok::space;
use rune::{El, TextEdit, Tx, col, text};
use rune_kit::{Placement, TextInput};

use crate::State;
use crate::requests::Answer;

#[derive(Clone, PartialEq, Eq)]
pub enum Named {
    Session(String),
    Desktop(String),
}

impl Named {
    fn key(&self) -> &'static str {
        match self {
            Named::Session(_) => "session.rename",
            Named::Desktop(_) => "desktop.rename",
        }
    }

    fn label(&self) -> &'static str {
        match self {
            Named::Session(_) => "Rename session",
            Named::Desktop(_) => "Rename desktop",
        }
    }
}

pub struct Rename {
    pub named: Named,
    pub under: String,
    pub was: String,
    pub edit: TextEdit,
    pub default_name: Option<String>,
    pub refused: Option<String>,
    pub saving: bool,
}

pub(crate) fn session(state: &mut State, session: &str, under: String, tx: &mut Tx) {
    let Some(label) = state
        .facts
        .sessions
        .iter()
        .find(|s| s.id == session)
        .map(|s| s.label.clone())
    else {
        return;
    };
    open(
        state,
        Named::Session(session.to_owned()),
        under,
        label,
        None,
        tx,
    );
}

pub(crate) fn desktop(state: &mut State, desktop: &str, under: String, tx: &mut Tx) {
    let desktops = &state.facts.desktops;
    let Some(found) = desktops.iter().find(|d| d.id == desktop) else {
        return;
    };
    let default_name = format!("Desktop {}", super::model::desktop_number(found, desktops));
    let name = found.name.clone();
    open(
        state,
        Named::Desktop(desktop.to_owned()),
        under,
        name,
        Some(default_name),
        tx,
    );
}

fn open(
    state: &mut State,
    named: Named,
    under: String,
    was: String,
    default_name: Option<String>,
    tx: &mut Tx,
) {
    let mut edit = TextEdit::new(was.clone());
    edit.anchor = 0;
    tx.focus(format!("{}.field", named.key()), true);
    state.window.session_menu = None;
    state.window.rename = Some(Rename {
        named,
        under,
        was,
        edit,
        default_name,
        refused: None,
        saving: false,
    });
}

pub(crate) fn follow(state: &mut State) {
    let gone = state
        .window
        .rename
        .as_ref()
        .is_some_and(|r| match &r.named {
            Named::Session(id) => !state.facts.sessions.iter().any(|s| &s.id == id),
            Named::Desktop(id) => !state.facts.desktops.iter().any(|d| &d.id == id),
        });
    if gone {
        state.window.rename = None;
    }
}

fn submit(state: &mut State, tx: &mut Tx) {
    let Some(rename) = &mut state.window.rename else {
        return;
    };
    if rename.saving {
        return;
    }
    let name = rename.edit.text.trim().to_owned();
    if name == rename.was.trim() {
        state.window.rename = None;
        return;
    }
    let named = rename.named.clone();
    match &named {
        Named::Session(id) => {
            let shell = state
                .facts
                .sessions
                .iter()
                .any(|s| &s.id == id && s.agent == "shell");
            if name.is_empty() && !shell {
                rename.refused = Some(
                    "An agent name cannot be empty; only shell terminals have automatic titles"
                        .into(),
                );
                return;
            }
            rename.saving = true;
            let command = RenameSessionMessage {
                session_id: id.clone(),
                label: name,
                ..RenameSessionMessage::new()
            };
            state.ask_as(
                id.clone(),
                &command,
                move |state, answer, _| {
                    let result: Option<RenameResultMessage> = answer.parse();
                    let refused = match result {
                        Some(r) if r.success => None,
                        r => Some(
                            r.and_then(|r| r.error)
                                .filter(|e| !e.is_empty())
                                .unwrap_or_else(|| {
                                    "The daemon did not answer; the name was not saved".into()
                                }),
                        ),
                    };
                    settle(state, &named, refused);
                },
                tx,
            );
        }
        Named::Desktop(id) => {
            rename.saving = true;
            let desktop_id = id.clone();
            state.ask(
                |request_id| DesktopRenameMessage {
                    request_id,
                    desktop_id,
                    name,
                    ..DesktopRenameMessage::new()
                },
                move |state, answer: Answer, _| settle(state, &named, answer.outcome().err()),
                tx,
            );
        }
    }
}

fn settle(state: &mut State, named: &Named, refused: Option<String>) {
    let Some(rename) = state
        .window
        .rename
        .as_mut()
        .filter(|r| &r.named == named && r.saving)
    else {
        return;
    };
    match refused {
        None => state.window.rename = None,
        Some(why) => {
            rename.saving = false;
            rename.refused = Some(why);
        }
    }
}

pub(crate) fn view(state: &State, t: &Tokens) -> Option<El> {
    let rename = state.window.rename.as_ref()?;
    Some(
        rune_kit::popover::anchored(
            attn_ui::kit(t),
            Frame::menu(t).draw(popover(rename, t)),
            &rename.under,
            Placement::BottomEnd,
            4.0,
        )
        .focus_return(rename.under.clone()),
    )
}

fn popover(rename: &Rename, t: &Tokens) -> El {
    let key = rename.named.key();
    let label = rename.named.label();
    let field = TextInput::new(&rename.edit)
        .key(format!("{key}.field"))
        .plain()
        .text_style(type_::BODY.mono())
        .height(type_::BODY.mono().line_px())
        .width(None)
        .label(label)
        .on_edit(|state: &mut State| {
            &mut state
                .window
                .rename
                .as_mut()
                .expect("the rename field shows only while renaming")
                .edit
        })
        .on_submit(submit);
    let field = match &rename.default_name {
        Some(default_name) => field.placeholder(default_name.clone()),
        None => field,
    };
    col()
        .key(key)
        .role("dialog")
        .label(label)
        .w(240.0)
        .padding(10.0)
        .gap(space::S1)
        .modal_tx(true, |state: &mut State, _: &mut Tx| {
            state.window.rename = None
        })
        .child(field.build(attn_ui::kit(t)))
        .children(rename.refused.as_deref().map(|why| {
            text(why.to_owned())
                .key(format!("{key}.refused"))
                .font(type_::LABEL)
                .color(t.error)
                .wrap()
                .w_full()
        }))
}
