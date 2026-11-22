use attn_protocol::{
    DelegateAssignment, DelegateCheckout, DelegateHandover, DelegateMessage, DelegateResultMessage,
    Seed, SeedContinuation, SeedDocument,
};
use attn_ui::Tokens;
use attn_ui::blocks::{Actions, Block, Field as FieldBlock, Section};
use attn_ui::system::{FieldState, Type, line_box, words};
use rune::{El, KeyEvent, TextEdit, Tx};
use rune_kit::{Checkbox, RadioGroup, RadioItem, TextInput};

use super::sheet::{Sheet, words_field};
use super::spot::{Sends, Spot};
use super::view::{plain, quiet};
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
    agent: String,
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
    pub(super) fn suggested(continuation: Option<&SeedContinuation>) -> Handover {
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
            agent: c.agent.clone(),
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
            plain("Handover")
                .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                    let draft = Handover::suggested(Some(&continuation));
                    opened.write_with(state, "", Some(draft), tx)
                })
                .build(self.t)
                .key(spot.key("open"))
                .into(),
        )
    }

    /// The handover being written, under the seed's head.
    pub(super) fn handover_form(&self, document: Option<&SeedDocument>) -> Option<Block> {
        let seed = &document?.seed;
        let spot = spot(self, seed);
        let shown = self.garden.shown(&spot);
        let (draft, written) = (shown.handover?, shown.words?);
        let t = self.t;
        let kit = attn_ui::kit(t);
        let field = Field { spot: &spot, t };
        let mut form = Section::new(spot.key("form"), "Handover")
            .form()
            .push(field.note(written))
            .push(field.line("cwd", "Working folder", "", &draft.cwd, |h| &mut h.cwd));
        let mut choices = RadioGroup::new().label("Git checkout");
        for choice in Checkout::ALL {
            choices = choices.item(
                RadioItem::new(choice.label(), draft.checkout == choice).on_select({
                    let spot = spot.clone();
                    move |state: &mut State| {
                        if let Some(draft) = spot.handover_mut(state) {
                            draft.checkout = choice;
                        }
                        state.window.garden.revision += 1;
                    }
                }),
            );
        }
        form = form.push(
            FieldBlock::new(spot.key("checkout-field"), "Git checkout")
                .control(choices.build(kit).key(spot.key("checkout")))
                .build(t),
        );
        if draft.checkout != Checkout::None {
            form = form.push(field.line("branch", "Branch", "", &draft.branch, |h| &mut h.branch));
        }
        if draft.checkout == Checkout::NewWorktree {
            form = form.push(field.line(
                "from",
                "Start from",
                "origin/main or a commit",
                &draft.from,
                |h| &mut h.from,
            ));
        }
        if draft.checkout.worktree() {
            form =
                form.push(
                    field.line("path", "Worktree path (optional)", "", &draft.path, |h| {
                        &mut h.path
                    }),
                );
        }
        if draft.checkout != Checkout::None {
            form = form.push(Block::control(
                spot.key("share.block"),
                Checkbox::new(draft.share)
                    .label("Allow sharing an occupied checkout")
                    .on_toggle({
                        let spot = spot.clone();
                        move |state: &mut State| {
                            if let Some(draft) = spot.handover_mut(state) {
                                draft.share = !draft.share;
                            }
                            state.window.garden.revision += 1;
                        }
                    })
                    .build(kit)
                    .key(spot.key("share")),
            ));
        }
        let harnesses = self.harnesses.iter().filter(|h| h.offered).map(|harness| {
            let id = harness.id.clone();
            let spot = spot.clone();
            attn_ui::system::Segment::new(spot.key(&format!("agent.{id}")), harness.name.clone())
                .chosen(draft.agent == harness.id)
                .on_press_tx(move |state: &mut State, _: &mut Tx| {
                    if let Some(draft) = spot.handover_mut(state) {
                        draft.agent = id.clone();
                    }
                    state.window.garden.revision += 1;
                })
        });
        form = form.push(
            FieldBlock::new(spot.key("agent-field"), "Harness")
                .control(
                    attn_ui::system::Segmented::new(spot.key("agent"), "Harness")
                        .options(harnesses)
                        .build(t),
                )
                .build(t),
        );
        if let Some(failure) = shown.failure {
            form = form.push(Block::custom(
                spot.key("error.block"),
                attn_ui::blocks::Refusal::new(spot.key("error"), failure.to_owned()).build(t),
            ));
        }
        let actions = if shown.busy {
            Block::custom(
                spot.key("busy"),
                words("Handing over…", Type::Small, t.text_secondary)
                    .role("status")
                    .label("Handing over…"),
            )
        } else {
            let (cancelled, sent) = (spot.clone(), spot.clone());
            Actions::new(spot.key("actions"))
                .push(
                    quiet("Cancel")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            cancelled.put_away(state, &cancelled.key("open"), tx)
                        })
                        .build(t)
                        .key(spot.key("cancel")),
                )
                .push(
                    plain("Handover")
                        .look(attn_ui::system::Look::Primary)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            hand_over(&sent, state, tx)
                        })
                        .build(t)
                        .key(spot.key("send")),
                )
                .build()
        };
        Some(form.push(actions).build(t))
    }
}

