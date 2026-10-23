import { type DiffLine, parseDiff } from "../../../../pkg/remote/web/src/diff";

// A unified diff as the desktop's diff panel draws it
// (desktop-shell/src/renderer/diffRows.ts): each line with its number in the
// new file (the old one for a removed line), and a fold between hunks that
// says how many lines it skips.
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
  // A malformed hunk header leaves the numbering unknown until the next good one.
  let numbered = true;
  for (const l of parseDiff(diff) as DiffLine[]) {
    if (l.kind === "hunk") {
      const m = HUNK.exec(l.text);
      if (m === null) {
        numbered = false;
        out.push({ k: "fold", skipped: null });
        continue;
      }
      numbered = true;
      oldNo = Number(m[1]);
      newNo = Number(m[3]);
      const skipped = lastEnd === null ? newNo - 1 : newNo - lastEnd;
      // A file's first hunk at its top skips nothing and needs no fold.
      if (skipped > 0 || lastEnd !== null)
        out.push({ k: "fold", skipped: skipped > 0 ? skipped : null });
      continue;
    }
    if (l.kind === "meta") {
      // File headers (diff --git, ---, +++) say nothing the panel does not.
      if (!/^(diff |index |--- |\+\+\+ )/.test(l.text)) out.push({ k: "meta", text: l.text });
      continue;
    }
    const text = l.text.slice(1);
    if (l.kind === "add") {
      out.push({ k: "line", kind: "add", num: numbered ? newNo : null, text });
      newNo++;
    } else if (l.kind === "del") {
      out.push({ k: "line", kind: "del", num: numbered ? oldNo : null, text });
      oldNo++;
    } else {
      out.push({ k: "line", kind: "ctx", num: numbered ? newNo : null, text });
      newNo++;
      oldNo++;
    }
    lastEnd = newNo;
  }
  return out;
}
