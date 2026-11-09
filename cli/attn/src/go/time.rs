use crate::go::duration::time_quote;
use std::sync::Once;

const RFC3339: &str = "2006-01-02T15:04:05Z07:00";
const RFC3339_NANO: &str = "2006-01-02T15:04:05.999999999Z07:00";
const SECONDS_PER_DAY: i64 = 86_400;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Time {
    unix_seconds: i64,
    nanos: u32,
    offset_seconds: i64,
}

impl Time {
    pub fn parse_rfc3339(value: &str) -> Result<Self, String> {
        Rfc3339Parser {
            layout: RFC3339,
            value,
            rest: value,
        }
        .parse()
    }

    pub fn parse_rfc3339_nano(value: &str) -> Result<Self, String> {
        Rfc3339Parser {
            layout: RFC3339_NANO,
            value,
            rest: value,
        }
        .parse()
    }

    pub fn unix_nanos(&self) -> i128 {
        i128::from(self.unix_seconds) * 1_000_000_000 + i128::from(self.nanos)
    }

    pub fn format(&self, layout: &str) -> String {
        format_fields(
            layout,
            &ZoneFields::at_offset(self.unix_seconds, self.offset_seconds),
            self.nanos,
        )
    }

    pub fn format_local(&self, layout: &str) -> String {
        format_fields(layout, &ZoneFields::local(self.unix_seconds), self.nanos)
    }
}

struct Rfc3339Parser<'a> {
    layout: &'a str,
    value: &'a str,
    rest: &'a str,
}

impl Rfc3339Parser<'_> {
    fn parse(mut self) -> Result<Time, String> {
        let year = self.year()?;
        self.literal("-")?;
        let month = self.number("01", true, 1, 12, "month")?;
        self.literal("-")?;
        let day = self.number("02", true, 0, 99, "day")?;
        self.literal("T")?;
        let hour = self.number("15", false, 0, 23, "hour")?;
        self.literal(":")?;
        let minute = self.number("04", true, 0, 59, "minute")?;
        self.literal(":")?;
        let second = self.number("05", true, 0, 59, "second")?;
        let nanos = self.fraction();
        let offset = self.offset()?;
        if !self.rest.is_empty() {
            return Err(format!(
                "parsing time {}: extra text: {}",
                time_quote(self.value),
                time_quote(self.rest)
            ));
        }
        if day < 1 || day > days_in_month(year, month) {
            return Err(self.out_of_range("day"));
        }
        let unix_seconds = days_from_civil(year, month, day) * SECONDS_PER_DAY
            + hour * 3600
            + minute * 60
            + second
            - offset;
        Ok(Time {
            unix_seconds,
            nanos,
            offset_seconds: offset,
        })
    }

    fn cannot_parse(&self, remaining: &str, element: &str) -> String {
        format!(
            "parsing time {} as {}: cannot parse {} as {}",
            time_quote(self.value),
            time_quote(self.layout),
            time_quote(remaining),
            time_quote(element)
        )
    }

    fn out_of_range(&self, what: &str) -> String {
        format!(
            "parsing time {}: {what} out of range",
            time_quote(self.value)
        )
    }

    fn year(&mut self) -> Result<i64, String> {
        let bytes = self.rest.as_bytes();
        if bytes.len() < 4 || !bytes[..4].iter().all(u8::is_ascii_digit) {
            return Err(self.cannot_parse(self.rest, "2006"));
        }
        let year = bytes[..4]
            .iter()
            .fold(0, |total, byte| total * 10 + i64::from(byte - b'0'));
        self.rest = &self.rest[4..];
        Ok(year)
    }

    fn literal(&mut self, expected: &str) -> Result<(), String> {
        match self.rest.strip_prefix(expected) {
            Some(rest) => {
                self.rest = rest;
                Ok(())
            }
            None => Err(self.cannot_parse(self.rest, expected)),
        }
    }

    fn number(
        &mut self,
        element: &str,
        two_digits: bool,
        low: i64,
        high: i64,
        what: &str,
    ) -> Result<i64, String> {
        let Some((value, consumed)) = leading_number(self.rest.as_bytes(), two_digits) else {
            return Err(self.cannot_parse(self.rest, element));
        };
        if value < low || value > high {
            return Err(self.out_of_range(what));
        }
        self.rest = &self.rest[consumed..];
        Ok(value)
    }

    fn fraction(&mut self) -> u32 {
        let bytes = self.rest.as_bytes();
        if bytes.len() < 2 || !matches!(bytes[0], b'.' | b',') || !bytes[1].is_ascii_digit() {
            return 0;
        }
        let digits = bytes[1..]
            .iter()
            .take_while(|byte| byte.is_ascii_digit())
            .count();
        let kept = &bytes[1..=digits.min(9)];
        let parsed = kept
            .iter()
            .fold(0u32, |total, byte| total * 10 + u32::from(byte - b'0'));
        let nanos = parsed * 10u32.pow(9 - u32::try_from(kept.len()).expect("at most 9"));
        self.rest = &self.rest[1 + digits..];
        nanos
    }

    fn offset(&mut self) -> Result<i64, String> {
        if let Some(rest) = self.rest.strip_prefix('Z') {
            self.rest = rest;
            return Ok(0);
        }
        let hold = self.rest;
        let bytes = hold.as_bytes();
        let fields = (bytes.len() >= 6 && bytes[3] == b':').then(|| {
            (
                two_digit_number(&bytes[1..3]),
                two_digit_number(&bytes[4..6]),
            )
        });
        let (Some((Some(hours), Some(minutes))), sign @ (b'+' | b'-')) =
            (fields, *bytes.first().unwrap_or(&0))
        else {
            return Err(self.cannot_parse(hold, "Z07:00"));
        };
        if hours > 24 {
            return Err(self.out_of_range("time zone offset hour"));
        }
        if minutes > 60 {
            return Err(self.out_of_range("time zone offset minute"));
        }
        self.rest = &hold[6..];
        let seconds = hours * 3600 + minutes * 60;
        Ok(if sign == b'-' { -seconds } else { seconds })
    }
}

