use rune::{El, row};

use super::{Radius, Space, Tone, Type, door, focus, layers, tag, words};
use crate::Tokens;

/// The mark of an agent's open asks: "?" in the accent's round, with their number past one.
pub fn ask_mark(count: usize, t: &Tokens) -> El {
    let mark = row()
        .min_w(door::line_height(Type::Tag))
        .justify_center()
        .items_center()
        .fill(t.accent)
        .child(words(said(count), Type::Tag, t.bg_app));
    door::corner(door::inset(mark, Space::S4, Space::S0), Radius::Round)
}

fn said(count: usize) -> String {
    if count > 1 {
        format!("?{count}")
    } else {
        "?".to_owned()
    }
}

fn mark_width(count: usize) -> f32 {
    door::line_height(Type::Tag).max(door::width(&said(count), Type::Tag) + 2.0 * Space::S4.pt())
}

/// An agent's open asks as a round button washed in the accent: the mark, the oldest ask's
/// subject, and how many more there are. A chip short of room shows its subject cut, or its mark
/// alone. The mark stands a step in from every edge.
pub struct AskChip {
    label: String,
    subject: Option<(String, Option<f32>)>,
    more: usize,
}

/// The room the chip leaves round its words past the mark, before its edge.
const END: Space = Space::S8;

impl AskChip {
    /// A chip read as `label`, showing its mark alone.
    pub fn new(label: impl Into<String>) -> AskChip {
        AskChip {
            label: label.into(),
            subject: None,
            more: 0,
        }
    }

    /// The words after the mark, cut to `up_to` points wide when given.
    pub fn subject(mut self, said: impl Into<String>, up_to: Option<f32>) -> Self {
        self.subject = Some((said.into(), up_to));
        self
    }

    /// How many asks past the subject's the chip stands for, said after the subject.
    pub fn more(mut self, more: usize) -> Self {
        self.more = more;
        self
    }

    fn more_said(&self) -> Option<String> {
        (self.subject.is_some() && self.more > 0).then(|| format!("+{}", self.more))
    }

    /// How wide the chip draws.
    pub fn width(&self) -> f32 {
        let gap = Space::S6.pt();
        let subject = self.subject.as_ref().map_or(0.0, |(said, up_to)| {
            let w = door::width(said, Type::Control);
            gap + up_to.map_or(w, |up_to| w.min(up_to))
        });
        let more = self
            .more_said()
            .map_or(0.0, |more| gap + door::width(&more, Type::Small));
        let end = if self.subject.is_some() {
            END.pt()
        } else {
            Space::S4.pt()
        };
        Space::S4.pt() + mark_width(1) + subject + more + end
    }

    pub fn build(self, t: &Tokens) -> El {
        let (ink, wash) = tag::inks(Tone::Accent, t);
        let more = self
            .more_said()
            .map(|more| words(more, Type::Small, t.text_tertiary).single_line());
        let subject = self.subject.map(|(said, up_to)| {
            let subject = words(said, Type::Control, t.text_primary)
                .single_line()
                .truncate()
                .min_w(0.0);
            match up_to {
                Some(up_to) => subject.max_w(up_to),
                None => subject,
            }
        });
        let end = if subject.is_some() { END } else { Space::S4 };
        let face = door::gap(row().items_center().min_w(0.0), Space::S6)
            .role("button")
            .focusable(true)
            .label(self.label)
            .h(door::line_height(Type::Tag) + 2.0 * Space::S4.pt())
            .fill(wash)
            .border(1.0, tag::edge(ink))
            .child(ask_mark(1, t))
            .children(subject)
            .children(more);
        let chip = door::corner(door::sides(face, Space::S4, end), Radius::Round);
        focus::ring(layers::pressable(chip, wash, t), t)
    }
}
