use attn_term::blocks::{Block, Blocks};
use attn_term::{PromptStep, Vt};
use serde_json::Value;

fn corpus(name: &str) -> Value {
    let path = format!(
        "{}/../../../internal/pty/testdata/{name}",
        env!("CARGO_MANIFEST_DIR")
    );
    serde_json::from_str(&std::fs::read_to_string(&path).expect("the corpus is checked in"))
        .expect("the corpus is JSON")
}

fn marker(step: &PromptStep) -> Value {
    match step {
        PromptStep::PromptStart => serde_json::json!({"kind": "prompt-start"}),
        PromptStep::InputStart => serde_json::json!({"kind": "input-start"}),
        PromptStep::OutputStart(None) => serde_json::json!({"kind": "pre-exec"}),
        PromptStep::OutputStart(Some(cmdline)) => {
            serde_json::json!({"kind": "pre-exec", "cmdline": cmdline})
        }
        PromptStep::CommandEnd(None) => serde_json::json!({"kind": "command-end"}),
        PromptStep::CommandEnd(Some(code)) => {
            serde_json::json!({"kind": "command-end", "exitCode": code})
        }
    }
}

#[test]
fn the_terminal_reads_the_daemons_osc_133_corpus_into_the_same_markers() {
    for case in corpus("osc133_segmenter_corpus.json")["cases"]
        .as_array()
        .unwrap()
    {
        let mut vt = Vt::new(80, 24);
        let mut got = Vec::new();
        for chunk in case["chunks"].as_array().unwrap() {
            vt.write(chunk.as_str().unwrap().as_bytes());
            got.extend(vt.take_prompt_events().iter().map(|e| marker(&e.step)));
        }
        assert_eq!(Value::from(got), case["markers"], "{}", case["name"]);
    }
}

fn step(step: &Value) -> PromptStep {
    match step["marker"].as_str().unwrap() {
        "prompt-start" => PromptStep::PromptStart,
        "input-start" => PromptStep::InputStart,
        "pre-exec" => PromptStep::OutputStart(step["cmdline"].as_str().map(str::to_owned)),
        "command-end" => PromptStep::CommandEnd(step["exitCode"].as_i64().map(|c| c as i32)),
        other => panic!("no marker {other}"),
    }
}

fn steps(case: &Value) -> Vec<Value> {
    if let Some(steps) = case["steps"].as_array() {
        return steps.clone();
    }
    let g = &case["generate"];
    let prefix = g["commandPrefix"].as_str().unwrap();
    let rows = g["rowsPerCycle"].as_u64().unwrap();
    (0..g["fullCycles"].as_u64().unwrap())
        .flat_map(|i| {
            let base = i * rows;
            [
                serde_json::json!({"marker": "prompt-start", "row": base, "col": 0}),
                serde_json::json!({"marker": "pre-exec", "row": base + 1, "col": 0, "cmdline": format!("{prefix}{i}")}),
                serde_json::json!({"marker": "command-end", "row": base + 2, "col": 0, "exitCode": 0}),
            ]
        })
        .collect()
}

/// A block as the corpus writes it.
fn norm(block: &Block<(u32, u16)>) -> Value {
    let mut out = serde_json::json!({
        "id": block.id,
        "promptRow": block.prompt.0,
        "command": block.command.clone().unwrap_or_default(),
    });
    if let Some((row, col)) = block.input {
        out["inputRow"] = row.into();
        out["inputCol"] = col.into();
    }
    if let Some((row, _)) = block.output {
        out["outputStartRow"] = row.into();
    }
    if let Some((row, _)) = block.end {
        out["endRow"] = row.into();
    }
    if let Some(code) = block.exit_code {
        out["exitCode"] = code.into();
    }
    out
}

#[test]
fn the_blocks_follow_the_shared_osc_133_block_corpus() {
    for case in corpus("osc133_block_corpus.json")["cases"]
        .as_array()
        .unwrap()
    {
        let name = case["name"].as_str().unwrap();
        let mut blocks = Blocks::<(u32, u16)>::default();
        for s in steps(case) {
            let at = (
                s["row"].as_u64().unwrap() as u32,
                s["col"].as_u64().unwrap() as u16,
            );
            blocks.apply(step(&s), at, |p| Some(*p));
        }
        let expect = &case["expect"];
        let got: Vec<Value> = blocks.blocks().iter().map(norm).collect();
        if let Some(completed) = expect.get("completed") {
            assert_eq!(&Value::from(got.clone()), completed, "{name}");
        }
        let field = |i: usize, f: &str| got.get(i).map(|b| b[f].clone()).unwrap_or(Value::Null);
        let last = got.len().saturating_sub(1);
        for (key, value) in [
            ("completedCount", Value::from(got.len())),
            ("firstId", field(0, "id")),
            ("lastId", field(last, "id")),
            ("firstCommand", field(0, "command")),
            ("lastCommand", field(last, "command")),
            ("firstPromptRow", field(0, "promptRow")),
        ] {
            if let Some(want) = expect.get(key) {
                assert_eq!(&value, want, "{name} {key}");
            }
        }
        let pending = blocks.pending().map(norm).unwrap_or(Value::Null);
        let want = expect["pending"].clone();
        let want = match want {
            Value::Object(mut b) => {
                b.entry("command").or_insert_with(|| "".into());
                Value::Object(b)
            }
            other => other,
        };
        assert_eq!(pending, want, "{name} pending");
        assert_eq!(blocks.next_id(), expect["nextId"], "{name} nextId");
    }
}
