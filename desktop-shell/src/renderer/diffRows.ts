import { type DiffLine, parseDiff } from "@web/diff";

// A unified diff as the diff panel draws it: each line with its number in
// the new file (the old one for a removed line), and a fold between hunks
// that says how many lines it skips.
export type DiffRow =
  | { k: "line"; kind: "add" | "del" | "ctx"; num: number | null; text: string }
  | { k: "fold"; skipped: number | null }
  | { k: "meta"; text: string };

const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

export function diffRows(diff: string): DiffRow[] {
  const out: DiffRow[] = [];
  let oldNo = 0;
  let newNo = 0;
  // Where the last hunk ended in the new file, to count the skipped lines.
  let lastEnd: number | null = null;
  for (const l of parseDiff(diff) as DiffLine[]) {
    if (l.kind === "hunk") {
      const m = HUNK.exec(l.text);
      if (m === null) {
        out.push({ k: "fold", skipped: null });
        continue;
      }
      oldNo = Number(m[1]);
      newNo = Number(m[3]);
      const skipped = lastEnd === null ? newNo - 1 : newNo - lastEnd;
      // A file's first hunk at its top skips nothing and needs no fold.
      if (skipped > 0 || lastEnd !== null) out.push({ k: "fold", skipped: skipped > 0 ? skipped : null });
      continue;
    }
    if (l.kind === "meta") {
      // File headers (diff --git, ---, +++) say nothing the panel does not.
      if (!/^(diff |index |--- |\+\+\+ )/.test(l.text)) out.push({ k: "meta", text: l.text });
      continue;
    }
    const text = l.text.slice(1);
    if (l.kind === "add") {
      out.push({ k: "line", kind: "add", num: newNo, text });
      newNo++;
    } else if (l.kind === "del") {
      out.push({ k: "line", kind: "del", num: oldNo, text });
      oldNo++;
    } else {
      out.push({ k: "line", kind: "ctx", num: newNo, text });
      newNo++;
      oldNo++;
    }
    lastEnd = newNo;
  }
  return out;
}
