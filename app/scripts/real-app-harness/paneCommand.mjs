let sequence = 0;

export function createPaneCommand(command) {
  const id = ++sequence;
  const start = `mark${id}start`;
  const end = `mark${id}end`;
  // Construct markers in the shell so echoed or redrawn input cannot finish a command.
  const text = `printf '\\nmark%sstart\\n' ${id}; ${command}; printf '\\nmark%send\\n' ${id}`;
  return {
    text,
    readOutput(paneText) {
      const flat = paneText.replace(/\n/g, '');
      const first = flat.indexOf(start);
      if (first === -1) return null;
      const last = flat.indexOf(end, first + start.length);
      if (last === -1) return null;
      return flat.slice(first + start.length, last);
    },
  };
}