/// The handover form's text fields, each sending the handover on its chord.
struct Field<'a> {
    spot: &'a Spot,
    t: &'a Tokens,
}

impl Field<'_> {
    fn note(&self, written: &TextEdit) -> Block {
        let (typed, edited) = (self.spot.clone(), self.spot.clone());
        let label = "What should the new agent know? (optional)";
        let area = words_field(
            written,
            self.spot.field(),
            label,
            "",
            move |state: &mut State| edited.words_mut(state),
            move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                keyed(&typed, state, ev, Sends::CmdReturn, tx)
            },
            self.t,
        );
        FieldBlock::new(self.spot.key("note"), label)
            .control(area)
            .build(self.t)
    }

    fn line(
        &self,
        part: &str,
        label: &str,
        placeholder: &str,
        edit: &TextEdit,
        pick: fn(&mut Handover) -> &mut TextEdit,
    ) -> Block {
        let (typed, edited) = (self.spot.clone(), self.spot.clone());
        let input = TextInput::new(edit)
            .key(self.spot.key(part))
            .label(label.to_owned())
            .placeholder(placeholder.to_owned())
            .on_edit(move |state: &mut State| pick(edited.handover_draft(state)))
            .on_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                keyed(&typed, state, ev, Sends::Return, tx)
            });
        FieldBlock::new(self.spot.key(&format!("{part}-field")), label.to_owned())
            .control(line_box(input, FieldState::Ready, self.t))
            .build(self.t)
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
pub(super) fn source_session(state: &State) -> Option<String> {
    let holder = state.window.keyboard.as_ref()?;
    let desktop = state
        .facts
        .desktops
        .iter()
        .find(|d| d.id == holder.desktop)?;
    crate::desktop::session_of_leaf(desktop, &holder.pane)
}

fn hand_over(spot: &Spot, state: &mut State, tx: &mut Tx) {
    let Some(draft) = spot.handover_mut(state) else {
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
    } else if draft.agent.is_empty() {
        Some("Harness")
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
    let agent = Some(draft.agent.clone());
    let share = draft.share.then_some(true);
    let request = draft.request.clone();
    let note = Some(spot.words(state)).filter(|n| !n.is_empty());
    let source_session_id = source_session(state);
    let review = super::review::receipt(spot, state);
    let seed_id = spot.seed().to_owned();
    let id = request.unwrap_or_else(|| state.next_request_id());
    if let Some(draft) = spot.handover_mut(state) {
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
            review,
            ..DelegateMessage::new()
        },
        spot.key("open"),
        {
            let spot = spot.clone();
            move |state, answer, tx| settled(&spot, state, answer, tx)
        },
        tx,
    );
}

/// Goes to the agent a handover started. A refusal takes a new request for the next try; a
/// handover the daemon never answered keeps its request, so it can tell a retry from another.
fn settled(spot: &Spot, state: &mut State, answer: &Answer, tx: &mut Tx) {
    super::review::settled(spot, state, tx);
    match answer {
        Answer::Reply(reply) if reply.body["success"] == true => {
            spot.forget(state);
            let session = answer
                .parse::<DelegateResultMessage>()
                .and_then(|r| r.result)
                .map(|r| r.session_id);
            if let Some(session) = session {
                crate::sidebar::open_agent(state, &session, tx);
            }
        }
        Answer::Reply(_) => {
            if let Some(draft) = spot.handover_mut(state) {
                draft.request = None;
            }
        }
        Answer::NotSent | Answer::Dropped => {}
    }
}
