//! Instants on the user's calendar and clock, in the user's time zone, with the hours the
//! system writes them in.

use std::cell::Cell;

use rune::time::LocalTime;

/// Whether the system writes the hours of the day from 1 to 12 with AM or PM, or from 0 to 23.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum HourCycle {
    Twelve,
    TwentyFour,
}

thread_local! {
    static HOUR_CYCLE: Cell<Option<HourCycle>> = const { Cell::new(None) };
}

/// Writes clock times in `cycle` on this thread, in place of the system's choice.
pub fn use_hour_cycle(cycle: HourCycle) {
    HOUR_CYCLE.set(Some(cycle));
}

pub(crate) fn hour_cycle() -> HourCycle {
    if let Some(cycle) = HOUR_CYCLE.get() {
        return cycle;
    }
    static SYSTEM: std::sync::OnceLock<HourCycle> = std::sync::OnceLock::new();
    *SYSTEM.get_or_init(system_hour_cycle)
}

#[cfg(target_os = "macos")]
fn system_hour_cycle() -> HourCycle {
    use objc2_foundation::{NSDateFormatter, NSLocale, NSString};
    let locale = NSLocale::currentLocale();
    let format = NSDateFormatter::dateFormatFromTemplate_options_locale(
        &NSString::from_str("j"),
        0,
        Some(&*locale),
    );
    match format {
        Some(format) if format.to_string().contains('a') => HourCycle::Twelve,
        _ => HourCycle::TwentyFour,
    }
}

#[cfg(not(target_os = "macos"))]
fn system_hour_cycle() -> HourCycle {
    let locale = unsafe { libc::newlocale(libc::LC_TIME_MASK, c"".as_ptr(), std::ptr::null_mut()) };
    if locale.is_null() {
        return HourCycle::TwentyFour;
    }
    let format = unsafe { std::ffi::CStr::from_ptr(libc::nl_langinfo_l(libc::T_FMT, locale)) }
        .to_string_lossy()
        .into_owned();
    unsafe { libc::freelocale(locale) };
    cycle_of_time_format(&format)
}

/// The hours a C library time format writes: "%r", "%I", "%l" and "%p" are the 12-hour ones.
#[cfg_attr(target_os = "macos", allow(dead_code))]
fn cycle_of_time_format(format: &str) -> HourCycle {
    match ["%r", "%I", "%l", "%p"].iter().any(|d| format.contains(d)) {
        true => HourCycle::Twelve,
        false => HourCycle::TwentyFour,
    }
}

/// `local`'s hour and minute, and its second when `seconds`, as `cycle` writes them:
/// "6:05 PM" or "18:05".
fn clock_in(local: &LocalTime, cycle: HourCycle, seconds: bool) -> String {
    let second = match seconds {
        true => format!(":{:02}", local.second),
        false => String::new(),
    };
    match cycle {
        HourCycle::TwentyFour => format!("{}:{:02}{second}", local.hour, local.minute),
        HourCycle::Twelve => {
            let hour = match local.hour % 12 {
                0 => 12,
                h => h,
            };
            let half = if local.hour < 12 { "AM" } else { "PM" };
            format!("{hour}:{:02}{second} {half}", local.minute)
        }
    }
}

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

/// `ms`'s hour and minute on the user's clock: "6:05 PM" or "18:05".
pub(crate) fn clock(ms: i64) -> String {
    clock_in(&LocalTime::at(ms), hour_cycle(), false)
}

/// [`clock`] with its hour in two digits, as the Tauri app's `hour: '2-digit'`: "06:05 PM" or
/// "18:05".
pub(crate) fn clock_two_digit(ms: i64) -> String {
    let clock = clock(ms);
    match clock.find(':') {
        Some(1) => format!("0{clock}"),
        _ => clock,
    }
}

/// `ms` on the user's clock as the Tauri app's `toLocaleTimeString` words it: "6:00:00 PM" or
/// "18:00:00".
pub(crate) fn time_of_day(ms: i64) -> String {
    clock_in(&LocalTime::at(ms), hour_cycle(), true)
}

