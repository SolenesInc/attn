use crate::go::quote::quote;

struct BodyLine<'a> {
    text: &'a str,
    in_fence: bool,
    heading_level: usize,
    heading: &'a str,
    id: &'a str,
}

struct Part<'a> {
    heading: &'a str,
    start: usize,
    end: usize,
}

struct Outline<'a> {
    heading: &'a str,
    ids: Vec<&'a str>,
}

fn is_blank_byte(byte: u8) -> bool {
    byte == b' ' || byte == b'\t'
}

fn match_heading(line: &str) -> Option<(usize, &str)> {
    let hashes = line.bytes().take_while(|byte| *byte == b'#').count();
    if !(1..=6).contains(&hashes) || !line[hashes..].starts_with([' ', '\t']) {
        return None;
    }
    let heading = line[hashes..]
        .trim_start_matches([' ', '\t'])
        .trim_end_matches([' ', '\t']);
    Some((hashes, heading))
}

fn id_after_bullet(rest: &str) -> Option<&str> {
    let stars = rest.bytes().take_while(|byte| *byte == b'*').count();
    let candidate = &rest[stars..];
    let first = *candidate.as_bytes().first()?;
    if !first.is_ascii_uppercase() {
        return None;
    }
    let digits = candidate[1..]
        .bytes()
        .take_while(u8::is_ascii_digit)
        .count();
    if digits == 0 {
        return None;
    }
    let id_end = 1 + digits;
    let after = candidate[id_end..]
        .bytes()
        .take_while(|byte| *byte == b'*')
        .count();
    let mut tail = candidate[id_end + after..].bytes();
    let closes = tail
        .next()
        .is_some_and(|byte| matches!(byte, b'.' | b':' | b')'));
    let follows = tail
        .next()
        .is_some_and(|byte| is_blank_byte(byte) || byte == b'*');
    (closes && follows).then(|| &candidate[..id_end])
}

fn match_item_id(line: &str) -> Option<&str> {
    let bytes = line.as_bytes();
    if bytes.len() > 1 && matches!(bytes[0], b'-' | b'*' | b'+') && is_blank_byte(bytes[1]) {
        let rest = line[1..].trim_start_matches([' ', '\t']);
        if let Some(id) = id_after_bullet(rest) {
            return Some(id);
        }
    }
    id_after_bullet(line)
}

fn starts_list(line: &str) -> bool {
    let bytes = line.as_bytes();
    if bytes.len() > 1 && matches!(bytes[0], b'-' | b'*' | b'+') && is_blank_byte(bytes[1]) {
        return true;
    }
    let digits = bytes
        .iter()
        .take_while(|byte| byte.is_ascii_digit())
        .count();
    digits > 0
        && matches!(bytes.get(digits), Some(b'.' | b')'))
        && bytes.get(digits + 1).copied().is_some_and(is_blank_byte)
}

fn is_id_ref(reference: &str) -> bool {
    let bytes = reference.as_bytes();
    bytes.len() >= 2 && bytes[0].is_ascii_alphabetic() && bytes[1..].iter().all(u8::is_ascii_digit)
}

fn scan(body: &str) -> Vec<BodyLine<'_>> {
    let mut fence = "";
    body.split('\n')
        .map(|text| {
            let mut line = BodyLine {
                text,
                in_fence: false,
                heading_level: 0,
                heading: "",
                id: "",
            };
            let unindented = text.trim_end_matches('\r');
            let trimmed = unindented.trim_start_matches([' ', '\t']);
            if !fence.is_empty() {
                line.in_fence = true;
                if trimmed.starts_with(fence) {
                    fence = "";
                }
            } else if trimmed.starts_with("```") {
                fence = "```";
            } else if trimmed.starts_with("~~~") {
                fence = "~~~";
            } else if let Some((level, heading)) = match_heading(unindented) {
                line.heading_level = level;
                line.heading = heading;
            } else if let Some(id) = match_item_id(unindented) {
                line.id = id;
            }
            line
        })
        .collect()
}

fn parts<'a>(lines: &[BodyLine<'a>]) -> Vec<Part<'a>> {
    let mut parts: Vec<Part<'a>> = Vec::new();
    for (index, line) in lines.iter().enumerate() {
        if line.heading_level != 1 && line.heading_level != 2 {
            continue;
        }
        if let Some(last) = parts.last_mut() {
            last.end = index;
        }
        parts.push(Part {
            heading: line.heading,
            start: index,
            end: lines.len(),
        });
    }
    parts
}

