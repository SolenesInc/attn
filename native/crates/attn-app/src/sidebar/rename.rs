use attn_protocol::{DesktopRenameMessage, RenameResultMessage, RenameSessionMessage};
use attn_ui::type_;
use attn_ui::{Frame, Tokens};
use rune::{El, TextEdit, Tx, col, row, text};
use rune_kit::{Button, ButtonVariant, Placement, TextInput};

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

/// What opened the popover, which it opens beside.
#[derive(Clone, Debug, PartialEq)]
pub enum Opener {
    /// A control keyed so, which the popover hangs under, its right edges lined up.
    Control(String),
    /// A command about the agent in the tile whose header is keyed so: the popover opens at
    /// the top left corner of the tile's body, as the Tauri app opens it over the terminal the
    /// command came from.
    Tile(String),
}

impl Opener {
    fn key(&self) -> &str {
        match self {
            Opener::Control(key) | Opener::Tile(key) => key,
        }
    }
}

pub struct Rename {
    pub named: Named,
    pub under: Opener,
    pub was: String,
    pub edit: TextEdit,
    pub default_name: Option<String>,
    pub refused: Option<String>,
    pub saving: bool,
}

pub(crate) fn session(state: &mut State, session: &str, under: Opener, tx: &mut Tx) {
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

pub(crate) fn desktop(state: &mut State, desktop: &str, under: Opener, tx: &mut Tx) {
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
    under: Opener,
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
            rename.under.key(),
            match rename.under {
                Opener::Control(_) => Placement::BottomEnd,
                Opener::Tile(_) => Placement::BottomStart,
            },
            match rename.under {
                Opener::Control(_) => 4.0,
                Opener::Tile(_) => 0.0,
            },
        )
        .focus_return(rename.under.key().to_owned()),
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
    let edge = if rename.edit.focused {
        t.accent
    } else {
        t.border
    };
    let framed = row()
        .w_full()
        .px(8.0)
        .py(6.0)
        .radius(6.0)
        .fill(t.bg_input)
        .border(1.0, edge)
        .child(field.build(attn_ui::kit(t)).grow(1.0));
    col()
        .key(key)
        .role("dialog")
        .label(label)
        .w(240.0)
        .padding(10.0)
        .modal_tx(true, |state: &mut State, _: &mut Tx| {
            state.window.rename = None
        })
        .child(framed)
        .children(rename.refused.as_deref().map(|why| {
            text(why.to_owned())
                .key(format!("{key}.refused"))
                .font(type_::LABEL)
                .color(t.error)
                .wrap()
                .w_full()
                .mt(6.0)
        }))
        .child(
            row()
                .w_full()
                .justify_end()
                .gap(6.0)
                .mt(8.0)
                .child(action(
                    key,
                    "cancel",
                    "Cancel",
                    false,
                    rename.saving,
                    t,
                    cancel,
                ))
                .child(action(key, "save", "Save", true, rename.saving, t, submit)),
        )
}

fn cancel(state: &mut State, _: &mut Tx) {
    state.window.rename = None;
}

/// `.rename-popover-btn`: a bordered monospace button, filled with the accent for Save.
fn action(
    key: &str,
    part: &str,
    title: &str,
    save: bool,
    saving: bool,
    t: &Tokens,
    press: fn(&mut State, &mut Tx),
) -> El {
    let (fill, edge, ink) = if save {
        (t.accent, t.accent, t.on_brand)
    } else {
        (rune::Color::TRANSPARENT, t.border, t.text_muted)
    };
    let face = title.to_owned();
    Button::new(title)
        .variant(ButtonVariant::Ghost)
        .disabled(saving)
        .on_press_tx(press)
        .label_with(move |_, _| text(face).font(type_::MONO_CAPTION).color(ink))
        .build(attn_ui::kit(t))
        .key(format!("{key}.{part}"))
        .h(type_::MONO_CAPTION.line_px() + 10.0)
        .px(12.0)
        .radius(6.0)
        .fill(fill)
        .border(1.0, edge)
}