/// `ms` on the user's calendar and clock as the Tauri app's `toLocaleString` words it:
/// "10/18/2026, 6:00:00 PM", or "10/18/2026, 18:00:00" on a 24-hour clock.
pub(crate) fn date_time(ms: i64) -> String {
    let local = LocalTime::at(ms);
    format!(
        "{}/{}/{}, {}",
        local.month,
        local.day,
        local.year,
        time_of_day(ms)
    )
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

/// `ms` as a file name stamps it: UTC to the second, "20261114-050112Z".
pub(crate) fn file_stamp(ms: i64) -> String {
    let t = LocalTime::at_offset(ms, 0);
    format!(
        "{:04}{:02}{:02}-{:02}{:02}{:02}Z",
        t.year, t.month, t.day, t.hour, t.minute, t.second
    )
}

/// Seconds since the Unix epoch of `YYYY-MM-DDTHH:MM:SS` with an optional fraction and a `Z`
/// or `±HH:MM` offset.
pub(crate) fn seconds_of_rfc3339(time: &str) -> Option<f64> {
    let (date, rest) = time.split_once('T')?;
    let mut date = date.split('-').map(|p| p.parse::<i64>().ok());
    let (y, m, d) = (date.next()??, date.next()??, date.next()??);
    let (clock, offset) = match rest.find(['Z', '+', '-']) {
        Some(at) => rest.split_at(at),
        None => (rest, ""),
    };
    let mut clock = clock.split(':');
    let h: i64 = clock.next()?.parse().ok()?;
    let min: i64 = clock.next()?.parse().ok()?;
    let sec: f64 = clock.next().unwrap_or("0").parse().ok()?;
    let offset_seconds = match offset.chars().next() {
        Some(sign @ ('+' | '-')) => {
            let (oh, om) = offset[1..].split_once(':')?;
            let seconds = oh.parse::<i64>().ok()? * 3600 + om.parse::<i64>().ok()? * 60;
            if sign == '+' { seconds } else { -seconds }
        }
        _ => 0,
    };
    let (y, m) = if m <= 2 { (y - 1, m + 9) } else { (y, m - 3) };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let doy = (153 * m + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146_097 + doe - 719_468;
    Some((days * 86_400 + h * 3600 + min * 60 - offset_seconds) as f64 + sec)
}

/// Milliseconds since the Unix epoch of an RFC 3339 time.
pub(crate) fn ms_of_rfc3339(time: &str) -> Option<i64> {
    seconds_of_rfc3339(time).map(|s| (s * 1000.0).round() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_c_library_time_format_names_its_hour_cycle() {
        for (format, cycle) in [
            ("%r", HourCycle::Twelve),
            ("%I:%M:%S %p", HourCycle::Twelve),
            ("%l:%M %p", HourCycle::Twelve),
            ("%T", HourCycle::TwentyFour),
            ("%H:%M:%S", HourCycle::TwentyFour),
            ("%H.%M.%S", HourCycle::TwentyFour),
            ("", HourCycle::TwentyFour),
        ] {
            assert_eq!(cycle_of_time_format(format), cycle, "{format:?}");
        }
    }

    #[test]
    fn a_clock_time_reads_as_its_hour_cycle_writes_it() {
        let at = |hour, minute, second| {
            let mut local = LocalTime::at_offset(0, 0);
            (local.hour, local.minute, local.second) = (hour, minute, second);
            local
        };
        for ((hour, minute, second), seconds, twelve, twenty_four) in [
            ((0, 5, 9), false, "12:05 AM", "0:05"),
            ((9, 30, 0), false, "9:30 AM", "9:30"),
            ((12, 0, 0), false, "12:00 PM", "12:00"),
            ((18, 7, 42), true, "6:07:42 PM", "18:07:42"),
            ((23, 59, 59), true, "11:59:59 PM", "23:59:59"),
        ] {
            let local = at(hour, minute, second);
            assert_eq!(clock_in(&local, HourCycle::Twelve, seconds), twelve);
            assert_eq!(
                clock_in(&local, HourCycle::TwentyFour, seconds),
                twenty_four
            );
        }
    }

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
