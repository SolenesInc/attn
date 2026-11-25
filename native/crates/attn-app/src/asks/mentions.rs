//! The seeds an ask names by id or slug, which the sheet turns into links that open them.

use std::ops::Range;
use std::sync::Arc;

use attn_client::Garden;
use attn_protocol::{AskSeedMention, AskSeedMentionsMessage, AskSeedMentionsResultMessage};
use rune::Tx;

use crate::State;
use crate::markdown::ids;
use crate::requests::Answer;
use crate::terminal::{Known, Subject};

/// What the daemon says the shown ask names, as of the garden it was asked under; none when
/// the question never reached the daemon, so the next follow asks again.
pub(crate) struct Mentions {
    ask: String,
    garden: Option<Arc<Garden>>,
    names: Vec<AskSeedMention>,
}

/// Asks the daemon which seeds `shown` names when the sheet moves to it or the garden changes,
/// keeping the links it shows until the answer arrives.
pub(crate) fn follow(state: &mut State, shown: Option<&str>, tx: &mut Tx) {
    let Some(ask) = shown else {
        return;
    };
    let garden = state.facts.garden.clone();
    let mentions = &mut state.window.asks.mentions;
    match mentions {
        Some(m) if m.ask == ask && asked_under(m, &garden) => return,
        Some(m) if m.ask == ask => m.garden = Some(garden.clone()),
        _ => {
            *mentions = Some(Mentions {
                ask: ask.to_owned(),
                garden: Some(garden.clone()),
                names: Vec::new(),
            })
        }
    }
    let asked = ask.to_owned();
    let ask_id = asked.clone();
    state.ask(
        |id| AskSeedMentionsMessage {
            ask_id,
            request_id: id,
            ..AskSeedMentionsMessage::new()
        },
        move |state, answer: Answer, _| {
            let Some(mentions) = state
                .window
                .asks
                .mentions
                .as_mut()
                .filter(|m| m.ask == asked && asked_under(m, &garden))
            else {
                return;
            };
            let Answer::Reply(reply) = &answer else {
                mentions.garden = None;
                return;
            };
            match reply.parse::<AskSeedMentionsResultMessage>() {
                Some(result) if result.success => mentions.names = result.mentions,
                _ => {
                    if let Err(why) = answer.outcome() {
                        state.window.asks.error = Some((asked, why));
                    }
                }
            }
        },
        tx,
    );
}

fn asked_under(mentions: &Mentions, garden: &Arc<Garden>) -> bool {
    mentions
        .garden
        .as_ref()
        .is_some_and(|g| Arc::ptr_eq(g, garden))
}

/// The seeds the sheet links in `ask`.
pub(crate) fn of<'a>(state: &'a State, ask: &str) -> &'a [AskSeedMention] {
    state
        .window
        .asks
        .mentions
        .as_ref()
        .filter(|m| m.ask == ask)
        .map_or(&[], |m| m.names.as_slice())
}

/// The links in a run of an ask's prose, as `Markdown::text_links` takes them, to the seeds of
/// `names` and the sessions of `ids`: the run's text outside code and links, so a seed named
/// inside a URL leaves the URL one link.
pub(crate) fn links(
    run: &str,
    names: &[AskSeedMention],
    ids: &Known,
) -> Vec<(Range<usize>, String)> {
    let seeds = found(run, names).into_iter();
    let seeds = seeds.map(|(at, seed)| (at, Subject::Seed(seed.to_owned())));
    let mut links: Vec<_> = seeds.collect();
    for (at, target) in ids.in_text(run) {
        let free = links
            .iter()
            .all(|(taken, _)| taken.end <= at.start || at.end <= taken.start);
        if free && matches!(target, Subject::Session(_)) {
            links.push((at, target));
        }
    }
    links.sort_by_key(|(at, _)| at.start);
    let hrefs = links
        .into_iter()
        .map(|(at, target)| (at, ids::href(&target)));
    hrefs.collect()
}

/// Where `text` names a seed: a name the daemon resolved, standing on its own, the longest
/// name first where two start at the same place.
pub(crate) fn found<'a>(text: &str, names: &'a [AskSeedMention]) -> Vec<(Range<usize>, &'a str)> {
    let mut longest: Vec<&AskSeedMention> = names.iter().filter(|m| !m.text.is_empty()).collect();
    longest.sort_by_key(|m| std::cmp::Reverse(m.text.len()));
    let bytes = text.as_bytes();
    let mut found = Vec::new();
    let mut at = 0;
    while at < text.len() {
        let starts_word = at == 0 || !is_name_byte(bytes[at - 1]);
        let named = (starts_word && text.is_char_boundary(at))
            .then(|| {
                longest.iter().find(|m| {
                    text[at..].starts_with(&m.text)
                        && bytes
                            .get(at + m.text.len())
                            .is_none_or(|next| !is_name_byte(*next))
                })
            })
            .flatten();
        match named {
            Some(m) => {
                found.push((at..at + m.text.len(), m.seed_id.as_str()));
                at += m.text.len();
            }
            None => at += 1,
        }
    }
    found
}

fn is_name_byte(b: u8) -> bool {
    b.is_ascii_alphanumeric() || b == b'_' || b == b'-'
}
