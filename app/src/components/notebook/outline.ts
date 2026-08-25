// ATX headings only (`# …` through `###### …`); setext underlines (`===`/`---`)
// are intentionally ignored, being ambiguous against rules and table separators.

export interface OutlineHeading {
  level: number;
  text: string;
  // Must index into the SAME string the editor holds (the live draft), which is
  // what it scrolls to the top.
  pos: number;
  line: number;
}

// A closing '#' run must be PRECEDED by whitespace, so `## C#` keeps its
// trailing hash; the required space after the '#' run excludes `#hashtag`.
const ATX_HEADING = /^ {0,3}(#{1,6})[ \t]+(.*?)(?:[ \t]+#+)?[ \t]*$/;
// A `#` line inside a fence is code, not a heading, so fence state must be tracked.
const FENCE = /^[ \t]*(`{3,}|~{3,})/;

export function parseOutline(md: string): OutlineHeading[] {
  const out: OutlineHeading[] = [];
  if (!md) return out;
  const lines = md.split('\n');
  let offset = 0;
  let fenceChar = '';
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const fence = FENCE.exec(line);
    if (fence) {
      const char = fence[1][0];
      if (fenceChar === '') fenceChar = char;
      else if (fenceChar === char) fenceChar = '';
    } else if (fenceChar === '') {
      const heading = ATX_HEADING.exec(line);
      if (heading) {
        const text = heading[2].trim();
        if (text) out.push({ level: heading[1].length, text, pos: offset, line: i + 1 });
      }
    }
    offset += line.length + 1;
  }
  return out;
}
