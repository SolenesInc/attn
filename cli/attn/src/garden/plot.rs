use super::json::{Json, Parsed, Syntax, only_space_after, parse_first_value};
use super::{check_id_error, step_slug, valid_check_id};
use crate::go::quote::quote;
use attn_protocol::go_values::{
    MAX_BODY_BYTES, MAX_CHECK_TEXT_CHARS, MAX_CHECKS_PER_SEED, MAX_TITLE_CHARS,
};
use std::collections::HashMap;

const PAYLOAD_SHAPE: &str = r#"{"title": …, "body": …, "part_of": "<plot>", "discovered_from": "<seed>", "children": [{"title": …, "after": ["<sibling slug>"]}]}"#;

#[derive(Debug, Default, Clone)]
pub struct Check {
    pub id: String,
    pub text: String,
    pub shared: bool,
}

#[derive(Debug, Default, Clone)]
pub struct Child {
    pub title: String,
    pub body: String,
    pub checks: Vec<Check>,
    pub after: Vec<String>,
}

#[derive(Debug, Default, Clone)]
pub struct PlotSpec {
    pub title: String,
    pub body: String,
    pub checks: Vec<Check>,
    pub part_of: String,
    pub discovered_from: String,
    pub children: Vec<Child>,
}

pub fn parse_plot_spec(raw: &[u8]) -> Result<PlotSpec, String> {
    if let Some(refused) = refuse_blocks(raw) {
        return Err(refused);
    }
    let parsed = match parse_first_value(raw) {
        Ok(parsed) => parsed,
        Err(Syntax::EndOfInput) => {
            let cause = if raw
                .iter()
                .all(|b| matches!(*b, b' ' | b'\t' | b'\r' | b'\n'))
            {
                "EOF"
            } else {
                "unexpected EOF"
            };
            return Err(not_a_payload(cause));
        }
        Err(Syntax::Invalid(message)) => return Err(not_a_payload(&message)),
    };
    let mut decoder = Decoder::default();
    let spec = decoder.spec(&parsed.value);
    if let Some(error) = decoder.first_error {
        return Err(not_a_payload(&error));
    }
    validate(&spec)?;
    Ok(spec)
}

fn not_a_payload(cause: &str) -> String {
    format!("this is not a plot payload: {cause} (the shape is {PAYLOAD_SHAPE})")
}

fn field<'a>(fields: &'a [(String, Json)], name: &str) -> Option<&'a Json> {
    let mut found = None;
    for (key, value) in fields {
        if key == name || key.eq_ignore_ascii_case(name) {
            found = Some(value);
        }
    }
    found
}

fn lenient_strings(value: &Json) -> Option<Vec<String>> {
    let Json::Array(items) = value else {
        return None;
    };
    items
        .iter()
        .map(|item| match item {
            Json::String(text) => Some(text.clone()),
            Json::Null => Some(String::new()),
            _ => None,
        })
        .collect()
}

fn occurrences<'v>(fields: &'v [(String, Json)], name: &str) -> impl Iterator<Item = &'v Json> {
    let name = name.to_string();
    fields
        .iter()
        .filter(move |(key, _)| key.eq_ignore_ascii_case(&name))
        .map(|(_, item)| item)
}

