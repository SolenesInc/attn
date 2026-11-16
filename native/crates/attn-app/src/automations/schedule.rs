//! Schedules the form's controls can write: every N minutes or hours, on days of the week at a
//! time, or monthly on a day. A cron reads as one only when the controls would write it back
//! exactly; any other stays as written, on the Cron tab.

pub(crate) const ALL_DAYS: [u8; 7] = [0, 1, 2, 3, 4, 5, 6];
pub(crate) const WEEKDAYS: [u8; 5] = [1, 2, 3, 4, 5];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Unit {
    Minutes,
    Hours,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) enum Friendly {
    Every {
        unit: Unit,
        n: u32,
        window: Option<(u32, u32)>,
        days: Vec<u8>,
        minute: u32,
    },
    Days {
        hour: u32,
        minute: u32,
        days: Vec<u8>,
    },
    Monthly {
        day: u32,
        hour: u32,
        minute: u32,
    },
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Tab {
    Every,
    Days,
    Monthly,
    Cron,
}

impl Tab {
    pub(crate) const ALL: [(Tab, &'static str, &'static str); 4] = [
        (Tab::Every, "every", "Every"),
        (Tab::Days, "days", "On days"),
        (Tab::Monthly, "monthly", "Monthly"),
        (Tab::Cron, "cron", "Cron"),
    ];
}

impl Friendly {
    pub(crate) fn tab(&self) -> Tab {
        match self {
            Friendly::Every { .. } => Tab::Every,
            Friendly::Days { .. } => Tab::Days,
            Friendly::Monthly { .. } => Tab::Monthly,
        }
    }

    pub(crate) fn cron(&self) -> String {
        match self {
            Friendly::Monthly { day, hour, minute } => format!("{minute} {hour} {day} * *"),
            Friendly::Days { hour, minute, days } => {
                format!("{minute} {hour} * * {}", days_field(days))
            }
            Friendly::Every {
                unit: Unit::Minutes,
                n,
                window,
                days,
                ..
            } => {
                let hours = window.map_or("*".to_owned(), |(a, b)| format!("{a}-{b}"));
                let step = if *n == 1 {
                    "*".to_owned()
                } else {
                    format!("*/{n}")
                };
                format!("{step} {hours} * * {}", days_field(days))
            }
            Friendly::Every {
                unit: Unit::Hours,
                n,
                window,
                days,
                minute,
            } => {
                let base = window.map_or("*".to_owned(), |(a, b)| format!("{a}-{b}"));
                let hours = if *n == 1 { base } else { format!("{base}/{n}") };
                format!("{minute} {hours} * * {}", days_field(days))
            }
        }
    }

    /// The schedule the controls of `tab` start from, keeping what `current` already says.
    pub(crate) fn for_tab(tab: Tab, current: Option<&Friendly>) -> Friendly {
        let days = match current {
            Some(Friendly::Every { days, .. } | Friendly::Days { days, .. }) => days.clone(),
            _ => ALL_DAYS.to_vec(),
        };
        let (hour, minute) = match current {
            Some(Friendly::Days { hour, minute, .. } | Friendly::Monthly { hour, minute, .. }) => {
                (*hour, *minute)
            }
            _ => (9, 0),
        };
        match tab {
            Tab::Days => Friendly::Days { hour, minute, days },
            Tab::Monthly => Friendly::Monthly {
                day: match current {
                    Some(Friendly::Monthly { day, .. }) => *day,
                    _ => 1,
                },
                hour,
                minute,
            },
            Tab::Every | Tab::Cron => Friendly::Every {
                unit: Unit::Minutes,
                n: 30,
                window: None,
                days,
                minute: 0,
            },
        }
    }
}

fn days_field(days: &[u8]) -> String {
    let mut sorted = days.to_vec();
    sorted.sort_unstable();
    sorted.dedup();
    if sorted.len() == 7 {
        return "*".into();
    }
    let mut parts = Vec::new();
    let mut i = 0;
    while i < sorted.len() {
        let mut j = i;
        while j + 1 < sorted.len() && sorted[j + 1] == sorted[j] + 1 {
            j += 1;
        }
        if j - i >= 2 {
            parts.push(format!("{}-{}", sorted[i], sorted[j]));
        } else {
            parts.extend(sorted[i..=j].iter().map(u8::to_string));
        }
        i = j + 1;
    }
    parts.join(",")
}

fn int(field: &str) -> Option<u32> {
    (!field.is_empty() && field.bytes().all(|b| b.is_ascii_digit()))
        .then(|| field.parse().ok())
        .flatten()
}

fn days_of(field: &str) -> Option<Vec<u8>> {
    if field == "*" {
        return Some(ALL_DAYS.to_vec());
    }
    let mut days = Vec::new();
    for part in field.split(',') {
        let digit = |s: &str| (s.len() == 1).then(|| int(s)).flatten();
        if let Some((a, b)) = part.split_once('-') {
            let (a, b) = (digit(a)?, digit(b)?);
            days.extend((a..=b).map(|d| d as u8));
        } else {
            days.push(digit(part)? as u8);
        }
    }
    days.iter().all(|d| *d <= 6).then_some(days)
}

fn hours_of(field: &str) -> Option<(u32, Option<(u32, u32)>)> {
    if field == "*" {
        return Some((1, None));
    }
    if let Some(n) = field.strip_prefix("*/") {
        return Some((int(n)?, None));
    }
    let (range, step) = match field.split_once('/') {
        Some((range, step)) => (range, Some(int(step)?)),
        None => (field, None),
    };
    let (a, b) = range.split_once('-')?;
    Some((step.unwrap_or(1), Some((int(a)?, int(b)?))))
}

fn read(fields: &[&str]) -> Option<Friendly> {
    let [minute, hour, dom, month, dow] = fields else {
        return None;
    };
    if *month != "*" {
        return None;
    }
    if *dom != "*" {
        if *dow != "*" {
            return None;
        }
        return Some(Friendly::Monthly {
            day: int(dom)?,
            hour: int(hour)?,
            minute: int(minute)?,
        });
    }
    let days = days_of(dow)?;
    let step = if *minute == "*" {
        Some(1)
    } else {
        minute.strip_prefix("*/").and_then(int)
    };
    if let Some(n) = step {
        let (hours_n, window) = hours_of(hour)?;
        if hours_n != 1 {
            return None;
        }
        return Some(Friendly::Every {
            unit: Unit::Minutes,
            n,
            window,
            days,
            minute: 0,
        });
    }
    let minute = int(minute)?;
    if let Some(hour) = int(hour) {
        return Some(Friendly::Days { hour, minute, days });
    }
    let (n, window) = hours_of(hour)?;
    Some(Friendly::Every {
        unit: Unit::Hours,
        n,
        window,
        days,
        minute,
    })
}

/// The schedule `cron` is, when the controls would write it back exactly.
pub(crate) fn friendly(cron: &str) -> Option<Friendly> {
    let trimmed = cron.trim();
    let fields: Vec<&str> = trimmed.split_whitespace().collect();
    let schedule = read(&fields)?;
    (schedule.cron() == trimmed).then_some(schedule)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_friendly_schedule_writes_back_exactly_and_a_custom_cron_is_left_as_written() {
        let friendly_crons = [
            "*/30 * * * *",
            "*/10 7-23 * * *",
            "0 3 * * 2",
            "0 9 * * 1-5",
            "0 0 1 * *",
            "0 * * * *",
            "0 */2 * * *",
            "15 8-18/2 * * 1-5",
            "0 9 * * 0,6",
            "* * * * *",
            "0 9 * * 1,3,5",
        ];
        for cron in friendly_crons {
            let schedule = friendly(cron).unwrap_or_else(|| panic!("{cron} reads as friendly"));
            assert_eq!(schedule.cron(), cron);
        }
        let custom = [
            "0 9 * * MON-FRI",
            "15 9-17/2 1-7 * 1",
            "0 9,17 * * *",
            "@daily",
            "@every 90m",
            "0 9 13 * 5",
            "0 9 * * 1,2,3,4,5",
            "0  9 * * *",
            "5 4 * 1 *",
        ];
        for cron in custom {
            assert_eq!(friendly(cron), None, "{cron}");
        }
    }

    #[test]
    fn each_tab_reads_the_schedule_it_writes() {
        assert_eq!(
            friendly("*/10 7-23 * * *"),
            Some(Friendly::Every {
                unit: Unit::Minutes,
                n: 10,
                window: Some((7, 23)),
                days: ALL_DAYS.to_vec(),
                minute: 0,
            })
        );
        assert_eq!(friendly("0 9 * * 1-5").map(|f| f.tab()), Some(Tab::Days));
        assert_eq!(friendly("0 0 1 * *").map(|f| f.tab()), Some(Tab::Monthly));
    }
}
