use attn_protocol::{
    DelegateAssignment, DelegateCheckout, DelegateHandover, DelegateMessage, DelegateResultMessage,
    Seed, SeedContinuation, SeedDocument,
};
use attn_ui::Tokens;
use rune::tok::{space, type_};
use rune::{El, KeyEvent, TextEdit, Tx, col, row, text, text_input};
use rune_kit::{Checkbox, RadioGroup, RadioItem};

use super::sheet::Sheet;
use super::spot::{Sends, Spot};
use super::view::kit_button;
use crate::State;
use crate::requests::Answer;

/// Where and with what a handover starts the seed's next agent, as the user is writing it.
#[derive(Default)]
pub(crate) struct Handover {
    request: Option<String>,
    checkout: Checkout,
    cwd: TextEdit,
    branch: TextEdit,
    from: TextEdit,
    path: TextEdit,
    agent: TextEdit,
    share: bool,
}

#[derive(Clone, Copy, Default, PartialEq)]
enum Checkout {
    #[default]
    None,
    Reuse,
    NewWorktree,
    ExistingBranch,
}

impl Checkout {
    const ALL: [Checkout; 4] = [
        Checkout::None,
        Checkout::Reuse,
        Checkout::NewWorktree,
        Checkout::ExistingBranch,
    ];

    fn label(self) -> &'static str {
        match self {
            Checkout::None => "Not a Git folder",
            Checkout::Reuse => "Reuse this checkout",
            Checkout::NewWorktree => "Create a new branch worktree",
            Checkout::ExistingBranch => "Create a worktree for an existing branch",
        }
    }

    fn wire(self) -> Option<&'static str> {
        match self {
            Checkout::None => None,
            Checkout::Reuse => Some("reuse"),
            Checkout::NewWorktree => Some("new_worktree"),
            Checkout::ExistingBranch => Some("existing_branch_worktree"),
        }
    }

    fn worktree(self) -> bool {
        matches!(self, Checkout::NewWorktree | Checkout::ExistingBranch)
    }
}

impl Handover {
    /// The draft the seed's last agent suggests: the branch it worked on recreated in its
    /// repository, or its folder reused, under the agent it ran.
    fn suggested(continuation: Option<&SeedContinuation>) -> Handover {
        let Some(c) = continuation else {
            return Handover::default();
        };
        let recreate = c.handover_placement == "recreate_branch";
        let reuse = c.handover_placement == "reuse_cwd";
        let root = c
            .repository_root
            .as_deref()
            .map(|r| r.trim_end_matches('/'));
        let recreated = match (root, c.repository_subdir.as_deref()) {
            (Some(root), Some(subdir)) => format!("{root}/{subdir}"),
            (Some(root), None) => root.to_owned(),
            (None, _) => c.cwd.clone(),
        };
        let cwd = if recreate {
            recreated
        } else if reuse {
            c.cwd.clone()
        } else {
            String::new()
        };
        Handover {
            checkout: if recreate {
                Checkout::ExistingBranch
            } else if reuse && c.repository_root.is_some() {
                Checkout::Reuse
            } else {
                Checkout::None
            },
            cwd: TextEdit::new(cwd),
            branch: TextEdit::new(c.branch.clone().unwrap_or_default()),
            agent: TextEdit::new(c.agent.clone()),
            ..Handover::default()
        }
    }
}

fn spot(sheet: &Sheet, seed: &Seed) -> Spot {
    Spot::new(&sheet.prefix, &seed.id, "handover")
}

