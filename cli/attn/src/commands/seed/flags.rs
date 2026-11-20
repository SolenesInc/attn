use super::table_generated::COMMANDS;
use crate::command::{Command, flags_of};
use attn::go::flag::{FlagError, FlagSet, Value};

pub struct Flags {
    pub verb: &'static Command,
    fs: FlagSet<'static>,
    pub caller: Option<(String, String)>,
}

pub enum ParseStop {
    Help,
    Invalid(String),
}

const GROUP: Command = Command {
    name: "",
    read_only: false,
    result: false,
    wrong_args: "",
    no_args: "",
    args: &[],
};

pub fn verb_named(name: &str) -> &'static Command {
    COMMANDS
        .iter()
        .find(|verb| verb.name == name)
        .unwrap_or(&GROUP)
}

impl Flags {
    pub fn new(name: &str) -> Self {
        let verb = verb_named(name);
        Self {
            verb,
            fs: FlagSet::new("", &flags_of(verb)),
            caller: None,
        }
    }

    pub fn parse(&mut self, args: &[String]) -> Result<Vec<String>, ParseStop> {
        let mut positionals = Vec::new();
        let mut rest = args.to_vec();
        loop {
            match self.fs.parse(&rest) {
                Ok(()) => {}
                Err(FlagError::Help) => return Err(ParseStop::Help),
                Err(FlagError::Invalid(message)) => return Err(ParseStop::Invalid(message)),
            }
            rest = self.fs.args().to_vec();
            if rest.is_empty() {
                return Ok(positionals);
            }
            positionals.push(rest.remove(0));
        }
    }

    pub fn string(&self, name: &str) -> &str {
        match self.fs.get(name) {
            Some(Value::String(value)) => value,
            _ => "",
        }
    }

    pub fn on(&self, name: &str) -> bool {
        matches!(self.fs.get(name), Some(Value::Bool(true)))
    }

    pub fn int(&self, name: &str) -> i64 {
        match self.fs.get(name) {
            Some(Value::Int(value)) => *value,
            _ => 0,
        }
    }

    pub fn repeated(&self, name: &str) -> &[String] {
        match self.fs.get(name) {
            Some(Value::Repeated(values)) => values,
            _ => &[],
        }
    }

    pub fn was_set(&self, name: &str) -> bool {
        self.fs.is_set(name)
    }
}
