//! What the attention supervisor did about an agent, as the chip in its tile's headline says it.

use attn_protocol::Session;
use attn_ui::Tokens;
use attn_ui::blocks::{Verdict, VerdictChip};
use rune::El;

use super::Widths;

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(super) struct Chip {
    verdict: Verdict,
    said: String,
    reason: String,
}

pub(super) fn of(session: &Session) -> Option<Chip> {
    let attention = session.attention.as_ref()?;
    let verdict = match attention.verdict.as_str() {
        "open" => Verdict::Open,
        "finished" => Verdict::Finished,
        _ => return None,
    };
    let said = attention
        .label
        .as_deref()
        .map(str::trim)
        .filter(|label| !label.is_empty())
        .unwrap_or(match verdict {
            Verdict::Open => "needs you",
            Verdict::Finished => "finished",
        })
        .to_owned();
    Some(Chip {
        verdict,
        said,
        reason: attention.reason.trim().to_owned(),
    })
}

pub(super) fn widths(chip: &Chip) -> Widths {
    let (full, icon) = VerdictChip::widths(&chip.said);
    Widths {
        full,
        short: None,
        icon: Some(icon),
    }
}

pub(super) fn view(leaf: &str, chip: &Chip, mark_only: bool, t: &Tokens) -> El {
    let by = match chip.verdict {
        Verdict::Open => "Opened by the supervisor",
        Verdict::Finished => "Settle closes it",
    };
    let help: Vec<&str> = [chip.reason.as_str(), by]
        .into_iter()
        .filter(|line| !line.is_empty())
        .collect();
    VerdictChip::new(
        format!("tile.{leaf}.verdict"),
        chip.verdict,
        chip.said.clone(),
        help.join("\n"),
    )
    .mark_only(mark_only)
    .build(t)
}