fn refuse_blocks(raw: &[u8]) -> Option<String> {
    let Parsed { value, rest } = parse_first_value(raw).ok()?;
    only_space_after(raw, rest).ok()?;
    let Json::Object(fields) = value else {
        return None;
    };
    if occurrences(&fields, "children").any(|item| !matches!(item, Json::Array(_) | Json::Null)) {
        return None;
    }
    let Some(Json::Array(children)) = field(&fields, "children") else {
        return None;
    };
    let mut probes = Vec::with_capacity(children.len());
    for child in children {
        let child_fields: &[(String, Json)] = match child {
            Json::Object(child_fields) => child_fields,
            Json::Null => &[],
            _ => return None,
        };
        if occurrences(child_fields, "title")
            .any(|item| !matches!(item, Json::String(_) | Json::Null))
        {
            return None;
        }
        let title = match field(child_fields, "title") {
            None | Some(Json::Null) => String::new(),
            Some(Json::String(title)) => title.clone(),
            Some(_) => return None,
        };
        let blocks = match field(child_fields, "blocks") {
            None | Some(Json::Null) => None,
            Some(value) => Some(lenient_strings(value)?),
        };
        probes.push((title, blocks));
    }
    for (index, (title, blocks)) in probes.iter().enumerate() {
        let Some(blocks) = blocks else {
            continue;
        };
        let rewrite = if blocks.is_empty() {
            "delete it".to_string()
        } else {
            let names: Vec<String> = blocks.iter().map(|b| quote(b.as_bytes())).collect();
            format!(
                r#"add "after": [{}] to {}"#,
                quote(step_slug(title).as_bytes()),
                names.join(" and ")
            )
        };
        return Some(format!(
            r#"child {} ({}) has "blocks", which plot payloads do not take; a child names the siblings it waits for with "after". Here: {rewrite}"#,
            index + 1,
            quote(title.as_bytes())
        ));
    }
    None
}

#[derive(Default)]
struct Decoder {
    first_error: Option<String>,
    path: Vec<String>,
}

fn is_index(token: &str) -> bool {
    !token.is_empty() && token.bytes().all(|byte| byte.is_ascii_digit())
}

impl Decoder {
    fn save(&mut self, error: String) {
        if self.first_error.is_none() {
            self.first_error = Some(error);
        }
    }

    fn mismatch(&mut self, value: &Json, go_type: &str) {
        let location = match self.path.last() {
            None => format!("Go value of type {go_type}"),
            Some(last) => {
                let lead = if is_index(last) {
                    ""
                } else {
                    "Go struct field "
                };
                format!("{lead}PlotSpec.{} of type {go_type}", self.path.join("."))
            }
        };
        self.save(format!(
            "json: cannot unmarshal {} into {location}",
            value.kind()
        ));
    }

    fn at<T>(&mut self, token: String, read: impl FnOnce(&mut Self) -> T) -> T {
        self.path.push(token);
        let value = read(self);
        self.path.pop();
        value
    }

