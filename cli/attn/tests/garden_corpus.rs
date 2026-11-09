use attn::garden::plot::{Check, Child, PlotSpec, parse_plot_spec};
use attn::garden::section::section;
use attn::garden::{check_id_error, step_slug, valid_check_id};
use serde_json::{Value, json};
use std::fs;

fn lines(path: &str) -> Vec<Value> {
    let path = format!("{}/{path}", env!("CARGO_MANIFEST_DIR"));
    fs::read_to_string(&path)
        .unwrap_or_else(|error| panic!("{path}: {error}"))
        .lines()
        .map(|line| serde_json::from_str(line).expect("corpus line is JSON"))
        .collect()
}

fn checks_json(checks: &[Check]) -> Value {
    checks
        .iter()
        .map(|check| json!({"id": check.id, "text": check.text, "shared": check.shared}))
        .collect()
}

fn child_json(child: &Child) -> Value {
    json!({"title": child.title, "body": child.body, "checks": checks_json(&child.checks), "after": child.after})
}

fn spec_json(spec: &PlotSpec) -> Value {
    json!({
        "title": spec.title,
        "body": spec.body,
        "checks": checks_json(&spec.checks),
        "part_of": spec.part_of,
        "discovered_from": spec.discovered_from,
        "children": spec.children.iter().map(child_json).collect::<Value>(),
    })
}

#[test]
fn plot_payloads_parse_and_refuse_as_specified() {
    let cases = lines("testdata/garden/plot_spec.jsonl");
    assert!(cases.len() > 4000, "the corpus is missing cases");
    let mut wrong = Vec::new();
    for case in &cases {
        let input: Vec<u8> = case["input"]
            .as_array()
            .expect("input bytes")
            .iter()
            .map(|byte| u8::try_from(byte.as_u64().expect("byte")).expect("byte"))
            .collect();
        let want_error = case["error"].as_str().expect("error text");
        let got = parse_plot_spec(&input);
        let matched = match &got {
            Ok(spec) => want_error.is_empty() && spec_json(spec) == case["spec"],
            Err(error) => error == want_error,
        };
        if !matched {
            wrong.push(format!(
                "{}\n  want {want_error:?} {}\n  got  {got:?}",
                String::from_utf8_lossy(&input),
                case["spec"]
            ));
        }
    }
    assert!(
        wrong.is_empty(),
        "{} of {} differ, first:\n{}",
        wrong.len(),
        cases.len(),
        wrong
            .iter()
            .take(12)
            .cloned()
            .collect::<Vec<_>>()
            .join("\n")
    );
}

#[test]
fn a_body_over_the_limit_is_refused_with_the_limit() {
    let at_limit = format!(
        r#"{{"title":"t","body":"{}","children":[{{"title":"a"}}]}}"#,
        "x".repeat(1_048_576)
    );
    assert!(parse_plot_spec(at_limit.as_bytes()).is_ok());
    let over = at_limit.replacen('x', "xx", 1);
    let error = parse_plot_spec(over.as_bytes()).expect_err("over the limit");
    assert!(
        error.starts_with(
            "crown: max_body_bytes=1048576, asked for 1048577; a seed's body is a plan"
        ),
        "{error}"
    );
}

#[test]
fn body_sections_resolve_as_specified() {
    let cases = lines("testdata/garden/section.jsonl");
    let mut wrong = Vec::new();
    for case in &cases {
        let (body, reference) = (
            case["body"].as_str().expect("body"),
            case["ref"].as_str().expect("ref"),
        );
        let want = match case["error"].as_str().expect("error") {
            "" => Ok(case["text"].as_str().expect("text").to_string()),
            error => Err(error.to_string()),
        };
        let got = section(body, reference);
        if got != want {
            wrong.push(format!(
                "{body:?} ref {reference:?}\n  want {want:?}\n  got  {got:?}"
            ));
        }
    }
    assert!(
        wrong.is_empty(),
        "{} of {} differ, first:\n{}",
        wrong.len(),
        cases.len(),
        wrong
            .iter()
            .take(12)
            .cloned()
            .collect::<Vec<_>>()
            .join("\n")
    );
}

#[test]
fn step_slugs_match_the_daemon() {
    for case in lines("../testdata/step_slug.jsonl") {
        let title = case["title"].as_str().expect("title");
        assert_eq!(
            step_slug(title),
            case["slug"].as_str().expect("slug"),
            "{title:?}"
        );
    }
}

#[test]
fn check_ids_are_judged_like_the_daemon() {
    for case in lines("../testdata/check_id.jsonl") {
        let id = case["id"].as_str().expect("id");
        let want = case["error"].as_str().expect("error");
        let got = if valid_check_id(id) {
            String::new()
        } else {
            check_id_error(id)
        };
        assert_eq!(got, want, "{id:?}");
    }
}
