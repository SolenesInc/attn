use rune::{El, col, row};

use crate::Tokens;
use crate::system::{Space, Type, door, words};

/// What someone said about some lines, set under them: who, which lines, then the words, on a
/// rail down its left side. A remark that asks the reader to look is drawn in the warning ink on
/// its tint.
pub struct Remark {
    who: String,
    lines: String,
    said: String,
    flagged: bool,
    indent: f32,
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
            flagged: false,
            indent: 0.0,
        }
    }

    /// The remark asks the reader to look here.
    pub fn flagged(mut self, flagged: bool) -> Self {
        self.flagged = flagged;
        self
    }

    /// How far in the remark starts, such as past a code row's line numbers.
    pub fn indent(mut self, points: f32) -> Self {
        self.indent = points;
        self
    }

    pub fn build(self, t: &Tokens) -> El {
        let (rail, who, said) = if self.flagged {
            (t.warning, t.warning, t.text_primary)
        } else {
            (t.border, t.text_tertiary, t.text_secondary)
        };
        let label = format!("{} {}: {}", self.who, self.lines, self.said);
        let line = door::gap(row().w_full().items_start(), Space::S8)
            .child(words(self.who, Type::Label, who))
            .child(words(self.lines, Type::Mono, t.text_tertiary))
            .child(
                words(self.said, Type::Body, said)
                    .selectable(true)
                    .wrap()
                    .grow(1.0),
            );
        let remark = door::inset(line, Space::S12, Space::S4)
            .border_sides([0.0, 0.0, 0.0, RAIL], rail)
            .role("note")
            .label(label);
        let remark = if self.flagged {
            remark.fill(t.warning.alpha(0.1))
        } else {
            remark
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