    fn fields<'v>(&mut self, value: &'v Json, go_type: &str) -> &'v [(String, Json)] {
        match value {
            Json::Object(fields) => fields,
            Json::Null => &[],
            other => {
                self.mismatch(other, go_type);
                &[]
            }
        }
    }

    fn known(&mut self, key: &str, known: &[&'static str]) -> Option<&'static str> {
        let name = known
            .iter()
            .find(|name| **name == key)
            .or_else(|| known.iter().find(|name| name.eq_ignore_ascii_case(key)));
        if name.is_none() {
            self.save(format!("json: unknown field {}", quote(key.as_bytes())));
        }
        name.copied()
    }

    fn string(&mut self, value: &Json, into: &mut String) {
        match value {
            Json::String(text) => into.clone_from(text),
            Json::Null => {}
            other => self.mismatch(other, "string"),
        }
    }

    fn spec(&mut self, value: &Json) -> PlotSpec {
        const KNOWN: &[&str] = &[
            "title",
            "body",
            "checks",
            "part_of",
            "discovered_from",
            "children",
        ];
        let mut spec = PlotSpec::default();
        for (key, item) in self.fields(value, "garden.PlotSpec") {
            let name = self.known(key, KNOWN);
            self.at(key.clone(), |this| match name {
                Some("title") => this.string(item, &mut spec.title),
                Some("body") => this.string(item, &mut spec.body),
                Some("part_of") => this.string(item, &mut spec.part_of),
                Some("discovered_from") => this.string(item, &mut spec.discovered_from),
                Some("checks") => spec.checks = this.checks(item),
                Some("children") => spec.children = this.children(item),
                _ => {}
            });
        }
        spec
    }

    fn array<'v>(&mut self, value: &'v Json, go_type: &str) -> &'v [Json] {
        match value {
            Json::Array(items) => items,
            Json::Null => &[],
            other => {
                self.mismatch(other, go_type);
                &[]
            }
        }
    }

    fn checks(&mut self, value: &Json) -> Vec<Check> {
        let items = self.array(value, "[]garden.Check");
        items
            .iter()
            .enumerate()
            .map(|(index, item)| self.at(index.to_string(), |this| this.check(item)))
            .collect()
    }

    fn check(&mut self, value: &Json) -> Check {
        const KNOWN: &[&str] = &["id", "text", "shared", "removed"];
        let mut check = Check::default();
        for (key, item) in self.fields(value, "garden.Check") {
            let name = self.known(key, KNOWN);
            self.at(key.clone(), |this| match name {
                Some("id") => this.string(item, &mut check.id),
                Some("text") => this.string(item, &mut check.text),
                Some("shared") => check.shared = this.boolean(item),
                Some("removed") => {
                    this.boolean(item);
                }
                _ => {}
            });
        }
        check
    }

    fn boolean(&mut self, value: &Json) -> bool {
        match value {
            Json::Bool(on) => *on,
            Json::Null => false,
            other => {
                self.mismatch(other, "bool");
                false
            }
        }
    }

    fn children(&mut self, value: &Json) -> Vec<Child> {
        let items = self.array(value, "[]garden.PlotChildSpec");
        items
            .iter()
            .enumerate()
            .map(|(index, item)| self.at(index.to_string(), |this| this.child(item)))
            .collect()
    }

    fn child(&mut self, value: &Json) -> Child {
        const KNOWN: &[&str] = &["title", "body", "checks", "after"];
        let mut child = Child::default();
        for (key, item) in self.fields(value, "garden.PlotChildSpec") {
            let name = self.known(key, KNOWN);
            self.at(key.clone(), |this| match name {
                Some("title") => this.string(item, &mut child.title),
                Some("body") => this.string(item, &mut child.body),
                Some("checks") => child.checks = this.checks(item),
                Some("after") => child.after = this.after(item),
                _ => {}
            });
        }
        child
    }

    fn after(&mut self, value: &Json) -> Vec<String> {
        let items = self.array(value, "[]string");
        let mut after = Vec::with_capacity(items.len());
        for (index, item) in items.iter().enumerate() {
            let mut text = String::new();
            self.at(index.to_string(), |this| this.string(item, &mut text));
            after.push(text);
        }
        after
    }
}

fn limit(value: i64) -> usize {
    usize::try_from(value).unwrap_or(usize::MAX)
}

pub fn validate_body(body: &str) -> Result<(), String> {
    if body.len() > limit(MAX_BODY_BYTES) {
        return Err(format!(
            "max_body_bytes={MAX_BODY_BYTES}, asked for {}; a seed's body is a plan, not an archive",
            body.len()
        ));
    }
    Ok(())
}

fn validate_plant(title: &str, body: &str) -> Result<(), String> {
    let trimmed = title.trim();
    if trimmed.is_empty() {
        return Err("a seed needs a title: `attn seed plant \"what this is\"`".to_string());
    }
    let chars = trimmed.chars().count();
    if chars > limit(MAX_TITLE_CHARS) {
        return Err(format!(
            "that title is {chars} characters and the limit is {MAX_TITLE_CHARS}; a title names the work in a line — the detail goes in the body (`-m`, or `-m -` to read stdin)"
        ));
    }
    validate_body(body)
}

fn validate_checks(checks: &[Check]) -> Result<(), String> {
    if checks.len() > limit(MAX_CHECKS_PER_SEED) {
        return Err(format!(
            "max_checks_per_seed={MAX_CHECKS_PER_SEED}, asked for {}",
            checks.len()
        ));
    }
    let mut seen = Vec::with_capacity(checks.len());
    for check in checks {
        if !valid_check_id(&check.id) {
            return Err(check_id_error(&check.id));
        }
        if seen.contains(&check.id.as_str()) {
            return Err(format!(
                "{} is defined twice; one id names one statement",
                check.id
            ));
        }
        seen.push(check.id.as_str());
        if check.text.trim().is_empty() {
            return Err(format!(
                "{} needs a statement: what this seed must prove",
                check.id
            ));
        }
        let chars = check.text.chars().count();
        if chars > limit(MAX_CHECK_TEXT_CHARS) {
            return Err(format!(
                "max_check_text_chars={MAX_CHECK_TEXT_CHARS}, and {}'s statement is {chars}; a check is one statement, the detail goes in the body",
                check.id
            ));
        }
    }
    Ok(())
}

