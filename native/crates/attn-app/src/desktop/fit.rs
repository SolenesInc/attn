#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(super) enum Piece {
    Seed,
    Role,
    Host,
    Usage,
    Provenance,
    Id,
    Settling,
    Ask,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub(super) enum Level {
    Full,
    Short,
    Icon,
    Hidden,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub(crate) struct Widths {
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

/// The order a headline gives up room in: the settling chip turns to its mark, then the ask chip
/// cuts its subject and turns to its mark. Neither hides.
pub(super) const HEADLINE_RANK: &[(Piece, Level)] = &[
    (Piece::Settling, Level::Icon),
    (Piece::Ask, Level::Short),
    (Piece::Ask, Level::Icon),
];

/// The width a headline keeps for its title before its chips give up room.
pub(super) const TITLE_MINIMUM: f32 = 120.0;

/// The order a subline gives up room in: each step shrinks or hides one piece, until the row
/// fits.
pub(super) const SUBLINE_RANK: &[(Piece, Level)] = &[
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

/// A header line's pieces at their widths, the room they have, the widths it always spends,
/// the order it gives up room in, and the pieces it never hides.
pub(super) struct Row<'a> {
    pub available: f32,
    pub gap: f32,
    pub fixed: &'a [f32],
    pub pieces: &'a [(Piece, Widths)],
    pub rank: &'a [(Piece, Level)],
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
        let items = row.fixed.len() + shown.len();
        row.fixed.iter().sum::<f32>()
            + shown.iter().sum::<f32>()
            + row.gap * items.saturating_sub(1) as f32
    };
    for &(piece, level) in row.rank {
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
pub(crate) fn text_w(s: &str, font: rune::TextStyle) -> f32 {
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
            fixed: &[],
            pieces: &SUBLINE,
            rank: SUBLINE_RANK,
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
            fixed: &[],
            pieces: &pieces,
            rank: SUBLINE_RANK,
            keep: &[],
        });
        assert_eq!(
            levels,
            vec![(Piece::Usage, Level::Hidden), (Piece::Role, Level::Hidden)]
        );
    }

    const HEADLINE_GAP: f32 = 8.0;
    const HEADLINE_DOT: f32 = 6.0;
    const SETTLING: Widths = Widths {
        full: 150.0,
        short: None,
        icon: Some(24.0),
    };
    const ASK: Widths = Widths {
        full: 200.0,
        short: Some(140.0),
        icon: Some(24.0),
    };
    const TITLE_ROOM: f32 = HEADLINE_DOT + HEADLINE_GAP + TITLE_MINIMUM;

    fn headline(available: f32, pieces: &[(Piece, Widths)]) -> Vec<(Piece, Level)> {
        fit(&Row {
            available,
            gap: HEADLINE_GAP,
            fixed: &[HEADLINE_DOT, TITLE_MINIMUM],
            pieces,
            rank: HEADLINE_RANK,
            keep: &[],
        })
    }

    #[test]
    fn a_headline_turns_settling_to_its_mark_then_cuts_the_ask_then_turns_it_to_its_mark() {
        use Level::*;
        let both = [(Piece::Settling, SETTLING), (Piece::Ask, ASK)];
        let g = HEADLINE_GAP;
        let cases = [
            (
                "everything fits",
                TITLE_ROOM + g + 150.0 + g + 200.0,
                [Full, Full],
            ),
            (
                "settling turns to an icon first",
                TITLE_ROOM + g + 150.0 + g + 199.0,
                [Icon, Full],
            ),
            (
                "then the ask subject is cut",
                TITLE_ROOM + g + 24.0 + g + 199.0,
                [Icon, Short],
            ),
            (
                "then the ask turns to an icon",
                TITLE_ROOM + g + 24.0 + g + 139.0,
                [Icon, Icon],
            ),
            (
                "the title minimum yields to signal icons, which stay",
                TITLE_ROOM,
                [Icon, Icon],
            ),
            (
                "no width at all still shows every signal",
                0.0,
                [Icon, Icon],
            ),
        ];
        for (name, available, [settling, ask]) in cases {
            assert_eq!(
                headline(available, &both),
                vec![(Piece::Settling, settling), (Piece::Ask, ask)],
                "{name}"
            );
        }
    }

    #[test]
    fn a_headline_keeps_the_title_its_minimum_against_a_full_or_short_ask() {
        let ask = [(Piece::Ask, ASK)];
        assert_eq!(
            headline(TITLE_ROOM + HEADLINE_GAP + 199.0, &ask),
            vec![(Piece::Ask, Level::Short)]
        );
        assert_eq!(
            headline(TITLE_ROOM + HEADLINE_GAP + 139.0, &ask),
            vec![(Piece::Ask, Level::Icon)]
        );
    }

    #[test]
    fn a_headline_never_hides_a_signal_at_any_width() {
        let both = [(Piece::Settling, SETTLING), (Piece::Ask, ASK)];
        for available in (0..=700).step_by(7) {
            assert!(
                headline(available as f32, &both)
                    .iter()
                    .all(|(_, level)| *level != Level::Hidden),
                "at {available}"
            );
        }
    }

    #[test]
    fn a_headline_without_signals_has_nothing_to_fit() {
        assert_eq!(headline(40.0, &[]), vec![]);
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
