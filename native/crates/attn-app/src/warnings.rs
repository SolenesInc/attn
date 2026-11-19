//! The daemon's warnings about its own start, such as terminals it could not recover: the
//! handshake brings them, the window shows them in a banner until dismissed, and the daemon
//! forgets them at once, so the next connection does not bring them again.

use std::sync::Arc;

use attn_protocol::{ClearWarningsMessage, DaemonWarning};
use attn_ui::Tokens;
use attn_ui::blocks::{Banner, BannerTone};
use rune::{El, Tx};

use crate::State;

/// The warnings the window shows: those the last handshake brought, until dismissed.
pub(crate) type Shown = Arc<Vec<DaemonWarning>>;

/// A handshake completed: the main window shows what it brought and tells the daemon they were
/// seen. A tile window shows no banner, so it acknowledges nothing.
pub(crate) fn connected(state: &mut State) {
    if state.window.tile.is_some() {
        return;
    }
    state.window.warnings = state.facts.warnings.clone();
    if !state.window.warnings.is_empty() {
        let _ = state.daemon.send(&ClearWarningsMessage::new());
    }
}

pub(crate) fn banner(state: &State, t: &Tokens) -> Option<El> {
    let warnings = &state.window.warnings;
    if warnings.is_empty() {
        return None;
    }
    let text = warnings
        .iter()
        .map(|w| w.message.as_str())
        .collect::<Vec<_>>()
        .join(" ");
    let mut banner = Banner::new("daemon-warnings", "Daemon warnings", BannerTone::Warn);
    let mut parts = pieces(&text).into_iter().enumerate().peekable();
    if let Some((_, Piece::Words(first))) = parts.next_if(|(_, p)| matches!(p, Piece::Words(_))) {
        banner = banner.says("daemon-warnings.text", first);
    }
    for (i, piece) in parts {
        let key = format!("daemon-warnings.{i}");
        banner = match piece {
            Piece::Words(said) => banner.note(key, said),
            Piece::Link(url) => {
                let (url, shown) = (url.to_owned(), shown(url));
                banner.link(
                    key,
                    shown.clone(),
                    shown,
                    move |_: &mut State, tx: &mut Tx| {
                        if let Err(why) = tx.open_url(&url) {
                            eprintln!("attn-app: could not open {url}: {why}");
                        }
                    },
                )
            }
        };
    }
    Some(
        banner
            .dismiss(
                "daemon-warnings.dismiss",
                "Dismiss warnings",
                |state: &mut State| {
                    state.window.warnings = Shown::default();
                },
            )
            .build(t),
    )
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Piece<'a> {
    Words(&'a str),
    Link(&'a str),
}

/// A warning's words and the bare web addresses among them, in order. An address ends at a
/// space, and punctuation that closes a sentence after it stays with the words.
fn pieces(text: &str) -> Vec<Piece<'_>> {
    let mut pieces = Vec::new();
    let mut rest = text;
    while let Some(start) = [rest.find("https://"), rest.find("http://")]
        .into_iter()
        .flatten()
        .min()
    {
        let len = rest[start..]
            .find(char::is_whitespace)
            .unwrap_or(rest.len() - start);
        let url =
            rest[start..start + len].trim_end_matches(['.', ',', ';', ':', '!', '?', ')', ']']);
        words(&mut pieces, &rest[..start]);
        pieces.push(Piece::Link(url));
        rest = &rest[start + url.len()..];
    }
    words(&mut pieces, rest);
    pieces
}

fn words<'a>(pieces: &mut Vec<Piece<'a>>, said: &'a str) {
    let said = said.trim();
    if !said.is_empty() {
        pieces.push(Piece::Words(said));
    }
}

/// An address as the banner shows it: its host, with `/…` when it goes deeper.
fn shown(url: &str) -> String {
    let rest = url.split_once("://").map_or(url, |(_, rest)| rest);
    match rest.split_once('/') {
        Some((host, path)) if !path.is_empty() => format!("{host}/…"),
        Some((host, _)) => host.to_owned(),
        None => rest.to_owned(),
    }
}
