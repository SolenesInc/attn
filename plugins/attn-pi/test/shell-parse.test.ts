// The corpus of codex-rs/shell-command/src/bash.rs `mod tests`, same inputs and
// same expected outputs. Windows and PowerShell cases are not ported: attn's
// bash tool runs a posix shell.
import { beforeAll, describe, expect, test } from "bun:test";
import { initShellParsing, parseBashCommands, parseShellScriptIntoCommands, shellApprovalCommand } from "../shell/index";

beforeAll(async () => {
  await initShellParsing();
});

function parseSeq(source: string): string[][] | undefined {
  return parseShellScriptIntoCommands(source);
}

describe("word only command sequences", () => {
  test("accepts a single simple command", () => {
    expect(parseSeq("ls -1")).toEqual([["ls", "-1"]]);
  });

  test("accepts multiple commands with allowed operators", () => {
    expect(parseSeq("ls && pwd; echo 'hi there' | wc -l")).toEqual([
      ["ls"],
      ["pwd"],
      ["echo", "hi there"],
      ["wc", "-l"],
    ]);
  });

  test("extracts double and single quoted strings", () => {
    expect(parseSeq('echo "hello world"')).toEqual([["echo", "hello world"]]);
    expect(parseSeq("echo 'hi there'")).toEqual([["echo", "hi there"]]);
  });

  test("accepts double quoted strings with newlines", () => {
    expect(parseSeq('git commit -m "line1\nline2"')).toEqual([["git", "commit", "-m", "line1\nline2"]]);
  });

  test("accepts mixed quote concatenation", () => {
    expect(parseSeq(`echo "/usr"'/'"local"/bin`)).toEqual([["echo", "/usr/local/bin"]]);
    expect(parseSeq(`echo '/usr'"/"'local'/bin`)).toEqual([["echo", "/usr/local/bin"]]);
  });

  test("rejects double quoted strings with expansions", () => {
    expect(parseSeq('echo "hi ${USER}"')).toBeUndefined();
    expect(parseSeq('echo "$HOME"')).toBeUndefined();
  });

  test("accepts numbers as words", () => {
    expect(parseSeq("echo 123 456")).toEqual([["echo", "123", "456"]]);
  });

  test("rejects parentheses and subshells", () => {
    expect(parseSeq("(ls)")).toBeUndefined();
    expect(parseSeq("ls || (pwd && echo hi)")).toBeUndefined();
  });

  test("rejects redirections and unsupported operators", () => {
    expect(parseSeq("ls > out.txt")).toBeUndefined();
    expect(parseSeq("echo hi & echo bye")).toBeUndefined();
  });

  test("rejects command and process substitutions and expansions", () => {
    expect(parseSeq("echo $(pwd)")).toBeUndefined();
    expect(parseSeq("echo `pwd`")).toBeUndefined();
    expect(parseSeq("echo $HOME")).toBeUndefined();
    expect(parseSeq('echo "hi $USER"')).toBeUndefined();
  });

  test("rejects runtime expansion in plain words", () => {
    const scripts = [
      "find . -{delete,print}",
      "rg --pre{=,=sh} pattern payload.sh",
      "find . -del*",
      "find . -delet?",
      "find . -delet[e]",
      "find . -de\\lete",
      "echo ~",
      "echo ~HOME",
      "echo HEAD~1",
      "echo HEAD^",
      "echo file~",
      "echo =sh",
      "echo foo^bar",
      "echo foo#bar",
      "l* -l",
    ];
    for (const script of scripts) {
      for (const shell of ["bash", "zsh"]) {
        for (const flag of ["-c", "-lc"]) {
          expect(parseBashCommands([shell, flag, script])).toBeUndefined();
        }
      }
    }
  });

  test("preserves quoted literals", () => {
    expect(parseSeq(`rg -g"*.py" pattern`)).toEqual([["rg", "-g*.py", "pattern"]]);
    expect(parseSeq('echo "\\n"')).toEqual([["echo", "\\n"]]);
    expect(parseSeq(`echo "~HOME" 'HEAD~1' "HEAD^" 'foo#bar' "=sh" 'file~'`)).toEqual([
      ["echo", "~HOME", "HEAD~1", "HEAD^", "foo#bar", "=sh", "file~"],
    ]);
    expect(parseSeq(`echo -"{a,b}" '*?[]~^#=\\\\'`)).toEqual([["echo", "-{a,b}", "*?[]~^#=\\\\"]]);
  });

  test("rejects double quoted escapes", () => {
    const scripts = [
      // Rust: r#"echo "\$HOME\`\"\\\n""#
      'echo "\\$HOME\\`\\"\\\\\\n"',
      // Rust: "find . \"-de\\\nlete\""
      'find . "-de\\\nlete"',
      // Rust: r#"echo "\\""#
      'echo "\\\\"',
    ];
    for (const script of scripts) {
      expect(parseSeq(script)).toBeUndefined();
      for (const shell of ["bash", "zsh"]) {
        expect(parseBashCommands([shell, "-lc", script])).toBeUndefined();
      }
    }
  });

  test("rejects a variable assignment prefix", () => {
    expect(parseSeq("FOO=bar ls")).toBeUndefined();
  });

  test("rejects a trailing operator parse error", () => {
    expect(parseSeq("ls &&")).toBeUndefined();
  });

  test("rejects an empty command position with a leading operator", () => {
    expect(parseSeq("&& ls")).toBeUndefined();
  });

  test("rejects an empty command position with a double separator", () => {
    expect(parseSeq("ls ;; pwd")).toBeUndefined();
  });

  test("rejects an empty command position with an empty pipeline segment", () => {
    expect(parseSeq("ls | | wc")).toBeUndefined();
  });

  test("parses zsh -lc plain commands", () => {
    expect(parseBashCommands(["zsh", "-lc", "ls"])).toEqual([["ls"]]);
  });

  test("accepts a concatenated flag and value", () => {
    expect(parseSeq(`rg -n "foo" -g"*.py"`)).toEqual([["rg", "-n", "foo", "-g*.py"]]);
  });

  test("accepts a concatenated flag with single quotes", () => {
    expect(parseSeq("grep -n 'pattern' -g'*.txt'")).toEqual([["grep", "-n", "pattern", "-g*.txt"]]);
  });

  test("rejects concatenation with variable substitution", () => {
    expect(parseSeq('rg -g"$VAR" pattern')).toBeUndefined();
    expect(parseSeq('rg -g"${VAR}" pattern')).toBeUndefined();
  });

  test("rejects concatenation with command substitution", () => {
    expect(parseSeq('rg -g"$(pwd)" pattern')).toBeUndefined();
    expect(parseSeq(`rg -g"$(echo '*.py')" pattern`)).toBeUndefined();
  });
});

