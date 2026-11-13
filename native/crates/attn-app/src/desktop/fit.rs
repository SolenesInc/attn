#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(super) enum Piece {
    Seed,
    Role,
    Host,
    Usage,
    Provenance,
    Id,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(super) enum Level {
    Full,
    Short,
    Icon,
    Hidden,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub(super) struct Widths {
    pub full: f32,
    pub short: Option<f32>,
    pub icon: Option<f32>,
}

impl Widths {
    fn at(&self, level: Level) -> Option<f32> {
        match level {
            Level::Full => Some(self.full),
            Level::Short => self.short,
            Level::Icon => self.icon,
            Level::Hidden => None,
        }
    }
}

/// The order a subline gives up room in: each step shrinks or hides one piece, until the row
/// fits.
const SUBLINE_RANK: &[(Piece, Level)] = &[
    (Piece::Usage, Level::Hidden),
    (Piece::Id, Level::Hidden),
    (Piece::Provenance, Level::Icon),
    (Piece::Host, Level::Short),
    (Piece::Role, Level::Icon),
    (Piece::Seed, Level::Short),
    (Piece::Seed, Level::Icon),
    (Piece::Provenance, Level::Hidden),
    (Piece::Role, Level::Hidden),
    (Piece::Host, Level::Hidden),
    (Piece::Seed, Level::Hidden),
];

/// A subline's pieces at their widths, the room they have, and the pieces it never hides.
pub(super) struct Row<'a> {
    pub available: f32,
    pub gap: f32,
    pub pieces: &'a [(Piece, Widths)],
    pub keep: &'a [Piece],
}

/// Each piece's level: everything in full, then the rank's steps in order until the row fits.
pub(super) fn fit(row: &Row) -> Vec<(Piece, Level)> {
    let mut levels: Vec<(Piece, Level)> =
        row.pieces.iter().map(|(p, _)| (*p, Level::Full)).collect();
    let used = |levels: &[(Piece, Level)]| {
        let shown: Vec<f32> = row
            .pieces
            .iter()
            .zip(levels)
            .filter_map(|((_, widths), (_, level))| widths.at(*level))
            .collect();
        shown.iter().sum::<f32>() + row.gap * shown.len().saturating_sub(1) as f32
    };
    for &(piece, level) in SUBLINE_RANK {
        if used(&levels) <= row.available {
            break;
        }
        let Some(i) = row.pieces.iter().position(|(p, _)| *p == piece) else {
            continue;
        };
        let allowed = match level {
            Level::Hidden => !row.keep.contains(&piece),
            _ => row.pieces[i].1.at(level).is_some(),
        };
        if allowed {
            levels[i].1 = level;
        }
    }
    levels
}

/// The width `s` lays out at in `font`, on one line.
pub(super) fn text_w(s: &str, font: rune::TextStyle) -> f32 {
    rune::text::measure(s, &[], font, None).0
}

/// A host's short mark: the first letter of each dash-separated word, then its trailing digits.
pub(super) fn host_mark(host: &str) -> String {
    let digits_at = host.trim_end_matches(|c: char| c.is_ascii_digit()).len();
    let (name, digits) = host.split_at(digits_at);
    let initials: String = name
        .split('-')
        .filter_map(|part| part.chars().next())
        .collect();
    let mark = format!("{initials}{digits}").to_lowercase();
    if mark.is_empty() {
        host.to_string()
    } else {
        mark
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SUBLINE_GAP: f32 = 10.0;
    const SUBLINE: [(Piece, Widths); 6] = [
        (
            Piece::Seed,
            Widths {
                full: 220.0,
                short: Some(140.0),
                icon: Some(16.0),
            },
        ),
        (
            Piece::Role,
            Widths {
                full: 80.0,
                short: None,
                icon: Some(22.0),
            },
        ),
        (
            Piece::Host,
            Widths {
                full: 70.0,
                short: Some(24.0),
                icon: None,
            },
        ),
        (
            Piece::Usage,
            Widths {
                full: 40.0,
                short: None,
                icon: None,
            },
        ),
        (
            Piece::Id,
            Widths {
                full: 60.0,
                short: None,
                icon: None,
            },
        ),
        (
            Piece::Provenance,
            Widths {
                full: 160.0,
                short: None,
                icon: Some(14.0),
            },
        ),
    ];

    fn subline(available: f32, keep: &[Piece]) -> Vec<Level> {
        fit(&Row {
            available,
            gap: SUBLINE_GAP,
            pieces: &SUBLINE,
            keep,
        })
        .into_iter()
        .map(|(_, level)| level)
        .collect()
    }

    fn width(levels: &[Level]) -> f32 {
        let shown: Vec<f32> = SUBLINE
            .iter()
            .zip(levels)
            .filter_map(|((_, w), level)| w.at(*level))
            .collect();
        shown.iter().sum::<f32>() + SUBLINE_GAP * shown.len().saturating_sub(1) as f32
    }

    #[test]
    fn each_subline_step_applies_only_once_the_previous_levels_overflow_and_stops_as_soon_as_the_row_fits()
     {
        use Level::*;
        let steps: [[Level; 6]; 12] = [
            [Full, Full, Full, Full, Full, Full],
            [Full, Full, Full, Hidden, Full, Full],
            [Full, Full, Full, Hidden, Hidden, Full],
            [Full, Full, Full, Hidden, Hidden, Icon],
            [Full, Full, Short, Hidden, Hidden, Icon],
            [Full, Icon, Short, Hidden, Hidden, Icon],
            [Short, Icon, Short, Hidden, Hidden, Icon],
            [Icon, Icon, Short, Hidden, Hidden, Icon],
            [Icon, Icon, Short, Hidden, Hidden, Hidden],
            [Icon, Hidden, Short, Hidden, Hidden, Hidden],
            [Icon, Hidden, Hidden, Hidden, Hidden, Hidden],
            [Hidden, Hidden, Hidden, Hidden, Hidden, Hidden],
        ];
        for (step, pair) in steps.windows(2).enumerate() {
            let (previous, expected) = (&pair[0], &pair[1]);
            assert_eq!(
                subline(width(previous), &[]),
                previous.to_vec(),
                "step {} holds",
                step + 1
            );
            assert_eq!(
                subline(width(previous) - 1.0, &[]),
                expected.to_vec(),
                "step {}",
                step + 1
            );
        }
    }

    #[test]
    fn an_offline_sessions_host_stays_as_its_short_mark() {
        use Level::*;
        assert_eq!(
            subline(0.0, &[Piece::Host]),
            vec![Hidden, Hidden, Short, Hidden, Hidden, Hidden]
        );
    }

    #[test]
    fn a_level_a_piece_does_not_have_is_skipped() {
        let pieces = [
            (
                Piece::Usage,
                Widths {
                    full: 40.0,
                    short: None,
                    icon: None,
                },
            ),
            (
                Piece::Role,
                Widths {
                    full: 80.0,
                    short: None,
                    icon: None,
                },
            ),
        ];
        let levels = fit(&Row {
            available: 30.0,
            gap: SUBLINE_GAP,
            pieces: &pieces,
            keep: &[],
        });
        assert_eq!(
            levels,
            vec![(Piece::Usage, Level::Hidden), (Piece::Role, Level::Hidden)]
        );
    }

    #[test]
    fn a_hosts_mark_is_the_first_letter_of_each_word_and_its_digits() {
        for (host, mark) in [
            ("ai-sandbox", "as"),
            ("gpu-box-2", "gb2"),
            ("studio", "s"),
            ("build12", "b12"),
        ] {
            assert_eq!(host_mark(host), mark, "{host}");
        }
    }
}