impl Sheet<'_> {
    /// Whether a note to the next agent or to the Chief is being written for `seed`.
    pub(super) fn composing(&self, seed: &Seed) -> bool {
        self.garden.shown(&spot(self, seed)).writing
            || self
                .garden
                .shown(&Spot::new(&self.prefix, &seed.id, "chief"))
                .writing
    }

    /// Handover, for an open seed whose last agent left a continuation.
    pub(super) fn handover_button(&self, seed: &Seed) -> Option<El> {
        let continuation = seed.continuation.clone()?;
        let spot = spot(self, seed);
        let opened = spot.clone();
        Some(
            kit_button("Handover")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    opened.write(state, "", tx);
                    if state.window.garden.shown(&opened).writing {
                        state.window.garden.handover =
                            Some(Handover::suggested(Some(&continuation)));
                    }
                })
                .build(attn_ui::kit(self.t))
                .key(spot.key("open")),
        )
    }

    /// The handover being written, under the seed's head.
    pub(super) fn handover_form(&self, document: Option<&SeedDocument>) -> Option<El> {
        let seed = &document?.seed;
        let spot = spot(self, seed);
        let shown = self.garden.shown(&spot);
        let draft = self.garden.handover.as_ref().filter(|_| shown.writing)?;
        let t = self.t;
        let kit = attn_ui::kit(t);
        let field = Field { spot: &spot, t };
        let mut form = col()
            .key(spot.key("form"))
            .role("form")
            .label("Handover")
            .w_full()
            .gap(space::S1)
            .padding(space::S2)
            .radius(6.0)
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .child(field.note(&self.garden.words))
            .child(field.line("cwd", "Working folder", "", &draft.cwd, |h| &mut h.cwd));
        let mut choices = RadioGroup::new().label("Git checkout");
        for choice in Checkout::ALL {
            choices = choices.item(
                RadioItem::new(choice.label(), draft.checkout == choice).on_select(
                    move |state: &mut State| {
                        let garden = &mut state.window.garden;
                        if let Some(draft) = garden.handover.as_mut() {
                            draft.checkout = choice;
                        }
                        garden.revision += 1;
                    },
                ),
            );
        }
        form = form.child(choices.build(kit).key(spot.key("checkout")));
        if draft.checkout != Checkout::None {
            form = form.child(field.line("branch", "Branch", "", &draft.branch, |h| &mut h.branch));
        }
        if draft.checkout == Checkout::NewWorktree {
            form = form.child(field.line(
                "from",
                "Start from",
                "origin/main or a commit",
                &draft.from,
                |h| &mut h.from,
            ));
        }
        if draft.checkout.worktree() {
            form =
                form.child(
                    field.line("path", "Worktree path (optional)", "", &draft.path, |h| {
                        &mut h.path
                    }),
                );
        }
        if draft.checkout != Checkout::None {
            form = form.child(
                Checkbox::new(draft.share)
                    .label("Allow sharing an occupied checkout")
                    .on_toggle(|state: &mut State| {
                        let garden = &mut state.window.garden;
                        if let Some(draft) = garden.handover.as_mut() {
                            draft.share = !draft.share;
                        }
                        garden.revision += 1;
                    })
                    .build(kit)
                    .key(spot.key("share")),
            );
        }
        form = form.child(field.line("agent", "Agent", "codex", &draft.agent, |h| &mut h.agent));
        if let Some(failure) = shown.failure {
            form = form.child(
                text(failure.to_owned())
                    .key(spot.key("error"))
                    .role("alert")
                    .label(failure.to_owned())
                    .font(type_::LABEL)
                    .color(t.error)
                    .wrap(),
            );
        }
        let actions = if shown.busy {
            row().child(
                text("Handing over…")
                    .role("status")
                    .label("Handing over…")
                    .font(type_::LABEL)
                    .color(t.text_secondary),
            )
        } else {
            let (cancelled, sent) = (spot.clone(), spot.clone());
            row()
                .gap(space::S1)
                .child(
                    kit_button("Cancel")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            cancelled.put_away(state, &cancelled.key("open"), tx)
                        })
                        .build(kit)
                        .key(spot.key("cancel")),
                )
                .child(
                    kit_button("Handover")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            hand_over(&sent, state, tx)
                        })
                        .build(kit)
                        .key(spot.key("send")),
                )
        };
        Some(form.child(actions))
    }
}

/// The handover form's text fields, each sending the handover on its chord.
struct Field<'a> {
    spot: &'a Spot,
    t: &'a Tokens,
}

