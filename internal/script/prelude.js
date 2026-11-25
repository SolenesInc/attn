(call, log, names) => {
  const { parse, stringify } = JSON;
  const compile = eval;
  delete globalThis.SharedArrayBuffer;

  const text = (value) =>
    typeof value === "string" ? value
    : typeof value === "bigint" || value instanceof Error ? String(value)
    : stringify(value) ?? String(value);

  class Refusal extends Error {
    constructor(command, { code, message, fix }, absent) {
      super(absent.length ? message + "\nundefined in the call: " + absent.join(", ") : message);
      this.code = code;
      this.fix = fix;
      this.command = command;
    }
    toString() {
      return this.command + " refused: " + this.code + ": " + this.message;
    }
  }
  Refusal.prototype.name = "Refusal";
  globalThis.Refusal = Refusal;

  const say = (...values) => log(stringify(values.map(text).join(" ")));
  globalThis.console = { log: say, info: say, warn: say, error: say, debug: say };

  for (const name of names) {
    globalThis[name] = {
      [name]: (args = {}, ...rest) => {
        if (rest.length || typeof args !== "object" || args === null || Array.isArray(args)) {
          throw new TypeError(name + " takes one object of named arguments, such as " + name + "({name: value})");
        }
        const answer = parse(call(name, stringify(args)));
        if (answer.refusal) {
          const absent = Object.keys(args).filter((key) => args[key] === undefined);
          throw new Refusal(answer.refusal.command || name, answer.refusal, absent);
        }
        return answer.json ? parse(answer.json) : (answer.text ?? "");
      },
    }[name];
  }

  return (source) =>
    compile("(async () => {" + source + "\n})")()
      .then((value) => (value === undefined ? undefined : stringify(text(value))));
}