fn validate_plot_checks(spec: &PlotSpec) -> Result<(), String> {
    validate_checks(&spec.checks).map_err(|error| format!("crown: {error}"))?;
    let mut used_by: HashMap<&str, String> = spec
        .checks
        .iter()
        .map(|check| (check.id.as_str(), "the crown".to_string()))
        .collect();
    for (index, child) in spec.children.iter().enumerate() {
        let number = index + 1;
        validate_checks(&child.checks).map_err(|error| format!("child {number}: {error}"))?;
        for check in &child.checks {
            if let Some(owner) = used_by.get(check.id.as_str()) {
                return Err(format!(
                    "child {number} and {owner} both define {}; one id names one statement in a plot tree",
                    check.id
                ));
            }
            used_by.insert(&check.id, format!("child {number}"));
        }
    }
    Ok(())
}

fn validate(spec: &PlotSpec) -> Result<(), String> {
    validate_plant(&spec.title, &spec.body).map_err(|error| format!("crown: {error}"))?;
    validate_plot_checks(spec)?;
    if spec.children.is_empty() {
        return Err("a plot is a crown with children and this payload has none; to plant one seed use `attn seed plant`".to_string());
    }
    let mut slugs: HashMap<String, usize> = HashMap::with_capacity(spec.children.len());
    for (index, child) in spec.children.iter().enumerate() {
        validate_plant(&child.title, &child.body)
            .map_err(|error| format!("child {}: {error}", index + 1))?;
        let slug = step_slug(&child.title);
        if let Some(previous) = slugs.get(&slug) {
            return Err(format!(
                "children {} and {} both derive the step slug {}; after addresses siblings by slug, so retitle one",
                previous + 1,
                index + 1,
                quote(slug.as_bytes())
            ));
        }
        slugs.insert(slug, index);
    }
    for (index, child) in spec.children.iter().enumerate() {
        for sibling in &child.after {
            if !slugs.contains_key(&step_slug(sibling)) {
                let all: Vec<String> = spec.children.iter().map(|c| step_slug(&c.title)).collect();
                return Err(format!(
                    "child {} waits for {}, which is no sibling's title or step slug; the slugs here are {}",
                    index + 1,
                    quote(sibling.as_bytes()),
                    all.join(", ")
                ));
            }
        }
    }
    if let Some((from, to)) = after_cycle(&spec.children, &slugs) {
        return Err(format!(
            "{} and {} wait for each other through after, so neither child would ever be ready; sequence one way only",
            quote(from.as_bytes()),
            quote(to.as_bytes())
        ));
    }
    Ok(())
}

#[derive(Clone, Copy, PartialEq)]
enum Visit {
    Unseen,
    Visiting,
    Done,
}

fn after_cycle(children: &[Child], slugs: &HashMap<String, usize>) -> Option<(String, String)> {
    fn walk(
        at: usize,
        children: &[Child],
        slugs: &HashMap<String, usize>,
        state: &mut [Visit],
    ) -> Option<(String, String)> {
        state[at] = Visit::Visiting;
        for sibling in &children[at].after {
            let next = slugs[&step_slug(sibling)];
            if state[next] == Visit::Visiting {
                return Some((
                    step_slug(&children[at].title),
                    step_slug(&children[next].title),
                ));
            }
            if state[next] == Visit::Unseen
                && let Some(found) = walk(next, children, slugs, state)
            {
                return Some(found);
            }
        }
        state[at] = Visit::Done;
        None
    }
    let mut state = vec![Visit::Unseen; children.len()];
    for at in 0..children.len() {
        if state[at] == Visit::Unseen
            && let Some(found) = walk(at, children, slugs, &mut state)
        {
            return Some(found);
        }
    }
    None
}