impl Field<'_> {
    fn note(&self, words: &TextEdit) -> El {
        let typed = self.spot.clone();
        self.boxed(
            text_input(words, type_::BODY)
                .key(self.spot.field())
                .label("What should the new agent know? (optional)")
                .on_edit(|state: &mut State| {
                    let garden = &mut state.window.garden;
                    garden.revision += 1;
                    &mut garden.words
                })
                .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                    keyed(&typed, state, ev, Sends::CmdReturn, tx)
                })
                .min_h(56.0),
        )
    }

    fn line(
        &self,
        part: &str,
        label: &str,
        placeholder: &str,
        edit: &TextEdit,
        pick: fn(&mut Handover) -> &mut TextEdit,
    ) -> El {
        let typed = self.spot.clone();
        self.boxed(
            text_input(edit, type_::BODY)
                .key(self.spot.key(part))
                .single_line()
                .label(label.to_owned())
                .placeholder(placeholder.to_owned(), self.t.text_muted)
                .on_edit(move |state: &mut State| {
                    let garden = &mut state.window.garden;
                    garden.revision += 1;
                    pick(garden.handover.get_or_insert_with(Handover::default))
                })
                .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                    keyed(&typed, state, ev, Sends::Return, tx)
                }),
        )
    }

    fn boxed(&self, input: El) -> El {
        input
            .w_full()
            .color(self.t.text_primary)
            .fill(self.t.bg_input)
            .border(1.0, self.t.border_focus)
            .radius(4.0)
            .padding(space::S1)
    }
}

fn keyed(spot: &Spot, state: &mut State, ev: &KeyEvent, sends: Sends, tx: &mut Tx) -> bool {
    let home = spot.key("open");
    spot.field_key(
        state,
        ev,
        (&home, sends),
        |state, _, tx| hand_over(spot, state, tx),
        tx,
    )
}

/// The session the user works in, which the new agent is handed over from.
fn source_session(state: &State) -> Option<String> {
    let holder = state.window.keyboard.as_ref()?;
    let desktop = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == holder.desktop)?;
    crate::desktop::session_of_leaf(desktop, &holder.pane)
}

fn hand_over(spot: &Spot, state: &mut State, tx: &mut Tx) {
    let Some(draft) = state.window.garden.handover.as_ref() else {
        return;
    };
    let words = |edit: &TextEdit| edit.text.trim().to_owned();
    let (cwd, branch, from) = (words(&draft.cwd), words(&draft.branch), words(&draft.from));
    let missing = if cwd.is_empty() {
        Some("Working folder")
    } else if draft.checkout != Checkout::None && branch.is_empty() {
        Some("Branch")
    } else if draft.checkout == Checkout::NewWorktree && from.is_empty() {
        Some("Start from")
    } else {
        None
    };
    if let Some(missing) = missing {
        spot.fail(state, format!("{missing} is required."));
        return;
    }
    let path = words(&draft.path);
    let checkout = draft.checkout.wire().map(|kind| DelegateCheckout {
        kind: kind.into(),
        branch,
        from: (draft.checkout == Checkout::NewWorktree).then_some(from),
        path: (draft.checkout.worktree() && !path.is_empty()).then_some(path),
    });
    let agent = Some(words(&draft.agent)).filter(|a| !a.is_empty());
    let share = draft.share.then_some(true);
    let request = draft.request.clone();
    let note = Some(state.window.garden.words.text.trim().to_owned()).filter(|n| !n.is_empty());
    let source_session_id = source_session(state);
    let seed_id = spot.seed().to_owned();
    let id = request.unwrap_or_else(|| state.next_request_id());
    if let Some(draft) = state.window.garden.handover.as_mut() {
        draft.request = Some(id.clone());
    }
    spot.send_as(
        state,
        Some(id),
        |request_id| DelegateMessage {
            request_id,
            source_session_id,
            assignment: DelegateAssignment {
                kind: "seed".into(),
                seed_id: Some(seed_id),
                handover: Some(DelegateHandover { note }),
                ..DelegateAssignment::default()
            },
            cwd,
            checkout,
            allow_worktree_reuse: share,
            agent,
            ..DelegateMessage::new()
        },
        spot.key("open"),
        settled,
        tx,
    );
}

/// Goes to the agent a handover started. A refusal takes a new request for the next try; a
/// handover the daemon never answered keeps its request, so it can tell a retry from another.
fn settled(state: &mut State, answer: &Answer, tx: &mut Tx) {
    match answer {
        Answer::Reply(reply) if reply.body["success"] == true => {
            let garden = &mut state.window.garden;
            garden.handover = None;
            garden.edit = None;
            garden.revision += 1;
            let session = answer
                .parse::<DelegateResultMessage>()
                .and_then(|r| r.result)
                .map(|r| r.session_id);
            if let Some(session) = session {
                crate::sidebar::open_agent(state, &session, tx);
            }
        }
        Answer::Reply(_) => {
            if let Some(draft) = state.window.garden.handover.as_mut() {
                draft.request = None;
            }
        }
        Answer::NotSent | Answer::Dropped => {}
    }
}
