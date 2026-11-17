use attn_protocol::{
    KeptConversation, Seed, SeedDocument, SeedResumeMessage, SeedResumeResultMessage,
    SeedSendToChiefMessage,
};
use attn_ui::type_;
use rune::tok::space;
use rune::{El, KeyEvent, Tx, col, row, text, text_input};

use super::model;
use super::sheet::Sheet;
use super::spot::{Sends, Spot};
use super::view::kit_button;
use crate::State;
use crate::requests::Refused;

fn kept_words(kept: &KeptConversation) -> String {
    use crate::ledger::{amount, date};
    if let Some(deleted) = &kept.deleted_at {
        let who = match kept.deleted_by.as_deref() {
            Some("user") => "you deleted attn’s copy",
            _ => "attn deleted its copy",
        };
        return format!("{who} on {}", date(deleted));
    }
    let size = format!("kept by attn ({})", amount(kept.bytes));
    match (&kept.pinned_at, &kept.delete_after) {
        (Some(_), _) => format!("{size} forever; pinned"),
        (None, Some(after)) => format!("{size} until {}; replant to keep it", date(after)),
        (None, None) => format!("{size} while an open seed points at it"),
    }
}

impl Sheet<'_> {
    fn chief_spot(&self, here: &Seed) -> Spot {
        Spot::new(&self.prefix, &here.id, "chief")
    }

    /// Resume, Handover and Send to Chief, while the seed is open and the daemon's reading of it is the
    /// seed the garden holds: Resume when the seed's agent can come back, naming the outpost it
    /// runs on, Handover when its last agent left a continuation, and Send to Chief while the
    /// profile has a Chief; neither of the last two while a note to the next agent or the Chief
    /// is being written.
    pub(super) fn continuation(
        &self,
        held: Option<&Seed>,
        document: Option<&SeedDocument>,
    ) -> Vec<El> {
        let Some(seed) = document.map(|d| &d.seed) else {
            return Vec::new();
        };
        if held.is_some_and(|h| h.rev != seed.rev) || model::is_closed(seed) {
            return Vec::new();
        }
        let kit = attn_ui::kit(self.t);
        let mut actions = Vec::new();
        if let Some(continuation) = &seed.continuation {
            let unreachable =
                continuation.directory_state == "unreachable" && !continuation.resume_available;
            let outpost = continuation.outpost.as_deref();
            if continuation.resume_available || (outpost.is_some() && unreachable) {
                let id = seed.id.clone();
                let button = match outpost {
                    Some(outpost) => kit_button(format!("Resume on {outpost}")).label_with({
                        let words = format!("Resume · {outpost}");
                        move |font, ink| text(words).font(font).color(ink)
                    }),
                    None => kit_button("Resume"),
                };
                let button = match continuation
                    .resume_reason
                    .as_deref()
                    .filter(|_| unreachable)
                {
                    Some(reason) => button.disabled(true).help(reason),
                    None => button,
                };
                actions.push(
                    button
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| resume(state, &id, tx))
                        .build(kit)
                        .key(self.key("resume")),
                );
            }
        }
        if self.composing(seed) {
            return actions;
        }
        actions.extend(self.handover_button(seed));
        let chief = self.chief_spot(seed);
        if self.chief {
            let opened = chief.clone();
            actions.push(
                kit_button("Send to Chief")
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| opened.write(state, "", tx))
                    .build(kit)
                    .key(chief.key("open")),
            );
        }
        actions
    }

    /// What became of the conversation attn keeps for the seed's agent, and why Resume is not
    /// offered, while the daemon's reading answers the garden as last pushed.
    pub(super) fn continuation_notes(
        &self,
        held: &Seed,
        document: Option<&SeedDocument>,
    ) -> Vec<El> {
        let Some(seed) = document
            .map(|d| &d.seed)
            .filter(|s| s.rev == held.rev && super::answered(self.garden, &s.id))
        else {
            return Vec::new();
        };
        let Some(continuation) = &seed.continuation else {
            return Vec::new();
        };
        let resumes = continuation.resume_available && !model::is_closed(seed);
        let kept = continuation
            .kept_conversation
            .as_ref()
            .map(|kept| self.note("head.kept", format!("conversation {}", kept_words(kept))));
        let reason = continuation
            .resume_reason
            .clone()
            .filter(|_| !resumes && continuation.directory_state != "unreachable")
            .map(|reason| self.note("head.resume-reason", reason));
        kept.into_iter().chain(reason).collect()
    }

    /// The note to the Chief being written, under the seed's head.
    pub(super) fn chief_form(&self, document: Option<&SeedDocument>) -> Option<El> {
        let seed = &document?.seed;
        let spot = self.chief_spot(seed);
        let shown = self.garden.shown(&spot);
        let written = shown.words.filter(|_| self.chief)?;
        let t = self.t;
        let kit = attn_ui::kit(t);
        let rev = seed.rev;
        let (typed, edited) = (spot.clone(), spot.clone());
        let mut form = col()
            .key(spot.key("form"))
            .role("form")
            .label("Send to Chief")
            .w_full()
            .gap(space::S1)
            .padding(space::S2)
            .radius(6.0)
            .fill(t.bg_elevated)
            .border(1.0, t.border)
            .child(
                text("What should Chief know? (optional)")
                    .font(type_::LABEL)
                    .color(t.text_secondary)
                    .a11y_hidden(),
            )
            .child(
                text_input(written, type_::BODY)
                    .key(spot.field())
                    .return_breaks_line()
                    .label("What should Chief know? (optional)")
                    .on_edit(move |state: &mut State| edited.words_mut(state))
                    .on_input_key(move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                        let home = typed.key("open");
                        typed.field_key(
                            state,
                            ev,
                            (&home, Sends::CmdReturn),
                            |state, words, tx| send_to_chief(&typed, state, rev, words, tx),
                            tx,
                        )
                    })
                    .w_full()
                    .min_h(56.0)
                    .color(t.text_primary)
                    .fill(t.bg_input)
                    .border(1.0, t.border_focus)
                    .radius(4.0)
                    .padding(space::S1),
            );
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
                text("Sending to Chief…")
                    .role("status")
                    .label("Sending to Chief…")
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
                    kit_button("Send to Chief")
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            let words = sent.words(state);
                            send_to_chief(&sent, state, rev, words, tx)
                        })
                        .build(kit)
                        .key(spot.key("send")),
                )
        };
        Some(form.child(actions))
    }
}

