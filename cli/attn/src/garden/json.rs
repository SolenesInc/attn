use crate::go::quote::{is_printable, quote};

#[derive(Debug, Clone, PartialEq)]
pub enum Json {
    Null,
    Bool(bool),
    Number,
    String(String),
    Array(Vec<Json>),
    Object(Vec<(String, Json)>),
}

impl Json {
    pub fn kind(&self) -> &'static str {
        match self {
            Json::Null => "null",
            Json::Bool(_) => "bool",
            Json::Number => "number",
            Json::String(_) => "string",
            Json::Array(_) => "array",
            Json::Object(_) => "object",
        }
    }
}

pub enum Syntax {
    EndOfInput,
    Invalid(String),
}

pub struct Parsed {
    pub value: Json,
    pub rest: usize,
}

struct Scanner<'a> {
    bytes: &'a [u8],
    at: usize,
}

pub fn parse_first_value(bytes: &[u8]) -> Result<Parsed, Syntax> {
    let mut scanner = Scanner { bytes, at: 0 };
    let value = scanner.value("looking for beginning of value")?;
    Ok(Parsed {
        value,
        rest: scanner.at,
    })
}

pub fn only_space_after(bytes: &[u8], at: usize) -> Result<(), String> {
    match bytes[at..]
        .iter()
        .position(|byte| !is_space(*byte))
        .map(|offset| at + offset)
    {
        Some(position) => Err(format!(
            "invalid character {} after top-level value",
            quote_rune(&bytes[position..])
        )),
        None => Ok(()),
    }
}

fn replace_invalid_utf8(mut bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len());
    loop {
        match std::str::from_utf8(bytes) {
            Ok(valid) => {
                out.push_str(valid);
                return out;
            }
            Err(error) => {
                let (valid, rest) = bytes.split_at(error.valid_up_to());
                out.push_str(std::str::from_utf8(valid).unwrap_or_default());
                out.push('\u{fffd}');
                bytes = &rest[1..];
            }
        }
    }
}

fn is_space(byte: u8) -> bool {
    matches!(byte, b' ' | b'\t' | b'\r' | b'\n')
}

fn quote_rune(bytes: &[u8]) -> String {
    let valid = match std::str::from_utf8(bytes) {
        Ok(text) => text,
        Err(error) => std::str::from_utf8(&bytes[..error.valid_up_to()]).unwrap_or_default(),
    };
    match valid.chars().next() {
        None => format!("'\\x{:x}'", bytes.first().copied().unwrap_or_default()),
        Some('\'') => r"'\''".to_string(),
        Some('"') => "'\"'".to_string(),
        Some(first) => {
            let quoted = quote(first.to_string().as_bytes());
            format!("'{}'", &quoted[1..quoted.len() - 1])
        }
    }
}

fn invalid_escape(what: &[u8]) -> Syntax {
    let text = String::from_utf8_lossy(what);
    let plain = !text
        .chars()
        .any(|c| c == '`' || c == '\u{fffd}' || c.is_whitespace() || !is_printable(c));
    let shown = if plain {
        format!("`{text}`")
    } else {
        quote(what)
    };
    let label = if what.len() > 6 {
        "surrogate pair"
    } else {
        "escape sequence"
    };
    Syntax::Invalid(format!("invalid {label} {shown} in string"))
}