fn two_digit_number(bytes: &[u8]) -> Option<i64> {
    match leading_number(bytes, true) {
        Some((value, 2)) => Some(value),
        _ => None,
    }
}

fn leading_number(bytes: &[u8], two_digits: bool) -> Option<(i64, usize)> {
    let first = i64::from(bytes.first().filter(|b| b.is_ascii_digit())? - b'0');
    match bytes.get(1).filter(|b| b.is_ascii_digit()) {
        Some(second) => Some((first * 10 + i64::from(second - b'0'), 2)),
        None if two_digits => None,
        None => Some((first, 1)),
    }
}

fn days_in_month(year: i64, month: i64) -> i64 {
    match month {
        2 if year % 4 == 0 && (year % 100 != 0 || year % 400 == 0) => 29,
        2 => 28,
        4 | 6 | 9 | 11 => 30,
        _ => 31,
    }
}

fn days_from_civil(year: i64, month: i64, day: i64) -> i64 {
    let shifted_year = if month <= 2 { year - 1 } else { year };
    let era = shifted_year.div_euclid(400);
    let year_of_era = shifted_year.rem_euclid(400);
    let shifted_month = (month + 9) % 12;
    let day_of_year = (153 * shifted_month + 2) / 5 + day - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    era * 146_097 + day_of_era - 719_468
}

fn civil_from_days(days: i64) -> (i64, i64, i64) {
    let shifted = days + 719_468;
    let era = shifted.div_euclid(146_097);
    let day_of_era = shifted.rem_euclid(146_097);
    let year_of_era =
        (day_of_era - day_of_era / 1460 + day_of_era / 36_524 - day_of_era / 146_096) / 365;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let shifted_month = (5 * day_of_year + 2) / 153;
    let day = day_of_year - (153 * shifted_month + 2) / 5 + 1;
    let month = if shifted_month < 10 {
        shifted_month + 3
    } else {
        shifted_month - 9
    };
    let year = year_of_era + era * 400 + i64::from(month <= 2);
    (year, month, day)
}

