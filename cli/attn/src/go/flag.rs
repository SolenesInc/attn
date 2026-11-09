use crate::go::{duration, quote::quote};
use std::collections::BTreeMap;
use std::fmt;

#[derive(Clone, Copy)]
pub enum FlagDefault<'a> {
    Bool(bool),
    String(&'a str),
    Int(i64),
    Duration(i64),
    Repeated,
}

#[derive(Clone, Copy)]
pub struct Flag<'a> {
    pub name: &'a str,
    pub default: FlagDefault<'a>,
    pub usage: &'a str,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Value {
    Bool(bool),
    String(String),
    Int(i64),
    Duration(i64),
    Repeated(Vec<String>),
}

impl fmt::Display for Value {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Value::Bool(value) => write!(f, "{value}"),
            Value::String(value) => f.write_str(value),
            Value::Int(value) => write!(f, "{value}"),
            Value::Duration(value) => f.write_str(&duration::format(*value)),
            Value::Repeated(values) => f.write_str(&values.join(", ")),
        }
    }
}

impl From<FlagDefault<'_>> for Value {
    fn from(default: FlagDefault<'_>) -> Self {
        match default {
            FlagDefault::Bool(value) => Value::Bool(value),
            FlagDefault::String(value) => Value::String(value.to_owned()),
            FlagDefault::Int(value) => Value::Int(value),
            FlagDefault::Duration(value) => Value::Duration(value),
            FlagDefault::Repeated => Value::Repeated(Vec::new()),
        }
    }
}

#[derive(Debug, PartialEq, Eq)]
pub enum FlagError {
    Help,
    Invalid(String),
}

impl fmt::Display for FlagError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            FlagError::Help => f.write_str("flag: help requested"),
            FlagError::Invalid(message) => f.write_str(message),
        }
    }
}

pub struct FlagSet<'a> {
    name: &'a str,
    flags: BTreeMap<&'a str, Flag<'a>>,
    values: BTreeMap<&'a str, Value>,
    set: Vec<&'a str>,
    rest: Vec<String>,
    output: String,
}

