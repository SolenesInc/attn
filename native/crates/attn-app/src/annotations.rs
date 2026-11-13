//! What the user can say about a marked passage, and the feedback a batch of marks sends, as
//! the Tauri app's `quickLabels.ts` says them.

use serde::Serialize;

use crate::prompts;

/// A one-tap verdict on a marked passage. `tip` is what the agent is told to do with it.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct QuickLabel {
    pub id: &'static str,
    pub emoji: &'static str,
    pub text: &'static str,
    pub color: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub tip: Option<String>,
}

fn label(
    id: &'static str,
    emoji: &'static str,
    text: &'static str,
    color: &'static str,
) -> QuickLabel {
    QuickLabel {
        id,
        emoji,
        text,
        color,
        tip: None,
    }
}

fn tipped(
    id: &'static str,
    emoji: &'static str,
    text: &'static str,
    color: &'static str,
) -> QuickLabel {
    QuickLabel {
        tip: Some(
            prompts::render("annotation-label", id, &[]).expect("each tipped label has a prompt"),
        ),
        ..label(id, emoji, text, color)
    }
}

/// The labels in the groups the label picker shows them in.
pub fn label_groups() -> Vec<Vec<QuickLabel>> {
    vec![
        vec![
            label("i-agree", "👍", "I agree", "green"),
            label("exactly-this", "💯", "Exactly this", "green"),
        ],
        vec![
            label("this-is-wrong", "❌", "This is wrong", "red"),
            label("dont-love-this", "😕", "I don't love this", "orange"),
        ],
        vec![
            label("clarify-this", "❓", "Clarify this", "yellow"),
            tipped("verify-this", "🔍", "Verify this", "blue"),
            tipped("show-the-receipt", "🧾", "Show the receipt", "teal"),
            tipped("give-me-an-example", "🔬", "Give me an example", "cyan"),
            tipped(
                "consider-alternatives",
                "🔄",
                "Consider alternatives",
                "pink",
            ),
        ],
        vec![label("cut-this", "🪓", "Cut this", "amber")],
        vec![
            label("your-call", "🪙", "Your call", "green"),
            label("ask-me-first", "🙋", "Ask me first", "yellow"),
        ],
    ]
}

/// Every label, in picker order.
pub fn labels() -> Vec<QuickLabel> {
    label_groups().into_iter().flatten().collect()
}

pub fn label_by_id(id: &str) -> Option<QuickLabel> {
    labels().into_iter().find(|l| l.id == id)
}

/// One mark as the feedback quotes it.
#[derive(Clone, Debug)]
pub struct Quoted<'a> {
    pub quote: &'a str,
    pub label: &'a str,
    pub comment: &'a str,
    /// Where it starts in its message, which orders the marks.
    pub start: usize,
}

/// The feedback a batch of terminal marks sends: the note, then each mark in message order.
/// Empty when there are no marks.
pub fn terminal_payload(marks: &[Quoted], note: &str) -> Result<String, String> {
    if marks.is_empty() {
        return Ok(String::new());
    }
    let mut ordered: Vec<&Quoted> = marks.iter().collect();
    ordered.sort_by_key(|m| m.start);
    let mut entries = String::new();
    for (i, mark) in ordered.into_iter().enumerate() {
        let label = (!mark.label.is_empty())
            .then(|| label_by_id(mark.label))
            .flatten();
        let heading = label
            .as_ref()
            .map(|l| format!("{} {}", l.emoji, l.text))
            .unwrap_or_default();
        let index = (i + 1).to_string();
        let quote = mark.quote.split('\n').collect::<Vec<_>>().join("\n> ");
        let tip = label
            .as_ref()
            .and_then(|l| l.tip.clone())
            .unwrap_or_default();
        let has_comment = (!mark.comment.is_empty()).to_string();
        entries.push_str(&prompts::render(
            "annotation-terminal",
            "entry",
            &[
                ("index", &index),
                ("heading", &heading),
                ("quote", &quote),
                ("tip", &tip),
                ("comment", mark.comment),
                ("has_comment", &has_comment),
            ],
        )?);
    }
    let submit = prompts::render(
        "annotation-terminal",
        "submit",
        &[("note", note.trim()), ("entries", &entries)],
    )?;
    Ok(submit.trim_end().to_owned())
}
