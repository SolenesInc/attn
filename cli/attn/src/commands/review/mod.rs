mod help_generated;
mod table_generated;

use super::{Run, Stop, print_stdout};
use crate::client::Reply;
use crate::command::{self, Command, Family, Parsed};
use attn_protocol::{ReviewAnswerResult, ReviewCheckResult, ReviewProblem, ReviewStartResult};
use std::fmt::Write as _;

struct Review;

pub fn run(run: &mut Run<'_>, args: &[String]) -> Result<(), Stop> {
    command::run_group(run, &Review, args)
}

pub fn problem_lines(problems: &[ReviewProblem], prefix: &str) -> String {
    let mut out = String::new();
    for problem in problems {
        if problem.line > 0 {
            let _ = writeln!(out, "{prefix}L{}: {}", problem.line, problem.message);
        } else {
            let _ = writeln!(out, "{prefix}{}", problem.message);
        }
    }
    out
}

fn short(commit: &str) -> &str {
    &commit[..commit.len().min(12)]
}

fn started(result: &ReviewStartResult) -> String {
    let mut out = String::new();
    if result.created {
        let _ = writeln!(out, "created {}", result.document);
    }
    match &result.base {
        Some(base) => {
            let _ = writeln!(
                out,
                "head {} over base {} in {}",
                short(&result.head),
                short(base),
                result.repository
            );
        }
        None => {
            let _ = writeln!(
                out,
                "head {} in {}, with no base: a tour of unchanged code",
                short(&result.head),
                result.repository
            );
        }
    }
    for left in &result.left_out {
        let _ = writeln!(out, "left out of head: {}: {}", left.path, left.reason);
    }
    let _ = writeln!(
        out,
        "Write the review below the frontmatter, then run `attn review check {0}` and `attn open {0}`.",
        result.document
    );
    out
}

impl Family for Review {
    fn group(&self) -> &'static str {
        "review"
    }

    fn flags(&self) -> &'static [&'static str] {
        table_generated::FLAGS
    }

    fn commands(&self) -> &'static [Command] {
        table_generated::COMMANDS
    }

    fn group_help(&self) -> &'static str {
        help_generated::GROUP_HELP
    }

    fn help(&self, verb: &str) -> &'static str {
        help_generated::help(verb)
    }

    fn usage(&self, verb: &str) -> &'static str {
        help_generated::usage(verb)
    }

    fn present_json(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        _parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        command::print_result(run, self, cmd, &result)?;
        if cmd.name == "check" {
            let checked: ReviewCheckResult = command::decode(run, self, cmd, &result)?;
            if !checked.problems.is_empty() {
                return Err(Stop(1));
            }
        }
        Ok(())
    }

    fn present(
        &self,
        run: &mut Run<'_>,
        cmd: &Command,
        parsed: &Parsed,
        reply: &Reply,
    ) -> Result<(), Stop> {
        let result = command::result(reply);
        match cmd.name {
            "start" => print_stdout(&started(&command::decode(run, self, cmd, &result)?)),
            "check" => {
                let checked: ReviewCheckResult = command::decode(run, self, cmd, &result)?;
                if checked.problems.is_empty() {
                    print_stdout(&format!("{}: no problems\n", parsed.text("document")));
                    return Ok(());
                }
                print_stdout(&problem_lines(&checked.problems, ""));
                return Err(Stop(1));
            }
            _ => {
                let answered: ReviewAnswerResult = command::decode(run, self, cmd, &result)?;
                print_stdout(&format!(
                    "answered {} on {}\n",
                    answered.question, answered.document
                ));
            }
        }
        Ok(())
    }
}
