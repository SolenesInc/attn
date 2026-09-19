// The corpus of codex-rs/shell-command/src/command_safety/is_dangerous_command.rs
// `mod tests`. The PowerShell case is not ported: it only fires on Windows.
import { beforeAll, describe, expect, test } from "bun:test";
import { dangerousCommandMatch, initShellParsing, isDangerousCommand } from "../shell/index";

const maxDangerousCommandWrapperDepth = 8;

beforeAll(async () => {
  await initShellParsing();
});

describe("dangerous command match", () => {
  test("rm -rf is dangerous", () => {
    expect(dangerousCommandMatch(["rm", "-rf", "/"])).toBe("forced-rm");
  });

  test("rm -f is dangerous", () => {
    expect(dangerousCommandMatch(["rm", "-f", "/"])).toBe("forced-rm");
  });

  test("forced rm variants are dangerous", () => {
    const commands = [
      ["/bin/rm", "-fr", "/tmp/example"],
      ["rm", "-r", "-f", "/tmp/example"],
      ["rm", "--force", "/tmp/example"],
      ["rm", "/tmp/example", "-f"],
      ["sudo", "rm", "-rf", "/tmp/example"],
      ["env", "TARGET=/tmp/example", "rm", "-rf", "/tmp/example"],
    ];
    for (const command of commands) expect(dangerousCommandMatch(command)).toBe("forced-rm");
  });

  test("deeply nested command wrappers fail closed", () => {
    for (const [depth, expected] of [
      [maxDangerousCommandWrapperDepth, "forced-rm"],
      [maxDangerousCommandWrapperDepth + 1, "other"],
    ] as const) {
      const command = [...Array.from({ length: depth }, () => "env"), "rm", "-rf", "/tmp/example"];
      expect(dangerousCommandMatch(command)).toBe(expected);
    }
  });

  test("forced rm in complex shell syntax is dangerous", () => {
    const scripts = [
      "printf x | rm -rf /tmp/example",
      "if test -d /tmp/example; then rm --force /tmp/example; fi",
      'rm -rf "$TARGET" >/dev/null',
      'for target in /tmp/a /tmp/b; do rm -r -f "$target"; done',
      'echo "$(rm -rf /tmp/example)"',
      "bash -c 'rm -rf /tmp/example'",
      "trap 'rm -rf /tmp/example' EXIT",
      "for a in '-C5a25KeRr' '--' '--json' '--bogus'; do HOME=$(mktemp -d) MDE_URL=http://127.0.0.1:1 MDE_TOKEN=x node cli/mde.cjs ls \"$a\" >/tmp/mde-review-out 2>/tmp/mde-review-err; code=$?; printf '%s\\t%s\\t%s\\n' \"$a\" \"$code\" \"$(tr '\\n' ' ' </tmp/mde-review-err)\"; rm -rf \"$HOME\"; done",
    ];
    for (const script of scripts) {
      expect(dangerousCommandMatch(["bash", "-lc", script])).toBe("forced-rm");
    }
  });

  test("non forced or non literal rm is not dangerous", () => {
    const commands = [
      ["rm", "-r", "/tmp/example"],
      ["rm", "--", "-f"],
      ["bash", "-lc", "echo 'rm -rf /tmp/example'"],
      ["bash", "-lc", "cmd=rm; $cmd -rf /tmp/example"],
      ["bash", "-lc", "if then rm -rf /tmp/example"],
      ["env", "TARGET=/tmp/example", "rm", "-r", "/tmp/example"],
      ["bash", "-lc", "trap 'echo rm -rf /tmp/example' EXIT"],
    ];
    for (const command of commands) {
      expect(dangerousCommandMatch(command)).toBeUndefined();
      expect(isDangerousCommand(command)).toBe(false);
    }
  });
});