fn send_to_chief(spot: &Spot, state: &mut State, expected_rev: i64, words: String, tx: &mut Tx) {
    let seed_id = spot.seed().to_owned();
    let review = super::review::receipt(spot, state);
    let source_session_id = super::handover::source_session(state);
    spot.send_as(
        state,
        None,
        |request_id| SeedSendToChiefMessage {
            seed_id,
            expected_rev,
            guidance: (!words.is_empty()).then_some(words),
            request_id: Some(request_id),
            review,
            source_session_id,
            ..SeedSendToChiefMessage::new()
        },
        spot.key("open"),
        {
            let spot = spot.clone();
            move |state, _, tx| super::review::settled(&spot, state, tx)
        },
        tx,
    );
}

/// Asks the daemon to bring the seed's agent back, and goes to the session it answers with.
fn resume(state: &mut State, id: &str, tx: &mut Tx) {
    let seed_id = id.to_owned();
    state.act(
        |request_id| SeedResumeMessage {
            seed_id,
            request_id: Some(request_id),
            ..SeedResumeMessage::new()
        },
        Refused::Bare,
        |state, reply, tx| {
            let session = reply
                .parse::<SeedResumeResultMessage>()
                .and_then(|r| r.session_id);
            if let Some(session) = session {
                crate::sidebar::open_agent(state, &session, tx);
            }
        },
        tx,
    );
}