struct ZoneFields {
    year: i64,
    month: usize,
    day: i64,
    hour: i64,
    minute: i64,
    second: i64,
    offset_seconds: i64,
}

static LOAD_LOCAL_ZONE: Once = Once::new();

unsafe extern "C" {
    fn tzset();
}

impl ZoneFields {
    fn at_offset(unix_seconds: i64, offset_seconds: i64) -> Self {
        let wall_clock = unix_seconds + offset_seconds;
        let (year, month, day) = civil_from_days(wall_clock.div_euclid(SECONDS_PER_DAY));
        let second_of_day = wall_clock.rem_euclid(SECONDS_PER_DAY);
        Self {
            year,
            month: usize::try_from(month - 1).expect("month is 1 to 12"),
            day,
            hour: second_of_day / 3600,
            minute: second_of_day / 60 % 60,
            second: second_of_day % 60,
            offset_seconds,
        }
    }

    fn local(unix_seconds: i64) -> Self {
        LOAD_LOCAL_ZONE.call_once(|| unsafe { tzset() });
        let mut fields = unsafe { std::mem::zeroed::<libc::tm>() };
        let converted = unsafe { libc::localtime_r(&raw const unix_seconds, &raw mut fields) };
        assert!(
            !converted.is_null(),
            "localtime_r rejected {unix_seconds} seconds"
        );
        Self {
            year: i64::from(fields.tm_year) + 1900,
            month: usize::try_from(fields.tm_mon).expect("tm_mon is 0 to 11"),
            day: i64::from(fields.tm_mday),
            hour: i64::from(fields.tm_hour),
            minute: i64::from(fields.tm_min),
            second: i64::from(fields.tm_sec),
            offset_seconds: fields.tm_gmtoff,
        }
    }
}

const MONTHS: [&str; 12] = [
    "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
];

const LAYOUT_ELEMENTS: [&str; 11] = [
    "2006",
    "Z07:00",
    ".999999999",
    ".000",
    "Jan",
    "15",
    "01",
    "02",
    "04",
    "05",
    "2",
];

fn format_fields(layout: &str, fields: &ZoneFields, nanos: u32) -> String {
    let mut out = String::with_capacity(layout.len() + 8);
    let mut rest = layout;
    while let Some(next) = rest.chars().next() {
        let Some(element) = LAYOUT_ELEMENTS
            .iter()
            .find(|element| rest.starts_with(**element))
        else {
            out.push(next);
            rest = &rest[next.len_utf8()..];
            continue;
        };
        rest = &rest[element.len()..];
        match *element {
            "2006" => out.push_str(&format!("{:04}", fields.year)),
            "Jan" => out.push_str(MONTHS[fields.month]),
            "01" => out.push_str(&format!("{:02}", fields.month + 1)),
            "02" => out.push_str(&format!("{:02}", fields.day)),
            "2" => out.push_str(&fields.day.to_string()),
            "15" => out.push_str(&format!("{:02}", fields.hour)),
            "04" => out.push_str(&format!("{:02}", fields.minute)),
            "05" => out.push_str(&format!("{:02}", fields.second)),
            ".000" => out.push_str(&format!(".{:03}", nanos / 1_000_000)),
            ".999999999" if nanos != 0 => {
                let digits = format!("{nanos:09}");
                out.push('.');
                out.push_str(digits.trim_end_matches('0'));
            }
            ".999999999" => {}
            "Z07:00" if fields.offset_seconds == 0 => out.push('Z'),
            "Z07:00" => {
                let sign = if fields.offset_seconds < 0 { '-' } else { '+' };
                let total_minutes = fields.offset_seconds.abs() / 60;
                out.push_str(&format!(
                    "{sign}{:02}:{:02}",
                    total_minutes / 60,
                    total_minutes % 60
                ));
            }
            _ => unreachable!("every layout element is handled"),
        }
    }
    out
}
