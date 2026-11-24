use attn_protocol::{
    KeptConversation, Seed, SeedDocument, SeedResumeMessage, SeedResumeResultMessage,
    SeedSendToChiefMessage,
};
use attn_ui::blocks::{Actions, Block, Field, Refusal, Section};
use attn_ui::system::{Look, Type, words};
use rune::{El, KeyEvent, Tx};

use super::model;
use super::sheet::{Sheet, words_field};
use super::spot::{Sends, Spot};
use super::view::{plain, quiet};
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
        let mut actions = Vec::new();
        if let Some(continuation) = &seed.continuation {
            let unreachable =
                continuation.directory_state == "unreachable" && !continuation.resume_available;
            let outpost = continuation.outpost.as_deref();
            if continuation.resume_available || (outpost.is_some() && unreachable) {
                let id = seed.id.clone();
                let (named, shown) = match outpost {
                    Some(outpost) => (
                        format!("Resume on {outpost}"),
                        format!("Resume · {outpost}"),
                    ),
                    None => ("Resume".to_owned(), "Resume".to_owned()),
                };
                let button = plain(shown);
                let button = match continuation
                    .resume_reason
                    .as_deref()
                    .filter(|_| unreachable)
                {
                    Some(reason) => button.unavailable(reason),
                    None => button,
                };
                actions.push(
                    button
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| resume(state, &id, tx))
                        .build(self.t)
                        .key(self.key("resume"))
                        .label(named)
                        .into(),
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
                plain("Send to Chief")
                    .on_press_tx(move |state: &mut State, tx: &mut Tx| opened.write(state, "", tx))
                    .build(self.t)
                    .key(chief.key("open"))
                    .into(),
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
            .map(|kept| self.small_note("head.kept", format!("conversation {}", kept_words(kept))));
        let reason = continuation
            .resume_reason
            .clone()
            .filter(|_| !resumes && continuation.directory_state != "unreachable")
            .map(|reason| self.small_note("head.resume-reason", reason));
        kept.into_iter().chain(reason).collect()
    }

    /// The note to the Chief being written, under the seed's head.
    pub(super) fn chief_form(&self, document: Option<&SeedDocument>) -> Option<Block> {
        let seed = &document?.seed;
        let spot = self.chief_spot(seed);
        let shown = self.garden.shown(&spot);
        let written = shown.words.filter(|_| self.chief)?;
        let t = self.t;
        let rev = seed.rev;
        let (typed, edited) = (spot.clone(), spot.clone());
        let label = "What should Chief know? (optional)";
        let mut form = Section::new(spot.key("form"), "Send to Chief").form().push(
            Field::new(spot.key("note"), label)
                .control(words_field(
                    written,
                    spot.field(),
                    label,
                    "",
                    move |state: &mut State| edited.words_mut(state),
                    move |state: &mut State, ev: &KeyEvent, tx: &mut Tx| {
                        let home = typed.key("open");
                        typed.field_key(
                            state,
                            ev,
                            (&home, Sends::CmdReturn),
                            |state, words, tx| send_to_chief(&typed, state, rev, words, tx),
                            tx,
                        )
                    },
                    t,
                ))
                .build(t),
        );
        if let Some(failure) = shown.failure {
            form = form.push(Block::custom(
                spot.key("error.block"),
                Refusal::new(spot.key("error"), failure.to_owned()).build(t),
            ));
        }
        let actions = if shown.busy {
            Block::custom(
                spot.key("busy"),
                words("Sending to Chief…", Type::Small, t.text_secondary)
                    .role("status")
                    .label("Sending to Chief…"),
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
                    plain("Send to Chief")
                        .look(Look::Primary)
                        .on_press_tx(move |state: &mut State, tx: &mut Tx| {
                            let words = sent.words(state);
                            send_to_chief(&sent, state, rev, words, tx)
                        })
                        .build(t)
                        .key(spot.key("send")),
                )
                .build()
        };
        Some(form.push(actions).build(t))
    }
}

fn send_to_chief(spot: &Spot, state: &mut State, expected_rev: i64, words: String, tx: &mut Tx) {
    let seed_id = spot.seed().to_owned();
    let source_session_id = super::handover::source_session(state);
    spot.send(
        state,
        |request_id| SeedSendToChiefMessage {
            seed_id,
            expected_rev,
            guidance: (!words.is_empty()).then_some(words),
            request_id: Some(request_id),
            source_session_id,
            ..SeedSendToChiefMessage::new()
        },
        spot.key("open"),
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