impl Scanner<'_> {
    fn next(&mut self) -> Result<u8, Syntax> {
        let byte = *self.bytes.get(self.at).ok_or(Syntax::EndOfInput)?;
        self.at += 1;
        Ok(byte)
    }

    fn next_non_space(&mut self) -> Result<u8, Syntax> {
        loop {
            let byte = self.next()?;
            if !is_space(byte) {
                return Ok(byte);
            }
        }
    }

    fn fail<T>(&self, context: &str) -> Result<T, Syntax> {
        Err(Syntax::Invalid(format!(
            "invalid character {} {context}",
            quote_rune(&self.bytes[self.at - 1..])
        )))
    }

    fn value(&mut self, context: &str) -> Result<Json, Syntax> {
        let first = self.next_non_space()?;
        self.value_from(first, context)
    }

    fn value_from(&mut self, first: u8, context: &str) -> Result<Json, Syntax> {
        match first {
            b'{' => self.object(),
            b'[' => self.array(),
            b'"' => self.string().map(Json::String),
            b't' => self.literal(b"rue", "true").map(|()| Json::Bool(true)),
            b'f' => self.literal(b"alse", "false").map(|()| Json::Bool(false)),
            b'n' => self.literal(b"ull", "null").map(|()| Json::Null),
            b'-' | b'0'..=b'9' => self.number(first).map(|()| Json::Number),
            _ => self.fail(context),
        }
    }

    fn literal(&mut self, expected: &[u8], word: &str) -> Result<(), Syntax> {
        for &want in expected {
            let byte = self.next()?;
            if byte != want {
                return self.fail(&format!(
                    "in literal {word} (expecting {})",
                    quote_rune(&[want])
                ));
            }
        }
        Ok(())
    }

    fn digits(&mut self) {
        while self.bytes.get(self.at).is_some_and(u8::is_ascii_digit) {
            self.at += 1;
        }
    }

    fn number(&mut self, first: u8) -> Result<(), Syntax> {
        let mut leading = first;
        if first == b'-' {
            leading = self.next()?;
            if !leading.is_ascii_digit() {
                return self.fail("in numeric literal");
            }
        }
        if leading != b'0' {
            self.digits();
        }
        if self.bytes.get(self.at) == Some(&b'.') {
            self.at += 1;
            let byte = self.next()?;
            if !byte.is_ascii_digit() {
                return self.fail("in numeric literal");
            }
            self.digits();
        }
        if matches!(self.bytes.get(self.at), Some(b'e' | b'E')) {
            self.at += 1;
            let mut byte = self.next()?;
            if matches!(byte, b'+' | b'-') {
                byte = self.next()?;
            }
            if !byte.is_ascii_digit() {
                return self.fail("in numeric literal");
            }
            self.digits();
        }
        Ok(())
    }

    fn hex4(&self, at: usize) -> Option<u32> {
        let digits = std::str::from_utf8(self.bytes.get(at..at + 4)?).ok()?;
        (digits.bytes().all(|byte| byte.is_ascii_hexdigit()))
            .then(|| u32::from_str_radix(digits, 16).ok())
            .flatten()
    }

    fn string(&mut self) -> Result<String, Syntax> {
        let mut out = Vec::new();
        loop {
            let byte = self.next()?;
            match byte {
                b'"' => return Ok(replace_invalid_utf8(&out)),
                b'\\' => self.escape(&mut out)?,
                control if control < 0x20 => {
                    return self.fail("in string");
                }
                other => out.push(other),
            }
        }
    }

    fn escape(&mut self, out: &mut Vec<u8>) -> Result<(), Syntax> {
        let byte = self.next()?;
        let plain = match byte {
            b'b' => 0x08,
            b'f' => 0x0c,
            b'n' => b'\n',
            b'r' => b'\r',
            b't' => b'\t',
            b'\\' | b'/' | b'"' => byte,
            b'u' => {
                let decoded = self.unicode_escape()?;
                let mut buffer = [0; 4];
                out.extend_from_slice(decoded.encode_utf8(&mut buffer).as_bytes());
                return Ok(());
            }
            _ => return Err(invalid_escape(&self.bytes[self.at - 2..self.at])),
        };
        out.push(plain);
        Ok(())
    }

    fn unicode_escape(&mut self) -> Result<char, Syntax> {
        let backslash = self.at - 2;
        let rest = &self.bytes[backslash..];
        if rest.len() < 6 {
            if rest[2..].iter().all(u8::is_ascii_hexdigit) {
                return Err(Syntax::EndOfInput);
            }
            return Err(invalid_escape(rest));
        }
        let Some(high) = self.hex4(self.at) else {
            return Err(invalid_escape(&rest[..6]));
        };
        self.at += 4;
        if (0xd800..0xdc00).contains(&high)
            && self.bytes.get(self.at..self.at + 2) == Some(br"\u")
            && let Some(low) = self.hex4(self.at + 2)
            && (0xdc00..0xe000).contains(&low)
        {
            self.at += 6;
            let code = 0x1_0000 + ((high - 0xd800) << 10) + (low - 0xdc00);
            return Ok(char::from_u32(code).unwrap_or('\u{fffd}'));
        }
        Ok(char::from_u32(high).unwrap_or('\u{fffd}'))
    }

    fn array(&mut self) -> Result<Json, Syntax> {
        let mut items = Vec::new();
        let first = self.next_non_space()?;
        if first == b']' {
            return Ok(Json::Array(items));
        }
        items.push(self.value_from(first, "looking for beginning of value")?);
        loop {
            match self.next_non_space()? {
                b',' => items.push(self.value("looking for beginning of value")?),
                b']' => return Ok(Json::Array(items)),
                _ => return self.fail("after array element"),
            }
        }
    }

    fn object(&mut self) -> Result<Json, Syntax> {
        let mut fields = Vec::new();
        let first = self.next_non_space()?;
        if first == b'}' {
            return Ok(Json::Object(fields));
        }
        let mut key_start = first;
        loop {
            if key_start != b'"' {
                return self.fail("looking for beginning of object key string");
            }
            let key = self.string()?;
            let colon = self.next_non_space()?;
            if colon != b':' {
                return self.fail("after object key");
            }
            let value = self.value("looking for beginning of value")?;
            fields.push((key, value));
            match self.next_non_space()? {
                b',' => key_start = self.next_non_space()?,
                b'}' => return Ok(Json::Object(fields)),
                _ => return self.fail("after object key:value pair"),
            }
        }
    }
}