describe("shell invocations the parser refuses to unwrap", () => {
  test("only a three word posix shell -c or -lc invocation exposes a script", () => {
    expect(parseBashCommands(["bash", "-lc", "ls"])).toEqual([["ls"]]);
    expect(parseBashCommands(["/bin/sh", "-c", "ls"])).toEqual([["ls"]]);
    expect(parseBashCommands(["bash", "-x", "ls"])).toBeUndefined();
    expect(parseBashCommands(["fish", "-c", "ls"])).toBeUndefined();
    expect(parseBashCommands(["bash", "-lc", "ls", "extra"])).toBeUndefined();
  });
});

describe("executable identity", () => {
  // executable_identity_tests.rs parent_directory_traversal_is_not_a_trusted_system_shell
  test("parent directory traversal is not a trusted system shell", () => {
    const command = ["/bin/../workspace/bash", "-c", "ls"];
    expect(shellApprovalCommand(command, "/bin/sh")).toEqual(["/bin/../workspace/bash"]);
  });

  test("the configured shell and system shells keep their whole command", () => {
    expect(shellApprovalCommand(["/bin/bash", "-lc", "ls"], "/bin/sh")).toEqual(["/bin/bash", "-lc", "ls"]);
    expect(shellApprovalCommand(["/usr/bin/zsh", "-lc", "ls"], "/bin/sh")).toEqual(["/usr/bin/zsh", "-lc", "ls"]);
    expect(shellApprovalCommand(["bash", "-lc", "ls"], "bash")).toEqual(["bash", "-lc", "ls"]);
  });
});
