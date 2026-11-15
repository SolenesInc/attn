//! A document's draft of marks as the reader keeps it against the daemon's: what changed here
//! since the daemon last answered, and the daemon's answer with those changes on top, as the
//! Tauri reader's `documentDraft.ts` keeps them.

use attn_protocol::MarkdownAnnotation;

/// The kinds of mark the daemon's draft can hold.
const KINDS: [&str; 5] = ["comment", "deletion", "global", "question", "edit"];

/// What a reader can say about a passage.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Saying {
    /// Struck: the passage should go.
    Deletion,
    /// A comment written on the passage.
    Comment(String),
    /// A quick label picked for the passage.
    Label(&'static str),
}

/// Marks a draft read from the daemon keeps: a known kind, anchored unless it speaks of the
/// whole document.
pub(crate) fn kept(marks: Vec<MarkdownAnnotation>) -> Vec<MarkdownAnnotation> {
    marks
        .into_iter()
        .filter(|m| {
            KINDS.contains(&m.r#type.as_str()) && (m.r#type == "global" || m.anchor.is_some())
        })
        .collect()
}

/// A new mark saying `saying` about the passage at `anchor`, written at `now_ms`.
pub(crate) fn new_mark(
    anchor: attn_protocol::MarkdownAnnotationAnchor,
    saying: Saying,
    now_ms: i64,
) -> MarkdownAnnotation {
    let mark = MarkdownAnnotation {
        id: uuid::Uuid::new_v4().to_string(),
        anchor: Some(anchor),
        created_at: now_ms,
        ..MarkdownAnnotation::default()
    };
    match saying {
        Saying::Deletion => MarkdownAnnotation {
            r#type: "deletion".into(),
            ..mark
        },
        Saying::Comment(text) => MarkdownAnnotation {
            r#type: "comment".into(),
            text: Some(text),
            ..mark
        },
        Saying::Label(id) => {
            let label = crate::annotations::label_by_id(id);
            MarkdownAnnotation {
                r#type: "comment".into(),
                quick_label_id: Some(id.to_owned()),
                quick_label_tip: label.as_ref().and_then(|l| l.tip.clone()),
                quick_label_text: label.map(|l| format!("{} {}", l.emoji, l.text)),
                ..mark
            }
        }
    }
}

/// A note on the whole document.
pub(crate) fn overall_note(text: String, now_ms: i64) -> MarkdownAnnotation {
    MarkdownAnnotation {
        id: uuid::Uuid::new_v4().to_string(),
        r#type: "global".into(),
        text: Some(text),
        created_at: now_ms,
        ..MarkdownAnnotation::default()
    }
}

fn wire_form(mark: &MarkdownAnnotation) -> serde_json::Value {
    serde_json::to_value(mark).unwrap_or_default()
}

fn changed_here(mark: &MarkdownAnnotation, synced: &[MarkdownAnnotation]) -> bool {
    synced
        .iter()
        .find(|s| s.id == mark.id)
        .is_none_or(|s| wire_form(s) != wire_form(mark))
}

/// What `local` changed since the daemon answered `synced`: the marks to write, and the ids to
/// take out.
pub(crate) fn unsaved(
    local: &[MarkdownAnnotation],
    synced: &[MarkdownAnnotation],
) -> (Vec<MarkdownAnnotation>, Vec<String>) {
    let upserts = local
        .iter()
        .filter(|m| changed_here(m, synced))
        .cloned()
        .collect();
    let removed = synced
        .iter()
        .filter(|s| !local.iter().any(|m| m.id == s.id))
        .map(|s| s.id.clone())
        .collect();
    (upserts, removed)
}

fn same_feedback(a: &MarkdownAnnotation, b: &MarkdownAnnotation) -> bool {
    (
        &a.r#type,
        &a.text,
        &a.quick_label_id,
        &a.answer,
        &a.edit_was,
        &a.edit_now,
        a.edit_after_head,
        a.edit_before_head,
    ) == (
        &b.r#type,
        &b.text,
        &b.quick_label_id,
        &b.answer,
        &b.edit_was,
        &b.edit_now,
        b.edit_after_head,
        b.edit_before_head,
    )
}

/// The daemon's `draft` with `local`'s unsaved changes on top. A mark the daemon dropped stays
/// only when it is new here or its feedback was rewritten here: a moved anchor alone does not
/// bring back a mark that was sent or removed. The daemon's answer to a question always wins.
pub(crate) fn rebase(
    local: &[MarkdownAnnotation],
    synced: &[MarkdownAnnotation],
    draft: Vec<MarkdownAnnotation>,
) -> Vec<MarkdownAnnotation> {
    let removed_here = |m: &MarkdownAnnotation| {
        synced.iter().any(|s| s.id == m.id) && !local.iter().any(|l| l.id == m.id)
    };
    let worth_keeping = |m: &MarkdownAnnotation| {
        synced
            .iter()
            .find(|s| s.id == m.id)
            .is_none_or(|s| !same_feedback(s, m))
    };
    let kept_local: Vec<MarkdownAnnotation> = local
        .iter()
        .filter(|l| !draft.iter().any(|d| d.id == l.id) && worth_keeping(l))
        .cloned()
        .collect();
    draft
        .into_iter()
        .filter(|d| !removed_here(d))
        .map(|d| match local.iter().find(|l| l.id == d.id) {
            Some(mine) if changed_here(mine, synced) => MarkdownAnnotation {
                answer: d.answer,
                ..mine.clone()
            },
            _ => d,
        })
        .chain(kept_local)
        .collect()
}

/// `marks` in the order a reader meets them, notes on the whole document last in the order
/// they were written.
pub(crate) fn reading_order(marks: &mut [MarkdownAnnotation]) {
    let place = |m: &MarkdownAnnotation| {
        m.anchor
            .as_ref()
            .map(|a| (a.start_line, a.file_start_line.unwrap_or(0), a.start))
    };
    marks.sort_by(|a, b| match (place(a), place(b)) {
        (Some(x), Some(y)) => x.cmp(&y),
        (Some(_), None) => std::cmp::Ordering::Less,
        (None, Some(_)) => std::cmp::Ordering::Greater,
        (None, None) => a.created_at.cmp(&b.created_at),
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    fn mark(id: &str, text: &str) -> MarkdownAnnotation {
        MarkdownAnnotation {
            id: id.into(),
            r#type: "comment".into(),
            text: Some(text.into()),
            ..MarkdownAnnotation::default()
        }
    }

    fn ids(marks: &[MarkdownAnnotation]) -> Vec<(String, Option<String>)> {
        marks
            .iter()
            .map(|m| (m.id.clone(), m.text.clone()))
            .collect()
    }

    #[test]
    fn a_rebase_keeps_this_readers_edits_and_drops_what_the_daemon_sent() {
        let synced = vec![
            mark("kept", "a"),
            mark("sent", "b"),
            mark("rewritten", "c"),
            mark("removed", "d"),
        ];
        let local = vec![
            mark("kept", "a"),
            mark("sent", "b"),
            mark("rewritten", "c2"),
            mark("new", "e"),
        ];
        let draft = vec![mark("kept", "a"), mark("removed", "d"), mark("other", "f")];
        assert_eq!(
            ids(&rebase(&local, &synced, draft)),
            [
                ("kept", "a"),
                ("other", "f"),
                ("rewritten", "c2"),
                ("new", "e")
            ]
            .map(|(i, t)| (i.to_owned(), Some(t.to_owned())))
        );
        let (upserts, removed) = unsaved(&local, &synced);
        assert_eq!(
            (ids(&upserts), removed),
            (
                vec![
                    ("rewritten".into(), Some("c2".into())),
                    ("new".into(), Some("e".into()))
                ],
                vec!["removed".to_owned()]
            )
        );
    }
}
