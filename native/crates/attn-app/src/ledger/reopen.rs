//! Asking the daemon to reopen a closed session, and what it answers.

use attn_protocol::{SessionReopenMessage, SessionReopenResultMessage};
use rune::Tx;

use super::answer_error;
use super::query::{self, Verdict};
use crate::State;

/// What came of asking to reopen a closed session.
pub(crate) enum Reopened {
    /// The session is on a desktop, where its agent can be opened.
    Shown(String),
    /// It stays closed, with why and what the daemon would take instead.
    Refused {
        why: String,
        verdict: Option<Verdict>,
    },
}

/// Asks to reopen `session` with `action`, then gives `done` what came of it. A failed reopen of
/// a session a desktop already holds says why in a notification and counts as shown.
pub(crate) fn ask(
    state: &mut State,
    session: String,
    action: String,
    done: impl FnOnce(&mut State, Reopened, &mut Tx) + 'static,
    tx: &mut Tx,
) {
    let (session_id, asked) = (session.clone(), action.clone());
    state.ask(
        |request_id| SessionReopenMessage {
            request_id: Some(request_id),
            session_id,
            action: Some(asked),
            ..SessionReopenMessage::new()
        },
        move |state, answer, tx| {
            let reply = answer.parse::<SessionReopenResultMessage>();
            if let Some(result) = reply
                .as_ref()
                .filter(|r| r.success)
                .and_then(|r| r.result.clone())
            {
                done(state, Reopened::Shown(result.session_id), tx);
                return;
            }
            if let Some(offer) = reply.as_ref().and_then(|r| r.reopen.as_ref()) {
                let verdict = Verdict::of(offer);
                let refused = Reopened::Refused {
                    why: verdict.refusal(&action),
                    verdict: Some(verdict),
                };
                done(state, refused, tx);
                return;
            }
            let why = answer_error(&answer, "Reopening that session failed");
            let held = state
                .facts
                .desktops
                .iter()
                .any(|d| d.panes.iter().any(|p| p.session_id == session));
            if held {
                state.window.notify(why, tx);
                done(state, Reopened::Shown(session), tx);
                return;
            }
            let refused = Reopened::Refused {
                why: query::compact_paths(&why),
                verdict: None,
            };
            done(state, refused, tx);
        },
        tx,
    );
}
