use attn::go::flag::{Flag, FlagDefault, FlagError, FlagSet};
use attn::go::{base64, duration, json, quote::quote, tabwriter};
use serde_json::Value;
use std::collections::BTreeMap;
use std::fmt::Write as _;
use std::fs;

fn corpus(file: &str) -> Vec<Value> {
    let path = format!("{}/../testdata/{file}", env!("CARGO_MANIFEST_DIR"));
    fs::read_to_string(&path)
        .unwrap_or_else(|error| panic!("{path}: {error}"))
        .lines()
        .map(|line| serde_json::from_str(line).expect("corpus line is JSON"))
        .collect()
}

fn text<'a>(case: &'a Value, field: &str) -> &'a str {
    case[field].as_str().expect("corpus field is a string")
}

fn strings(case: &Value, field: &str) -> Vec<String> {
    case[field]
        .as_array()
        .expect("corpus field is an array")
        .iter()
        .map(|item| item.as_str().expect("string").to_owned())
        .collect()
}

fn decode_hex(hex: &str) -> Vec<u8> {
    (0..hex.len())
        .step_by(2)
        .map(|index| u8::from_str_radix(&hex[index..index + 2], 16).expect("hex"))
        .collect()
}

fn assert_all_match(table: &str, total: usize, mismatches: &[String]) {
    assert!(total > 0, "{table} has no cases");
    assert!(
        mismatches.is_empty(),
        "{table}: {} of {total} cases differ from Go. First:\n{}",
        mismatches.len(),
        mismatches
            .iter()
            .take(5)
            .cloned()
            .collect::<Vec<_>>()
            .join("\n")
    );
}

fn check_text_table(file: &str, render: impl Fn(&str) -> String) {
    let cases = corpus(file);
    let mismatches: Vec<_> = cases
        .iter()
        .filter_map(|case| {
            let actual = render(text(case, "input"));
            (actual != text(case, "output")).then(|| format!("{case} got {actual:?}"))
        })
        .collect();
    assert_all_match(file, cases.len(), &mismatches);
}

#[test]
fn tabwriter_matches_go() {
    check_text_table("tabwriter.jsonl", tabwriter::format);
}

#[test]
fn json_indent_matches_go() {
    check_text_table("json_indent.jsonl", |input| {
        String::from_utf8(json::indent(input.as_bytes())).expect("UTF-8")
    });
}

#[test]
fn json_encoding_of_strings_matches_go() {
    check_text_table("json_encode.jsonl", |input| {
        String::from_utf8(json::encode(input).expect("encodes")).expect("UTF-8")
    });
}

#[test]
fn quote_matches_go() {
    let cases = corpus("quote.jsonl");
    let mismatches: Vec<_> = cases
        .iter()
        .filter_map(|case| {
            let actual = quote(&decode_hex(text(case, "input_hex")));
            (actual != text(case, "output")).then(|| format!("{case} got {actual}"))
        })
        .collect();
    assert_all_match("quote", cases.len(), &mismatches);
}

#[test]
fn parse_duration_matches_go() {
    let cases = corpus("parse_duration.jsonl");
    let mismatches: Vec<_> = cases
        .iter()
        .filter_map(|case| {
            let actual = duration::parse(text(case, "input"));
            let expected = match text(case, "error") {
                "" => Ok(case["nanos"].as_i64().expect("nanos")),
                error => Err(error.to_owned()),
            };
            (actual != expected).then(|| format!("{case} got {actual:?}"))
        })
        .collect();
    assert_all_match("parse_duration", cases.len(), &mismatches);
}

fn hex(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        let _ = write!(out, "{byte:02x}");
    }
    out
}

#[test]
fn base64_decode_matches_go() {
    let cases = corpus("base64.jsonl");
    let mismatches: Vec<_> = cases
        .iter()
        .filter_map(|case| {
            let actual = match base64::decode(text(case, "input")) {
                Ok(bytes) => hex(&bytes),
                Err(error) => error.to_string(),
            };
            let expected = match text(case, "error") {
                "" => text(case, "output"),
                error => error,
            };
            (actual != expected).then(|| format!("{case} got {actual}"))
        })
        .collect();
    assert_all_match("base64", cases.len(), &mismatches);
}

#[test]
fn duration_string_matches_go() {
    let cases = corpus("duration_string.jsonl");
    let mismatches: Vec<_> = cases
        .iter()
        .filter_map(|case| {
            let actual = duration::format(case["nanos"].as_i64().expect("nanos"));
            (actual != text(case, "output")).then(|| format!("{case} got {actual}"))
        })
        .collect();
    assert_all_match("duration_string", cases.len(), &mismatches);
}

struct SpecFlag {
    name: String,
    kind: String,
    default: String,
    usage: String,
}

fn flag_spec() -> Vec<SpecFlag> {
    let path = format!("{}/../testdata/flag_spec.json", env!("CARGO_MANIFEST_DIR"));
    let spec: Vec<Value> =
        serde_json::from_str(&fs::read_to_string(path).expect("flag spec")).expect("JSON");
    spec.iter()
        .map(|entry| SpecFlag {
            name: text(entry, "name").to_owned(),
            kind: text(entry, "kind").to_owned(),
            default: text(entry, "default").to_owned(),
            usage: text(entry, "usage").to_owned(),
        })
        .collect()
}

fn to_flag(spec: &SpecFlag) -> Flag<'_> {
    let default = match spec.kind.as_str() {
        "bool" => FlagDefault::Bool(spec.default == "true"),
        "string" => FlagDefault::String(&spec.default),
        "int" | "int64" => FlagDefault::Int(spec.default.parse().expect("int default")),
        "duration" => FlagDefault::Duration(duration::parse(&spec.default).expect("duration")),
        other => panic!("unknown flag kind {other}"),
    };
    Flag {
        name: &spec.name,
        default,
        usage: &spec.usage,
    }
}

#[test]
fn flag_parsing_matches_go() {
    let spec = flag_spec();
    let flags: Vec<_> = spec.iter().map(to_flag).collect();
    let cases = corpus("flag.jsonl");
    let mismatches: Vec<_> = cases
        .iter()
        .filter_map(|case| {
            let mut set = FlagSet::new("cmd", &flags);
            let error = match set.parse(&strings(case, "args")) {
                Ok(()) => String::new(),
                Err(error @ (FlagError::Help | FlagError::Invalid(_))) => error.to_string(),
            };
            let values: BTreeMap<String, String> = set
                .values()
                .map(|(name, value)| (name.to_owned(), value.to_string()))
                .collect();
            let expected_values: BTreeMap<String, String> =
                serde_json::from_value(case["values"].clone()).expect("values");
            let names: Vec<String> = set.set_names().iter().map(ToString::to_string).collect();
            let matches = error == text(case, "error")
                && set.output() == text(case, "output")
                && values == expected_values
                && names == strings(case, "set")
                && set.args() == strings(case, "rest");
            (!matches).then(|| {
                format!(
                    "{case}\n  got error={error:?} output={:?} values={values:?} set={names:?} rest={:?}",
                    set.output(),
                    set.args()
                )
            })
        })
        .collect();
    assert_all_match("flag", cases.len(), &mismatches);
}
