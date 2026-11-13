//! Instants on the user's calendar and clock, in the user's time zone.

use rune::time::LocalTime;

pub(crate) const DAY_S: i64 = 86_400;

/// Days since 1970-01-01 on the user's calendar at `ms`.
pub(crate) fn local_day(ms: i64) -> i64 {
    (ms.div_euclid(1000) + i64::from(LocalTime::at(ms).offset)).div_euclid(DAY_S)
}

/// 0 for Sunday: 1970-01-01 was a Thursday.
pub(crate) fn weekday(day: i64) -> i64 {
    (day + 4).rem_euclid(7)
}

/// The instant the user's clock reads `seconds` past midnight on calendar day `day`, across a
/// daylight saving change too.
pub(crate) fn local_instant(day: i64, seconds: i64) -> i64 {
    let local_s = day * DAY_S + seconds;
    let guess = local_s * 1000;
    let first = (local_s - i64::from(LocalTime::at(guess).offset)) * 1000;
    (local_s - i64::from(LocalTime::at(first).offset)) * 1000
}

/// The calendar day a `YYYY-MM-DD` date names, when it names one.
pub(crate) fn day_of_date(date: &str) -> Option<i64> {
    let b = date.trim().as_bytes();
    let digits = |r: std::ops::Range<usize>| {
        b[r].iter().try_fold(0i64, |n, c| {
            c.is_ascii_digit().then(|| n * 10 + i64::from(c - b'0'))
        })
    };
    if b.len() != 10 || b[4] != b'-' || b[7] != b'-' {
        return None;
    }
    let (year, month, day) = (digits(0..4)?, digits(5..7)?, digits(8..10)?);
    let days = days_in_month(year, month)?;
    (1..=days)
        .contains(&day)
        .then(|| days_from_civil(year, month, day))
}

fn days_in_month(year: i64, month: i64) -> Option<i64> {
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
    Some(match month {
        1 | 3 | 5 | 7 | 8 | 10 | 12 => 31,
        4 | 6 | 9 | 11 => 30,
        2 if leap => 29,
        2 => 28,
        _ => return None,
    })
}

fn days_from_civil(year: i64, month: i64, day: i64) -> i64 {
    let y = if month <= 2 { year - 1 } else { year };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = (month + 9) % 12;
    let doy = (153 * mp + 2) / 5 + day - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

pub(crate) fn clock(ms: i64) -> String {
    let local = LocalTime::at(ms);
    format!("{}:{:02}", local.hour, local.minute)
}

/// `ms` as the Tauri app sends an instant, `Date.toISOString`: UTC to the millisecond.
pub(crate) fn iso(ms: i64) -> String {
    let t = LocalTime::at_offset(ms, 0);
    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}.{:03}Z",
        t.year,
        t.month,
        t.day,
        t.hour,
        t.minute,
        t.second,
        ms.rem_euclid(1000)
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_date_names_its_day_and_a_day_that_does_not_exist_names_none() {
        assert_eq!(day_of_date("1970-01-01"), Some(0));
        assert_eq!(day_of_date("2000-03-01"), Some(11_017));
        assert_eq!(day_of_date("2024-02-29"), Some(19_782));
        for bad in [
            "2023-02-29",
            "2026-13-01",
            "2026-00-10",
            "2026-1-01",
            "26-01-01",
            "2026/01/01",
            "",
        ] {
            assert_eq!(day_of_date(bad), None, "{bad}");
        }
    }
}