impl<'a> FlagSet<'a> {
    pub fn new(name: &'a str, flags: &[Flag<'a>]) -> Self {
        let flags: BTreeMap<_, _> = flags.iter().map(|flag| (flag.name, *flag)).collect();
        let values = flags
            .values()
            .map(|flag| (flag.name, Value::from(flag.default)))
            .collect();
        Self {
            name,
            flags,
            values,
            set: Vec::new(),
            rest: Vec::new(),
            output: String::new(),
        }
    }

    pub fn parse(&mut self, args: &[String]) -> Result<(), FlagError> {
        self.rest = args.to_vec();
        while self.parse_one()? {}
        Ok(())
    }

    pub fn get(&self, name: &str) -> Option<&Value> {
        self.values.get(name)
    }

    pub fn value(&self, name: &str) -> &Value {
        self.values
            .get(name)
            .unwrap_or_else(|| panic!("flag -{name} is not defined"))
    }

    pub fn bool(&self, name: &str) -> bool {
        match self.value(name) {
            Value::Bool(value) => *value,
            other => panic!("flag -{name} is not a bool: {other:?}"),
        }
    }

    pub fn string(&self, name: &str) -> &str {
        match self.value(name) {
            Value::String(value) => value,
            other => panic!("flag -{name} is not a string: {other:?}"),
        }
    }

    pub fn int(&self, name: &str) -> i64 {
        match self.value(name) {
            Value::Int(value) => *value,
            other => panic!("flag -{name} is not an int: {other:?}"),
        }
    }

    pub fn duration(&self, name: &str) -> i64 {
        match self.value(name) {
            Value::Duration(value) => *value,
            other => panic!("flag -{name} is not a duration: {other:?}"),
        }
    }

    pub fn is_set(&self, name: &str) -> bool {
        self.set.contains(&name)
    }

    pub fn values(&self) -> impl Iterator<Item = (&'a str, &Value)> {
        self.values.iter().map(|(name, value)| (*name, value))
    }

    pub fn set_names(&self) -> &[&'a str] {
        &self.set
    }

    pub fn args(&self) -> &[String] {
        &self.rest
    }

    pub fn output(&self) -> &str {
        &self.output
    }

    fn parse_one(&mut self) -> Result<bool, FlagError> {
        let Some(argument) = self.rest.first().cloned() else {
            return Ok(false);
        };
        if argument.len() < 2 || !argument.starts_with('-') {
            return Ok(false);
        }
        let mut minuses = 1;
        if argument.as_bytes()[1] == b'-' {
            minuses = 2;
            if argument.len() == 2 {
                self.rest.remove(0);
                return Ok(false);
            }
        }
        let body = &argument[minuses..];
        if body.starts_with(['-', '=']) {
            return Err(self.fail(format!("bad flag syntax: {argument}")));
        }
        self.rest.remove(0);
        let (name, inline_value) = match body.find('=') {
            Some(index) => (&body[..index], Some(&body[index + 1..])),
            None => (body, None),
        };
        let Some(flag) = self.flags.get(name).copied() else {
            if name == "help" || name == "h" {
                self.write_usage();
                return Err(FlagError::Help);
            }
            return Err(self.fail(format!("flag provided but not defined: -{name}")));
        };
        if let FlagDefault::Bool(_) = flag.default {
            let raw = inline_value.unwrap_or("true");
            if let Err(error) = self.assign(flag, raw) {
                let message = if inline_value.is_some() {
                    format!(
                        "invalid boolean value {} for -{name}: {error}",
                        quote(raw.as_bytes())
                    )
                } else {
                    format!("invalid boolean flag {name}: {error}")
                };
                return Err(self.fail(message));
            }
        } else {
            let raw = match inline_value {
                Some(value) => value.to_owned(),
                None if self.rest.is_empty() => {
                    return Err(self.fail(format!("flag needs an argument: -{name}")));
                }
                None => self.rest.remove(0),
            };
            if let Err(error) = self.assign(flag, &raw) {
                return Err(self.fail(format!(
                    "invalid value {} for flag -{name}: {error}",
                    quote(raw.as_bytes())
                )));
            }
        }
        if let Err(position) = self.set.binary_search(&flag.name) {
            self.set.insert(position, flag.name);
        }
        Ok(true)
    }

    fn assign(&mut self, flag: Flag<'a>, raw: &str) -> Result<(), &'static str> {
        let (value, error) = match flag.default {
            FlagDefault::Bool(_) => {
                let parsed = parse_bool(raw);
                (Value::Bool(parsed.unwrap_or(false)), parsed.err())
            }
            FlagDefault::String(_) => (Value::String(raw.to_owned()), None),
            FlagDefault::Int(_) => {
                let (parsed, error) = parse_int(raw);
                (Value::Int(parsed), error)
            }
            FlagDefault::Duration(_) => {
                let parsed = duration::parse(raw);
                let error = parsed.is_err().then_some(PARSE_ERROR);
                (Value::Duration(parsed.unwrap_or(0)), error)
            }
            FlagDefault::Repeated => {
                if let Some(Value::Repeated(values)) = self.values.get_mut(flag.name) {
                    values.push(raw.to_owned());
                }
                return Ok(());
            }
        };
        self.values.insert(flag.name, value);
        error.map_or(Ok(()), Err)
    }

    fn fail(&mut self, message: String) -> FlagError {
        self.output.push_str(&message);
        self.output.push('\n');
        self.write_usage();
        FlagError::Invalid(message)
    }

    fn write_usage(&mut self) {
        if self.name.is_empty() {
            self.output.push_str("Usage:\n");
        } else {
            self.output.push_str(&format!("Usage of {}:\n", self.name));
        }
        let usage: String = self.flags.values().map(default_line).collect();
        self.output.push_str(&usage);
    }
}

const PARSE_ERROR: &str = "parse error";
const RANGE_ERROR: &str = "value out of range";

fn default_line(flag: &Flag<'_>) -> String {
    let (value_name, usage) = unquote_usage(flag);
    let mut line = format!("  -{}", flag.name);
    if !value_name.is_empty() {
        line.push(' ');
        line.push_str(&value_name);
    }
    line.push_str(if line.len() <= 4 { "\t" } else { "\n    \t" });
    line.push_str(&usage.replace('\n', "\n    \t"));
    match flag.default {
        FlagDefault::Bool(false)
        | FlagDefault::Int(0)
        | FlagDefault::Duration(0)
        | FlagDefault::Repeated
        | FlagDefault::String("") => {}
        FlagDefault::String(value) => {
            line.push_str(&format!(" (default {})", quote(value.as_bytes())));
        }
        other => line.push_str(&format!(" (default {})", Value::from(other))),
    }
    line.push('\n');
    line
}

