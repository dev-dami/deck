export type DiffKind = "meta" | "hunk" | "add" | "del" | "ctx";

export interface DiffLine {
  kind: DiffKind;
  text: string;
}

const HUNK_RE = /^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@/;

// Unified-diff classifier. Hunk counts disambiguate a removed line that happens
// to read "-- x" (shown as "--- x") from a real file header.
export function parseDiff(text: string): DiffLine[] {
  const out: DiffLine[] = [];
  let oldLeft = 0;
  let newLeft = 0;
  let inHunk = false;
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  for (const line of lines) {
    if (line.startsWith("@@")) {
      const m = HUNK_RE.exec(line);
      // Malformed headers (or "@@" alone) leave the counts open-ended.
      oldLeft = m ? Number(m[1] ?? 1) : Number.POSITIVE_INFINITY;
      newLeft = m ? Number(m[2] ?? 1) : Number.POSITIVE_INFINITY;
      inHunk = true;
      out.push({ kind: "hunk", text: line });
      continue;
    }
    if (line.startsWith("\\")) {
      out.push({ kind: "meta", text: line });
      continue;
    }
    if (inHunk && (oldLeft > 0 || newLeft > 0)) {
      if (line.startsWith("+")) {
        newLeft--;
        out.push({ kind: "add", text: line });
      } else if (line.startsWith("-")) {
        oldLeft--;
        out.push({ kind: "del", text: line });
      } else {
        oldLeft--;
        newLeft--;
        out.push({ kind: "ctx", text: line });
      }
      continue;
    }
    inHunk = false;
    out.push({ kind: "meta", text: line });
  }
  return out;
}

export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const l of lines) {
    if (l.kind === "add") added++;
    else if (l.kind === "del") removed++;
  }
  return { added, removed };
}
