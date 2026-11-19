use rune::{El, col, row};

use crate::Tokens;
use crate::system::{Radius, Space, Type, door, words};

/// Whose a remark is, which decides its ink.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Voice {
    /// An agent explaining the lines.
    Agent,
    /// An agent asking the reader to look here: the warning ink on its tint.
    Look,
    /// The reader's own note or question: the success ink on its tint.
    Yours,
    /// A change the reader made to the lines: the accent on its tint.
    Edited,
}

/// What someone said about some lines, set under them: who, which lines, then the words and
/// the controls that act on it, on a rail down its left side, with anything that answers it
/// underneath.
pub struct Remark {
    who: String,
    lines: String,
    said: String,
    voice: Voice,
    indent: f32,
    key: Option<String>,
    label: Option<String>,
    controls: Vec<El>,
    under: Vec<El>,
}

impl Remark {
    pub fn new(
        who: impl Into<String>,
        lines: impl Into<String>,
        said: impl Into<String>,
    ) -> Remark {
        Remark {
            who: who.into(),
            lines: lines.into(),
            said: said.into(),
            voice: Voice::Agent,
            indent: 0.0,
            key: None,
            label: None,
            controls: Vec::new(),
            under: Vec::new(),
        }
    }

    pub fn voice(mut self, voice: Voice) -> Self {
        self.voice = voice;
        self
    }

    /// How far in the remark starts, such as past a code row's line numbers.
    pub fn indent(mut self, points: f32) -> Self {
        self.indent = points;
        self
    }

    pub fn key(mut self, key: impl Into<String>) -> Self {
        self.key = Some(key.into());
        self
    }

    /// What assistive tools read for the remark, when its words alone don't say it.
    pub fn label(mut self, label: impl Into<String>) -> Self {
        self.label = Some(label.into());
        self
    }

    /// A control or a quiet word at the end of the remark's line.
    pub fn control(mut self, control: impl Into<El>) -> Self {
        self.controls.push(control.into());
        self
    }

    /// Something under the remark's line, such as the answer to a question.
    pub fn under(mut self, part: impl Into<El>) -> Self {
        self.under.push(part.into());
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let (ink, who, said, tint) = match self.voice {
            Voice::Agent => (t.border, t.text_tertiary, t.text_secondary, None),
            Voice::Look => (
                t.warning,
                t.warning,
                t.text_primary,
                Some(t.warning.alpha(0.1)),
            ),
            Voice::Yours => (
                t.success,
                t.success,
                t.text_primary,
                Some(t.success.alpha(0.08)),
            ),
            Voice::Edited => (
                t.accent,
                t.accent,
                t.text_secondary,
                Some(t.accent.alpha(0.08)),
            ),
        };
        let label = self
            .label
            .unwrap_or_else(|| format!("{} {}: {}", self.who, self.lines, self.said));
        let line = door::gap(row().w_full().items_start(), Space::S8)
            .child(words(self.who, Type::Label, who))
            .child(words(self.lines, Type::Mono, t.text_tertiary))
            .child(
                words(self.said, Type::Body, said)
                    .selectable(true)
                    .wrap()
                    .grow(1.0),
            )
            .children(self.controls);
        let remark = door::gap(col().w_full(), Space::S2)
            .child(line)
            .children(self.under);
        let remark = door::corner(door::inset(remark, Space::S12, Space::S4), Radius::Tag)
            .border_sides([0.0, 0.0, 0.0, RAIL], ink)
            .role("note")
            .label(label);
        let remark = match self.key {
            Some(key) => remark.key(key),
            None => remark,
        };
        let remark = match tint {
            Some(tint) => remark.fill(tint),
            None => remark,
        };
        door::inset(
            row()
                .w_full()
                .child(col().w(self.indent))
                .child(remark.grow(1.0)),
            Space::S0,
            Space::S2,
        )
    }
}

const RAIL: f32 = 2.0;