fn join_lines(lines: &[BodyLine<'_>]) -> String {
    let texts: Vec<&str> = lines.iter().map(|line| line.text).collect();
    texts
        .join("\n")
        .trim_end_matches(['\r', '\n', '\t', ' '])
        .to_string()
}

fn outline<'a>(lines: &[BodyLine<'a>], parts: &[Part<'a>]) -> Vec<Outline<'a>> {
    parts
        .iter()
        .map(|part| {
            let mut ids: Vec<&str> = Vec::new();
            for line in &lines[part.start + 1..part.end] {
                if !line.id.is_empty() && !ids.contains(&line.id) {
                    ids.push(line.id);
                }
            }
            Outline {
                heading: part.heading,
                ids,
            }
        })
        .collect()
}

fn describe(outline: &[Outline<'_>]) -> String {
    if outline.is_empty() {
        return "none, the body has no headings".to_string();
    }
    let described: Vec<String> = outline
        .iter()
        .map(|part| {
            let owned: Vec<String> = part.ids.iter().map(ToString::to_string).collect();
            let ids = id_ranges(&owned);
            if ids.is_empty() {
                part.heading.to_string()
            } else {
                format!("{} {ids}", part.heading)
            }
        })
        .collect();
    described.join(" · ")
}

pub fn id_ranges(ids: &[String]) -> String {
    let mut letters: Vec<char> = Vec::new();
    let mut numbers: Vec<(char, Vec<i64>)> = Vec::new();
    for id in ids {
        let mut chars = id.chars();
        let Some(letter) = chars.next() else {
            continue;
        };
        let Ok(number) = chars.as_str().parse::<i64>() else {
            continue;
        };
        if !letters.contains(&letter) {
            letters.push(letter);
            numbers.push((letter, Vec::new()));
        }
        if let Some((_, values)) = numbers.iter_mut().find(|(held, _)| *held == letter) {
            values.push(number);
        }
    }
    let mut runs = Vec::new();
    for (letter, mut values) in numbers {
        values.sort_unstable();
        values.dedup();
        let mut start = 0;
        while start < values.len() {
            let mut end = start;
            while end + 1 < values.len() && values[end + 1] == values[end] + 1 {
                end += 1;
            }
            match end - start {
                0 => runs.push(format!("{letter}{}", values[start])),
                1 => {
                    runs.push(format!("{letter}{}", values[start]));
                    runs.push(format!("{letter}{}", values[end]));
                }
                _ => runs.push(format!("{letter}{}–{letter}{}", values[start], values[end])),
            }
            start = end + 1;
        }
    }
    runs.join(", ")
}

fn item_end(lines: &[BodyLine<'_>], start: usize) -> usize {
    let list_item = starts_list(lines[start].text);
    for index in start + 1..lines.len() {
        let line = &lines[index];
        if line.in_fence {
            continue;
        }
        if line.heading_level > 0 || !line.id.is_empty() {
            return index;
        }
        let blank = line.text.trim().is_empty();
        let indented = line.text.starts_with([' ', '\t']);
        if blank || indented {
            continue;
        }
        let list = starts_list(line.text);
        if list_item && list {
            return index;
        }
        if (!list || list_item) && lines[index - 1].text.trim().is_empty() {
            return index;
        }
    }
    lines.len()
}

fn items_starting_with(lines: &[BodyLine<'_>], parts: &[Part<'_>], id: &str) -> String {
    let mut blocks = Vec::new();
    let mut last_heading = None;
    for (index, line) in lines.iter().enumerate() {
        if line.id != id {
            continue;
        }
        let owner = parts
            .iter()
            .position(|part| part.start < index && index < part.end);
        if let Some(position) = owner
            && Some(position) != last_heading
        {
            blocks.push(lines[parts[position].start].text.to_string());
            last_heading = Some(position);
        }
        blocks.push(join_lines(&lines[index..item_end(lines, index)]));
    }
    blocks.join("\n\n")
}

pub fn section(body: &str, reference: &str) -> Result<String, String> {
    let reference = reference.trim();
    let lines = scan(body);
    let parts = parts(&lines);
    if is_id_ref(reference) {
        let id = reference.to_uppercase();
        let items = items_starting_with(&lines, &parts, &id);
        if !items.is_empty() {
            return Ok(items);
        }
        return Err(format!(
            "no item starts with {id}; the parts are: {}",
            describe(&outline(&lines, &parts))
        ));
    }
    let wanted = reference.to_lowercase();
    let mut prefixed: Vec<&Part<'_>> = Vec::new();
    for part in &parts {
        let heading = part.heading.to_lowercase();
        if heading == wanted {
            return Ok(join_lines(&lines[part.start..part.end]));
        }
        if !reference.is_empty() && heading.starts_with(&wanted) {
            prefixed.push(part);
        }
    }
    match prefixed.as_slice() {
        [only] => Ok(join_lines(&lines[only.start..only.end])),
        [] => Err(format!(
            "no part matches {}; the parts are: {}",
            quote(reference.as_bytes()),
            describe(&outline(&lines, &parts))
        )),
        many => {
            let headings: Vec<&str> = many.iter().map(|part| part.heading).collect();
            Err(format!(
                "{} begins {} headings: {}; give more of the one you want",
                quote(reference.as_bytes()),
                many.len(),
                headings.join(", ")
            ))
        }
    }
}