fn unquote_usage(flag: &Flag<'_>) -> (String, String) {
    let usage = flag.usage;
    if let Some(open) = usage.find('`')
        && let Some(length) = usage[open + 1..].find('`')
    {
        let close = open + 1 + length;
        let name = &usage[open + 1..close];
        return (
            name.to_owned(),
            format!("{}{name}{}", &usage[..open], &usage[close + 1..]),
        );
    }
    let name = match flag.default {
        FlagDefault::Bool(_) => "",
        FlagDefault::String(_) => "string",
        FlagDefault::Int(_) => "int",
        FlagDefault::Duration(_) => "duration",
        FlagDefault::Repeated => "value",
    };
    (name.to_owned(), usage.to_owned())
}

fn parse_bool(raw: &str) -> Result<bool, &'static str> {
    match raw {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Ok(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Ok(false),
        _ => Err(PARSE_ERROR),
    }
}

fn parse_int(raw: &str) -> (i64, Option<&'static str>) {
    let (negative, digits) = match raw.as_bytes().first() {
        Some(b'-') => (true, &raw[1..]),
        Some(b'+') => (false, &raw[1..]),
        _ => (false, raw),
    };
    let magnitude = match parse_uint_auto_base(digits) {
        Ok(magnitude) => magnitude,
        Err(UintError::Syntax) => return (0, Some(PARSE_ERROR)),
        Err(UintError::Range) => u64::MAX,
    };
    if !negative && magnitude >= 1 << 63 {
        return (i64::MAX, Some(RANGE_ERROR));
    }
    if negative && magnitude > 1 << 63 {
        return (i64::MIN, Some(RANGE_ERROR));
    }
    let signed = if negative {
        0i64.wrapping_sub_unsigned(magnitude)
    } else {
        i64::try_from(magnitude).expect("magnitude is below 2^63")
    };
    (signed, None)
}

enum UintError {
    Syntax,
    Range,
}

fn parse_uint_auto_base(digits: &str) -> Result<u64, UintError> {
    if digits.is_empty() {
        return Err(UintError::Syntax);
    }
    let bytes = digits.as_bytes();
    let (base, body): (u64, &[u8]) = match bytes {
        [b'0', prefix, _, ..] if prefix.eq_ignore_ascii_case(&b'b') => (2, &bytes[2..]),
        [b'0', prefix, _, ..] if prefix.eq_ignore_ascii_case(&b'o') => (8, &bytes[2..]),
        [b'0', prefix, _, ..] if prefix.eq_ignore_ascii_case(&b'x') => (16, &bytes[2..]),
        [b'0', ..] => (8, &bytes[1..]),
        _ => (10, bytes),
    };
    let cutoff = u64::MAX / base + 1;
    let mut value: u64 = 0;
    let mut has_underscore = false;
    for &byte in body {
        let digit = match byte {
            b'_' => {
                has_underscore = true;
                continue;
            }
            b'0'..=b'9' => byte - b'0',
            b'a'..=b'z' => byte - b'a' + 10,
            b'A'..=b'Z' => byte - b'A' + 10,
            _ => return Err(UintError::Syntax),
        };
        if u64::from(digit) >= base {
            return Err(UintError::Syntax);
        }
        if value >= cutoff {
            return Err(UintError::Range);
        }
        value *= base;
        value = value
            .checked_add(u64::from(digit))
            .ok_or(UintError::Range)?;
    }
    if has_underscore && !underscores_are_valid(bytes) {
        return Err(UintError::Syntax);
    }
    Ok(value)
}

fn underscores_are_valid(bytes: &[u8]) -> bool {
    #[derive(PartialEq)]
    enum Seen {
        Start,
        Digit,
        Underscore,
        Other,
    }
    let mut seen = Seen::Start;
    let mut index = 0;
    let mut is_hex = false;
    if let [b'0', prefix, ..] = bytes
        && matches!(prefix.to_ascii_lowercase(), b'b' | b'o' | b'x')
    {
        index = 2;
        seen = Seen::Digit;
        is_hex = prefix.eq_ignore_ascii_case(&b'x');
    }
    for &byte in &bytes[index..] {
        if byte.is_ascii_digit() || (is_hex && byte.is_ascii_hexdigit()) {
            seen = Seen::Digit;
        } else if byte == b'_' {
            if seen != Seen::Digit {
                return false;
            }
            seen = Seen::Underscore;
        } else {
            if seen == Seen::Underscore {
                return false;
            }
            seen = Seen::Other;
        }
    }
    seen != Seen::Underscore
}
